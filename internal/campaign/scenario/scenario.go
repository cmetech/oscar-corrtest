// Package scenario defines the frozen, versioned alerting-day campaign scenario.
package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

type Phase struct {
	Name  string        `json:"name"`
	Start time.Duration `json:"start"`
	End   time.Duration `json:"end"`
}

type BaselineSpec struct {
	Rate           float64            `json:"rate"`
	UniqueRatio    float64            `json:"uniqueRatio"`
	PoolSize       int                `json:"poolSize"`
	Instances      int                `json:"instances"`
	Severity       map[string]float64 `json:"severity"`
	ResolveAfter   time.Duration      `json:"resolveAfter"`
	TicketEveryNth int                `json:"ticketEveryNth"`
	TicketFrom     time.Duration      `json:"ticketFrom"`
	TicketTo       time.Duration      `json:"ticketTo"`
	// Burst raises the firing rate to Burst.Rate for [At, At+For); nil keeps
	// the uniform plan (and the scenario hash) unchanged.
	Burst *BurstSpec `json:"burst,omitempty"`
}

// BurstSpec is a timed spike of the baseline firing rate.
type BurstSpec struct {
	At   time.Duration `json:"at"`
	For  time.Duration `json:"for"`
	Rate float64       `json:"rate"`
}

// CaseRef points at a built-in or example scenario by pattern and level
// ("builtin", "basic", "advanced"). The runtime contract requires both cases
// (P01 and its N01 control), so a reference always runs the whole document.
type CaseRef struct {
	Pattern string `json:"pattern"`
	Level   string `json:"level"`
}

type CorrelationSpec struct {
	SetA             []CaseRef `json:"setA"`
	SetB             []CaseRef `json:"setB"`
	TicketedPatterns []string  `json:"ticketedPatterns"`
}

type PolicySpec struct {
	Key        string        `json:"key"`
	Action     string        `json:"action"`
	Mode       string        `json:"mode"`
	Interval   time.Duration `json:"interval"`
	Every      int           `json:"every"`
	MaxResends *int          `json:"maxResends"`
	Fresh      bool          `json:"fresh"`
	Cohort     int           `json:"cohort"`
	Notifier   string        `json:"notifier"`
}

type FrontOfficeSpec struct {
	Users       int           `json:"users"`
	Interval    time.Duration `json:"interval"`
	Jitter      time.Duration `json:"jitter"`
	SearchEvery time.Duration `json:"searchEvery"`
}

type Limits struct {
	HistoryP95               time.Duration `json:"historyP95"`
	Drain                    time.Duration `json:"drain"`
	ContainerSlopeMiBPerHour float64       `json:"containerSlopeMiBPerHour"`
	FrontOfficeP95           time.Duration `json:"frontOfficeP95"`
	FrontOfficeP99           time.Duration `json:"frontOfficeP99"`
	CorrelationSuccess       float64       `json:"correlationSuccess"`
	TicketSuccess            float64       `json:"ticketSuccess"`
	ResendSuccess            float64       `json:"resendSuccess"`
	RegressionPct            float64       `json:"regressionPct"`
	QuerySetVersion          string        `json:"querySetVersion"`
}

type PrecheckLimits struct {
	IdleQueueDepth      int           `json:"idleQueueDepth"`
	IdleQueueAge        time.Duration `json:"idleQueueAge"`
	ClockSkew           time.Duration `json:"clockSkew"`
	TargetDiskFreePct   float64       `json:"targetDiskFreePct"`
	TargetMemAvailPct   float64       `json:"targetMemAvailPct"`
	RunnerDiskFreeGiB   float64       `json:"runnerDiskFreeGiB"`
	MetricsFreshness    time.Duration `json:"metricsFreshness"`
	RestartWindow       time.Duration `json:"restartWindow"`
	HistoryRowsAdvisory int64         `json:"historyRowsAdvisory"`
}

// QueueIdle is the one per-queue idle rule: pre-check B3 (quiet target), the
// drain wait and the scorecard's drained flag all use it (ruling C37).
func (p PrecheckLimits) QueueIdle(depth float64) bool {
	return depth <= float64(p.IdleQueueDepth)
}

// StandingSpec is the AT&T-shaped standing pool (v2): Size alerts on hosts that
// are not in inventory, first posted across one Interval from T0 and then
// re-delivered unchanged every Interval until Until, as native Alertmanager
// does on repeat_interval. Posts go through the Alertmanager webhook route
// (native Alertmanager suppresses unchanged reposts), Concurrency at a time.
type StandingSpec struct {
	Size        int           `json:"size"`
	Instances   int           `json:"instances"`
	Interval    time.Duration `json:"interval"`
	Until       time.Duration `json:"until"`
	Concurrency int           `json:"concurrency"`
}

// BreakerSpec is the forced intake circuit-breaker window (v2, phase
// "breaker"): queue thresholds are overridden to QueueHigh/QueueLow, the
// standing pool is posted at once with BurstConcurrency to trip the breaker,
// and ResolveProbes resolve-only posts check whether resolves are admitted
// while it is tripped. The override is a recorded mutation, restored at the
// phase end and by cleanup.
type BreakerSpec struct {
	QueueHigh        int `json:"queueHigh"`
	QueueLow         int `json:"queueLow"`
	BurstConcurrency int `json:"burstConcurrency"`
	ResolveProbes    int `json:"resolveProbes"`
}

// DeadTargetSpec adds a webhook notifier whose URL refuses connections, so every
// alert leaves a delivery that fails before send (retryable_pre_send) while the
// notifier queue is backed up: the delivery re-publish loop of 2026-11-16.
// MaxCopies gates publishes per delivery; MinQueueAge is the tm_notifier wait
// the run must reach, or it never exercised the loop (INCONCLUSIVE).
type DeadTargetSpec struct {
	Name        string        `json:"name"`
	URL         string        `json:"url"`
	MaxCopies   float64       `json:"maxCopies"`
	MinQueueAge time.Duration `json:"minQueueAge"`
}

// FingerprintSpec is the fingerprint-policy lane: at T0+At a scoped policy
// that ignores one label is published and served (waiting up to Converge),
// Repeats variants of one alert differing only in that label must read back
// from History, Verify later, as one row and fingerprint, and a variant
// differing in device as a second one; then the policy is reverted. When
// OSCAR's fingerprint policies are not commissioned the lane is skipped.
type FingerprintSpec struct {
	At       time.Duration `json:"at"`
	Repeats  int           `json:"repeats"`
	Converge time.Duration `json:"converge"`
	Verify   time.Duration `json:"verify"`
}

type Scenario struct {
	ID          string          `json:"id"`
	Version     string          `json:"version"`
	Custom      bool            `json:"custom"`
	Phases      []Phase         `json:"phases"`
	Baseline    BaselineSpec    `json:"baseline"`
	Correlation CorrelationSpec `json:"correlation"`
	Resend      []PolicySpec    `json:"resend"`
	Redelivery  time.Duration   `json:"redelivery"`
	ResendUntil time.Duration   `json:"resendUntil"`
	FrontOffice FrontOfficeSpec `json:"frontOffice"`
	// v2 facets; nil (and absent from the hash) in v1.
	Standing *StandingSpec `json:"standing,omitempty"`
	Breaker  *BreakerSpec  `json:"breaker,omitempty"`
	// DeadTarget is the delivery-retry isolation facet.
	DeadTarget *DeadTargetSpec `json:"deadTarget,omitempty"`
	// Fingerprint is the fingerprint-policy lane facet; nil runs no lane.
	Fingerprint *FingerprintSpec `json:"fingerprint,omitempty"`
	Limits      Limits           `json:"limits"`
	Precheck    PrecheckLimits   `json:"precheck"`
	Seed        int64            `json:"seed"`
}

// TransportDedupWindow is OSCAR's JetStream duplicate window for ordinary alerts
// (Nats-Msg-Id fingerprint:status:startsAt): an unchanged repost inside it is
// absorbed before the correlator, so unchanged cohorts must redeliver slower.
const TransportDedupWindow = 120 * time.Second

func AlertingDayV1() Scenario {
	min := time.Minute
	var setA, setB []CaseRef
	for _, b := range corr.AllBuiltins() {
		setA = append(setA, CaseRef{Pattern: b.Pattern, Level: "builtin"})
		setB = append(setB, CaseRef{Pattern: b.Pattern, Level: "builtin"})
	}
	for _, e := range corr.AllExamples() {
		setA = append(setA, CaseRef{Pattern: e.Pattern, Level: e.Level})
	}
	n := func(i int) *int { return &i }
	return Scenario{
		ID: "alerting-day", Version: "v1",
		Phases: []Phase{{"preflight", -10 * min, 0}, {"baseline", 0, 30 * min}, {"correlation", 30 * min, 60 * min},
			{"ticketing", 60 * min, 90 * min}, {"resend", 90 * min, 120 * min}, {"drain", 120 * min, 150 * min}},
		Baseline: BaselineSpec{Rate: 3.5, UniqueRatio: 0.9, PoolSize: 100, Instances: 50,
			Severity:     map[string]float64{"critical": 0.05, "major": 0.15, "minor": 0.40, "warning": 0.40},
			ResolveAfter: 15 * min, TicketEveryNth: 20, TicketFrom: 60 * min, TicketTo: 120 * min},
		Correlation: CorrelationSpec{SetA: setA, SetB: setB, TicketedPatterns: []string{"flood", "threshold"}},
		Resend: []PolicySpec{
			{"R1", "notification", "time", 110 * time.Second, 0, n(5), false, 10, "CAMPAIGN_SINK"},
			{"R2", "notification", "occurrences", 0, 3, nil, true, 10, "CAMPAIGN_SINK"},
			{"R3", "notification", "either", 300 * time.Second, 5, n(2), true, 10, "CAMPAIGN_SINK"},
			{"R4", "ticket", "time", 590 * time.Second, 0, n(2), false, 10, "ALERTHISTORY"},
		},
		Redelivery: 130 * time.Second, ResendUntil: 118 * min, // clear of TransportDedupWindow
		FrontOffice: FrontOfficeSpec{Users: 4, Interval: 15 * time.Second, Jitter: 3 * time.Second, SearchEvery: 2 * min},
		// Slope 512 MiB/h (was 100) and front-office p95 5s (was 2s): raised by
		// the OSCAR owner on 2026-11-20; runs before that carry the old hash.
		Limits: Limits{HistoryP95: 40 * time.Second, Drain: 15 * min, ContainerSlopeMiBPerHour: 512,
			FrontOfficeP95: 5 * time.Second, FrontOfficeP99: 5 * time.Second, CorrelationSuccess: 1, TicketSuccess: 1, ResendSuccess: 1,
			RegressionPct: 20, QuerySetVersion: "q1"},
		Precheck: PrecheckLimits{IdleQueueDepth: 50, IdleQueueAge: 30 * time.Second, ClockSkew: 250 * time.Millisecond,
			TargetDiskFreePct: 20, TargetMemAvailPct: 25, RunnerDiskFreeGiB: 10, MetricsFreshness: 60 * time.Second, RestartWindow: 30 * min, HistoryRowsAdvisory: 5_000_000},
		Seed: 20261113,
	}
}

// AlertingDayV2 is v1 plus the AT&T 26.4.4 shape (2026-11-16 capacity findings):
// a standing pool of 2,000 alerts on hosts not in inventory, repeated unchanged
// every 10 minutes (~3.3/s on top of the 3.5/s baseline, ~6.8/s against AT&T's
// 7.8/s), and a forced breaker window at T+120-130 before a 20-minute drain.
// The baseline lane runs to the drain start (T+130), so Alertmanager traffic
// meets the tripped breaker. Query set q2 adds the per-alert cost budgets.
func AlertingDayV2() Scenario {
	min := time.Minute
	s := AlertingDayV1()
	s.Version = "v2"
	s.Phases = []Phase{{"preflight", -10 * min, 0}, {"baseline", 0, 30 * min}, {"correlation", 30 * min, 60 * min},
		{"ticketing", 60 * min, 90 * min}, {"resend", 90 * min, 120 * min}, {"breaker", 120 * min, 130 * min}, {"drain", 130 * min, 150 * min}}
	s.Standing = &StandingSpec{Size: 2000, Instances: 500, Interval: 10 * min, Until: 120 * min, Concurrency: 8}
	s.Breaker = &BreakerSpec{QueueHigh: 100, QueueLow: 10, BurstConcurrency: 32, ResolveProbes: 50}
	s.Limits.QuerySetVersion = "q2"
	return s
}

// AlertingDaySmokeV1 is the bounded live smoke (spec 13.11, plan C19 step 6):
// about 12 minutes (phases of 2 minutes; resend 4, since policy activation
// waits ~90s for OSCAR cutover and does not scale), baseline 1/s with 2 planned
// tickets, one resend policy and one set A document of 2 correlation cases
// (flood: P01 and N01), through the same engine paths as v1. It is Custom,
// so it is never comparable and never enters the reference or the trend.
func AlertingDaySmokeV1() Scenario {
	min := time.Minute
	n := func(i int) *int { return &i }
	s := AlertingDayV1() // same limits, pre-check limits, front office and seed
	s.ID, s.Custom = "alerting-day-smoke", true
	s.Phases = []Phase{{"preflight", -2 * min, 0}, {"baseline", 0, 2 * min}, {"correlation", 2 * min, 4 * min},
		{"ticketing", 4 * min, 6 * min}, {"resend", 6 * min, 10 * min}, {"drain", 10 * min, 12 * min}}
	s.Baseline = BaselineSpec{Rate: 1, UniqueRatio: 0.9, PoolSize: 100, Instances: 50,
		Severity:     map[string]float64{"critical": 0.05, "major": 0.15, "minor": 0.40, "warning": 0.40},
		ResolveAfter: min, TicketEveryNth: 100, TicketFrom: 4 * min, TicketTo: 8 * min} // seed 20261113: 2 tickets
	s.Correlation = CorrelationSpec{SetA: []CaseRef{{Pattern: "flood", Level: "builtin"}}, TicketedPatterns: []string{"flood", "threshold"}}
	s.Resend = []PolicySpec{{"R1", "notification", "time", 50 * time.Second, 0, n(2), false, 2, "CAMPAIGN_SINK"}}
	s.ResendUntil = 9*min + 48*time.Second // v1 stops cohorts just before drain; ~3 deliveries after activation
	return s
}

// AlertingDaySmokeV2 is the v1 smoke plus a small standing pool (100 alerts
// re-delivered every 2 minutes) and a 2-minute forced breaker window before a
// 2-minute drain: about 14 minutes, proving the v2 facets live (including the
// rate-limit permission) before a 2.5-hour v2 run. Custom: never comparable.
func AlertingDaySmokeV2() Scenario {
	min := time.Minute
	s := AlertingDaySmokeV1()
	s.Version = "v2"
	s.Phases = []Phase{{"preflight", -2 * min, 0}, {"baseline", 0, 2 * min}, {"correlation", 2 * min, 4 * min},
		{"ticketing", 4 * min, 6 * min}, {"resend", 6 * min, 10 * min}, {"breaker", 10 * min, 12 * min}, {"drain", 12 * min, 14 * min}}
	s.Standing = &StandingSpec{Size: 100, Instances: 25, Interval: 2 * min, Until: 10 * min, Concurrency: 4}
	s.Breaker = &BreakerSpec{QueueHigh: 100, QueueLow: 10, BurstConcurrency: 32, ResolveProbes: 10}
	s.Limits.QuerySetVersion = "q2"
	return s
}

// DeliveryRetryV1 isolates the delivery re-publish loop (oscar #57) in about 25
// minutes: an 8-minute lane at 8 alerts/s (each resolved after 2 minutes) backs
// up tm_notifier while a dead webhook target fails every delivery before send,
// then a 15-minute drain. It passes when publishes per delivery stay at or below
// MaxCopies and the queue drains; History, memory and front-office limits are
// loose because the backlog is deliberate. Custom: never comparable.
func DeliveryRetryV1() Scenario {
	min := time.Minute
	s := AlertingDayV1()
	s.ID, s.Version, s.Custom = "delivery-retry", "v1", true
	s.Phases = []Phase{{"preflight", -2 * min, 0}, {"baseline", 0, 8 * min}, {"drain", 8 * min, 23 * min}}
	s.Baseline = BaselineSpec{Rate: 8, UniqueRatio: 0.9, PoolSize: 100, Instances: 50,
		Severity: map[string]float64{"critical": 0.05, "major": 0.15, "minor": 0.40, "warning": 0.40}, ResolveAfter: 2 * min}
	s.Correlation, s.Resend, s.ResendUntil = CorrelationSpec{}, nil, 0
	s.DeadTarget = &DeadTargetSpec{Name: "CAMPAIGN_DEAD", URL: "https://load.home.arpa:9/dead",
		MaxCopies: 1.2, MinQueueAge: 100 * time.Second}
	s.Limits = Limits{HistoryP95: 30 * min, Drain: 15 * min, ContainerSlopeMiBPerHour: 1e6,
		FrontOfficeP95: min, FrontOfficeP99: 2 * min, QuerySetVersion: "q2"}
	return s
}

// ResendOnlyV1 isolates v1's four resend policies (time, occurrences, either,
// ticket) in about 52 minutes: a 5 alerts/s lane with no baseline tickets or
// correlation, a 30-minute resend phase from T+5 (activation waits ~90 s for
// cutover), then a 15-minute drain. Policies, redelivery and limits are v1's.
// Custom: never comparable.
func ResendOnlyV1() Scenario {
	min := time.Minute
	s := AlertingDayV1()
	s.ID, s.Version, s.Custom = "resend-only", "v1", true
	s.Phases = []Phase{{"preflight", -2 * min, 0}, {"baseline", 0, 5 * min}, {"resend", 5 * min, 35 * min}, {"drain", 35 * min, 50 * min}}
	s.Baseline.Rate = 5
	s.Baseline.TicketEveryNth, s.Baseline.TicketFrom, s.Baseline.TicketTo = 0, 0, 0
	s.Correlation = CorrelationSpec{}
	s.ResendUntil = 33 * min // as v1: cohorts stop 2 minutes before drain
	return s
}

// FunctionalV1 checks every functional lane at once under a moderate 5/s
// base load (Corey, 2026-11-17: overlapping, not sequential): the baseline
// with planned tickets (T+5-45), correlation set A (T+5-35) then the ticketed
// set B loop until drain, the four v1 resend policies (T+5-35) and the
// fingerprint-policy lane (T+10; SKIPPED where fingerprint policies are not
// enabled). No standing pool or forced breaker: those deliberately disrupt
// the other lanes. Limits are v1's. Custom: never comparable.
func FunctionalV1() Scenario {
	min := time.Minute
	s := AlertingDayV1()
	s.ID, s.Version, s.Custom = "functional", "v1", true
	s.Phases = []Phase{{"preflight", -2 * min, 0}, {"baseline", 0, 50 * min}, {"correlation", 5 * min, 35 * min},
		{"ticketing", 5 * min, 50 * min}, {"resend", 5 * min, 35 * min}, {"drain", 50 * min, 65 * min}}
	s.Baseline.Rate = 5
	s.Baseline.TicketFrom, s.Baseline.TicketTo = 5*min, 45*min
	s.ResendUntil = 33 * min // as v1: cohorts stop 2 minutes before the resend phase ends
	s.Fingerprint = &FingerprintSpec{At: 10 * min, Repeats: 5, Converge: 2 * min, Verify: time.Minute}
	return s
}

// FunctionalBurstV1 is functional@v1 with a 5-minute spike at T+25-30 (all
// lanes running) at half the 32-core target (10/s with 25/s spikes), sized
// for the 20-core homelab: 5/s base, 12.5/s spike. Custom: never comparable.
func FunctionalBurstV1() Scenario {
	s := FunctionalV1()
	s.ID = "functional-burst"
	s.Baseline.Burst = &BurstSpec{At: 25 * time.Minute, For: 5 * time.Minute, Rate: 12.5}
	return s
}

// FunctionalBurstFullV1 is the 32-core target profile: 10/s base with a
// 5-minute 25/s spike at T+25-30, functional@v1's lanes and spread.
func FunctionalBurstFullV1() Scenario {
	s := FunctionalBurstV1()
	s.ID = "functional-burst-full"
	s.Baseline.Rate = 10
	s.Baseline.Burst.Rate = 25
	return s
}

// Builtins are the built-in campaign scenarios.
func Builtins() []Scenario {
	return []Scenario{AlertingDayV1(), AlertingDayV2(), AlertingDaySmokeV1(), AlertingDaySmokeV2(), DeliveryRetryV1(), ResendOnlyV1(), FunctionalV1(), FunctionalBurstV1(), FunctionalBurstFullV1()}
}

// Lookup returns the built-in scenario named "id@version".
func Lookup(ref string) (Scenario, bool) {
	for _, s := range Builtins() {
		if s.ID+"@"+s.Version == ref {
			return s, true
		}
	}
	return Scenario{}, false
}

// ByHash returns the built-in scenario with this content hash.
func ByHash(h string) (Scenario, bool) {
	for _, s := range Builtins() {
		if h != "" && s.Hash() == h {
			return s, true
		}
	}
	return Scenario{}, false
}

// Phase returns the named phase, or the zero Phase.
func (s Scenario) Phase(name string) Phase {
	for _, p := range s.Phases {
		if p.Name == name {
			return p
		}
	}
	return Phase{}
}

// Hash is the hex sha256 of canonical JSON: sorted keys, durations as integer milliseconds.
func (s Scenario) Hash() string {
	b, _ := json.Marshal(canon(reflect.ValueOf(s))) // maps marshal with sorted keys
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var durType = reflect.TypeOf(time.Duration(0))

// canon converts a value to JSON-ready any, using json tags and ms for durations.
func canon(v reflect.Value) any {
	switch {
	case v.Type() == durType:
		return v.Int() / int64(time.Millisecond)
	case v.Kind() == reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return canon(v.Elem())
	case v.Kind() == reflect.Struct:
		m := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			tag := strings.Split(v.Type().Field(i).Tag.Get("json"), ",")
			if len(tag) > 1 && tag[1] == "omitempty" && v.Field(i).IsZero() {
				continue // a facet a scenario does not use leaves its hash unchanged
			}
			m[tag[0]] = canon(v.Field(i))
		}
		return m
	case v.Kind() == reflect.Slice:
		if v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = canon(v.Index(i))
		}
		return out
	case v.Kind() == reflect.Map:
		m := map[string]any{}
		for _, k := range v.MapKeys() {
			m[k.String()] = canon(v.MapIndex(k))
		}
		return m
	}
	return v.Interface()
}
