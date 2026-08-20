package runner

import (
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

type assertionResult struct {
	Kind        string         `json:"kind"`
	Outcome     string         `json:"outcome,omitempty"`
	Expected    int            `json:"expected"`
	Observed    int            `json:"observed"`
	Verdict     domain.Verdict `json:"verdict"`
	Explanation string         `json:"explanation"`
}

func evaluateAssertions(assertions []scenario.Assertion, snapshot observationSnapshot) ([]assertionResult, error) {
	results := make([]assertionResult, 0, len(assertions))
	for _, assertion := range assertions {
		observed := 0
		switch assertion.Kind {
		case "synthetic-alert-count":
			observed = len(snapshot.Parents)
		case "audit-count", "parent-link-count":
			for _, audit := range snapshot.Audits {
				if audit.Outcome == assertion.Outcome {
					observed++
				}
			}
		default:
			return nil, fmt.Errorf("unsupported assertion kind %q", assertion.Kind)
		}
		verdict := domain.VerdictFail
		if observed == assertion.Equals {
			verdict = domain.VerdictPass
		}
		results = append(results, assertionResult{
			Kind: assertion.Kind, Outcome: assertion.Outcome, Expected: assertion.Equals, Observed: observed, Verdict: verdict,
			Explanation: fmt.Sprintf("observed %d, expected exactly %d", observed, assertion.Equals),
		})
	}
	return results, nil
}

func assertionsPass(results []assertionResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Verdict != domain.VerdictPass {
			return false
		}
	}
	return true
}

func requiresFullWindow(assertions []scenario.Assertion) bool {
	for _, assertion := range assertions {
		if assertion.Equals == 0 {
			return true
		}
	}
	return false
}
