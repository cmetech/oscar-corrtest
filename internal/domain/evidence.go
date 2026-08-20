package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// ExecutionFacts is the bounded, credential-free evidence used to finalize a
// run. Stable keys link each fact back to rows created from the compiled plan.
type ExecutionFacts struct {
	Cases       []CaseFact            `json:"cases"`
	Attempts    []AlertAttemptFact    `json:"attempts"`
	Resolutions []AlertResolutionFact `json:"resolutions,omitempty"`
	Artifacts   []Artifact            `json:"artifacts,omitempty"`
}

type CaseFact struct {
	StableKey  string          `json:"stableKey"`
	Verdict    Verdict         `json:"verdict"`
	StartedAt  time.Time       `json:"startedAt"`
	EndedAt    time.Time       `json:"endedAt"`
	Assertions []AssertionFact `json:"assertions"`
	Evidence   json.RawMessage `json:"evidence"`
}

type AssertionFact struct {
	StableKey        string          `json:"stableKey"`
	Kind             string          `json:"kind"`
	ExpectedJSON     json.RawMessage `json:"expected"`
	ObservedJSON     json.RawMessage `json:"observed"`
	Verdict          Verdict         `json:"verdict"`
	Explanation      string          `json:"explanation"`
	ObservationStart time.Time       `json:"observationStart"`
	ObservationEnd   time.Time       `json:"observationEnd"`
}

type AlertAttemptFact struct {
	CaseStableKey  string `json:"caseStableKey"`
	EventID        string `json:"eventId"`
	EventIndex     int    `json:"eventIndex"`
	SendState      string `json:"sendState"`
	InjectionClass string `json:"injectionClass"`
	StatusCode     int    `json:"statusCode"`
	Fingerprint    string `json:"fingerprint,omitempty"`
}

type AlertResolutionFact struct {
	AlertName      string `json:"alertName"`
	Fingerprint    string `json:"fingerprint"`
	InjectionClass string `json:"injectionClass"`
	StatusCode     int    `json:"statusCode"`
	Accepted       bool   `json:"accepted"`
}

func (facts ExecutionFacts) Validate() error {
	for _, item := range facts.Cases {
		if item.StableKey == "" || !item.Verdict.Valid() || item.StartedAt.IsZero() || item.EndedAt.Before(item.StartedAt) || !json.Valid(item.Evidence) {
			return fmt.Errorf("case fact %q is invalid", item.StableKey)
		}
		for _, assertion := range item.Assertions {
			if assertion.StableKey == "" || assertion.Kind == "" || !assertion.Verdict.Valid() || !json.Valid(assertion.ExpectedJSON) || !json.Valid(assertion.ObservedJSON) || assertion.ObservationStart.IsZero() || assertion.ObservationEnd.Before(assertion.ObservationStart) {
				return fmt.Errorf("assertion fact %q is invalid", assertion.StableKey)
			}
		}
	}
	for _, attempt := range facts.Attempts {
		if attempt.CaseStableKey == "" || attempt.EventID == "" || attempt.EventIndex <= 0 || attempt.SendState == "" || attempt.InjectionClass == "" || attempt.StatusCode < 100 || attempt.StatusCode > 599 {
			return fmt.Errorf("alert attempt fact %q/%d is invalid", attempt.CaseStableKey, attempt.EventIndex)
		}
	}
	for _, resolution := range facts.Resolutions {
		if resolution.AlertName == "" || resolution.Fingerprint == "" || resolution.InjectionClass == "" || resolution.StatusCode < 100 || resolution.StatusCode > 599 {
			return fmt.Errorf("alert resolution fact %q is invalid", resolution.AlertName)
		}
	}
	for _, item := range facts.Artifacts {
		if err := item.Validate(); err != nil {
			return err
		}
	}
	return nil
}
