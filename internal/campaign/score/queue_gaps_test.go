package score

import (
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func hasReason(reasons []string, prefix string) bool {
	for _, r := range reasons {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

// holes drops every point in [from, to) seconds after t0: missed scrapes.
func holes(pts []Point, from, to float64) []Point {
	var o []Point
	for _, p := range pts {
		if s := p.Time - float64(t0.Unix()); s < from || s >= to {
			o = append(o, p)
		}
	}
	return o
}

// OSC-232 AC-1: a starved taskmanager exporter misses scrapes mid-run
// (camp_dee1740eb5ba2fa2: 75-165 s gaps on all 17 queues). A gap before the
// last resolve leaves the drain judgeable: the queue is judged, not skipped
// as "coverage".
func TestQueueDrainJudgedThroughScrapeGaps(t *testing.T) {
	in := good()
	in.Ledger[1].SentAt = t0.Add(time.Hour) // the last resolve at T+1 h
	in.QueueDepth["q"] = holes(dense(func(s float64) float64 {
		if s < 3600 {
			return 500
		}
		return 0
	}), 1800, 1980) // a 3-minute hole before the last resolve
	in.QueueAge["q"] = holes(in.QueueAge["q"], 1800, 1980)
	c := Compute(in, lim)
	if hasReason(c.Reasons, "queue series coverage") || c.Verdict != domain.VerdictPass {
		t.Fatalf("verdict %s reasons %v", c.Verdict, c.Reasons)
	}
	if q, ok := c.Queue["q"]; !ok || !q.Drained || q.DrainSeconds != 0 || q.MaxGapSeconds != 240 {
		t.Fatalf("queue %+v (present %v), want drained at the last resolve with a 240 s gap reported", q, ok)
	}
}

// OSC-232 AC-2: a series that stops before the scoring end cannot say the
// queue drained.
func TestQueueSeriesEndingEarlyIsCoverage(t *testing.T) {
	in := good()
	in.QueueDepth["q"] = in.QueueDepth["q"][:100]
	c := Compute(in, lim)
	if !hasReason(c.Reasons, "queue series coverage for q") || c.Verdict != domain.VerdictInconclusive {
		t.Fatalf("depth truncated: verdict %s reasons %v", c.Verdict, c.Reasons)
	}
	in = good()
	in.QueueAge["q"] = in.QueueAge["q"][:100]
	c = Compute(in, lim)
	if !hasReason(c.Reasons, "queue series coverage for q") || c.Verdict != domain.VerdictInconclusive {
		t.Fatalf("age truncated: verdict %s reasons %v", c.Verdict, c.Reasons)
	}
}

// OSC-232 AC-3: an idle reading just after the last resolve, then a backlog,
// is not a drain; the drain ends where the final idle stretch starts
// (tm_history_handoff in camp_dee1740eb5ba2fa2: 0 at +7 s, 962 later, idle
// from +6 min).
func TestDrainTimedFromFinalIdleStretch(t *testing.T) {
	in := good() // last accepted resolve at T+60 s
	in.Precheck = scenario.PrecheckLimits{IdleQueueDepth: 50}
	in.QueueDepth["q"] = dense(func(s float64) float64 {
		if s >= 120 && s < 480 {
			return 900
		}
		return 0
	})
	c := Compute(in, lim)
	if q := c.Queue["q"]; !q.Drained || q.DrainSeconds != 420 {
		t.Fatalf("queue %+v, want drained 420 s after the last resolve", q)
	}
}

// OSC-232 (plan review F1): a backlog can rise and fall inside a scrape gap,
// so an idle reading before a gap is not evidence of a drain; the drain is
// timed from the first sample after the gap.
func TestDrainRestartsAfterGap(t *testing.T) {
	in := good() // last accepted resolve at T+60 s; idle from T+60 s
	in.QueueDepth["q"] = holes(in.QueueDepth["q"], 120, 720)
	c := Compute(in, lim)
	if q := c.Queue["q"]; !q.Drained || q.DrainSeconds != 660 || q.MaxGapSeconds != 660 {
		t.Fatalf("queue %+v, want drained 660 s after the last resolve, gap 660 s", q)
	}
}

// OSC-232 (plan review F2): series() concatenates every series that shares a
// queue_name; the judgment must not depend on point order.
func TestQueuePointsSortedPerQueue(t *testing.T) {
	in := good()
	want := Compute(in, lim).Queue["q"]
	for _, pts := range [][]Point{in.QueueDepth["q"], in.QueueAge["q"]} {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	c := Compute(in, lim)
	if got := c.Queue["q"]; got.Drained != want.Drained || got.DrainSeconds != want.DrainSeconds || got.MaxGapSeconds != want.MaxGapSeconds || c.Verdict != domain.VerdictPass {
		t.Fatalf("reversed points: %+v (verdict %s %v), sorted: %+v", got, c.Verdict, c.Reasons, want)
	}
}
