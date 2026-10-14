package domain

import "testing"

func TestAttemptStatusZeroMeansUncertainTransportOnly(t *testing.T) {
	for _, test := range []struct {
		name, state, class string
		status             int
		valid              bool
	}{
		{"uncertain transport", "ATTEMPTED", "indeterminate", 0, true},
		{"uncertain server response", "ATTEMPTED", "indeterminate", 503, true},
		{"accepted", "SENT", "accepted", 200, true},
		{"missing accepted status", "SENT", "accepted", 0, false},
		{"invented acceptance", "ATTEMPTED", "accepted", 0, false},
		{"invalid status", "ATTEMPTED", "indeterminate", -1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := ExecutionFacts{Attempts: []AlertAttemptFact{{CaseStableKey: "P01", EventID: "owned", EventIndex: 1, SendState: test.state, InjectionClass: test.class, StatusCode: test.status}}}
			if err := facts.Validate(); (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}
