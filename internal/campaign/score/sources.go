package score

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// SourceSample is one front-office source filter request (OSC-207).
// Answered: a 2xx page was decoded (Count, Rows, Mismatch, Unverifiable).
type SourceSample struct {
	Action, Value                       string
	At, End                             time.Time
	Seconds                             float64
	Status                              int
	Err                                 string
	Answered                            bool
	Count, Rows, Mismatch, Unverifiable int
}

// SourceActionStats are one source action's latency and exactness.
type SourceActionStats struct {
	Percentiles
	Answered, Mismatch, Unverifiable, HTTPErrors, OtherErrors int
}

// SourceFilterStats is the scorecard's source filter section.
type SourceFilterStats struct {
	Actions       map[string]SourceActionStats
	EqualsChecked int      // source_equals answers compared with the History read-back
	EqualsOutside []string `json:",omitempty"` // the first few counts outside their window
}

// InFlightAllowance: OSCAR's History writer stamps created_at
// (CURRENT_TIMESTAMP(3) at INSERT) inside a FOR UPDATE transaction, so a row
// stamped before a read can commit after the read's snapshot and be missing
// from its count. No commit lag under load is measured; 30 s is a wide
// allowance, and a row really lost stays missing from every later count.
const InFlightAllowance = 30 * time.Second

// JudgeSourceFilters gates the source filter actions. Latency: per action
// p95/p99 at the front-office limits. Exactness: FAIL on a returned row whose
// derived value misses the filter, on an HTTP error answer other than 429
// (a refused filter or OSCAR's 5 s deadline, 503), or on a
// source_equals count outside [campaign rows of that Source created before
// At - skew - InFlightAllowance, those created by End + skew]; the request
// and the read-back window last_occurrence from T0, and every row of a worker
// server carries campaign_run, so history (the campaign read-back) holds every
// row the count may include. Missing, errored or unverifiable evidence is
// INCONCLUSIVE.
func JudgeSourceFilters(samples []SourceSample, history []oscar.HistoryRecord, historyOK bool, skew time.Duration, lim scenario.Limits) (SourceFilterStats, []Headline, []string, []string) {
	st := SourceFilterStats{Actions: map[string]SourceActionStats{}}
	var h []Headline
	var fails, incon []string
	by := map[string][]SourceSample{}
	for _, s := range samples {
		by[s.Action] = append(by[s.Action], s)
	}
	for _, a := range SourceActions {
		ss := by[a]
		if len(ss) == 0 {
			incon = append(incon, "front-office samples for "+a)
			continue
		}
		var xs []float64
		var as SourceActionStats
		statuses := map[int]int{}
		for _, s := range ss {
			xs = append(xs, s.Seconds)
			switch {
			case s.Answered:
				as.Answered++
				as.Mismatch += s.Mismatch
				as.Unverifiable += s.Unverifiable
			case s.Status >= 400 && s.Status != 429: // a refusal or the 5 s deadline (503): the filter failed
				as.HTTPErrors++
				statuses[s.Status]++
			default:
				as.OtherErrors++
			}
		}
		as.Percentiles = pcts(xs)
		as.ErrorRate = float64(as.HTTPErrors+as.OtherErrors) / float64(len(ss))
		st.Actions[a] = as
		h = append(h, limitHeadline("frontoffice."+a+".p95", "s", as.P95, lim.FrontOfficeP95.Seconds(), false),
			limitHeadline("frontoffice."+a+".p99", "s", as.P99, lim.FrontOfficeP99.Seconds(), false))
		if as.Mismatch > 0 {
			fails = append(fails, fmt.Sprintf("source filter %s: %d returned rows do not match the filter", a, as.Mismatch))
		}
		if as.HTTPErrors > 0 {
			fails = append(fails, fmt.Sprintf("source filter %s: %d HTTP error answers %v", a, as.HTTPErrors, statuses))
		}
		if as.OtherErrors > 0 {
			incon = append(incon, fmt.Sprintf("source filter %s: %d requests without an answer (429, transport, timeout or decode)", a, as.OtherErrors))
		}
		if as.Unverifiable > 0 {
			incon = append(incon, fmt.Sprintf("source filter %s: %d returned rows without labels to check", a, as.Unverifiable))
		}
		if as.Answered == 0 {
			incon = append(incon, "source filter "+a+": no answered request")
		}
	}
	for _, x := range h {
		if !x.Pass {
			fails = append(fails, fmt.Sprintf("%s %.3g %s breaches limit %.3g", x.Name, x.Value, x.Unit, *x.Limit))
		}
	}
	if !historyOK {
		return st, h, fails, append(incon, "source_equals count: no History read-back")
	}
	created := map[string][]time.Time{} // Source (lower case) -> row creation times
	for _, r := range history {
		src := strings.ToLower(AlertSource(r.Labels))
		created[src] = append(created[src], r.CreatedAt)
	}
	for _, ts := range created {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	}
	upTo := func(ts []time.Time, t time.Time, inclusive bool) int {
		return sort.Search(len(ts), func(i int) bool { return ts[i].After(t) || !inclusive && ts[i].Equal(t) })
	}
	expected, outside := false, 0
	for _, s := range by["source_equals"] {
		if !s.Answered {
			continue
		}
		ts := created[strings.ToLower(s.Value)]
		lo, hi := upTo(ts, s.At.Add(-skew-InFlightAllowance), false), upTo(ts, s.End.Add(skew), true)
		st.EqualsChecked++
		expected = expected || lo > 0
		if s.Count < lo || s.Count > hi {
			if len(st.EqualsOutside) < 5 {
				st.EqualsOutside = append(st.EqualsOutside, fmt.Sprintf("%s at %s: %d, History %d..%d", s.Value, s.At.UTC().Format(time.RFC3339), s.Count, lo, hi))
			}
			outside++
		}
	}
	if outside > 0 {
		fails = append(fails, fmt.Sprintf("source_equals: %d of %d counts outside the campaign History window (%s)", outside, st.EqualsChecked, strings.Join(st.EqualsOutside, "; ")))
	}
	if !expected {
		incon = append(incon, "source_equals count: no answer expected any campaign row")
	}
	return st, h, fails, incon
}
