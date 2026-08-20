package scenario

import "time"

// Builtin returns a fresh built-in scenario. Unknown names return a document
// that the compiler rejects, keeping registry policy in one boundary.
func Builtin(pattern string) Scenario {
	if pattern != "flood" {
		return Scenario{APIVersion: "corrtest.oscar/v1alpha1", Kind: "CorrelationScenario", Pattern: pattern}
	}
	return Scenario{
		APIVersion: "corrtest.oscar/v1alpha1", Kind: "CorrelationScenario", Name: "flood-basic",
		Suite: "builtin-all", Pattern: "flood", MaxDuration: 90 * time.Second,
		Cases: []Case{
			{Name: "emits-one-parent-at-threshold", Code: "P01", Polarity: "positive", Role: "interface_down", Repeat: 5,
				Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-p01"},
				Assertions: []Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 1}, {Kind: "synthetic-alert-count", Equals: 1}}},
			{Name: "does-not-fire-below-threshold", Code: "N01", Polarity: "negative", Role: "interface_down", Repeat: 4,
				Window: 30 * time.Second, GroupBy: []string{"site"}, Labels: map[string]string{"site": "corrtest-n01"},
				Assertions: []Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 0}, {Kind: "synthetic-alert-count", Equals: 0}}},
		},
	}
}
