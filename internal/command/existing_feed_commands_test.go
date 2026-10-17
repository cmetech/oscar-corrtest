package command

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	appRuntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

type ownedFeedCommandRuntime struct {
	fakeRuntime
	plan  runner.OwnedExistingFeedPlan
	calls int
}

func (f *ownedFeedCommandRuntime) ExecuteOwnedExistingFeed(_ context.Context, p runner.OwnedExistingFeedPlan) (domain.Run, error) {
	f.calls++
	f.plan = p
	return domain.Run{ID: "owned-run", Status: domain.RunCompleted, Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupDirty}, nil
}

func ownedFeedCommandPlan(t *testing.T) runner.OwnedExistingFeedPlan {
	t.Helper()
	snapshot, err := os.ReadFile("../oscar/testdata/existing_feed/splunk.json")
	if err != nil {
		t.Fatal(err)
	}
	plan := runner.OwnedExistingFeedPlan{APIVersion: runner.OwnedExistingFeedVersion, Manifest: oscar.ExistingFeedManifestSpec{CohortID: strings.Repeat("a", 32), TargetID: "receiver", ReceiverOrigin: "http://owned-receiver", Profile: "splunk", ProfileHash: "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b", Mapping: oscar.ExistingFeedMappingSnapshot{Source: "tests/oscar-webhookreceiver/fixtures/splunk/baseline.json", SHA256: "11a7c673edb751f5c0ce3524457977b503a39ee36979b51ce9a1c6a1f5213eae", BodyBase64: base64.StdEncoding.EncodeToString(snapshot)}, UserAgent: "owned-existing-feed/1", RequestHost: "owned-receiver", StartsAt: time.Now().UTC().Truncate(time.Second)}, NativeTargetID: "native", NativeOrigin: "http://owned-native", HistoryTargetID: "history", HistoryOrigin: "http://owned-history", OwnershipReference: "controller/owned-lab-artifact"}
	return plan
}
func TestOwnedExistingFeedCLIRequiresFiniteStrictPlan(t *testing.T) {
	plan := ownedFeedCommandPlan(t)
	var err error
	raw, _ := json.Marshal(plan)
	for _, action := range []string{"plan", "run", "unknown-field", "trailing-json", "oversized", "foreign-profile", "missing-ownership"} {
		t.Run(action, func(t *testing.T) {
			source := raw
			switch action {
			case "unknown-field":
				source = append([]byte(`{"unexpected":true,`), raw[1:]...)
			case "trailing-json":
				source = append(append([]byte{}, raw...), []byte(` {}`)...)
			case "oversized":
				source = []byte(strings.Repeat("x", (2<<20)+1))
			case "foreign-profile":
				source = bytes.Replace(raw, []byte(`"profile":"splunk"`), []byte(`"profile":"foreign"`), 1)
			case "missing-ownership":
				source = bytes.Replace(raw, []byte(`"ownershipReference":"controller/owned-lab-artifact"`), []byte(`"ownershipReference":""`), 1)
			}
			path := filepath.Join(t.TempDir(), "owned.json")
			if err = os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runtime := &ownedFeedCommandRuntime{}
			opened := 0
			app := NewConfigured(&stdout, &stderr, version.Info{Version: "test"}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) { opened++; return runtime, nil }, testGetenv)
			command := action
			if action != "plan" && action != "run" {
				command = "run"
			}
			code := app.Run(context.Background(), []string{"owned-existing-feed", command, "--manifest", path, "--output", "json"})
			switch action {
			case "plan":
				if code != 0 || opened != 0 || !strings.Contains(stdout.String(), "owned-existing-feed-v1") {
					t.Fatalf("plan %d %s %s", code, &stdout, &stderr)
				}
			case "run":
				if code != 4 || opened != 1 || runtime.calls != 1 || !runtime.closed || runtime.plan.Manifest.Profile != "splunk" || !strings.Contains(stdout.String(), "owned-run") {
					t.Fatalf("run %d %s %s", code, &stdout, &stderr)
				}
			default:
				if code == 0 || opened != 0 || runtime.calls != 0 {
					t.Fatalf("invalid plan opened runtime: %d %s", code, &stderr)
				}
			}
		})
	}
}

func TestOwnedExistingFeedCLIRealRuntimeRetainsAmbiguousSendAcrossReopen(t *testing.T) {
	t.Setenv("OWNED_FEED_TEST_KEY", "owned-key")
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/alerts":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/alerts/history":
			_, _ = w.Write([]byte(`{"records":[],"total_records":0,"total_pages":0,"page":1,"per_page":100}`))
		case "/api/v1/webhookreceiver/webhook/splunk":
			if r.Method != "POST" {
				t.Error("wrong method")
			}
			posts.Add(1)
			_, _ = w.Write([]byte(`{"status":"ok"}`)) // missing revision leaves UNKNOWN
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}
	info := version.Info{Version: "test"}
	runtime, err := appRuntime.Open(ctx, settings, info)
	if err != nil {
		t.Fatal(err)
	}
	plan := ownedFeedCommandPlan(t)
	ids := []string{}
	for _, name := range []string{"receiver", "native", "history"} {
		target, err := runtime.CreateTarget(ctx, domain.TargetInput{DisplayName: name, BaseURL: server.URL, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OWNED_FEED_TEST_KEY"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, target.ID)
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	plan.Manifest.TargetID = ids[0]
	plan.Manifest.ReceiverOrigin = server.URL
	plan.NativeTargetID = ids[1]
	plan.NativeOrigin = server.URL
	plan.HistoryTargetID = ids[2]
	plan.HistoryOrigin = server.URL
	raw, _ := json.Marshal(plan)
	path := filepath.Join(t.TempDir(), "owned.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := NewConfigured(&stdout, &stderr, info, nil, func(ctx context.Context, _ config.Settings) (ApplicationRuntime, error) {
		return appRuntime.Open(ctx, settings, info)
	}, testGetenv)
	code := app.Run(ctx, []string{"owned-existing-feed", "run", "--manifest", path, "--output", "json"})
	var envelope struct {
		Data domain.Run `json:"data"`
	}
	if err = json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("missing durable run output: %s %v", &stdout, err)
	}
	if code != 4 || posts.Load() != 1 || envelope.Data.CleanupStatus != domain.CleanupUnknown || envelope.Data.Status != domain.RunCompleted {
		t.Fatalf("result=%+v code=%d posts=%d stderr=%s", envelope.Data, code, posts.Load(), &stderr)
	}
	stdout.Reset()
	stderr.Reset()
	if code = app.Run(ctx, []string{"owned-existing-feed", "run", "--manifest", path, "--output", "json"}); code != 3 || posts.Load() != 1 {
		t.Fatalf("reopen resent cohort: code=%d posts=%d stderr=%s", code, posts.Load(), &stderr)
	}
	runtime, err = appRuntime.Open(ctx, settings, info)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	retained, err := runtime.GetRun(ctx, envelope.Data.ID)
	if err != nil || retained.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("UNKNOWN lost: %+v %v", retained, err)
	}
	events, err := runtime.ListRunEvents(ctx, retained.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for _, event := range events {
		if event.Type == "alert.attempting" {
			attempts++
		}
	}
	if attempts != 1 {
		t.Fatalf("durable attempts=%d", attempts)
	}
}
