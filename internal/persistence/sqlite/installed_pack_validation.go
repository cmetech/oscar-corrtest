package sqlite

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// ValidateInstalledPackSnapshot validates detached registration, input outcomes
// and their alert.attempting events without SQL, filesystem or network access.
// Events must be the original filtered attempt events in sequence order. The
// returned manifest is correspondence only, never permission to send or clean.
// Storage additionally checks canonical raw bytes, hashes and duplicated columns;
// exporters must separately verify original artifacts and the complete run.
func ValidateInstalledPackSnapshot(snapshot InstalledPackSnapshot, attemptEvents []domain.RunEvent) (oscar.InstalledPackManifest, error) {
	var zero oscar.InstalledPackManifest
	if len(snapshot.Attempts) > 15 || len(attemptEvents) != len(snapshot.Attempts) {
		return zero, fmt.Errorf("installed attempt/event count differs")
	}
	raw, err := json.Marshal(snapshot.Registration)
	if err != nil || len(raw) > installedRegistrationLimit {
		return zero, fmt.Errorf("installed registration exceeds bound")
	}
	manifest, err := validateInstalledRegistration(snapshot.Registration)
	if err != nil {
		return zero, err
	}
	var sequence int64
	for index, attempt := range snapshot.Attempts {
		if index > 0 {
			prior := snapshot.Attempts[index-1]
			if prior.Result == nil || prior.Result.Class != oscar.InjectionForwarded || prior.TransportError != "" {
				return zero, fmt.Errorf("installed attempt follows uncertain input")
			}
		}
		expected, _, err := manifest.Prepare(index)
		if err != nil {
			return zero, err
		}
		want, _ := json.Marshal(expected)
		actual, err := json.Marshal(attempt.Evidence)
		if err != nil || !bytes.Equal(actual, want) {
			return zero, fmt.Errorf("installed evidence differs from frozen input")
		}
		if attempt.RecordedAt.IsZero() {
			return zero, fmt.Errorf("installed attempt time required")
		}
		if (attempt.Result == nil) != (attempt.CompletedAt == nil) {
			return zero, fmt.Errorf("partial installed outcome")
		}
		if attempt.Result == nil {
			if attempt.TransportError != "" {
				return zero, fmt.Errorf("unrecorded installed outcome contains error")
			}
		} else {
			if attempt.CompletedAt.IsZero() {
				return zero, fmt.Errorf("installed outcome time required")
			}
			if err = validateInstalledResult(*attempt.Result, attempt.TransportError); err != nil {
				return zero, err
			}
		}
		event := attemptEvents[index]
		if event.RunID != snapshot.Registration.RunID || event.Type != "alert.attempting" || event.Sequence <= sequence || event.DetailJSON != string(want) || !event.OccurredAt.Equal(attempt.RecordedAt) {
			return zero, fmt.Errorf("installed attempt event differs")
		}
		sequence = event.Sequence
	}
	return manifest, nil
}
