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

// evaluateAssertions counts evidence per assertion. In shadow (phase_a_audit_only)
// the correlator writes audit rows but CORRELATOR_DISPATCH_ENABLED=false gates the
// synthetic emit, so synthetic-alert-count can never be satisfied and is SKIPPED
// rather than scored.
func evaluateAssertions(assertions []scenario.Assertion, snapshot observationSnapshot, shadow bool) ([]assertionResult, error) {
	results := make([]assertionResult, 0, len(assertions))
	for _, assertion := range assertions {
		observed := 0
		if shadow && assertion.Kind == "synthetic-alert-count" {
			results = append(results, assertionResult{
				Kind: assertion.Kind, Expected: assertion.Equals, Observed: len(snapshot.Parents), Verdict: domain.VerdictSkipped,
				Explanation: "synthetic emit is gated off in shadow (phase_a_audit_only); assert on audit outcomes instead",
			})
			continue
		}
		switch assertion.Kind {
		case "downstream-no-match-count":
			if !snapshot.NoMatchReady {
				results = append(results, assertionResult{Kind: assertion.Kind, Expected: assertion.Equals, Verdict: domain.VerdictInconclusive, Explanation: "current source History and explicit no_matching_rules audit not proven; receiver health, revision, delivery and isolation are outside this witness"})
				continue
			}
			observed = snapshot.NoMatchCount
		case "source-episode-state-count":
			if !snapshot.SourceEpisodesReady {
				results = append(results, assertionResult{Kind: assertion.Kind, Outcome: assertion.Outcome, Expected: assertion.Equals, Verdict: domain.VerdictInconclusive, Explanation: "current named episode timestamp, labels, identity or unambiguous History status not proven; no physical recovery inference"})
				continue
			}
			for _, source := range snapshot.SourceEpisodes {
				if source.Status == assertion.Outcome {
					observed++
				}
			}
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
	scored := 0
	for _, result := range results {
		if result.Verdict == domain.VerdictSkipped {
			continue
		}
		scored++
		if result.Verdict != domain.VerdictPass {
			return false
		}
	}
	if scored == 0 {
		return false
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
