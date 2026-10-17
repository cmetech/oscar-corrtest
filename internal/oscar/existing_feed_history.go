package oscar

import "fmt"

// VerifyHistory checks one scoped firing History row against an independently
// verified original Native episode. It does not prove delivery receipt/task success.
func (m ExistingFeedManifest) VerifyHistory(index int, native ExistingFeedNativeObservation, rows []HistoryRecord) error {
	if err := m.VerifyNative(index, []ExistingFeedNativeObservation{native}); err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("existing-feed History identity missing or ambiguous")
	}
	row := rows[0]
	if row.ID == "" || row.Fingerprint == "" || row.AlertName != native.Labels["alertname"] || row.Status != "firing" || !row.StartsAt.Equal(native.StartsAt) || row.Labels["am_fingerprint"] != native.Fingerprint || row.Labels["oscar_fingerprint"] != row.Fingerprint {
		return fmt.Errorf("existing-feed History differs from original Native episode")
	}
	for name, value := range native.Labels {
		if name == "alertname" {
			if promoted, present := row.Labels[name]; present && promoted != value {
				return fmt.Errorf("existing-feed History alertname conflicts with promoted value")
			}
			continue
		}
		if !row.MatchesInputLabel(name, value) {
			return fmt.Errorf("existing-feed History input label %q is missing or changed", name)
		}
	}
	return nil
}
