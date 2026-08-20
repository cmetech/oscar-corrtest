// Package scenario defines declarative, network-free correlation test inputs.
package scenario

import "time"

// Scenario is a closed, typed correlation scenario compiled before target mutation.
type Scenario struct {
	APIVersion  string        `json:"apiVersion" yaml:"apiVersion"`
	Kind        string        `json:"kind" yaml:"kind"`
	Name        string        `json:"name" yaml:"name"`
	Suite       string        `json:"suite" yaml:"suite"`
	Pattern     string        `json:"pattern" yaml:"pattern"`
	MaxDuration time.Duration `json:"maxDuration" yaml:"maxDuration"`
	Cases       []Case        `json:"cases" yaml:"cases"`
}

// Case declares stimuli and a typed expected outcome.
type Case struct {
	Name       string            `json:"name" yaml:"name"`
	Code       string            `json:"code" yaml:"code"`
	Polarity   string            `json:"polarity" yaml:"polarity"`
	Role       string            `json:"role" yaml:"role"`
	Repeat     int               `json:"repeat" yaml:"repeat"`
	Window     time.Duration     `json:"window" yaml:"window"`
	GroupBy    []string          `json:"groupBy" yaml:"groupBy"`
	Labels     map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Assertions []Assertion       `json:"assertions" yaml:"assertions"`
	Events     []Event           `json:"events,omitempty" yaml:"events,omitempty"`
}

// Event is one deterministic alert occurrence. Varying labels are explicit
// because they intentionally affect OSCAR fingerprints.
type Event struct {
	Role   string            `json:"role" yaml:"role"`
	Status string            `json:"status" yaml:"status"`
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Delay  time.Duration     `json:"delay,omitempty" yaml:"delay,omitempty"`
}

// Assertion is intentionally non-executable and closed by kind.
type Assertion struct {
	Kind    string `json:"kind" yaml:"kind"`
	Outcome string `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	Equals  int    `json:"equals" yaml:"equals"`
}
