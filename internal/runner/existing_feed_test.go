package runner

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/history"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
)

func TestOwnedExistingFeedRunnerDurableRealAdapters(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) { testOwnedExistingFeedRunner(t, profile) })
	}
}
func testOwnedExistingFeedRunner(t *testing.T, profile string) {
	for _, fault := range []string{"", "history-null", "history-missing-records", "history-count-mismatch", "record-failure", "outcome-failure", "wrong-ack", "native-foreign", "history-foreign", "history-duplicate", "preexisting-native", "preexisting-history", "redirect-history", "cancel-after-record", "receiver-drift", "native-drift", "history-drift", "missing-ownership"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "corrtest.db")
			db, err := storage.Open(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			snapshot, err := os.ReadFile("../oscar/testdata/existing_feed/" + profile + ".json")
			if err != nil {
				t.Fatal(err)
			}
			spec := oscar.ExistingFeedManifestSpec{CohortID: strings.Repeat("a", 32), TargetID: "receiver", Profile: "splunk", ProfileHash: "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b", Mapping: oscar.ExistingFeedMappingSnapshot{Source: "tests/oscar-webhookreceiver/fixtures/splunk/baseline.json", SHA256: "11a7c673edb751f5c0ce3524457977b503a39ee36979b51ce9a1c6a1f5213eae", BodyBase64: base64.StdEncoding.EncodeToString(snapshot)}, UserAgent: "owned-existing-feed/1", RequestHost: "owned-receiver", StartsAt: time.Now().UTC().Truncate(time.Second)}
			spec.Profile = profile
			hostKey := "node"
			if profile == "middleware_custom_alert" {
				hostKey = "hostname"
				spec.ProfileHash = "faff8119b0665b4d15b7fcce6c7bc65f160f092af83c928d2c5d2d35a0c8b51e"
				spec.Mapping.Source = "tests/oscar-webhookreceiver/fixtures/middleware_custom_alert/EXT_CUSTOM_ALERTS.json"
				spec.Mapping.SHA256 = "bfcac60131c132c22385138f7346fbcd4d38dc1b96746429570ba24907deaffd"
			}
			var manifest oscar.ExistingFeedManifest
			var run domain.Run
			var mu sync.Mutex
			posts := 0
			mutations := 0
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == "POST" {
					if r.URL.Path != "/api/v1/webhookreceiver/webhook/"+profile {
						mutations++
						t.Errorf("unexpected mutation %s", r.URL)
						return
					}
					attempts, readErr := db.ListExistingFeedAttempts(context.Background(), run.ID, profile)
					saved, _ := db.GetRun(context.Background(), run.ID)
					if readErr != nil || len(attempts) != posts+1 || attempts[posts].Result != nil || saved.CleanupStatus != domain.CleanupUnknown {
						t.Errorf("send without durable UNKNOWN: %+v %v", attempts, readErr)
					}
					_, body, _ := manifest.Prepare(posts)
					got, _ := io.ReadAll(r.Body)
					if string(got) != string(body) || r.Host != "owned-receiver" || r.Header.Get("If-Match") != `"`+spec.ProfileHash+`"` {
						t.Error("request drift")
					}
					posts++
					if fault == "cancel-after-record" {
						cancel()
						<-r.Context().Done()
						return
					}
					if fault != "wrong-ack" {
						w.Header().Set("X-Oscar-Profile-Revision", spec.ProfileHash)
					}
					_, _ = w.Write([]byte(`{"status":"ok"}`))
					return
				}
				if r.Method != "GET" {
					mutations++
					t.Errorf("unexpected method %s", r.Method)
					return
				}
				switch r.URL.Path {
				case "/api/v2/alerts":
					rows := []oscar.ExistingFeedNativeObservation{}
					for i := 0; i < 6; i++ {
						e, _, _ := manifest.Prepare(i)
						if r.URL.Query().Get("filter") == hostKey+`="`+e.Expected.Labels[hostKey]+`"` && (i < posts || fault == "preexisting-native") {
							labels := e.Expected.Labels
							if fault == "native-foreign" {
								labels["site"] = "foreign"
							}
							rows = append(rows, oscar.ExistingFeedNativeObservation{Labels: labels, StartsAt: spec.StartsAt, Fingerprint: fmt.Sprintf("%016x", i+1)})
						}
					}
					_ = json.NewEncoder(w).Encode(rows)
				case "/api/v1/alerts/history":
					if fault == "history-null" {
						_, _ = w.Write([]byte(`null`))
						return
					}
					if fault == "history-missing-records" {
						_, _ = w.Write([]byte(`{"total_records":0,"total_pages":0,"page":1,"per_page":100}`))
						return
					}
					if fault == "history-count-mismatch" {
						_, _ = w.Write([]byte(`{"records":[],"total_records":1,"total_pages":1,"page":1,"per_page":100}`))
						return
					}
					if fault == "redirect-history" {
						http.Redirect(w, r, "/unexpected", 307)
						return
					}
					rows := []map[string]any{}
					limit := posts
					if fault == "preexisting-history" {
						limit = 1
					}
					for i := 0; i < limit; i++ {
						e, _, _ := manifest.Prepare(i)
						labels := e.Expected.Labels
						labels["am_fingerprint"] = fmt.Sprintf("%016x", i+1)
						labels["oscar_fingerprint"] = fmt.Sprintf("oscar-%d", i)
						if fault == "history-foreign" {
							labels["site"] = "foreign"
						}
						wireLabels := []map[string]string{}
						for key, value := range labels {
							wireLabels = append(wireLabels, map[string]string{"label": key, "value": value})
						}
						row := map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1), "alertname": e.Expected.Labels["alertname"], "fingerprint": labels["oscar_fingerprint"], "status": "firing", "startsAt": spec.StartsAt, "createdAt": spec.StartsAt, "labels": wireLabels}
						rows = append(rows, row)
						if fault == "history-duplicate" {
							rows = append(rows, row)
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"records": rows, "total_records": len(rows), "per_page": 100, "page": 1, "total_pages": (len(rows) + 99) / 100})
				default:
					t.Errorf("unexpected read %s", r.URL)
				}
			}))
			defer server.Close()
			spec.ReceiverOrigin = server.URL
			manifest, err = oscar.FreezeExistingFeedManifest(spec)
			if err != nil {
				t.Fatal(err)
			}
			target := func(id string) domain.Target {
				return domain.Target{ID: id, BaseURL: server.URL, APIProfile: "public-v1", Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OWNED_PASSWORD"}}
			}
			receiver, native, historyTarget := target("receiver"), target("native"), target("history")
			for _, target := range []domain.Target{receiver, native, historyTarget} {
				target.DisplayName = target.ID
				target.CreatedAt = spec.StartsAt
				target.UpdatedAt = spec.StartsAt
				if err = db.CreateTarget(ctx, target); err != nil {
					t.Fatal(err)
				}
			}
			run, err = history.New(db, nil, nil).CreateRun(ctx, "history", "", "test")
			if err != nil {
				t.Fatal(err)
			}
			plan := OwnedExistingFeedPlan{APIVersion: "corrtest.oscar/owned-existing-feed-v1", Manifest: spec, NativeTargetID: "native", NativeOrigin: server.URL, HistoryTargetID: "history", HistoryOrigin: server.URL, OwnershipReference: "owned-controller-artifact/exact-cohort"}
			switch fault {
			case "receiver-drift":
				receiver.BaseURL += "/foreign"
			case "native-drift":
				native.BaseURL += "/foreign"
			case "history-drift":
				historyTarget.BaseURL += "/foreign"
			case "missing-ownership":
				plan.OwnershipReference = ""
			}
			if fault == "record-failure" || fault == "outcome-failure" {
				rawDB, openErr := sql.Open("sqlite", dbPath)
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer rawDB.Close()
				statement := `CREATE TRIGGER refuse_attempt BEFORE INSERT ON run_events WHEN NEW.type='alert.attempting' BEGIN SELECT RAISE(ABORT,'record unavailable'); END`
				if fault == "outcome-failure" {
					statement = `CREATE TRIGGER refuse_outcome BEFORE UPDATE ON existing_feed_attempts WHEN NEW.result_json IS NOT NULL BEGIN SELECT RAISE(ABORT,'outcome unavailable'); END`
				}
				if _, err = rawDB.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			engine, err := NewOwnedExistingFeedRunner(plan, receiver, native, historyTarget, db, oscar.Options{Getenv: func(string) string { return "owned-password" }, PollInterval: time.Millisecond})
			if strings.HasSuffix(fault, "drift") || fault == "missing-ownership" {
				if err == nil {
					t.Fatal("invalid plan accepted")
				}
				if posts != 0 {
					t.Fatal("invalid plan sent")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(fault, "history-") {
				var boundedCancel context.CancelFunc
				ctx, boundedCancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer boundedCancel()
			}
			err = engine.Execute(ctx, run)
			saved, getErr := db.GetRun(context.Background(), run.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if fault == "" {
				if err != nil || posts != 6 || saved.Verdict != domain.VerdictPass || saved.CleanupStatus != domain.CleanupDirty || saved.Status != domain.RunCompleted {
					t.Fatalf("run %+v posts=%d err=%v", saved, posts, err)
				}
				var report struct {
					Observations []json.RawMessage `json:"observations"`
					Coverage     map[string]string `json:"coverage"`
				}
				_ = json.Unmarshal(saved.CanonicalReportJSON, &report)
				if len(report.Observations) != 6 || report.Coverage["inputPath"] != "owned-existing-feed" || report.Coverage["correlation"] != "unproved" {
					t.Fatalf("coverage %+v", report)
				}
			} else {
				if err == nil || posts > 1 || saved.Verdict == domain.VerdictPass {
					t.Fatalf("fault accepted posts=%d run=%+v err=%v", posts, saved, err)
				}
				if posts > 0 && saved.CleanupStatus == domain.CleanupNotRequired {
					t.Fatal("responsibility lost")
				}
			}
			if fault == "record-failure" && posts != 0 {
				t.Fatal("recorder failure sent")
			}
			if (fault == "outcome-failure" || fault == "cancel-after-record" || fault == "wrong-ack") && saved.CleanupStatus != domain.CleanupUnknown {
				t.Fatal("uncertain outcome responsibility changed")
			}
			if (fault == "history-null" || fault == "history-missing-records" || fault == "history-count-mismatch") && posts != 0 {
				t.Fatal("malformed History response allowed input")
			}
			if fault == "history-foreign" || fault == "history-duplicate" {
				events, _ := db.ListRunEvents(context.Background(), run.ID)
				nativeSaved := false
				for _, event := range events {
					if event.Type == "existing_feed.native_observed" {
						nativeSaved = true
					}
				}
				if !nativeSaved {
					t.Fatal("original Native fingerprint lost on History failure")
				}
			}
			before := posts
			if err = engine.Execute(context.Background(), run); err == nil || posts != before {
				t.Fatal("stale queued run replayed")
			}
			if fault == "" {
				fresh, freshErr := history.New(db, nil, nil).CreateRun(context.Background(), "history", "", "test")
				if freshErr != nil {
					t.Fatal(freshErr)
				}
				reopened, newErr := NewOwnedExistingFeedRunner(plan, receiver, native, historyTarget, db, oscar.Options{Getenv: func(string) string { return "owned-password" }})
				if newErr != nil {
					t.Fatal(newErr)
				}
				if err = reopened.Execute(context.Background(), fresh); err == nil || posts != before {
					t.Fatal("fresh run reused cohort")
				}
			}
			if mutations != 0 {
				t.Fatal("unexpected mutation")
			}
		})
	}
}
