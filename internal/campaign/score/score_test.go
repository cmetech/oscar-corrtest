package score

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

var t0 = time.Unix(1_800_000_000, 0)

var lim = scenario.Limits{HistoryP95: 40 * time.Second, Drain: 15 * time.Minute,
	ContainerSlopeMiBPerHour: 100, FrontOfficeP95: 2 * time.Second, FrontOfficeP99: 5 * time.Second,
	CorrelationSuccess: 1, TicketSuccess: 1, ResendSuccess: 1, RegressionPct: 20, QuerySetVersion: "q1"}

func dense(f func(sec float64) float64) []Point {
	var p []Point
	for sec := 0.0; sec <= 7200; sec += 60 {
		p = append(p, Point{Time: float64(t0.Unix()) + sec, Value: f(sec)})
	}
	return p
}

func flat(v float64) []Point { return dense(func(float64) float64 { return v }) }

func slopeSeries(perHour float64) []Point {
	return dense(func(sec float64) float64 { return perHour * sec / 3600 })
}

var worker = map[string]string{"alertmanager": "oscar-alertmanager-alertmanager_worker-1", "taskmanager": "oscar-taskmanager-worker-1",
	"notifier": "oscar-taskmanager-notifier_worker-1", "history": "oscar-taskmanager-history_worker-1", "handoff": "oscar-taskmanager-history_handoff_worker-1"}

// good returns inputs that pass every gate.
func good() Inputs {
	in := Inputs{ScenarioHash: "h", Start: t0, End: t0.Add(2 * time.Hour), CleanupClean: true, Comparable: true,
		Ledger: []domain.LaneRow{
			{Kind: "firing", Identity: "a", SentAt: t0, Done: true, Status: 200},
			{Kind: "resolve", Identity: "a", SentAt: t0.Add(time.Minute), Done: true, Status: 200}},
		History:  []oscar.HistoryRecord{{AlertName: "a", CreatedAt: t0.Add(5 * time.Second)}},
		Receipts: []targetops.Receipt{{ReceivedAt: t0.Add(6 * time.Second), Alerts: []targetops.SinkAlert{{AlertName: "a", Status: "firing"}}}},
		QueueDepth: map[string][]Point{"q": dense(func(s float64) float64 {
			if s < 60 {
				return 5
			}
			return 0
		})},
		QueueAge:    map[string][]Point{"q": flat(0)},
		Containers:  map[string][]Point{},
		FrontOffice: map[string][]FOSample{}}
	for _, n := range worker {
		in.Containers[n] = flat(200)
	}
	for _, a := range FOActions {
		in.FrontOffice[a] = []FOSample{{Seconds: 0.5}}
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
	}{{100.0, domain.VerdictPass}, {100.1, domain.VerdictFail}} {
		in := good()
		in.Containers[worker["history"]] = slopeSeries(tc.slope)
		if c := Compute(in, lim); c.Verdict != tc.want {
			t.Fatalf("%v: %v %v", tc.slope, c.Verdict, c.Reasons)
		}
	}
}

func TestMissingMetricSeriesIsInconclusive(t *testing.T) {
	in := good()
	delete(in.Containers, worker["handoff"])
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
		"oscar-alertmanager-alertmanager_worker-1":   "alertmanager",
		"oscar-taskmanager-alert_hooks_beat-1":       "taskmanager",
		"oscar-taskmanager-alert_hooks_worker-1":     "taskmanager",
		"oscar-taskmanager-discovery_worker-1":       "taskmanager",
		"oscar-taskmanager-history_handoff_worker-1": "handoff",
		"oscar-taskmanager-history_worker-1":         "history",
		"oscar-taskmanager-monitor_worker-1":         "taskmanager",
		"oscar-taskmanager-notifier_worker-1":        "notifier",
		"oscar-taskmanager-probe_worker-1":           "taskmanager",
		"oscar-taskmanager-tasks_worker-1":           "taskmanager",
		"oscar-taskmanager-ticket_control_worker-1":  "taskmanager",
		"oscar-taskmanager-ticket_pump_worker-1":     "taskmanager",
		"oscar-taskmanager-worker-1":                 "taskmanager",
		"oscar-taskmanager-worklog_worker-1":         "taskmanager",
		"oscar-workflow-airflow-worker-1":            "",
		"oscarui":                                    "",
	} {
		if got := RoleOf(name); got != want {
			t.Errorf("%s: %q want %q", name, got, want)
		}
	}
}

func TestNoEvidenceIsInconclusive(t *testing.T) {
	in := good()
	in.PlannedTickets = 5 // plan has tickets, evidence has none
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
	in = good()
	in.History = nil
	in.Ledger = in.Ledger[1:] // only a resolve: no accepted firing, nothing in History
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
}

func TestSparseOrTruncatedSeriesInconclusive(t *testing.T) {
	in := good()
	pts := flat(1)
	in.Containers[worker["notifier"]] = append(append([]Point{}, pts[:10]...), pts[40:]...) // 30 min hole
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
	in = good()
	in.QueueDepth["q"] = flat(5)[:30] // truncated before the drain completed
	in.QueueAge["q"] = flat(0)[:30]
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
}

func TestFrontOfficeActionRequired(t *testing.T) {
	in := good()
	delete(in.FrontOffice, "search")
	if c := Compute(in, lim); c.Verdict != domain.VerdictInconclusive {
		t.Fatal(c.Verdict, c.Reasons)
	}
}

func TestHigherBetterRegressionUsesReferenceBase(t *testing.T) {
	mk := func(v float64) Scorecard {
		return Scorecard{Headlines: []Headline{{Name: "r", Value: v, HigherBetter: true}}}
	}
	if d := CompareTo(mk(0.8), mk(1)); d[0].Regressed {
		t.Fatal("exactly 20% worse regressed")
	}
	if d := CompareTo(mk(0.79), mk(1)); !d[0].Regressed {
		t.Fatal("not regressed")
	}
}

func TestResendExactMatch(t *testing.T) {
	p := scenario.PolicySpec{Mode: "time", Interval: time.Minute}
	ds := []resend.Delivery{{At: 0}, {At: 70 * time.Second}}
	ok, err := resendOK(ResendCase{Policy: p, Deliveries: ds, Observed: []time.Duration{70*time.Second + 60*time.Second}})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := resendOK(ResendCase{Policy: p, Deliveries: ds, Observed: []time.Duration{70*time.Second + 61*time.Second}}); ok {
		t.Fatal("late resend passed")
	}
	if ok, _ := resendOK(ResendCase{Policy: p, Deliveries: ds}); ok {
		t.Fatal("missing resend passed")
	}
}

// Live smoke 2026-11-15: one delivery per cohort modelled zero resends, and
// zero observed scored 2/2. A case that models no resend is no evidence.
func TestResendNothingModelledIsUnknown(t *testing.T) {
	p := scenario.PolicySpec{Mode: "time", Interval: 50 * time.Second}
	r := reconcileResend([]ResendCase{{Policy: p, Deliveries: []resend.Delivery{{At: 0}}}})
	if r.Succeeded != 0 || r.Unknown != 1 {
		t.Fatalf("single-delivery cohort scored %+v, want unknown", r)
	}
}

// TestBaselineUnderDelivered: offered (sent planned seqs in the run window)
// below 98% of planned FAILs (ruling C34b); missed rows and resolve-all rows
// beyond the plan never count as offered.
func TestBaselineUnderDelivered(t *testing.T) {
	ledger := func(sent, missed int) []domain.LaneRow {
		var rows []domain.LaneRow
		for i := 0; i < sent; i++ {
			rows = append(rows, domain.LaneRow{Seq: i, Kind: "firing", Identity: "a", SentAt: t0, Done: true, Status: 200})
		}
		for i := sent; i < sent+missed; i++ {
			rows = append(rows, domain.LaneRow{Seq: i, Kind: "firing", Identity: "a", ScheduledAt: t0, Done: true, Missed: true, ErrorClass: "missed"})
		}
		// resolve-all rows sit beyond every planned seq
		for i := 0; i < 5; i++ {
			rows = append(rows, domain.LaneRow{Seq: 1000 + i, Kind: "resolve", Identity: "a", SentAt: t0.Add(time.Minute), Done: true, Status: 200})
		}
		return rows
	}
	for _, tc := range []struct {
		sent, missed int
		fail         bool
	}{{98, 0, false}, {97, 0, true}, {97, 3, true}, {100, 0, false}} {
		in := good()
		in.PlannedBaseline, in.Ledger = 100, ledger(tc.sent, tc.missed)
		c := Compute(in, lim)
		got := false
		for _, r := range c.Reasons {
			got = got || strings.HasPrefix(r, "baseline_under_delivered")
		}
		if got != tc.fail || (tc.fail && c.Verdict != domain.VerdictFail) || c.Baseline.Planned != 100 || c.Baseline.Offered != tc.sent {
			t.Errorf("sent %d missed %d: verdict %s baseline %+v reasons %v", tc.sent, tc.missed, c.Verdict, c.Baseline, c.Reasons)
		}
	}
	in := good() // unknown plan (0): no gate
	if c := Compute(in, lim); c.Verdict != domain.VerdictPass {
		t.Fatalf("no plan: %s %v", c.Verdict, c.Reasons)
	}
}

// Ruling C38: definite 5xx answers are counted separately in the scorecard.
func TestDefinite5xxReported(t *testing.T) {
	in := good()
	in.Ledger = append(in.Ledger,
		domain.LaneRow{Seq: 2, Kind: "firing", Identity: "b", SentAt: t0, Done: true, Status: 503, ErrorClass: "http"},
		domain.LaneRow{Seq: 3, Kind: "firing", Identity: "c", SentAt: t0, Done: true, Status: 429, ErrorClass: "http"},
		domain.LaneRow{Seq: 4, Kind: "firing", Identity: "d", SentAt: t0, Done: true, ErrorClass: "timeout"})
	if b := Compute(in, lim).Baseline; b.Definite5xx != 1 || b.ErrorsByClass["http"] != 2 || b.ErrorsByClass["timeout"] != 1 {
		t.Fatalf("baseline %+v", b)
	}
}

// Ruling C37: drained means every queue back at or below the idle depth
// (pre-check B3's threshold), not exactly 0.
func TestDrainedAtIdleDepth(t *testing.T) {
	in := good()
	in.Precheck = scenario.PrecheckLimits{IdleQueueDepth: 50}
	in.QueueDepth["q"] = dense(func(s float64) float64 {
		if s < 120 {
			return 400
		}
		return 40 // a steady background level, never 0
	})
	c := Compute(in, lim)
	if q := c.Queue["q"]; !q.Drained || q.DrainSeconds > 120 || c.Verdict != domain.VerdictPass {
		t.Fatalf("queue %+v verdict %s reasons %v", q, c.Verdict, c.Reasons)
	}
	in.QueueDepth["q"] = flat(60)
	if c := Compute(in, lim); c.Queue["q"].Drained {
		t.Fatal("a queue above the idle depth is not drained")
	}
}

// Homelab task stats are reported, or reported as not collected; never a
// silent 0/0.
func TestHomelabTaskStats(t *testing.T) {
	in := good()
	in.TasksCollected = true
	in.Tasks = []targetops.TaskRun{{Job: "j", Status: "SUCCESS", Duration: 2 * time.Second}, {Job: "j", Status: "FAILURE", Duration: 4 * time.Second}}
	in.PreTasks = []targetops.TaskRun{{Job: "j", Status: "SUCCESS", Duration: time.Second}}
	if h := Compute(in, lim).Homelab; !h.Collected || h.Completed != 1 || h.Failed != 1 || h.DurationP95 != 4 || h.PreP95 != 1 {
		t.Fatalf("collected %+v", h)
	}
	in = good()
	in.TasksNotCollected = "task history: HTTP 503"
	if h := Compute(in, lim).Homelab; h.Collected || h.NotCollected != "task history: HTTP 503" {
		t.Fatalf("not collected %+v", h)
	}
}
