package score

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type Delta struct {
	Name      string
	Value     float64
	Ref       float64
	WorsePct  float64
	Regressed bool // more than RegressionPct (default 20) worse than the reference
}

type ComparisonMode string

const (
	ControlBeforeAfter ComparisonMode = "control_before_after"
	ConfiguredPolicy   ComparisonMode = "configured_policy"

	MaxComparisonReferences = 3
)

var (
	ErrMissingBaseline        = errors.New("missing baseline: specify at least one reference run ID")
	ErrIncompatibleComparison = errors.New("incompatible campaign comparison")
)

// IdentifiedScorecard binds the scorecard to its stored campaign and start declaration.
type IdentifiedScorecard struct {
	RunID, ScenarioHash                   string
	Provenance                            domain.CampaignProvenance
	Status                                domain.CampaignStatus
	Cleanup                               domain.CleanupStatus
	Comparable, Custom, FullLoadCompleted bool
	PrecheckOnly                          bool
	Acknowledged                          []string
	StartedAt, EndedAt                    time.Time
	Scorecard                             Scorecard
}

type RunProvenance struct {
	RunID string
	domain.CampaignProvenance
	RunBy              string
	StartedAt, EndedAt time.Time
	Cleanup            domain.CleanupStatus
	Comparable         bool
	Verdict            domain.Verdict
}

type ComparisonItem struct {
	RunProvenance
	Deltas []Delta
}

type ReferenceVariation struct {
	Name, Unit string
	Min, Max   float64
}

type Comparison struct {
	Mode           ComparisonMode
	ScenarioHash   string
	Reference      Scorecard
	References     []RunProvenance
	ReferenceCount int
	Provisional    bool
	Variation      []ReferenceVariation
	Items          []ComparisonItem
}

// CompareCompatible compares only explicitly selected runs for a frozen workload.
func CompareCompatible(references []IdentifiedScorecard, candidates []IdentifiedScorecard, scenarioHash string, mode ComparisonMode) (Comparison, error) {
	if len(references) == 0 {
		return Comparison{}, ErrMissingBaseline
	}
	if len(references) > MaxComparisonReferences || len(candidates) == 0 {
		return Comparison{}, fmt.Errorf("%w: select one to three references and at least one candidate", ErrIncompatibleComparison)
	}
	var workload scenario.Scenario
	var refRole, candidateRole domain.CampaignRole
	switch mode {
	case ControlBeforeAfter:
		workload = scenario.AlertingDayControlV1()
		refRole, candidateRole = domain.CampaignPreChangeControl, domain.CampaignPostChangeControl
	case ConfiguredPolicy:
		workload = scenario.AlertingDayV3()
		refRole, candidateRole = domain.CampaignConfiguredPolicyReference, domain.CampaignConfiguredPolicyCandidate
	default:
		return Comparison{}, fmt.Errorf("%w: select control_before_after or configured_policy mode", ErrIncompatibleComparison)
	}
	if workload.Hash() != scenarioHash {
		return Comparison{}, fmt.Errorf("%w: scenario hash does not match %s@%s for %s", ErrIncompatibleComparison, workload.ID, workload.Version, mode)
	}

	base := references[0]
	seen := make(map[string]bool, len(references)+len(candidates))
	res := Comparison{Mode: mode, ScenarioHash: scenarioHash, ReferenceCount: len(references), Provisional: len(references) == 1}
	var latestReferenceEnd time.Time
	for _, r := range references {
		if err := validateComparableRun(r, refRole, base, scenarioHash, seen); err != nil {
			return Comparison{}, err
		}
		if r.Scorecard.Verdict != domain.VerdictPass || r.Provenance.TargetBuild != base.Provenance.TargetBuild {
			return Comparison{}, fmt.Errorf("%w: reference %q must be passing and share the reference target build", ErrIncompatibleComparison, r.RunID)
		}
		if latestReferenceEnd.Before(r.EndedAt) {
			latestReferenceEnd = r.EndedAt
		}
		res.References = append(res.References, r.summary())
	}
	for _, c := range candidates {
		if err := validateComparableRun(c, candidateRole, base, scenarioHash, seen); err != nil {
			return Comparison{}, err
		}
		if c.StartedAt.Before(latestReferenceEnd) {
			return Comparison{}, fmt.Errorf("%w: candidate %q must start after the references have ended", ErrIncompatibleComparison, c.RunID)
		}
		if mode == ControlBeforeAfter && c.Provenance.TargetBuild == base.Provenance.TargetBuild {
			return Comparison{}, fmt.Errorf("%w: control candidate %q must record a post-change build", ErrIncompatibleComparison, c.RunID)
		}
	}

	res.Reference, res.Variation = referenceMedian(references, workload.Limits.RegressionPct)
	for _, c := range candidates {
		card := c.Scorecard
		card.RegressionPct = workload.Limits.RegressionPct
		res.Items = append(res.Items, ComparisonItem{RunProvenance: c.summary(), Deltas: CompareTo(card, res.Reference)})
	}
	return res, nil
}

func (r IdentifiedScorecard) summary() RunProvenance {
	return RunProvenance{RunID: r.RunID, CampaignProvenance: r.Provenance, RunBy: r.Scorecard.RunBy,
		StartedAt: r.StartedAt, EndedAt: r.EndedAt, Cleanup: r.Cleanup, Comparable: r.Comparable, Verdict: r.Scorecard.Verdict}
}

func validateComparableRun(r IdentifiedScorecard, role domain.CampaignRole, base IdentifiedScorecard, hash string, seen map[string]bool) error {
	card, p := r.Scorecard, r.Provenance
	switch {
	case r.RunID == "" || seen[r.RunID]:
		return fmt.Errorf("%w: missing or repeated run ID %q", ErrIncompatibleComparison, r.RunID)
	case r.ScenarioHash != hash || card.ScenarioHash != hash:
		return fmt.Errorf("%w: run %q has a different workload hash", ErrIncompatibleComparison, r.RunID)
	case r.Status != domain.CampaignCompleted || r.StartedAt.IsZero() || !r.EndedAt.After(r.StartedAt) ||
		!r.FullLoadCompleted || card.EvidenceIncomplete || r.PrecheckOnly || (card.Verdict != domain.VerdictPass && card.Verdict != domain.VerdictFail):
		return fmt.Errorf("%w: run %q has incomplete evidence", ErrIncompatibleComparison, r.RunID)
	case r.Cleanup != domain.CleanupClean || !card.CleanupClean || !r.Comparable || !card.Comparable ||
		r.Custom || len(r.Acknowledged) != 0:
		return fmt.Errorf("%w: run %q must be clean and comparable", ErrIncompatibleComparison, r.RunID)
	case p.Role != role || p.TargetBuild == "" || p.CorrTestBuild == "" || p.WorkloadConfig == "":
		return fmt.Errorf("%w: run %q lacks recorded build/configuration provenance for role %s", ErrIncompatibleComparison, r.RunID, role)
	case p.CorrTestBuild != base.Provenance.CorrTestBuild || p.WorkloadConfig != base.Provenance.WorkloadConfig:
		return fmt.Errorf("%w: run %q must share the reference CorrTest build and workload configuration", ErrIncompatibleComparison, r.RunID)
	}

	headlines := make(map[string]Headline, len(card.Headlines))
	for _, h := range card.Headlines {
		if h.Name == "" || headlines[h.Name].Name != "" || math.IsNaN(h.Value) || math.IsInf(h.Value, 0) {
			return fmt.Errorf("%w: run %q has invalid headline evidence", ErrIncompatibleComparison, r.RunID)
		}
		headlines[h.Name] = h
	}
	if len(headlines) == 0 || len(headlines) != len(base.Scorecard.Headlines) {
		return fmt.Errorf("%w: run %q has incomplete headline evidence", ErrIncompatibleComparison, r.RunID)
	}
	for _, h := range base.Scorecard.Headlines {
		got := headlines[h.Name]
		if got.Name == "" || got.Unit != h.Unit || got.HigherBetter != h.HigherBetter {
			return fmt.Errorf("%w: run %q has incompatible headline %q", ErrIncompatibleComparison, r.RunID, h.Name)
		}
	}
	seen[r.RunID] = true
	return nil
}

func referenceMedian(runs []IdentifiedScorecard, regressionPct float64) (Scorecard, []ReferenceVariation) {
	first := runs[0].Scorecard
	ref := Scorecard{ScenarioHash: first.ScenarioHash, Comparable: true, CleanupClean: true, Verdict: domain.VerdictPass, RegressionPct: regressionPct}
	var variation []ReferenceVariation
	for _, h := range first.Headlines {
		var vs []float64
		for _, r := range runs {
			for _, ch := range r.Scorecard.Headlines {
				if ch.Name == h.Name {
					vs = append(vs, ch.Value)
					break
				}
			}
		}
		sort.Float64s(vs)
		variation = append(variation, ReferenceVariation{Name: h.Name, Unit: h.Unit, Min: vs[0], Max: vs[len(vs)-1]})
		h.Value, h.Pass = vs[len(vs)/2], true
		if len(vs) == 2 {
			h.Value = (vs[0] + vs[1]) / 2
		}
		ref.Headlines = append(ref.Headlines, h)
	}
	return ref, variation
}

// CompareTo reports each headline against the reference. Headlines with a
// reference of zero or less are skipped (ponytail: relative change is
// undefined at zero; add an absolute floor if noisy zero-baselines matter).
func CompareTo(card, ref Scorecard) []Delta {
	pct := card.RegressionPct
	if pct == 0 {
		pct = 20
	}
	var out []Delta
	for _, h := range card.Headlines {
		for _, r := range ref.Headlines {
			if r.Name != h.Name || r.Value <= 0 {
				continue
			}
			// both directions use the reference as the base
			worse := h.Value - r.Value
			if h.HigherBetter {
				worse = r.Value - h.Value
			}
			d := Delta{Name: h.Name, Value: h.Value, Ref: r.Value, WorsePct: worse / r.Value * 100}
			// integer-scaled compare so exactly 20% is not regressed
			d.Regressed = worse*100 > r.Value*pct
			out = append(out, d)
		}
	}
	return out
}
