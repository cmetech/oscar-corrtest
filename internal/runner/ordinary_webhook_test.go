package runner_test

import (
	"context"
	"encoding/json"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOrdinaryWebhookFloodThroughSeparateReceiverAndPublicAPI(t *testing.T) {
	for _, mode := range []string{"normal", "old receiver", "missing history", "acl denied"} {
		t.Run(mode, func(t *testing.T) {
			db := openDatabase(t)
			run := newRun()
			ctx := context.Background()
			if err := db.CreateRun(ctx, run); err != nil {
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
			plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			fixture := newCorrelationWireFixture("")
			public := testoscar.NewWithResponder(t, func(req testoscar.Request) testoscar.Response {
				if req.Path == "/api/v1/alerts" {
					t.Error("source escaped ordinary receiver route")
				}
				return fixture.respond(req)
			})
			sends, clears := 0, 0
			receiver := testoscar.NewWithResponder(t, func(req testoscar.Request) testoscar.Response {
				if req.Path != domain.OrdinaryWebhookPath || req.Header.Get("If-Match") != `"`+oscar.OrdinaryProfileHash+`"` {
					t.Error("receiver route/precondition missing")
				}
				var native oscar.OrdinaryWebhookRequest
				if err := json.Unmarshal([]byte(req.Body), &native); err != nil {
					t.Error(err)
				}
				start, e1 := time.Parse(time.RFC3339Nano, native.StartsAt)
				end, e2 := time.Parse(time.RFC3339Nano, native.EndsAt)
				if e1 != nil || e2 != nil {
					t.Error("missing explicit native times")
				}
				status := "firing"
				if !end.After(fixture.model.Now()) {
					status = "resolved"
					clears++
				} else {
					sends++
				}
				if native.Labels["oscar_fingerprint"] != "" {
					t.Error("native label identity altered")
				}
				if mode == "acl denied" {
					return testoscar.Response{Body: `{"status":"acl_denied"}`}
				}
				if mode != "missing history" {
					group, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: native.Labels["alertname"], Status: status, Labels: native.Labels, Annotations: native.Annotations, StartsAt: start, EndsAt: end})
					if err != nil {
						t.Fatal(err)
					}
					body, _ := json.Marshal(group)
					req.Path = "/api/v1/alerts"
					req.Body = string(body)
					out := fixture.respond(req)
					if out.Status >= 400 {
						return out
					}
				}
				header := http.Header{}
				if mode != "old receiver" {
					header.Set("X-Oscar-Profile-Revision", oscar.OrdinaryProfileHash)
				}
				return testoscar.Response{Header: header, Body: `{"status":"ok"}`}
			})
			client, err := oscar.New(domain.Target{BaseURL: public.URL(), APIProfile: "public-v1", OrdinaryWebhook: &domain.OrdinaryWebhookBinding{Endpoint: receiver.URL() + domain.OrdinaryWebhookPath, ValiditySeconds: 600, Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "RECEIVER"}}}, oscar.Options{InputPath: "ordinary-webhook", Now: fixture.model.Now, Getenv: func(string) string { return "key" }})
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(db, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, ObservationWindow: time.Second, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			saved, _ := db.GetRun(ctx, run.ID)
			if mode == "normal" {
				if err != nil || saved.Verdict != domain.VerdictPass || saved.CleanupStatus != domain.CleanupClean || sends != 9 || clears != 9 {
					t.Fatalf("err=%v verdict=%s cleanup=%s sends=%d clears=%d report=%s", err, saved.Verdict, saved.CleanupStatus, sends, clears, saved.CanonicalReportJSON)
				}
			} else {
				if saved.Verdict == domain.VerdictPass || saved.CleanupStatus != domain.CleanupDirty || clears != 0 {
					t.Fatalf("uncertainty lost: verdict=%s cleanup=%s clears=%d", saved.Verdict, saved.CleanupStatus, clears)
				}
			}
			var report struct{ Coverage map[string]string }
			if err := json.Unmarshal(saved.CanonicalReportJSON, &report); err != nil {
				t.Fatal(err)
			}
			if report.Coverage["inputPath"] != "ordinary-webhook" || report.Coverage["physicalClear"] != "NOT_PROVEN" || report.Coverage["receiverAdmission"] != "NOT_TESTED" {
				t.Fatal("route coverage wrong")
			}
		})
	}
}
