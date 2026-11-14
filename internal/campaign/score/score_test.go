package score

import (
	"fmt"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

var t0 = time.Unix(1_800_000_000, 0)

var lim = scenario.Limits{HistoryP95: 40 * time.Second, Drain: 15 * time.Minute, ParentAnonSlopeMiBPerHour: 25,
	ContainerSlopeMiBPerHour: 100, FrontOfficeP95: 2 * time.Second, FrontOfficeP99: 5 * time.Second,
	CorrelationSuccess: 1, TicketSuccess: 1, ResendSuccess: 1, RegressionPct: 20, QuerySetVersion: "q1"}

func flat(v float64) []Point {
	return []Point{{Time: float64(t0.Unix()), Value: v}, {Time: float64(t0.Unix()) + 3600, Value: v}}
}

func slopeSeries(perHour float64) []Point {
	return []Point{{Time: float64(t0.Unix()), Value: 0}, {Time: float64(t0.Unix()) + 3600, Value: perHour}}
}

// good returns inputs that pass every gate.
func good() Inputs {
	in := Inputs{ScenarioHash: "h", Start: t0, CleanupClean: true, Comparable: true,
		QueueDepth: map[string][]Point{"q": {{Time: float64(t0.Unix()), Value: 5}, {Time: float64(t0.Unix()) + 60, Value: 0}}},
		Memory:     map[string]MemSeries{}, FrontOffice: map[string][]FOSample{"grid": {{Seconds: 0.5}}}}
	for _, r := range Roles {
		in.Memory[r] = MemSeries{Parent: flat(100), Container: flat(200)}
	}
	return in
}

func TestPercentileNearestRank(t *testing.T) {
	var xs []float64
	for i := 1; i <= 100; i++ {
		xs = append(xs, float64(i))
	}
	if got := Percentile(xs, 95); got != 95 {
		t.Fatal(got)
	}
}

func TestSlopePerHour(t *testing.T) {
	got := Slope([]Point{{Time: 0, Value: 0}, {Time: 900, Value: 5}, {Time: 1800, Value: 10}})
	if got < 19.999 || got > 20.001 {
		t.Fatal(got)
	}
}

func TestGoodInputsPass(t *testing.T) {
	if c := Compute(good(), lim); c.Verdict != domain.VerdictPass {
		t.Fatal(c.Verdict, c.Reasons)
	}
}

func ticketInputs(n, verified int, unknown bool) TicketInputs {
	ti := TicketInputs{Incidents: map[string][]targetops.Incident{}, Bound: map[string]bool{}}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("Campaign-r-%d", i)
		ti.Expected = append(ti.Expected, id)
		if i < verified {
			ti.Incidents[id] = []targetops.Incident{{Comments: []targetops.Comment{{Value: "[OSCAR AUTO-UPDATE 2026] Alert has been resolved"}}}}
			ti.Bound[id] = true
		}
	}
	if !unknown { // lookups that found nothing are failures, not unknown
		for _, id := range ti.Expected[verified:] {
			ti.Incidents[id] = nil
		}
	}
	return ti
}

func TestUnknownCountsInDenominator(t *testing.T) {
	in := good()
	in.Tickets = ticketInputs(100, 99, true)
	c := Compute(in, lim)
	if c.Tickets.Ratio != 0.99 || c.Tickets.Unknown != 1 || c.Verdict != domain.VerdictFail {
		t.Fatalf("%+v %v", c.Tickets, c.Verdict)
	}
	for _, h := range c.Headlines {
		if h.Name == "tickets.success" && h.Pass {
			t.Fatal("headline passed")
		}
	}
}

func TestDuplicateIncidentFails(t *testing.T) {
	in := good()
	in.Tickets = ticketInputs(2, 2, false)
	id := in.Tickets.Expected[0]
	in.Tickets.Incidents[id] = append(in.Tickets.Incidents[id], in.Tickets.Incidents[id][0])
	c := Compute(in, lim)
	if c.Tickets.Duplicate != 1 || c.Verdict != domain.VerdictFail {
		t.Fatalf("%+v %v", c.Tickets, c.Verdict)
	}
}

func TestHistoryLatencyFromLedgerSentAt(t *testing.T) {
	row := func(id string, ok bool) domain.LaneRow {
		r := domain.LaneRow{Kind: "firing", Identity: id, SentAt: t0, Done: true, Status: 200}
		if !ok {
			r.Status, r.ErrorClass = 500, "http"
		}
		return r
	}
	rows := []domain.LaneRow{row("a", true), row("b", true), row("c", false)}
	hist := []oscar.HistoryRecord{{AlertName: "a", CreatedAt: t0.Add(7 * time.Second)}, {AlertName: "c", CreatedAt: t0.Add(time.Second)}}
	lat, missing := historyLatencies(rows, hist)
	if len(lat) != 1 || lat[0] != 7 || missing != 1 {
		t.Fatal(lat, missing)
	}
}

func TestMemoryLimitBoundary(t *testing.T) {
	for _, tc := range []struct {
		slope float64
		want  domain.Verdict
	}{{25.0, domain.VerdictPass}, {25.1, domain.VerdictFail}} {
		in := good()
		in.Memory["history"] = MemSeries{Parent: slopeSeries(tc.slope), Container: flat(1)}
		if c := Compute(in, lim); c.Verdict != tc.want {
			t.Fatalf("%v: %v %v", tc.slope, c.Verdict, c.Reasons)
		}
	}
}

func TestMissingMetricSeriesIsInconclusive(t *testing.T) {
	in := good()
	delete(in.Memory, "handoff")
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
	in = good()
	in.QueueDepth = nil
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
}

func cardWith(p95 float64) Scorecard {
	return Scorecard{Comparable: true, CleanupClean: true, Verdict: domain.VerdictPass, ScenarioHash: "h",
		Headlines: []Headline{{Name: "latency.history_p95", Value: p95}}}
}

func TestRegressionTwentyPercent(t *testing.T) {
	ref := cardWith(10)
	for _, tc := range []struct {
		v    float64
		want bool
	}{{12.0, false}, {12.1, true}} {
		d := CompareTo(cardWith(tc.v), ref)
		if len(d) != 1 || d[0].Regressed != tc.want {
			t.Fatalf("%v: %+v", tc.v, d)
		}
	}
}

func TestNotComparableExcludedFromReference(t *testing.T) {
	bad := cardWith(1000)
	bad.Comparable = false
	dirty := cardWith(2000)
	dirty.CleanupClean = false
	other := cardWith(3000)
	other.ScenarioHash = "x"
	fail := cardWith(4000)
	fail.Verdict = domain.VerdictFail
	ref := Reference([]Scorecard{bad, dirty, fail, cardWith(10), other, cardWith(20), cardWith(30), cardWith(9999)})
	if got := ref.Headlines[0].Value; got != 20 {
		t.Fatal(got)
	}
}

func TestRoleOfLiveNames(t *testing.T) {
	for name, want := range map[string]string{
		"oscar-taskmanager-alertmanager-1":      "alertmanager",
		"oscar-taskmanager-alertmanager_worker": "alertmanager",
		"oscar-taskmanager-notifier_worker-1":   "notifier",
		"oscar-taskmanager-worker-1":            "taskmanager",
		"notifier":                              "notifier",
		"history":                               "history",
		"handoff":                               "handoff",
		"oscar-mysql":                           "",
	} {
		if got := RoleOf(name); got != want {
			t.Errorf("%s: %q want %q", name, got, want)
		}
	}
}

func TestResendExactMatch(t *testing.T) {
	p := scenario.PolicySpec{Mode: "time", Interval: time.Minute}
	ds := []resend.Delivery{{At: 0}, {At: 70 * time.Second}}
	ok, err := resendOK(ResendCase{Policy: p, Deliveries: ds, Observed: []time.Duration{70 * time.Second}})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := resendOK(ResendCase{Policy: p, Deliveries: ds}); ok {
		t.Fatal("missing resend passed")
	}
}
