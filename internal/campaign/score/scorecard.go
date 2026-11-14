package score

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Roles are the worker roles memory is reported under. The gate itself is per
// container (ruling C23): no celery worker exports a parent anon series.
var Roles = []string{"alertmanager", "taskmanager", "notifier", "history", "handoff"}

// FOActions is the frozen list of API-level front-office actions; their
// samples are pooled for the p95/p99 gates and each needs N > 0.
var FOActions = []string{"firing_page1", "severity_filter", "status_filter", "paging", "instance_filter", "search", "active_alerts"}

const (
	metricStep = 15 * time.Second
	maxGap     = 4 * metricStep
)

// roleRules is checked in order; the first match wins. Live cAdvisor names:
// oscar-alertmanager-alertmanager_worker-1, oscar-taskmanager-notifier_worker-1,
// oscar-taskmanager-history_handoff_worker-1 (handoff, so before history),
// oscar-taskmanager-history_worker-1, and the remaining oscar-taskmanager-*.
var roleRules = []struct {
	role  string
	match func(string) bool
}{
	{"alertmanager", func(n string) bool { return strings.Contains(n, "alertmanager_worker") }},
	{"notifier", func(n string) bool { return strings.Contains(n, "notifier") }},
	{"handoff", func(n string) bool { return strings.Contains(n, "handoff") }},
	{"history", func(n string) bool { return strings.Contains(n, "history") }},
	{"taskmanager", func(n string) bool { return strings.HasPrefix(n, "oscar-taskmanager-") }},
}

// RoleOf maps a container name to a worker role, or "" for none.
func RoleOf(name string) string {
	for _, r := range roleRules {
		if r.match(name) {
			return r.role
		}
	}
	return ""
}

// ContainerSeries turns a container_memory_working_set_bytes matrix into
// per-container MiB series keyed by the cAdvisor name label; only containers
// with a worker role are kept.
func ContainerSeries(m targetops.Matrix) map[string][]Point {
	out := map[string][]Point{}
	for _, s := range m {
		name := s.Metric["name"]
		if RoleOf(name) == "" {
			continue
		}
		for _, p := range s.Values {
			out[name] = append(out[name], Point{Time: p.Time, Value: p.Value / (1 << 20)})
		}
		sort.Slice(out[name], func(i, j int) bool { return out[name][i].Time < out[name][j].Time })
	}
	return out
}

type Headline struct {
	Name         string
	Value        float64
	Unit         string
	Limit        *float64
	Pass         bool
	HigherBetter bool `json:",omitempty"`
}

type Percentiles struct {
	P50, P95, P99, ErrorRate float64
	N                        int
}

type PhaseQueueStats struct{ PeakDepth, P95Depth, PeakAge float64 }

type QueueStats struct {
	Phases       map[string]PhaseQueueStats
	DrainSeconds float64
	Drained      bool
}

type LatencyStats struct {
	History, Sink, Correlation Percentiles
	HistoryMissing             int
}

type ContainerMem struct{ SlopeMiBh, NetMiB float64 }

type TaskStats struct {
	Completed, Failed int
	DurationP95       float64
	PreP95            float64
}

type Scorecard struct {
	ScenarioHash, Trigger, RunBy string
	Comparable, CleanupClean     bool
	RegressionPct                float64
	Baseline                     Baseline
	Queue                        map[string]QueueStats
	Latency                      LatencyStats
	Memory                       map[string]map[string]ContainerMem // role -> container
	Correlation, Tickets, Resend Rate
	FrontOffice                  map[string]Percentiles // API-level, per action
	Homelab                      TaskStats
	Headlines                    []Headline
	Verdict                      domain.Verdict
	Reasons                      []string
}

type FOSample struct {
	Seconds float64
	Err     bool
}

type Inputs struct {
	ScenarioHash, Trigger, RunBy string
	Comparable, CleanupClean     bool
	Start, End                   time.Time // End is the end of drain
	Phases                       []scenario.Phase
	Ledger                       []domain.LaneRow
	History                      []oscar.HistoryRecord
	Receipts                     []targetops.Receipt
	QueueDepth, QueueAge         map[string][]Point
	Containers                   map[string][]Point // ContainerSeries output, MiB
	Tickets                      TicketInputs
	Resend                       []ResendCase
	Correlation                  []CorrRun
	// Planned counts; evidence of zero against a positive plan is INCONCLUSIVE.
	PlannedTickets, PlannedResend, PlannedCorrelation int
	FrontOffice                                       map[string][]FOSample // API-level only
	Tasks, PreTasks                                   []targetops.TaskRun
	GuardStopped                                      bool
	NotRun                                            []string
}

func pcts(xs []float64) Percentiles {
	return Percentiles{P50: Percentile(xs, 50), P95: Percentile(xs, 95), P99: Percentile(xs, 99), N: len(xs)}
}

func window(pts []Point, from, to float64) []Point {
	var o []Point
	for _, p := range pts {
		if p.Time >= from && p.Time <= to {
			o = append(o, p)
		}
	}
	return o
}

func net(pts []Point) float64 {
	if len(pts) < 2 {
		return 0
	}
	return pts[len(pts)-1].Value - pts[0].Value
}

// covered: the window has points, starts and ends within 4 steps of its
// bounds, and no gap exceeds 4 steps.
func covered(pts []Point, from, to float64) bool {
	w := window(pts, from, to)
	g := maxGap.Seconds()
	if len(w) < 2 || w[0].Time-from > g || to-w[len(w)-1].Time > g {
		return false
	}
	for i := 1; i < len(w); i++ {
		if w[i].Time-w[i-1].Time > g {
			return false
		}
	}
	return true
}

func queueStats(in Inputs, incon *[]string) map[string]QueueStats {
	out := map[string]QueueStats{}
	if len(in.QueueDepth) == 0 {
		*incon = append(*incon, "queue depth series")
	}
	runFrom, runTo := float64(in.Start.Unix()), float64(in.End.Unix())
	var lastResolve float64
	for _, r := range in.Ledger {
		if r.Kind == "resolve" && accepted(r) && float64(r.SentAt.Unix()) > lastResolve {
			lastResolve = float64(r.SentAt.Unix())
		}
	}
	for q, depth := range in.QueueDepth {
		if !covered(depth, runFrom, runTo) || !covered(in.QueueAge[q], runFrom, runTo) {
			*incon = append(*incon, "queue series coverage for "+q)
			continue
		}
		qs := QueueStats{Phases: map[string]PhaseQueueStats{}}
		for _, ph := range in.Phases {
			from, to := float64(in.Start.Add(ph.Start).Unix()), float64(in.Start.Add(ph.End).Unix())
			var ds, as []float64
			for _, p := range window(depth, from, to) {
				ds = append(ds, p.Value)
			}
			for _, p := range window(in.QueueAge[q], from, to) {
				as = append(as, p.Value)
			}
			qs.Phases[ph.Name] = PhaseQueueStats{PeakDepth: Percentile(ds, 100), P95Depth: Percentile(ds, 95), PeakAge: Percentile(as, 100)}
		}
		if lastResolve == 0 {
			*incon = append(*incon, "no accepted resolve to time the drain from")
			continue
		}
		// the series is covered to the end of drain, so no zero means undrained
		qs.DrainSeconds = depth[len(depth)-1].Time - lastResolve
		for _, p := range depth {
			if p.Time >= lastResolve && p.Value <= 0 {
				qs.Drained, qs.DrainSeconds = true, p.Time-lastResolve
				break
			}
		}
		out[q] = qs
	}
	return out
}

func limitHeadline(name, unit string, v, limit float64, higher bool) Headline {
	h := Headline{Name: name, Value: v, Unit: unit, Limit: &limit, HigherBetter: higher}
	h.Pass = v <= limit
	if higher {
		h.Pass = v >= limit
	}
	return h
}

// Compute builds the scorecard and verdict. FAIL: a breached limit, a covered
// queue that never drained, duplicate/unknown/failed results or a guard stop.
// INCONCLUSIVE: missing or sparse evidence of any kind. Otherwise PASS.
func Compute(in Inputs, lim scenario.Limits) Scorecard {
	c := Scorecard{ScenarioHash: in.ScenarioHash, Trigger: in.Trigger, RunBy: in.RunBy, Comparable: in.Comparable,
		CleanupClean: in.CleanupClean, RegressionPct: lim.RegressionPct, FrontOffice: map[string]Percentiles{},
		Memory: map[string]map[string]ContainerMem{}}
	var fails, incon []string
	fail := func(f string, a ...any) { fails = append(fails, fmt.Sprintf(f, a...)) }

	c.Baseline = reconcileBaseline(in.Ledger)
	if c.Baseline.Accepted == 0 {
		incon = append(incon, "no accepted baseline rows")
	}
	hl, miss := historyLatencies(in.Ledger, in.History)
	c.Latency.History, c.Latency.HistoryMissing = pcts(hl), miss
	c.Latency.Sink = pcts(sinkLatencies(in.Ledger, in.Receipts))
	var corrLat []float64
	c.Correlation, corrLat = reconcileCorrelation(in.Correlation)
	c.Latency.Correlation = pcts(corrLat)
	c.Tickets = reconcileTickets(in.Tickets)
	c.Resend = reconcileResend(in.Resend)
	for name, n := range map[string]int{"History latency": c.Latency.History.N, "sink latency": c.Latency.Sink.N} {
		if n == 0 {
			incon = append(incon, "no "+name+" samples")
		}
	}
	if in.PlannedCorrelation > 0 && c.Latency.Correlation.N == 0 {
		incon = append(incon, "no correlation latency samples")
	}

	c.Queue = queueStats(in, &incon)
	var drain float64
	for q, qs := range c.Queue {
		drain = max(drain, qs.DrainSeconds)
		if !qs.Drained {
			fail("queue %s did not drain", q)
		}
	}

	var h []Headline
	if len(c.Queue) > 0 {
		h = append(h, limitHeadline("queue.drain_seconds", "s", drain, lim.Drain.Seconds(), false))
	}
	h = append(h,
		limitHeadline("latency.history_p95", "s", c.Latency.History.P95, lim.HistoryP95.Seconds(), false),
		Headline{Name: "latency.sink_p95", Unit: "s", Value: c.Latency.Sink.P95, Pass: true},
		Headline{Name: "latency.correlation_p95", Unit: "s", Value: c.Latency.Correlation.P95, Pass: true})

	// Memory: every worker container's working-set slope over 0-120 min.
	from := float64(in.Start.Unix())
	to := min(from+7200, float64(in.End.Unix()))
	names := make([]string, 0, len(in.Containers))
	for n := range in.Containers {
		names = append(names, n)
	}
	sort.Strings(names)
	roleSeen := map[string]bool{}
	for _, n := range names {
		role := RoleOf(n)
		if role == "" {
			continue
		}
		pts := in.Containers[n]
		if !covered(pts, from, to) {
			incon = append(incon, "memory series coverage for "+n)
			continue
		}
		roleSeen[role] = true
		m := ContainerMem{SlopeMiBh: Slope(window(pts, from, to)), NetMiB: net(pts)}
		if c.Memory[role] == nil {
			c.Memory[role] = map[string]ContainerMem{}
		}
		c.Memory[role][n] = m
		h = append(h, limitHeadline("memory."+n+".slope", "MiB/h", m.SlopeMiBh, lim.ContainerSlopeMiBPerHour, false))
	}
	for _, r := range Roles {
		if !roleSeen[r] {
			incon = append(incon, "no memory series for role "+r)
		}
	}

	need := func(name string, planned, expected int, ratio float64, limit float64) {
		switch {
		case expected == 0 && planned > 0, expected < planned:
			incon = append(incon, name+": no evidence for the planned cases")
		case expected == 0:
		default:
			h = append(h, limitHeadline(name+".success", "ratio", ratio, limit, true))
		}
	}
	need("correlation", in.PlannedCorrelation, c.Correlation.Expected, c.Correlation.Ratio, lim.CorrelationSuccess)
	need("tickets", in.PlannedTickets, c.Tickets.Expected, c.Tickets.Ratio, lim.TicketSuccess)
	need("resend", in.PlannedResend, c.Resend.Expected, c.Resend.Ratio, lim.ResendSuccess)
	if c.Tickets.Duplicate > 0 || c.Tickets.Unknown > 0 {
		fail("tickets: %d duplicates, %d unknown", c.Tickets.Duplicate, c.Tickets.Unknown)
	}
	if c.Correlation.Failed+c.Correlation.Unknown > 0 {
		fail("correlation: %d failed or dirty, %d unknown", c.Correlation.Failed, c.Correlation.Unknown)
	}

	var pooled []float64
	for _, action := range FOActions {
		ss := in.FrontOffice[action]
		if len(ss) == 0 {
			incon = append(incon, "front-office samples for "+action)
			continue
		}
		var xs []float64
		errs := 0
		for _, s := range ss {
			xs = append(xs, s.Seconds)
			if s.Err {
				errs++
			}
		}
		p := pcts(xs)
		p.ErrorRate = float64(errs) / float64(len(ss))
		c.FrontOffice[action] = p
		pooled = append(pooled, xs...)
	}
	if len(pooled) > 0 {
		h = append(h, limitHeadline("frontoffice.p95", "s", Percentile(pooled, 95), lim.FrontOfficeP95.Seconds(), false),
			limitHeadline("frontoffice.p99", "s", Percentile(pooled, 99), lim.FrontOfficeP99.Seconds(), false))
	}

	var td, pd []float64
	for _, t := range in.Tasks {
		if s := strings.ToLower(t.Status); strings.Contains(s, "fail") || strings.Contains(s, "error") {
			c.Homelab.Failed++
		} else {
			c.Homelab.Completed++
		}
		td = append(td, t.Duration.Seconds())
	}
	for _, t := range in.PreTasks {
		pd = append(pd, t.Duration.Seconds())
	}
	c.Homelab.DurationP95, c.Homelab.PreP95 = Percentile(td, 95), Percentile(pd, 95)

	c.Headlines = h
	for _, x := range h {
		if !x.Pass {
			fail("%s %.3g %s breaches limit %.3g", x.Name, x.Value, x.Unit, *x.Limit)
		}
	}
	if c.Latency.HistoryMissing > 0 {
		fail("%d accepted alerts missing from History", c.Latency.HistoryMissing)
	}
	if in.GuardStopped {
		fail("a guard stopped the run")
	}
	if !in.CleanupClean {
		incon = append(incon, "cleanup not CLEAN")
	}
	for _, n := range in.NotRun {
		incon = append(incon, "not run: "+n)
	}

	switch {
	case len(fails) > 0:
		c.Verdict, c.Reasons = domain.VerdictFail, fails
	case len(incon) > 0:
		c.Verdict, c.Reasons = domain.VerdictInconclusive, incon
	default:
		c.Verdict = domain.VerdictPass
	}
	return c
}
