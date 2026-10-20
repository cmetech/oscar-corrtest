package oscar

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func currentRuntimeRows(t *testing.T) (InstalledPackExpected, InstalledPackBinding, []map[string]any) {
	t.Helper()
	e, b, rows := installedPreflightFixture(t)
	rows[5]["loaded_at"] = "2026-10-20T12:00:00Z"
	rows[5]["last_attempt_ms"] = int64(1792497600000)
	rows[5]["checked_at_ms"] = int64(1792497605000)
	return e, b, rows
}
func currentRuntimeObservation(t *testing.T, rows []map[string]any) InstalledPackObservation {
	t.Helper()
	raw := make([]json.RawMessage, len(rows))
	for i, v := range rows {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		raw[i] = b
	}
	return InstalledPackObservation{Packs: raw[0], Load: raw[1], Mapping: raw[2], Rules: raw[3], Rule: raw[4], Runtime: raw[5]}
}
func TestCompactCurrentRuntimeOptInAllowsRestartButLegacyReadRemainsStrict(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "explicit-current"}[current], func(t *testing.T) {
			e, b, rows := currentRuntimeRows(t)
			reader, calls, close := installedPreflightPeer(t, &b, e, rows, nil)
			defer close()
			before := b
			var observation *InstalledPackObservation
			var err error
			if current {
				observation, err = reader.ReadCurrentRuntime(context.Background(), e, b)
			} else {
				observation, err = reader.Read(context.Background(), e, b)
			}
			if *calls != 6 {
				t.Fatalf("expected six reads, got%d", *calls)
			}
			if !current {
				if err == nil || observation != nil {
					t.Fatal("legacy read accepted replaced runtime")
				}
				return
			}
			if err != nil || observation == nil {
				t.Fatal("current read rejected unchanged restarted rule", err)
			}
			pinned, err := CurrentInstalledRuntimeBinding(e, b, *observation)
			if err != nil {
				t.Fatal(err)
			}
			want := b
			want.Runtime.LoadedAt = "2026-10-20T12:00:00Z"
			want.Runtime.LastAttemptMS = 1792497600000
			if !reflect.DeepEqual(want, pinned) || !reflect.DeepEqual(before, b) {
				t.Fatal("changed non-time binding or mutated caller")
			}
			if ValidateInstalledPackObservation(e, b, *observation) == nil {
				t.Fatal("legacy pure validation weakened")
			}
			if err = ValidateInstalledPackObservation(e, pinned, *observation); err != nil {
				t.Fatal(err)
			}
			pinned.Runtime.LoadedRuleIDs[0] = 999
			if b.Runtime.LoadedRuleIDs[0] != 7 {
				t.Fatal("derived binding aliases original slice")
			}
		})
	}
}
func TestCompactCurrentRuntimeRejectsChangedConfigurationAndInvalidRuntime(t *testing.T) {
	for _, fault := range []string{"rule", "mapping", "load", "rule_id", "dispatch", "authoritative", "dependencies", "unloaded", "rejected", "loaded_time", "numeric_time", "boolean_time", "unsafe_time", "omitted_time", "duplicate_time"} {
		t.Run(fault, func(t *testing.T) {
			e, b, rows := currentRuntimeRows(t)
			b.TargetOrigin = "https://original.invalid"
			r := rows[5]
			switch fault {
			case "rule":
				rows[4]["name"] = "foreign"
			case "mapping":
				rows[2]["state"] = "changed"
			case "load":
				rows[1]["load_id"] = "550e8400-e29b-41d4-a716-446655440001"
			case "rule_id":
				r["loaded_rule_ids"] = []int{999}
			case "dispatch":
				r["dispatch_enabled"] = true
			case "authoritative":
				r["authoritative"] = true
			case "dependencies":
				r["dependencies"].(map[string]any)["redis"] = false
			case "unloaded":
				r["load_status"] = "failed"
			case "rejected":
				r["rejected_total"] = 1
			case "loaded_time":
				r["loaded_at"] = "not-time"
			case "numeric_time":
				r["loaded_at"] = 1792497600000
			case "boolean_time":
				r["last_attempt_ms"] = true
			case "unsafe_time":
				r["last_attempt_ms"] = int64(9007199254740992)
			case "omitted_time":
				delete(r, "last_attempt_ms")
			}
			obs := currentRuntimeObservation(t, rows)
			if fault == "duplicate_time" {
				obs.Runtime = []byte(strings.Replace(string(obs.Runtime), `"loaded_at":`, `"loaded_at":"2026-10-01T00:00:00Z","loaded_at":`, 1))
			}
			if _, err := CurrentInstalledRuntimeBinding(e, b, obs); err == nil {
				t.Fatal("invalid current binding accepted")
			}
		})
	}
}
func TestCompactCurrentRuntimePinnedBindingRejectsSubsequentRestart(t *testing.T) {
	e, b, rows := currentRuntimeRows(t)
	b.TargetOrigin = "https://original.invalid"
	first := currentRuntimeObservation(t, rows)
	pinned, err := CurrentInstalledRuntimeBinding(e, b, first)
	if err != nil {
		t.Fatal(err)
	}
	rows[5]["loaded_at"] = "2026-10-20T12:01:00Z"
	rows[5]["last_attempt_ms"] = int64(1792497660000)
	if err = ValidateInstalledPackObservation(e, pinned, currentRuntimeObservation(t, rows)); err == nil {
		t.Fatal("mid-observation restart accepted")
	}
}
