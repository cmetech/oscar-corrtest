package domain

import (
	"encoding/json"
	"testing"
)

func TestPossibleAlertInputsNeverUsesRuleAbsenceAsRecovery(t *testing.T) {
	for _, test := range []struct {
		name   string
		run    Run
		events []RunEvent
		want   bool
	}{
		{"queued", Run{Status: RunQueued}, nil, false},
		{"rules only", Run{Status: RunCompleted}, []RunEvent{{Type: "run.transition", DetailJSON: `{"from":"SETTING_UP","to":"CANCELLING"}`}}, false},
		{"injecting legacy", Run{Status: RunInjecting}, nil, true},
		{"observing legacy", Run{Status: RunObserving}, nil, true},
		{"cleaning crash", Run{Status: RunCleaningUp}, nil, true},
		{"durable attempt", Run{Status: RunCompleted}, []RunEvent{{Type: "alert.attempting"}}, true},
		{"old prepared", Run{Status: RunCompleted}, []RunEvent{{Type: "alert.prepared"}}, true},
		{"legacy cancelled", Run{Status: RunInterrupted}, []RunEvent{{Type: "run.transition", DetailJSON: `{"from":"INJECTING","to":"CANCELLING"}`}}, true},
		{"legacy interrupt", Run{Status: RunInterrupted}, []RunEvent{{Type: "run.interrupted", DetailJSON: `{"previousStatus":"OBSERVING"}`}}, true},
		{"bad transition", Run{Status: RunInterrupted}, []RunEvent{{Type: "run.transition", DetailJSON: `{broken`}}, true},
		{"canonical attempt", Run{Status: RunCompleted, CanonicalReportJSON: json.RawMessage(`{"attempts":[{}]}`)}, nil, true},
		{"bad report", Run{Status: RunCompleted, CanonicalReportJSON: json.RawMessage(`{broken`)}, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := PossibleAlertInputs(test.run, test.events); got != test.want {
				t.Fatalf("got=%v want=%v", got, test.want)
			}
		})
	}
}
