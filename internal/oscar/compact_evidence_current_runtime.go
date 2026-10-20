package oscar

import (
	"encoding/json"
	"fmt"
)

// CurrentInstalledRuntimeBinding derives only load timestamps from the final
// runtime body and validates all six reads against the original installation.
// It is pure replay, not authentication: callers must retain the authenticated
// observation. Use its detached result unchanged for postflight validation.
func CurrentInstalledRuntimeBinding(expected InstalledPackExpected, original InstalledPackBinding, observation InstalledPackObservation) (InstalledPackBinding, error) {
	if _, _, err := installedPackInputs(expected, original, original.TargetOrigin); err != nil {
		return InstalledPackBinding{}, err
	}
	current, err := currentInstalledRuntimeBinding(original, observation.Runtime)
	if err != nil {
		return InstalledPackBinding{}, err
	}
	if err = ValidateInstalledPackObservation(expected, current, observation); err != nil {
		return InstalledPackBinding{}, err
	}
	return current, nil
}

func currentInstalledRuntimeBinding(original InstalledPackBinding, raw []byte) (InstalledPackBinding, error) {
	fail := func() (InstalledPackBinding, error) {
		return InstalledPackBinding{}, fmt.Errorf("installed observation current runtime load unavailable")
	}
	if len(raw) == 0 || len(raw) > maxResponseBytes {
		return fail()
	}
	value, err := installedPackObject(raw)
	if err != nil {
		return fail()
	}
	loadedAt, ok := value["loaded_at"].(string)
	if !ok || !installedPackTime(loadedAt) {
		return fail()
	}
	number, ok := value["last_attempt_ms"].(json.Number)
	if !ok {
		return fail()
	}
	lastAttempt, err := number.Int64()
	if err != nil || lastAttempt <= 0 || lastAttempt > 1<<53-1 {
		return fail()
	}
	current := original
	current.Runtime.LoadedRuleIDs = append([]int(nil), original.Runtime.LoadedRuleIDs...)
	current.Runtime.LoadedAt = loadedAt
	current.Runtime.LastAttemptMS = lastAttempt
	return current, nil
}
