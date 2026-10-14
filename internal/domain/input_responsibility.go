package domain

import "encoding/json"

// PossibleAlertInputs is a conservative retention guard, not a recovery proof.
// Rule deletion cannot discharge source responsibility. Legacy phase evidence
// also matters when a run predates the durable before-send event.
func PossibleAlertInputs(run Run, events []RunEvent) bool {
	afterIngest := func(status RunStatus) bool {
		return status == RunInjecting || status == RunObserving || status == RunAsserting
	}
	if afterIngest(run.Status) || run.Status == RunCleaningUp || run.Status == RunRecovering {
		return true
	}
	if len(run.CanonicalReportJSON) > 0 {
		var report struct {
			Attempts []json.RawMessage `json:"attempts"`
		}
		if err := json.Unmarshal(run.CanonicalReportJSON, &report); err != nil || len(report.Attempts) > 0 {
			return true
		}
	}
	for _, event := range events {
		switch event.Type {
		case "alert.attempting", "alert.prepared", "checkpoint.started", "checkpoint.completed":
			return true
		case "run.transition", "run.interrupted":
			var detail struct {
				From     RunStatus `json:"from"`
				To       RunStatus `json:"to"`
				Previous RunStatus `json:"previousStatus"`
			}
			if json.Unmarshal([]byte(event.DetailJSON), &detail) != nil {
				return true
			}
			if afterIngest(detail.From) || afterIngest(detail.To) || afterIngest(detail.Previous) || detail.Previous == RunCleaningUp || detail.Previous == RunRecovering {
				return true
			}
		}
	}
	return false
}
