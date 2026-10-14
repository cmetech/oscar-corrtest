package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOrdinaryRuntimeProbeIsRecordedAndUnknownStaysDirty(t *testing.T) {
	for _, mode := range []string{"known probe", "old receiver", "unknown clear", "short validity"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			t.Setenv("RECEIVER_KEY", "credential-sentinel")
			settings := config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}
			app, err := OpenWithOptions(ctx, settings, version.Info{Version: "test"}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			var native oscar.OrdinaryWebhookRequest
			posts, publicCalls := 0, 0
			runID := ""
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				raw, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(raw, &native); err != nil {
					t.Error(err)
				}
				runID = native.Labels["oscar_test_run_id"]
				savedRun, readErr := app.GetRun(ctx, runID)
				if readErr != nil || savedRun.InputPath() != "ordinary-webhook" {
					t.Error("probe sent before durable ordinary route/plan")
				}
				events, err := app.ListRunEvents(ctx, runID)
				if err != nil {
					t.Error(err)
				}
				matched := false
				for _, event := range events {
					var fact struct {
						Request oscar.InjectionRequestEvidence
					}
					if event.Type == "alert.attempting" && json.Unmarshal([]byte(event.DetailJSON), &fact) == nil {
						body, _ := base64.StdEncoding.DecodeString(fact.Request.BodyBase64)
						if string(body) == string(raw) && fact.Request.ProfilePrecondition == `"`+oscar.OrdinaryProfileHash+`"` {
							matched = true
						}
					}
				}
				if !matched {
					t.Error("probe POST preceded exact durable evidence")
				}
				if mode != "old receiver" {
					w.Header().Set("X-Oscar-Profile-Revision", oscar.OrdinaryProfileHash)
				}
				if mode == "unknown clear" && posts == 2 {
					w.WriteHeader(502)
					return
				}
				w.Write([]byte(`{"status":"ok"}`))
			}))
			defer receiver.Close()
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				publicCalls++
				switch r.URL.Path {
				case "/api/v1/correlation_rules/validate":
					w.Write([]byte(`{"valid":true}`))
				case "/api/v1/alerts/history":
					labels := []map[string]string{}
					for k, v := range native.Labels {
						labels = append(labels, map[string]string{"Label": k, "Value": v})
					}
					json.NewEncoder(w).Encode(map[string]any{"total_records": 1, "page": 1, "per_page": 100, "records": []map[string]any{{"id": "probe", "alertname": native.Labels["alertname"], "fingerprint": "server-fp", "startsAt": native.StartsAt, "status": "firing", "labels": labels}}})
				default:
					t.Errorf("unexpected public mutation %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer public.Close()
			validity := 600
			if mode == "short validity" {
				validity = 60
			}
			target, err := app.CreateTarget(ctx, domain.TargetInput{DisplayName: "ordinary", BaseURL: public.URL, OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: receiver.URL + domain.OrdinaryWebhookPath, ValiditySeconds: validity, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "RECEIVER_KEY"}}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("../../scenarios/reliability/flood-ordinary-webhook.yaml")
			if err != nil {
				t.Fatal(err)
			}
			doc, err := scenario.Decode(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "known probe" {
				run, err := app.CreateRun(ctx, target.ID, "", "test")
				if err != nil {
					t.Fatal(err)
				}
				plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
				if err != nil {
					t.Fatal(err)
				}
				client, err := app.newOSCARClient(target, plan.InputPath)
				if err != nil {
					t.Fatal(err)
				}
				diagnostic := app.diagnose(ctx, client, target, run, plan, "phase_a_audit_only")
				if !diagnostic.Compatible || diagnostic.InputPath != "ordinary-webhook" || !diagnostic.LabelProbe.CleanupForwarded || posts != 2 || client.UnresolvedInputCleanup() {
					t.Fatalf("diagnostic=%+v posts=%d", diagnostic, posts)
				}
				if err := app.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenWithOptions(ctx, settings, version.Info{Version: "test"}, Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				interrupted, err := reopened.GetRun(ctx, run.ID)
				if err != nil || interrupted.InputPath() != "ordinary-webhook" || interrupted.CleanupStatus != domain.CleanupUnknown {
					t.Fatalf("pre-run restart lost route or responsibility: %+v %v", interrupted, err)
				}
				return
			}
			saved, err := app.ExecuteScenario(ctx, target.ID, doc, "phase_a_audit_only")
			if err != nil {
				t.Fatal(err)
			}
			if saved.Verdict == domain.VerdictPass {
				t.Fatal("unqualified probe passed")
			}
			if mode == "short validity" {
				if posts != 0 || publicCalls != 0 || saved.CleanupStatus != domain.CleanupNotRequired {
					t.Fatalf("insufficient validity mutated target: posts=%d public=%d cleanup=%s", posts, publicCalls, saved.CleanupStatus)
				}
			} else {
				want := 1
				if mode == "unknown clear" {
					want = 2
				}
				if saved.CleanupStatus != domain.CleanupDirty || posts != want {
					t.Fatalf("unknown probe laundered: cleanup=%s posts=%d", saved.CleanupStatus, posts)
				}
			}
		})
	}
}
