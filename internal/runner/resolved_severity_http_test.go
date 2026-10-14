package runner_test

import (
	"bytes"
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
	"testing"
	"time"
)

func TestStagedRecoveryBindsOriginalSeverityFromActualHistoryWire(t *testing.T) {
	for _, mode := range []string{"canonical resolved", "missing original", "wrong original", "wrong current", "changed source label"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := os.ReadFile("../../scenarios/reliability/flood-lifecycle-downstream.yaml")
			if err != nil {
				t.Fatal(err)
			}
			doc, err := scenario.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t)
			run := newRun()
			ctx := context.Background()
			if err := db.CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			fixture := newCorrelationWireFixture("valid")
			server := testoscar.NewWithResponder(t, func(req testoscar.Request) testoscar.Response {
				response := fixture.respond(req)
				if req.Method != http.MethodGet || req.Path != "/api/v1/alerts/history" {
					return response
				}
				var body map[string]any
				if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
					t.Fatal(err)
				}
				for _, raw := range body["records"].([]any) {
					row := raw.(map[string]any)
					filtered := []any{}
					for _, value := range row["labels"].([]any) {
						label := value.(map[string]any)
						if label["Label"] == "severity" {
							row["severity"] = label["Value"]
							row["original_severity"] = label["Value"]
						} else {
							filtered = append(filtered, label)
						}
					}
					row["labels"] = filtered
					if row["status"] == "resolved" {
						row["severity"] = "cleared"
						switch mode {
						case "missing original":
							delete(row, "original_severity")
						case "wrong original":
							row["original_severity"] = "critical"
						case "wrong current":
							row["severity"] = "critical"
						case "changed source label":
							for _, value := range filtered {
								label := value.(map[string]any)
								if label["Label"] == "site" {
									label["Value"] = "foreign"
								}
							}
						}
					}
				}
				return wireJSON(body)
			})
			client, err := oscar.New(domain.Target{BaseURL: server.URL(), APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "key" }})
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(db, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := db.GetRun(ctx, run.ID)
			var report struct {
				Checkpoints []struct {
					Verdict        string
					SourceEpisodes []struct{ Rows []json.RawMessage }
				}
				Coverage map[string]string
			}
			json.Unmarshal(saved.CanonicalReportJSON, &report)
			if mode == "canonical resolved" {
				if saved.Verdict != domain.VerdictPass || len(report.Checkpoints) != 5 {
					t.Fatalf("canonical recovery/refire not observed: %s", saved.CanonicalReportJSON)
				}
				for _, point := range report.Checkpoints {
					if point.Verdict != "PASS" {
						t.Fatal("checkpoint did not pass")
					}
				}
				if !bytes.Contains(saved.CanonicalReportJSON, []byte(`"originalSeverity":"warning"`)) {
					t.Fatal("original severity evidence omitted")
				}
			} else if saved.Verdict != domain.VerdictInconclusive || len(fixture.stimuli) != 7 {
				t.Fatalf("unproved recovery advanced: verdict=%s stimuli=%d", saved.Verdict, len(fixture.stimuli))
			}
			if report.Coverage["physicalClear"] != "NOT_PROVEN" {
				t.Fatal("History observation became physical recovery proof")
			}
		})
	}
}
