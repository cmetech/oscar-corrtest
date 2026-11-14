package score

import (
	"fmt"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Roles are the worker roles whose memory is scored.
var Roles = []string{"alertmanager", "taskmanager", "notifier", "history", "handoff"}

// roleRules is checked in order; the first match wins. notifier precedes
// taskmanager because live names look like oscar-taskmanager-notifier_worker-1.
var roleRules = []struct {
	role  string
	match func(string) bool
}{
	{"alertmanager", func(n string) bool { return strings.HasPrefix(n, "oscar-taskmanager-alertmanager") }},
	{"notifier", func(n string) bool { return strings.Contains(n, "notifier") }},
	{"history", func(n string) bool { return strings.Contains(n, "history") }},
	{"handoff", func(n string) bool { return strings.Contains(n, "handoff") }},
	{"taskmanager", func(n string) bool { return strings.HasPrefix(n, "oscar-taskmanager-") }},
}

// RoleOf maps a container (or job) name to a worker role, or "" for none.
func RoleOf(name string) string {
	for _, r := range roleRules {
		if r.match(name) {
			return r.role
		}
	}
	return ""
}

// GroupByRole sums a matrix per role per timestamp, in MiB. The role comes
// from the first present label among name, container_name, job.
func GroupByRole(m targetops.Matrix) map[string][]Point {
	sums := map[string]map[float64]float64{}
	for _, s := range m {
		name := s.Metric["name"]
		if name == "" {
			name = s.Metric["container_name"]
		}
		if name == "" {
			name = s.Metric["job"]
		}
		role := RoleOf(name)
		if role == "" {
			continue
		}
		if sums[role] == nil {
			sums[role] = map[float64]float64{}
		}
		for _, p := range s.Values {
			sums[role][p.Time] += p.Value / (1 << 20)
		}
	}
	out := map[string][]Point{}
	for role, byT := range sums {
		for t, v := range byT {
			out[role] = append(out[role], Point{Time: t, Value: v})
		}
		sortPoints(out[role])
	}
	return out
}

func sortPoints(p []Point) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].Time < p[j-1].Time; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
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

type RoleMemory struct{ ParentSlopeMiBh, ContainerSlopeMiBh, ParentNetMiB, ContainerNetMiB float64 }

type TaskStats struct {
	Completed, Failed   int
	DurationP95, PreP95 float64
}

type Scorecard struct {
	ScenarioHash, Trigger, RunBy string
	Comparable, CleanupClean     bool
	RegressionPct                float64
	Baseline                     Baseline
	Queue                        map[string]QueueStats
	Latency                      LatencyStats
	Memory                       map[string]RoleMemory
	Correlation, Tickets, Resend Rate
	FrontOffice                  map[string]Percentiles // API-level
	Homelab                      TaskStats
	Headlines                    []Headline
	Verdict                      domain.Verdict
	Reasons                      []string
}

type FOSample struct {
	Seconds float64
	Err     bool
}

// MemSeries is one role's parent anon and container working-set series in MiB.
type MemSeries struct{ Parent, Container []Point }

type Inputs struct {
	ScenarioHash, Trigger, RunBy string
	Comparable, CleanupClean     bool
	Start                        time.Time
	Phases                       []scenario.Phase
	Ledger                       []domain.LaneRow
	History                      []oscar.HistoryRecord
	Receipts                     []targetops.Receipt
	QueueDepth, QueueAge         map[string][]Point
	Memory                       map[string]MemSeries
	Tickets                      TicketInputs
	Resend                       []ResendCase
	Correlation                  []CorrRun
	FrontOffice                  map[string][]FOSample // API-level only
	Tasks, PreTasks              []targetops.TaskRun
	GuardStopped                 bool
	NotRun                       []string // cases that were not run
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

func queueStats(in Inputs) (map[string]QueueStats, []string) {
	out := map[string]QueueStats{}
	var missing []string
	if len(in.QueueDepth) == 0 {
		missing = append(missing, "queue depth series")
	}
	var lastResolve float64
	for _, r := range in.Ledger {
		if r.Kind == "resolve" && accepted(r) && float64(r.SentAt.Unix()) > lastResolve {
			lastResolve = float64(r.SentAt.Unix())
		}
	}
	for q, depth := range in.QueueDepth {
		qs := QueueStats{Phases: map[string]PhaseQueueStats{}, Drained: true}
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
		if lastResolve > 0 && len(depth) > 0 {
			qs.Drained = false
			qs.DrainSeconds = depth[len(depth)-1].Time - lastResolve
			for _, p := range depth {
				if p.Time >= lastResolve && p.Value <= 0 {
					qs.Drained, qs.DrainSeconds = true, p.Time-lastResolve
					break
				}
			}
		}
		out[q] = qs
	}
	return out, missing
}

func limitHeadline(name, unit string, v, limit float64, higher bool) Headline {
	h := Headline{Name: name, Value: v, Unit: unit, Limit: &limit, HigherBetter: higher}
	h.Pass = v <= limit
	if higher {
		h.Pass = v >= limit
	}
	return h
}

// Compute builds the scorecard and verdict. Missing metric series or not-run
// cases make the run INCONCLUSIVE; a breached limit or guard stop is FAIL.
func Compute(in Inputs, lim scenario.Limits) Scorecard {
	c := Scorecard{ScenarioHash: in.ScenarioHash, Trigger: in.Trigger, RunBy: in.RunBy, Comparable: in.Comparable,
		CleanupClean: in.CleanupClean, RegressionPct: lim.RegressionPct, FrontOffice: map[string]Percentiles{}, Memory: map[string]RoleMemory{}}
	var fails, incon []string
	fail := func(f string, a ...any) { fails = append(fails, fmt.Sprintf(f, a...)) }

	c.Baseline = reconcileBaseline(in.Ledger)
	hl, miss := historyLatencies(in.Ledger, in.History)
	c.Latency.History, c.Latency.HistoryMissing = pcts(hl), miss
	c.Latency.Sink = pcts(sinkLatencies(in.Ledger, in.Receipts))
	var corrLat []float64
	c.Correlation, corrLat = reconcileCorrelation(in.Correlation)
	c.Latency.Correlation = pcts(corrLat)
	c.Tickets = reconcileTickets(in.Tickets)
	c.Resend = reconcileResend(in.Resend)

	var qMissing []string
	c.Queue, qMissing = queueStats(in)
	incon = append(incon, qMissing...)
	var drain float64
	for q, qs := range c.Queue {
		drain = max(drain, qs.DrainSeconds)
		if !qs.Drained {
			fail("queue %s did not drain", q)
		}
	}

	h := []Headline{
		limitHeadline("queue.drain_seconds", "s", drain, lim.Drain.Seconds(), false),
		limitHeadline("latency.history_p95", "s", c.Latency.History.P95, lim.HistoryP95.Seconds(), false),
		{Name: "latency.sink_p95", Unit: "s", Value: c.Latency.Sink.P95, Pass: true},
		{Name: "latency.correlation_p95", Unit: "s", Value: c.Latency.Correlation.P95, Pass: true},
	}
	runStart := float64(in.Start.Unix())
	for _, role := range Roles {
		s, ok := in.Memory[role]
		if !ok || len(s.Parent) < 2 || len(s.Container) < 2 {
			incon = append(incon, "memory series for "+role)
			continue
		}
		w := func(p []Point) []Point { return window(p, runStart, runStart+7200) }
		m := RoleMemory{ParentSlopeMiBh: Slope(w(s.Parent)), ContainerSlopeMiBh: Slope(w(s.Container)), ParentNetMiB: net(s.Parent), ContainerNetMiB: net(s.Container)}
		c.Memory[role] = m
		h = append(h,
			limitHeadline("memory."+role+".parent_slope", "MiB/h", m.ParentSlopeMiBh, lim.ParentAnonSlopeMiBPerHour, false),
			limitHeadline("memory."+role+".container_slope", "MiB/h", m.ContainerSlopeMiBh, lim.ContainerSlopeMiBPerHour, false))
	}
	h = append(h,
		limitHeadline("correlation.success", "ratio", c.Correlation.Ratio, lim.CorrelationSuccess, true),
		limitHeadline("tickets.success", "ratio", c.Tickets.Ratio, lim.TicketSuccess, true),
		limitHeadline("resend.success", "ratio", c.Resend.Ratio, lim.ResendSuccess, true))
	if c.Tickets.Duplicate > 0 || c.Tickets.Unknown > 0 {
		fail("tickets: %d duplicates, %d unknown", c.Tickets.Duplicate, c.Tickets.Unknown)
	}
	if c.Correlation.Failed > 0 {
		fail("correlation: %d failed or dirty", c.Correlation.Failed)
	}
	if c.Correlation.Unknown > 0 {
		incon = append(incon, "correlation results inconclusive")
	}

	for action, ss := range in.FrontOffice {
		var xs []float64
		errs := 0
		for _, s := range ss {
			xs = append(xs, s.Seconds)
			if s.Err {
				errs++
			}
		}
		p := pcts(xs)
		if len(ss) > 0 {
			p.ErrorRate = float64(errs) / float64(len(ss))
		}
		c.FrontOffice[action] = p
		if action == "grid" { // the Grid action carries the front-office gates
			h = append(h, limitHeadline("frontoffice.grid_p95", "s", p.P95, lim.FrontOfficeP95.Seconds(), false),
				limitHeadline("frontoffice.grid_p99", "s", p.P99, lim.FrontOfficeP99.Seconds(), false))
		}
	}
	if _, ok := c.FrontOffice["grid"]; !ok {
		incon = append(incon, "front-office grid timings")
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
