package precheck

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

func TestEscalationSchedulePrecheck(t *testing.T) {
	if !NeedsEscalation(scenario.AlertingDayV4()) || NeedsEscalation(scenario.AlertingDayV3()) {
		t.Fatal("B12 applies to v4's escalation cohort only")
	}
	on := targetops.PolicyStatus{ActivationEnabled: true, ScheduleOperations: json.RawMessage(`{"backlog":0}`)}
	if r := EscalationSchedule(on, nil); r.ID != "B12" || r.Tier != Blocking || r.Status != Pass {
		t.Fatalf("scheduling on: %+v", r)
	}
	for name, st := range map[string]targetops.PolicyStatus{
		"schedule off": {ActivationEnabled: true, ScheduleOperations: json.RawMessage(`null`)},
		"absent":       {ActivationEnabled: true},
		"policies off": {ScheduleOperations: json.RawMessage(`{}`)},
	} {
		if r := EscalationSchedule(st, nil); r.Status != Fail {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := EscalationSchedule(targetops.PolicyStatus{}, errors.New("boom")); r.Status != Error {
		t.Fatalf("unreadable: %+v", r)
	}
}

func TestReportSetupPrecheck(t *testing.T) {
	if r := ReportSetup("k", targetops.ReportInfo{ID: "id", SetupState: "approved"}, nil); r.ID != "B13" || r.Status != Pass {
		t.Fatalf("approved: %+v", r)
	}
	if r := ReportSetup("k", targetops.ReportInfo{ID: "id", SetupState: "setup_required"}, nil); r.Status != Fail {
		t.Fatalf("not approved: %+v", r)
	}
	if r := ReportSetup("k", targetops.ReportInfo{}, errors.New("HTTP 403")); r.Status != Error || r.Tier != Blocking {
		t.Fatalf("unreadable: %+v", r)
	}
}
