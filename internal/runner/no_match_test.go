package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
	"testing"
	"time"
)

func noMatchScenario() scenario.Scenario {
	doc := scenario.Builtin("flood")
	doc.Cases[0].Assertions = []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 1}}
	item := &doc.Cases[1]
	item.Role = ""
	item.Repeat = 0
	item.Control = "downstream-unmatched"
	for i := 0; i < 5; i++ {
		item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: fmt.Sprintf("control_%d", i), Status: "firing"})
	}
	item.Assertions = []scenario.Assertion{{Kind: "downstream-no-match-count", Equals: 5}}
	return doc
}

type noMatchAPI struct {
	*episodeHistoryAPI
	witnessMode string
}

func (api *noMatchAPI) CorrelationAudit(ctx context.Context, key string) ([]oscar.AuditRecord, error) {
	rows, err := api.episodeHistoryAPI.CorrelationAudit(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || rows[0].RuleID != 72 {
		return rows, nil
	}
	if api.witnessMode == "error" {
		return nil, fmt.Errorf("audit unavailable")
	}
	if api.witnessMode == "absent" {
		return nil, nil
	}
	row := oscar.AuditRecord{RuleIdentityNull: true, ID: rows[0].ID, CreatedAt: api.Now(), AlertFingerprint: key, Outcome: "released_no_trigger", Reason: "no_matching_rules"}
	switch api.witnessMode {
	case "no index":
		row.Reason = "no_rule_index"
	case "no alertname":
		row.Reason = "no_alertname_label"
	case "bound rule":
		row.RuleIdentityNull = false
		row.RuleID = 72
		row.RuleName = "unexpected-rule"
	case "zero identity":
		row.RuleIdentityNull = false
	case "old audit":
		row.CreatedAt = row.CreatedAt.Add(-time.Hour)
	case "future audit":
		row.CreatedAt = row.CreatedAt.Add(time.Hour)
	case "no timestamp":
		row.CreatedAt = time.Time{}
	case "wrong fingerprint":
		row.AlertFingerprint = "other"
	case "empty fingerprint":
		row.AlertFingerprint = ""
	case "shared id":
		row.ID = 1
	case "invalid id":
		row.ID = 0
	case "wrong outcome":
		row.Outcome = "pass_through"
	}
	if api.witnessMode == "duplicate witness" {
		return []oscar.AuditRecord{row, row}, nil
	}
	if api.witnessMode == "conflicting witness" {
		other := row
		other.ID++
		other.Reason = "no_rule_index"
		return []oscar.AuditRecord{row, other}, nil
	}
	return []oscar.AuditRecord{row}, nil
}
func TestDownstreamNoMatchRequiresPositiveCurrentWitness(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate witness", "conflicting witness", "invalid id", "shared id", "absent", "no index", "no alertname", "bound rule", "zero identity", "old audit", "future audit", "no timestamp", "wrong fingerprint", "empty fingerprint", "wrong outcome", "stale History", "resolved History", "error"} {
		t.Run(mode, func(t *testing.T) {
			db := openDatabase(t)
			run := newRun()
			if err := db.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, noMatchScenario(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			api := &noMatchAPI{witnessMode: mode, episodeHistoryAPI: &episodeHistoryAPI{checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}}}
			if mode == "resolved History" {
				api.mode = "all resolved"
			}
			if mode == "stale History" {
				api.mode = "stale"
			}
			err = runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, ObservationWindow: time.Second, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if (err != nil) != (mode == "error") {
				t.Fatalf("error=%v", err)
			}
			stored, _ := db.GetRun(context.Background(), run.ID)
			want := domain.VerdictInconclusive
			if mode == "valid" || mode == "duplicate witness" {
				want = domain.VerdictPass
			}
			if mode == "error" {
				want = domain.VerdictError
			}
			if stored.Verdict != want {
				t.Fatalf("verdict=%s want=%s report=%s", stored.Verdict, want, stored.CanonicalReportJSON)
			}
			var report struct{ Coverage map[string]string }
			json.Unmarshal(stored.CanonicalReportJSON, &report)
			if report.Coverage["receiverAdmission"] != "NOT_TESTED" {
				t.Fatal("receiver claim changed")
			}
		})
	}
}
