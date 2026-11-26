package score

import (
	"fmt"
	"math"
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
	// MaxGapSeconds is the longest unsampled stretch of the run, edges
	// included: the phase peaks are sampled, not continuous. VictoriaMetrics
	// repeats a raw sample over adjacent steps, so the scrape gap behind it can
	// be about one step longer.
	MaxGapSeconds float64
}

type LatencyStats struct {
	History, Sink, Correlation Percentiles
	HistoryMissing             int
}

type ContainerMem struct{ SlopeMiBh, NetMiB float64 }

// TaskStats are the homelab pack's task runs in the window (reported, not
// gated). Collected is false, with NotCollected saying why, when the task
// history was unavailable: never a silent 0/0.
type TaskStats struct {
	Completed, Failed int
	DurationP95       float64
	PreP95            float64
	Collected         bool
	NotCollected      string `json:",omitempty"`
}

type Scorecard struct {
	ObserverConfig               string `json:",omitempty"`
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
	ATT                          *ATT               // v2 only: the AT&T-shape measurements (report-only)
	Fingerprint                  *FingerprintStats  // fingerprint-policy lane facet only
	Offered                      *OfferedRate       `json:",omitempty"` // every lane's posts per minute
	Escalation                   *Rate              `json:",omitempty"` // v4 escalation episodes
	TaskExec                     *TaskExecStats     `json:",omitempty"` // v4 on-demand task runs
	HomelabHealth                *HomelabHealth     `json:",omitempty"` // v4 scheduled homelab job gate
	Reports                      *ReportStats       `json:",omitempty"` // v4 alert reports
	Maintenance                  *MaintenanceStats  `json:",omitempty"` // OSC-189 maintenance-mode lane
	SourceFilters                *SourceFilterStats `json:",omitempty"` // OSC-207 Source / Source IP filters
	Receivers                    *ReceiverStats     `json:",omitempty"` // OSC-207 receivers lane
	SkippedLanes                 []string           `json:",omitempty"` // lanes switched off by run option: never a pass
	Verdict                      domain.Verdict
	EvidenceIncomplete           bool `json:",omitempty"`
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
	// PlannedBaseline is the number of lane plan events (seqs 0..n-1, sorted
	// by schedule) inside the window the lane actually ran; 0 = unknown.
	PlannedBaseline   int
	FrontOffice       map[string][]FOSample // API-level only
	Tasks, PreTasks   []targetops.TaskRun
	TasksCollected    bool   // Tasks/PreTasks came from the task history
	TasksNotCollected string // why they did not
	GuardStopped      bool
	Precheck          scenario.PrecheckLimits // QueueIdle decides drained (ruling C37)
	NotRun            []string
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

func CoveredSeries(pts []Point, from, to time.Time) bool {
	for i, p := range pts {
		if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || math.IsNaN(p.Time) || math.IsInf(p.Time, 0) || i > 0 && p.Time <= pts[i-1].Time {
			return false
		}
	}
	return covered(pts, float64(from.Unix()), float64(to.Unix()))
}

// byTime returns pts sorted by time: series() appends every series that
// shares a queue_name.
func byTime(pts []Point) []Point {
	out := append([]Point(nil), pts...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// reachesEnd: the series has a sample within maxGap of the run end. Gaps
// before then (a starved exporter misses scrapes) leave the phase peaks
// sampled; the drain is judged only where it was sampled (queueStats).
func reachesEnd(pts []Point, from, to float64) bool {
	w := window(pts, from, to)
	return len(w) > 0 && to-w[len(w)-1].Time <= maxGap.Seconds()
}

// largestGap is the longest unsampled stretch of [from, to], edges included.
func largestGap(pts []Point, from, to float64) float64 {
	g, prev := 0.0, from
	for _, p := range window(pts, from, to) {
		g, prev = max(g, p.Time-prev), p.Time
	}
	return max(g, to-prev)
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
	g := maxGap.Seconds()
	for q, raw := range in.QueueDepth {
		depth, age := byTime(raw), byTime(in.QueueAge[q])
		if !reachesEnd(depth, runFrom, runTo) || !reachesEnd(age, runFrom, runTo) {
			*incon = append(*incon, "queue series coverage for "+q)
			continue
		}
		qs := QueueStats{Phases: map[string]PhaseQueueStats{}, MaxGapSeconds: largestGap(depth, runFrom, runTo)}
		for _, ph := range in.Phases {
			from, to := float64(in.Start.Add(ph.Start).Unix()), float64(in.Start.Add(ph.End).Unix())
			var ds, as []float64
			for _, p := range window(depth, from, to) {
				ds = append(ds, p.Value)
			}
			for _, p := range window(age, from, to) {
				as = append(as, p.Value)
			}
			qs.Phases[ph.Name] = PhaseQueueStats{PeakDepth: Percentile(ds, 100), P95Depth: Percentile(ds, 95), PeakAge: Percentile(as, 100)}
		}
		if lastResolve == 0 {
			*incon = append(*incon, "no accepted resolve to time the drain from")
			continue
		}
		after := window(depth, lastResolve, runTo)
		if len(after) == 0 {
			*incon = append(*incon, "queue series coverage for "+q)
			continue
		}
		// The drain ends where the final idle stretch starts (ruling C37: at or
		// below the idle depth). A non-idle sample restarts the stretch, and so
		// does a sample after a gap over maxGap: a backlog can rise and fall
		// unseen inside a gap. A gap therefore only makes the drain read later.
		idleFrom, prev := -1.0, lastResolve
		for _, p := range after {
			switch {
			case !in.Precheck.QueueIdle(p.Value):
				idleFrom = -1
			case idleFrom < 0 || p.Time-prev > g:
				idleFrom = p.Time
			}
			prev = p.Time
		}
		// the series reaches the end, so never idle means undrained
		qs.DrainSeconds = after[len(after)-1].Time - lastResolve
		if idleFrom >= 0 {
			qs.Drained, qs.DrainSeconds = true, idleFrom-lastResolve
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

	c.Baseline = reconcileBaseline(in.Ledger, in.PlannedBaseline)
	if c.Baseline.Accepted == 0 {
		incon = append(incon, "no accepted baseline rows")
	}
	if p := c.Baseline.Planned; p > 0 && c.Baseline.Offered*100 < p*98 {
		fail("baseline_under_delivered: offered %d of %d planned (< 98%%)", c.Baseline.Offered, p)
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
		Headline{Name: "latency.correlation_run_wall_p95", Unit: "s", Value: c.Latency.Correlation.P95, Pass: true})

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

	c.Homelab.Collected, c.Homelab.NotCollected = in.TasksCollected, in.TasksNotCollected
	if !in.TasksCollected && c.Homelab.NotCollected == "" {
		c.Homelab.NotCollected = "task history not collected"
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

	c.EvidenceIncomplete = len(incon) != 0
	switch {
	case len(fails) > 0:
		c.Verdict, c.Reasons = domain.VerdictFail, append(fails, incon...)
	case len(incon) > 0:
		c.Verdict, c.Reasons = domain.VerdictInconclusive, incon
	default:
		c.Verdict = domain.VerdictPass
	}
	return c
}
