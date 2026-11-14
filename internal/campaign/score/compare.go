package score

import "sort"

type Delta struct {
	Name      string
	Value     float64
	Ref       float64
	WorsePct  float64
	Regressed bool // more than RegressionPct (default 20) worse than the reference
}

// Reference is the median, per headline, of the first three comparable, clean,
// PASS cards that share the first eligible card's scenario hash. Agent and
// human runs share one reference (decided Q23).
func Reference(cards []Scorecard) Scorecard {
	var picked []Scorecard
	for _, c := range cards {
		if !c.Comparable || !c.CleanupClean || c.Verdict != "PASS" {
			continue
		}
		if len(picked) > 0 && c.ScenarioHash != picked[0].ScenarioHash {
			continue
		}
		picked = append(picked, c)
		if len(picked) == 3 {
			break
		}
	}
	if len(picked) == 0 {
		return Scorecard{}
	}
	ref := Scorecard{ScenarioHash: picked[0].ScenarioHash, Comparable: true, CleanupClean: true, Verdict: "PASS", RegressionPct: picked[0].RegressionPct}
	for _, h := range picked[0].Headlines {
		var vs []float64
		for _, c := range picked {
			for _, ch := range c.Headlines {
				if ch.Name == h.Name {
					vs = append(vs, ch.Value)
				}
			}
		}
		sort.Float64s(vs)
		m := vs[len(vs)/2]
		if len(vs)%2 == 0 {
			m = (vs[len(vs)/2-1] + vs[len(vs)/2]) / 2
		}
		h.Value, h.Pass = m, true
		ref.Headlines = append(ref.Headlines, h)
	}
	return ref
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
