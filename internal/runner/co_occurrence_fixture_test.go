package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

// The independent model uses generic no_trigger; the real co-occurrence
// contract reports enriched when a firing advances a non-triggered window.
type coOccurrenceAPI struct {
	*episodeHistoryAPI
	badRecovery bool
}

func (api *coOccurrenceAPI) CorrelationAudit(ctx context.Context, key string) ([]oscar.AuditRecord, error) {
	rows, err := api.episodeHistoryAPI.CorrelationAudit(ctx, key)
	for i := range rows {
		if rows[i].Pattern == "co_occurrence" && rows[i].Outcome == "no_trigger" {
			rows[i].Outcome = "enriched"
		}
	}
	return rows, err
}
func (api *coOccurrenceAPI) FindHistory(ctx context.Context, q oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	rows, err := api.episodeHistoryAPI.FindHistory(ctx, q)
	if api.badRecovery && len(api.sent) == 3 {
		for i := range rows {
			rows[i].Status = "firing"
		}
	}
	return rows, err
}
func coOccurrenceFixture(t *testing.T) scenario.Scenario {
	t.Helper()
	raw, err := os.ReadFile("../../scenarios/reliability/co-occurrence-lifecycle-downstream.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func TestStagedCoOccurrenceFixturePreservesRolesAndRecoveryEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "recovery missing", "stale episode"} {
		t.Run(mode, func(t *testing.T) {
			db := openDatabase(t)
			run := newRun()
			if err := db.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, coOccurrenceFixture(t), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			positive := plan.Cases[0]
			clauses := positive.Rule.MatchCriteria["required_matches"].([]any)
			if len(clauses) != 2 || positive.Rule.MatchCriteria["min_matches"] != 2 {
				t.Fatal("required MatchClause semantics changed")
			}
			matched := map[string]bool{}
			for _, clause := range clauses {
				matched[clause.(map[string]string)["alertname"]] = true
			}
			if !matched[positive.Alerts[0].Name] || !matched[positive.Alerts[1].Name] {
				t.Fatal("fixture roles are not both required")
			}
			if positive.Alerts[0].Name == positive.Alerts[1].Name || positive.Alerts[0].Labels["oscar_test_event_id"] == positive.Alerts[1].Labels["oscar_test_event_id"] {
				t.Fatal("required roles collapsed")
			}
			if positive.Alerts[0].Name != positive.Alerts[2].Name || positive.Alerts[1].Name != positive.Alerts[3].Name {
				t.Fatal("recovery role changed")
			}
			api := &coOccurrenceAPI{badRecovery: mode == "recovery missing", episodeHistoryAPI: &episodeHistoryAPI{checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}}}
			if mode == "stale episode" {
				api.mode = "stale"
			}
			if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
				t.Fatal(err)
			}
			stored, _ := db.GetRun(context.Background(), run.ID)
			var report struct {
				Checkpoints []struct {
					Verdict    string
					Assertions []struct {
						Outcome  string
						Observed int
					}
				}
				Coverage map[string]string
			}
			if err := json.Unmarshal(stored.CanonicalReportJSON, &report); err != nil {
				t.Fatal(err)
			}
			want := domain.VerdictPass
			sent := 5
			points := 4
			if mode == "recovery missing" {
				want = domain.VerdictFail
				sent = 3
				points = 3
			}
			if mode == "stale episode" {
				want = domain.VerdictInconclusive
				sent = 3
				points = 3
			}
			if stored.Verdict != want || len(api.sent) != sent || len(report.Checkpoints) != points {
				t.Fatalf("verdict=%s sent=%d points=%d report=%s", stored.Verdict, len(api.sent), len(report.Checkpoints), stored.CanonicalReportJSON)
			}
			if report.Checkpoints[0].Assertions[1].Observed != 0 || report.Checkpoints[1].Assertions[1].Observed != 1 {
				t.Fatal("intermediate/final trigger evidence collapsed")
			}
			if report.Coverage["physicalClear"] != "NOT_PROVEN" || report.Coverage["receiverAdmission"] != "NOT_TESTED" {
				t.Fatal("fixture overclaims lifecycle coverage")
			}
		})
	}
}
