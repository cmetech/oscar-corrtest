package testpack

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPersistenceBundledScheduleMatchesClosedContract(t *testing.T) {
	raw, err := os.ReadFile("../../test-packs/correlation-persistence-v1/scenarios/persistence-showcase.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, PersistenceContractJSON()) {
		t.Fatal("bundled Persistence schedule differs from the qualified offline contract")
	}
}

func TestPersistenceContractKeepsRealDurationsAndBothGateOrders(t *testing.T) {
	cases := PersistenceCases()
	if len(cases) != 14 {
		t.Fatalf("finite Persistence case inventory changed: %d", len(cases))
	}
	byID := map[string]PersistenceCase{}
	for _, c := range cases {
		if _, duplicate := byID[c.ID]; duplicate {
			t.Fatalf("duplicate case %q", c.ID)
		}
		byID[c.ID] = c
		if c.ExpectParent && (c.RecoveryBarrier != "accepted-parent-history-and-membership" || c.ParentSeverity == "") {
			t.Fatalf("parent/recovery evidence weakened: %+v", c)
		}
	}
	first, late := byID["n30-count-first"], byID["n30-age-first"]
	if first.UnresolvedSeconds != 300 || first.MinSourceOccurrences != 3 || first.NoParentBeforeSeconds != 300 || first.EligibleAtSeconds != 300 || first.Inputs[2].AtSeconds != 180 {
		t.Fatalf("count-first schedule differs: %+v", first)
	}
	if late.UnresolvedSeconds != 300 || late.MinSourceOccurrences != 3 || late.NoParentBeforeSeconds != 420 || late.EligibleAtSeconds != 420 || late.Inputs[2].AtSeconds != 420 {
		t.Fatalf("late occurrence restarts or shortens timer: %+v", late)
	}
	for _, id := range []string{"n30-one-hour-count-one", "n30-one-hour-count-two"} {
		c := byID[id]
		if c.ExpectParent || c.ObservationSeconds != 3600 || c.ExpectedWaitReason != "persistence_occurrence_threshold_pending" {
			t.Fatalf("hour-long negative weakened: %+v", c)
		}
	}
	for variant, duration := range map[string]int{"n14": 300, "n18": 600, "n28-diameter": 60, "n28-interface": 120} {
		for original, severity := range map[string]string{"4": "major", "5": "critical"} {
			c := byID[variant+"-original-"+original]
			if c.UnresolvedSeconds != duration || c.MinSourceOccurrences != 0 || c.SourceSeverity != "minor" || c.OriginalSeverity != original || c.ParentSeverity != severity || c.NoParentBeforeSeconds != duration || !c.ExpectParent {
				t.Fatalf("mapped time-only requirement changed: %+v", c)
			}
		}
	}
	canceled := byID["n30-recovery-before-age"]
	if canceled.ExpectParent || canceled.Inputs[3].Operation != "resolved" || canceled.Inputs[3].AtSeconds != 240 || canceled.ObservationSeconds <= 300 {
		t.Fatalf("cancellation schedule weakened: %+v", canceled)
	}
	for _, input := range byID["n30-unchanged-count-observations"].Inputs {
		if input.SourceOccurrences != 1 {
			t.Fatal("notification/observation count incorrectly treated as occurrences")
		}
	}
}

func TestPersistenceContractDetachedAndExplicitlyNotExecutionAuthority(t *testing.T) {
	first := PersistenceCases()
	first[0].Inputs[0].SourceOccurrences = 99
	first[0].ParentSeverity = "minor"
	if PersistenceCases()[0].Inputs[0].SourceOccurrences != 1 || PersistenceCases()[0].ParentSeverity != "critical" {
		t.Fatal("caller can change later contract reads")
	}
	raw := PersistenceContractJSON()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["executionAuthority"] != false || document["coverage"] != "offline-persistence-schedule-contract" {
		t.Fatalf("offline schedule grants authority: %s", raw)
	}
	if err := ValidatePersistenceContract(raw); err != nil {
		t.Fatal(err)
	}
	raw[0] = 'x'
	if err := ValidatePersistenceContract(PersistenceContractJSON()); err != nil {
		t.Fatalf("mutated returned bytes changed contract: %v", err)
	}
}

func TestPersistenceContractRefusesChangedScheduleAndUnknownFields(t *testing.T) {
	raw := PersistenceContractJSON()
	for name, bad := range map[string][]byte{
		"shortened wait": bytes.Replace(raw, []byte(`"unresolvedSeconds":300`), []byte(`"unresolvedSeconds":30`), 1),
		"count disabled": bytes.Replace(raw, []byte(`"minSourceOccurrences":3`), []byte(`"minSourceOccurrences":0`), 1),
		"authority":      bytes.Replace(raw, []byte(`"executionAuthority":false`), []byte(`"executionAuthority":true`), 1),
		"extra field":    []byte(strings.Replace(string(raw), "{", `{"capabilities":{"trusted":true},`, 1)),
		"duplicate key":  []byte(strings.Replace(string(raw), "{", `{"executionAuthority":false,`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(raw, bad) {
				t.Fatal("test mutation did not alter contract")
			}
			if err := ValidatePersistenceContract(bad); err == nil {
				t.Fatal("altered closed contract accepted")
			}
		})
	}
}
