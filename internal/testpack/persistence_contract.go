package testpack

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PersistenceInput is a relative schedule instruction for a future protected
// producer. SourceOccurrences is cumulative episode evidence, never a delivery
// counter. These values alone confer no source trust or permission to send.
type PersistenceInput struct {
	AtSeconds         int    `json:"atSeconds"`
	Operation         string `json:"operation"`
	SourceOccurrences int    `json:"sourceOccurrences,omitempty"`
}

// PersistenceCase records finite timing prerequisites, not a compiled runtime
// plan. EligibleAtSeconds is the earliest allowed parent time, not a scheduler
// deadline or capacity promise. Recovery requires an accepted real parent and
// membership readback; it is never an unconditional timed cleanup operation.
type PersistenceCase struct {
	ID                    string             `json:"id"`
	Variant               string             `json:"variant"`
	Requirements          []string           `json:"requirements"`
	UnresolvedSeconds     int                `json:"unresolvedSeconds"`
	MinSourceOccurrences  int                `json:"minSourceOccurrences,omitempty"`
	SourceSeverity        string             `json:"sourceSeverity"`
	OriginalSeverity      string             `json:"originalSeverity,omitempty"`
	ParentSeverity        string             `json:"parentSeverity,omitempty"`
	Inputs                []PersistenceInput `json:"inputs"`
	ExpectParent          bool               `json:"expectParent"`
	NoParentBeforeSeconds int                `json:"noParentBeforeSeconds"`
	EligibleAtSeconds     int                `json:"eligibleAtSeconds,omitempty"`
	ObservationSeconds    int                `json:"observationSeconds"`
	ExpectedWaitReason    string             `json:"expectedWaitReason,omitempty"`
	RecoveryBarrier       string             `json:"recoveryBarrier,omitempty"`
}

// PersistenceCases returns new owned values on every call. Cases deliberately
// avoid unresolved cross-policy precedence and production predicate claims.
// Each case needs an isolated source-alarm identity and one selected policy.
func PersistenceCases() []PersistenceCase {
	firing := func(at, count int) PersistenceInput { return PersistenceInput{at, "firing", count} }
	base := func(id string, requirements []string, inputs []PersistenceInput, eligible, observe int) PersistenceCase {
		c := PersistenceCase{ID: id, Variant: "n30", Requirements: requirements, UnresolvedSeconds: 300,
			MinSourceOccurrences: 3, SourceSeverity: "major", Inputs: inputs, ExpectParent: eligible > 0,
			NoParentBeforeSeconds: eligible, EligibleAtSeconds: eligible, ObservationSeconds: observe}
		if c.ExpectParent {
			c.ParentSeverity = "critical"
			c.RecoveryBarrier = "accepted-parent-history-and-membership"
		}
		return c
	}
	cases := []PersistenceCase{
		base("n30-count-first", []string{"P01", "P10"}, []PersistenceInput{firing(0, 1), firing(120, 2), firing(180, 3)}, 300, 360),
		base("n30-age-first", []string{"P02", "P10"}, []PersistenceInput{firing(0, 1), firing(120, 2), firing(420, 3)}, 420, 480),
		base("n30-one-hour-count-one", []string{"P03"}, []PersistenceInput{firing(0, 1)}, 0, 3600),
		base("n30-one-hour-count-two", []string{"P03"}, []PersistenceInput{firing(0, 1), firing(120, 2)}, 0, 3600),
		base("n30-recovery-before-age", []string{"P04"}, []PersistenceInput{firing(0, 1), firing(120, 2), firing(180, 3), {240, "resolved", 3}}, 0, 360),
		base("n30-unchanged-count-observations", []string{"P05-unchanged-count-only"}, []PersistenceInput{firing(0, 1), firing(120, 1), firing(240, 1), firing(310, 1)}, 0, 360),
	}
	for i := 2; i < len(cases); i++ {
		cases[i].NoParentBeforeSeconds = cases[i].ObservationSeconds
		if i != 4 {
			cases[i].ExpectedWaitReason = "persistence_occurrence_threshold_pending"
		}
	}
	for _, variant := range []struct {
		name string
		age  int
	}{{"n14", 300}, {"n18", 600}, {"n28-diameter", 60}, {"n28-interface", 120}} {
		for _, mapping := range []struct{ original, severity string }{{"4", "major"}, {"5", "critical"}} {
			cases = append(cases, PersistenceCase{ID: variant.name + "-original-" + mapping.original,
				Variant: variant.name, Requirements: []string{"P11", "P10"}, UnresolvedSeconds: variant.age,
				SourceSeverity: "minor", OriginalSeverity: mapping.original, ParentSeverity: mapping.severity,
				Inputs: []PersistenceInput{firing(0, 0)}, ExpectParent: true, NoParentBeforeSeconds: variant.age,
				EligibleAtSeconds: variant.age, ObservationSeconds: variant.age + 60,
				RecoveryBarrier: "accepted-parent-history-and-membership"})
		}
	}
	return cases
}

// PersistenceContractJSON is a closed offline planning artifact. It neither
// enables a rule nor constructs an executable runner. The future executor must
// independently validate protected source readiness and full original readbacks.
func PersistenceContractJSON() []byte {
	document := struct {
		APIVersion         string            `json:"apiVersion"`
		Coverage           string            `json:"coverage"`
		ExecutionAuthority bool              `json:"executionAuthority"`
		Cases              []PersistenceCase `json:"cases"`
	}{"corrtest.oscar/persistence-schedule-v1", "offline-persistence-schedule-contract", false, PersistenceCases()}
	raw, err := json.Marshal(document)
	if err != nil {
		panic(err) // Only closed strings and small integers are generated above.
	}
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		panic(err)
	}
	return canonical
}

// ValidatePersistenceContract refuses changes, unknown fields, duplicate JSON
// keys, or shortened waits. Successful validation is not execution authority.
func ValidatePersistenceContract(raw []byte) error {
	if len(raw) > 64<<10 {
		return fmt.Errorf("Persistence schedule contract exceeds limit")
	}
	canonical, err := CanonicalJSON(raw)
	if err != nil || !bytes.Equal(canonical, PersistenceContractJSON()) {
		return fmt.Errorf("unsupported Persistence schedule contract")
	}
	return nil
}
