// Package scenario defines the frozen, versioned alerting-day campaign scenario.
package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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
	// Bursts are further spikes (v4 has two); empty keeps the hash unchanged.
	Bursts []BurstSpec `json:"bursts,omitempty"`
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
	// Cycle (v4) loops set A then set B from the correlation phase start
	// until the drain, instead of set A once and then set B from ticketing.
	Cycle bool `json:"cycle,omitempty"`
	// ParentTickets (v4) tickets a ticketed run's synthetic parent, not its
	// first trigger alert.
	ParentTickets bool `json:"parentTickets,omitempty"`
	// Rate (v4) paces the cycling documents to about Rate firing source
	// alerts/s (SpikeRate inside the baseline bursts), with up to Parallel
	// documents in flight at once. Zero keeps one document at a time.
	Rate      float64 `json:"rate,omitempty"`
	SpikeRate float64 `json:"spikeRate,omitempty"`
	Parallel  int     `json:"parallel,omitempty"`
}

type EscalationSpec struct {
	After          time.Duration `json:"after"`
	TargetSeverity string        `json:"targetSeverity"`
}

type PolicySpec struct {
	Key        string          `json:"key"`
	Action     string          `json:"action"`
	Mode       string          `json:"mode"`
	Interval   time.Duration   `json:"interval"`
	Every      int             `json:"every"`
	MaxResends *int            `json:"maxResends"`
	Fresh      bool            `json:"fresh"`
	Cohort     int             `json:"cohort"`
	Notifier   string          `json:"notifier"`
	Escalation *EscalationSpec `json:"escalation,omitempty"`
	// Episode > 0 (v4 escalation cohort) resolves each member and fires it
	// again with a fresh startsAt every Episode, so every member opens a new
	// OSCAR episode (and a new escalation) throughout the run. Such a cohort
	// is scored on its escalations, not on the resend model.
	Episode time.Duration `json:"episode,omitempty"`
}

// TaskExecSpec (v4) runs Job (a homelab pack task, by name) through OSCAR's
// task API every Every from At until the drain, as the first front-office
// user, without waiting for earlier runs; every BurstEvery-th request issues
// Burst concurrent runs. A run must reach a terminal state within Timeout.
type TaskExecSpec struct {
	Job        string        `json:"job"`
	At         time.Duration `json:"at"`
	Every      time.Duration `json:"every"`
	Timeout    time.Duration `json:"timeout"`
	BurstEvery int           `json:"burstEvery,omitempty"`
	Burst      int           `json:"burst,omitempty"`
}

// TaskRequest is one request of the task lane: its offset from T0 and how
// many concurrent runs it issues.
type TaskRequest struct {
	At   time.Duration
	Runs int
}

// Requests is the lane's schedule over a load window ending at loadEnd: a
// request every Every from At while a run can still reach its Timeout before
// loadEnd (so every result is load evidence and the drain starts on time).
func (t TaskExecSpec) Requests(loadEnd time.Duration) []TaskRequest {
	var out []TaskRequest
	for k := 0; t.Every > 0 || k == 0; k++ {
		at := t.At + time.Duration(k)*t.Every
		if at+t.Timeout >= loadEnd {
			break
		}
		n := 1
		if t.BurstEvery > 0 && (k+1)%t.BurstEvery == 0 {
			n = max(t.Burst, 1)
		}
		out = append(out, TaskRequest{At: at, Runs: n})
	}
	return out
}

// ReportSpec (v4) requests the alert report Key (alert history from T0 to the
// request) at each At offset, as the first front-office user, and verifies
// its artifact in object storage. A run must be ready within Timeout.
type ReportSpec struct {
	Key     string          `json:"key"`
	At      []time.Duration `json:"at"`
	Timeout time.Duration   `json:"timeout"`
}

// MaintenanceSpec (OSC-189) is the maintenance-mode lane: at At, a campaign
// pattern, template and scheduled window for the alerthistory handler are
// created; the window runs [Start, Start+For). Probes are posted every Every:
// matching ones from Start+5s while they can still be processed Lag before the
// end, non-matching controls alongside, and matching ones for a minute after
// the end. Probes are not offered load.
type MaintenanceSpec struct {
	At    time.Duration `json:"at"`
	Start time.Duration `json:"start"`
	For   time.Duration `json:"for"`
	Every time.Duration `json:"every"`
	Lag   time.Duration `json:"lag"`
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
	// v4 lanes; zero (and absent from the hash) elsewhere.
	TaskSuccess float64       `json:"taskSuccess,omitempty"` // on-demand task runs that succeed
	TaskP95     time.Duration `json:"taskP95,omitempty"`     // request to terminal result
	// HomelabFailRise > 0 gates the homelab pack's scheduled job failure rate
	// during load against the 30 minutes before T0 (and stopped job classes).
	HomelabFailRise float64 `json:"homelabFailRise,omitempty"`
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
	// Every > 0 (v4) repeats the round every Every, on fresh devices and with
	// the policy kept on, until a round can no longer verify before the drain;
	// each round's alerts are resolved after its read-back. Verify < Every.
	Every time.Duration `json:"every,omitempty"`
}

// NativeCohort identifies a distinct source-delivery path in the qualification workload.
type NativeCohort string

const (
	NativeDirectNoPolicy  NativeCohort = "direct_no_policy"
	NativeCanonicalFirst  NativeCohort = "canonical_first_no_policy"
	NativeCanonicalRepeat NativeCohort = "canonical_repeat_no_policy"
)

// NativeWorkloadSpec records the paths that must be observed through real
// native routes. Enrollment here refers to canonical identity, not a policy.
type NativeWorkloadSpec struct {
	Cohorts           []NativeCohort `json:"cohorts"`
	ManagedTL1        bool           `json:"managedTL1,omitempty"`
	ConfiguredPolicy  bool           `json:"configuredPolicy,omitempty"`
	ManagedPolicy     *PolicySpec    `json:"managedPolicy,omitempty"`
	ManagedRepeat     *PolicySpec    `json:"managedRepeat,omitempty"`
	ManagedSegment    time.Duration  `json:"managedSegment,omitempty"`
	ManagedPriorities map[string]int `json:"managedPriorities,omitempty"`
	ManagedShare      float64        `json:"managedShare,omitempty"`
	ManagedRate       float64        `json:"managedRate,omitempty"`
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
	// Tasks and Report are the v4 on-demand task and alert-report lanes.
	Tasks  *TaskExecSpec `json:"tasks,omitempty"`
	Report *ReportSpec   `json:"report,omitempty"`
	// Paced (v4) spreads the controller lanes' posts evenly (cohort members
	// across Redelivery, fingerprint posts across half a round) so no minute
	// exceeds the offered-rate budget, and reports offered posts per minute.
	Paced          bool                `json:"paced,omitempty"`
	LoadEnd        time.Duration       `json:"loadEnd,omitempty"`
	NativeWorkload *NativeWorkloadSpec `json:"nativeWorkload,omitempty"`
	Limits         Limits              `json:"limits"`
	Precheck       PrecheckLimits      `json:"precheck"`
	Seed           int64               `json:"seed"`

	// OfferedRate and OfferedSpikeRate are the total offered firing rate
	// over every lane, and inside a burst, of a Paced scenario; zero means
	// v4's (V4TotalRate, V4SpikeRate), so v4's hash is unchanged.
	OfferedRate      float64 `json:"offeredRate,omitempty"`
	OfferedSpikeRate float64 `json:"offeredSpikeRate,omitempty"`
	// SmoothCorrelationOffers (OSC-189) paces a cycling correlation lane so
	// every minute lands near its plan (engine smoothCorrelation) instead of
	// starting documents as pacing credit allows; false keeps that pacer.
	SmoothCorrelationOffers bool `json:"smoothCorrelationOffers,omitempty"`
	// Maintenance is the maintenance-mode lane facet; nil runs no lane.
	Maintenance *MaintenanceSpec `json:"maintenance,omitempty"`
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
			{"R1", "notification", "time", 110 * time.Second, 0, n(5), false, 10, "CAMPAIGN_SINK", nil, 0},
			{"R2", "notification", "occurrences", 0, 3, nil, true, 10, "CAMPAIGN_SINK", nil, 0},
			{"R3", "notification", "either", 300 * time.Second, 5, n(2), true, 10, "CAMPAIGN_SINK", nil, 0},
			{"R4", "ticket", "time", 590 * time.Second, 0, n(2), false, 10, "ALERTHISTORY", nil, 0},
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
	s.Resend = []PolicySpec{{"R1", "notification", "time", 50 * time.Second, 0, n(2), false, 2, "CAMPAIGN_SINK", nil, 0}}
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

// AlertingDayControlV1 freezes the ordinary no-policy control for both builds.
func AlertingDayControlV1() Scenario {
	s := AlertingDayV2()
	s.ID, s.Version = "alerting-day-control", "v1"
	s.LoadEnd = 150 * time.Minute
	s.Phases = append(s.Phases[:4],
		Phase{"sustained_load", 90 * time.Minute, s.LoadEnd},
		Phase{"drain", s.LoadEnd, s.LoadEnd + 30*time.Minute})
	s.Standing.Until = s.LoadEnd
	s.Breaker = nil
	s.Resend, s.Redelivery, s.ResendUntil = nil, 0, 0
	s.NativeWorkload = &NativeWorkloadSpec{Cohorts: []NativeCohort{
		NativeDirectNoPolicy, NativeCanonicalFirst, NativeCanonicalRepeat,
	}}
	s.Limits.FrontOfficeP95 = 2 * time.Second
	s.Limits.RegressionPct = 5
	return s
}

// AlertingDayV3 retains the ordinary load and adds native-backed policy cohorts.
func AlertingDayV3() Scenario {
	s := AlertingDayControlV1()
	s.ID, s.Version = "alerting-day", "v3"
	v2 := AlertingDayV2()
	s.Phases[4] = v2.Phase("resend")
	s.Phases = append(s.Phases[:5], Phase{"sustained_load", 120 * time.Minute, s.LoadEnd}, s.Phase("drain"))
	s.Resend, s.Redelivery, s.ResendUntil = v2.Resend, v2.Redelivery, v2.ResendUntil
	s.NativeWorkload.ConfiguredPolicy, s.NativeWorkload.ManagedTL1 = true, true
	s.NativeWorkload.ManagedPolicy = &PolicySpec{Key: "TL1", Action: "notification", Mode: "time", Interval: 60 * time.Second, Notifier: "CAMPAIGN_SINK", Escalation: &EscalationSpec{After: 3 * time.Minute, TargetSeverity: "critical"}}
	s.NativeWorkload.ManagedRepeat = &PolicySpec{Key: "REPEAT", Action: "notification", Mode: "time", Interval: time.Minute, Notifier: "CAMPAIGN_SINK"}
	s.NativeWorkload.ManagedSegment = 10 * time.Minute
	s.NativeWorkload.ManagedPriorities = map[string]int{"TL1": 5, "REPEAT": 6, "EDIT": 7}
	s.NativeWorkload.ManagedRate = 0.2
	s.Limits.RegressionPct = 10
	return s
}

// AlertingDaySmokeV3 is a bounded, non-comparable run of the V3 paths.
func AlertingDaySmokeV3() Scenario {
	s := AlertingDaySmokeV2()
	v3 := AlertingDayV3()
	s.Version = "v3"
	s.LoadEnd = 20 * time.Minute
	s.Standing.Until = s.LoadEnd
	s.Phases = append(s.Phases[:5], Phase{"sustained_load", 10 * time.Minute, s.LoadEnd}, Phase{"drain", s.LoadEnd, s.LoadEnd + 2*time.Minute})
	s.Breaker = nil
	s.NativeWorkload, s.Limits = v3.NativeWorkload, v3.Limits
	return s
}

// ManagedAlertsSmokeV1 is a bounded functional lane for real TL1 source
// actions. It shares V3's managed source path and does not carry ordinary load.
func ManagedAlertsSmokeV1() Scenario {
	s := AlertingDaySmokeV3()
	s.ID, s.Version = "managed-alerts-smoke", "v1"
	s.LoadEnd = 20 * time.Minute
	s.Phases = []Phase{{"preflight", -2 * time.Minute, 0}, {"managed_lifecycle", 0, s.LoadEnd},
		{"drain", s.LoadEnd, s.LoadEnd + 5*time.Minute}}
	s.Baseline, s.Correlation, s.Standing = BaselineSpec{}, CorrelationSpec{}, nil
	s.Resend, s.Redelivery, s.ResendUntil = nil, 0, 0
	s.NativeWorkload.Cohorts = nil
	s.NativeWorkload.ManagedRate = 0
	s.NativeWorkload.ManagedRepeat = nil
	s.NativeWorkload.ManagedSegment = 0
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

// V4 offered-rate budget. The v4 rates (5/s, 10/s spikes) are the TOTAL
// firing posts per second over every lane; the worker lane carries what the
// controller lanes leave. Resolves are posted on top and reported per minute.
const (
	V4TotalRate = 5.0
	V4SpikeRate = 10.0
	// CorrelationBudget bounds an unpaced cycling correlation lane (one
	// document at a time: one pass of the 32 v1 documents offers 155 firing
	// source alerts and each observes at least 45 s, so at most ~0.11/s).
	// Synthetic parents are OSCAR's, not offered.
	CorrelationBudget = 0.15
)

// ControllerFiringRate is the planned firing rate of the controller lanes
// (correlation, resend cohorts and the fingerprint rounds) that the v4 worker
// lane leaves room for, outside the bursts.
func (s Scenario) ControllerFiringRate() float64 { return s.controllerRate(false) }

// ControllerSpikeRate is ControllerFiringRate inside a burst, where the
// correlation lane runs at its SpikeRate.
func (s Scenario) ControllerSpikeRate() float64 { return s.controllerRate(true) }

func (s Scenario) controllerRate(spike bool) float64 {
	rate := 0.0
	switch c := s.Correlation; {
	case c.Cycle && spike && c.SpikeRate > 0:
		rate += c.SpikeRate
	case c.Cycle && c.Rate > 0:
		rate += c.Rate
	case c.Cycle:
		rate += CorrelationBudget
	}
	if s.Redelivery > 0 {
		for _, p := range s.Resend {
			rate += float64(p.Cohort) / s.Redelivery.Seconds()
		}
	}
	if f := s.Fingerprint; f != nil && f.Every > 0 {
		rate += float64(f.Repeats+1) / f.Every.Seconds()
	}
	return rate
}

// OfferedTotals is the planned total offered firing rate over every lane, and
// inside a burst: OfferedRate and OfferedSpikeRate, each defaulting to v4's.
func (s Scenario) OfferedTotals() (total, spike float64) {
	total, spike = s.OfferedRate, s.OfferedSpikeRate
	if total == 0 {
		total = V4TotalRate
	}
	if spike == 0 {
		spike = V4SpikeRate
	}
	return total, spike
}

// fillWorker sets the worker lane to what the controller lanes leave of the
// total and the spike total (OfferedTotals), rounded down to 0.01/s.
func (s *Scenario) fillWorker() {
	total, spike := s.OfferedTotals()
	s.Baseline.Rate = math.Floor((total-s.ControllerFiringRate())*100+1e-6) / 100
	for i := range s.Baseline.Bursts {
		s.Baseline.Bursts[i].Rate = math.Floor((spike-s.ControllerSpikeRate())*100+1e-6) / 100
	}
}

// AlertingDayV4 is the 150-minute mixed-load gate for the single-node target
// (Corey, 2026-11-24: 3-5 alerts/s sustained, spikes to 10/s): every lane
// runs concurrently instead of v3's sequential phases, then the 30-minute
// drain. Total offered firing rate is 5/s, and 10/s for 5 minutes at T+45
// and T+105. Shares of the 5/s (and of the 10/s in a spike):
//   - correlation (20%): set A and set B cycled from T+1 to the drain,
//     paced to 1 alert/s (2/s in a spike: firing source alerts plus each
//     run's label-survival probe) with up to 64 documents in flight; each
//     case scored as in v1. Every run's names carry its own token, so
//     parallel runs cannot satisfy or poison each other;
//   - tickets (~5%): every 12th new worker alert, plus each ticketed set B
//     run's synthetic parent (flood, threshold) and the R4 cohort;
//   - fingerprint (5%): a 15-alert round every minute under one scoped
//     policy (14 variants posted over 30 s that must collapse, one other
//     device that must not), reverted at the end;
//   - resend (10%): 65 cohort members re-delivered every 130 s, paced: v1's
//     R1-R4 (13 each) and R5, a configured time policy that escalates to
//     critical after 4 minutes, its 13 members posted through native
//     Alertmanager (escalation needs a native episode) and starting a new
//     episode every 15 minutes;
//   - the worker lane: the rest, 3.25/s (7.25/s in a spike), v1's mix;
//   - front-office API users as v1, a homelab task run through the task API
//     every minute from T+1 to T+145 (each run may take its 4-minute timeout
//     inside the load window), every 5th request 3 runs at once, and an
//     alert report at T+60 and T+120. Task requests are not alerts.
//
// The homelab pack's scheduled jobs are gated: their failure rate during load
// may rise at most 5 points over the 30 minutes before T0, and no job class
// may stop running.
//
// No v2 standing pool or forced breaker: the pool repeats are v1's 10%.
func AlertingDayV4() Scenario {
	min := time.Minute
	s := AlertingDayV1()
	s.Version = "v4"
	s.Phases = []Phase{{"preflight", -10 * min, 0}, {"baseline", 0, 150 * min}, {"correlation", min, 150 * min},
		{"ticketing", 0, 150 * min}, {"resend", 2 * min, 148 * min}, {"drain", 150 * min, 180 * min}}
	s.Baseline.TicketEveryNth, s.Baseline.TicketFrom, s.Baseline.TicketTo = 12, 0, 150*min
	s.Baseline.Bursts = []BurstSpec{{At: 45 * min, For: 5 * min}, {At: 105 * min, For: 5 * min}}
	s.Paced = true
	s.Correlation.Cycle, s.Correlation.ParentTickets = true, true
	s.Correlation.Rate, s.Correlation.SpikeRate, s.Correlation.Parallel = 1, 2, 64
	for i := range s.Resend {
		s.Resend[i].Cohort = 13
	}
	s.Resend = append(s.Resend, PolicySpec{Key: "R5", Action: "notification", Mode: "time", Interval: 300 * time.Second, Cohort: 13,
		Notifier: "CAMPAIGN_SINK", Escalation: &EscalationSpec{After: 4 * min, TargetSeverity: "critical"}, Episode: 15 * min})
	s.ResendUntil = 146 * min
	s.Fingerprint = &FingerprintSpec{At: 2 * min, Repeats: 14, Converge: 2 * min, Verify: 55 * time.Second, Every: min}
	s.Tasks = &TaskExecSpec{Job: "monitor:check_connection:homelab", At: min, Every: min, Timeout: 4 * min, BurstEvery: 5, Burst: 3}
	s.Report = &ReportSpec{Key: "core-alarm-history-dump", At: []time.Duration{60 * min, 120 * min}, Timeout: 20 * min}
	s.Limits = AlertingDayV3().Limits
	s.Limits.TaskSuccess, s.Limits.TaskP95, s.Limits.HomelabFailRise = 1, 2*min, 0.05
	s.fillWorker()
	return s
}

// AlertingDaySmokeV4 compresses v4 into about 15 minutes (12 of load at the
// same 5/s and 10/s totals, 1-minute spikes at T+4 and T+7, then a 3-minute
// drain): one set A and one ticketed set B document cycled at 0.2 alerts/s
// (0.4/s in a spike), up to 8 in flight so 90 s runs (OSCAR slow) still hold
// the spike rate (OSC-155). Both spikes end by T+8, when the cycling
// documents stop (engine cycleMargin before the drain). R1 and the R5
// escalation cohort (2 members each, escalating after 2 minutes),
// fingerprint rounds of 6, a task every minute from T+1 to T+7 with one
// 3-run burst at T+5, and one report; the worker lane fills the rest.
// Custom: never comparable.
func AlertingDaySmokeV4() Scenario {
	min := time.Minute
	n := func(i int) *int { return &i }
	s := AlertingDayV4()
	s.ID, s.Custom = "alerting-day-smoke", true
	s.Phases = []Phase{{"preflight", -2 * min, 0}, {"baseline", 0, 12 * min}, {"correlation", min, 12 * min},
		{"ticketing", 0, 12 * min}, {"resend", min, 11 * min}, {"drain", 12 * min, 15 * min}}
	s.Baseline.ResolveAfter = min
	s.Baseline.TicketEveryNth, s.Baseline.TicketTo = 100, 12*min
	s.Baseline.Bursts = []BurstSpec{{At: 4 * min, For: min}, {At: 7 * min, For: min}}
	s.Correlation = CorrelationSpec{SetA: []CaseRef{{Pattern: "flood", Level: "builtin"}}, SetB: []CaseRef{{Pattern: "threshold", Level: "builtin"}},
		TicketedPatterns: []string{"flood", "threshold"}, Cycle: true, ParentTickets: true, Rate: 0.2, SpikeRate: 0.4, Parallel: 8}
	s.Resend = []PolicySpec{{Key: "R1", Action: "notification", Mode: "time", Interval: 50 * time.Second, MaxResends: n(2), Cohort: 2, Notifier: "CAMPAIGN_SINK"},
		{Key: "R5", Action: "notification", Mode: "time", Interval: 120 * time.Second, Cohort: 2, Notifier: "CAMPAIGN_SINK",
			Escalation: &EscalationSpec{After: 2 * min, TargetSeverity: "critical"}, Episode: 5 * min}}
	s.ResendUntil = 10*min + 30*time.Second
	s.Fingerprint = &FingerprintSpec{At: min, Repeats: 5, Converge: 2 * min, Verify: 55 * time.Second, Every: min}
	s.Tasks = &TaskExecSpec{Job: "monitor:check_connection:homelab", At: min, Every: min, Timeout: 4 * min, BurstEvery: 5, Burst: 3}
	s.Report = &ReportSpec{Key: "core-alarm-history-dump", At: []time.Duration{4 * min}, Timeout: 7 * min}
	s.fillWorker()
	return s
}

// AlertingDay3psV1 is alerting-day@v4 at a moderate load for functional
// qualification (Corey, 2026-11-25, OSCAR 26.4.6-rc13): a total offered
// firing rate of 3/s, and 6/s in 1-minute spikes at v4's spike starts (T+45,
// T+105), with v4's shares of the total:
//   - correlation 20%: 0.6/s (1.2/s in a spike), up to 64 in flight;
//   - resend about 10%: v4's R1-R5 with 8 members each (40 per 130 s, 0.31/s);
//   - fingerprint 5%: rounds of 9 a minute (8 variants and one other device);
//   - tickets about 5%: every 12th new worker alert, as v4;
//   - the worker lane: the rest, 1.94/s (4.34/s in a spike).
//
// Phases, task lane, alert reports, front office, homelab job gate, pre-check
// limits, limits and seed are v4's; the offered-rate check judges each minute
// against 3/s and 6/s. Not comparable with v4: every headline differs by load.
func AlertingDay3psV1() Scenario {
	s := AlertingDayV4()
	s.ID, s.Version = "alerting-day-3ps", "v1"
	s.OfferedRate, s.OfferedSpikeRate = 3, 6
	s.Baseline.Bursts = []BurstSpec{{At: 45 * time.Minute, For: time.Minute}, {At: 105 * time.Minute, For: time.Minute}}
	s.Correlation.Rate, s.Correlation.SpikeRate = 0.6, 1.2
	s.Resend = append([]PolicySpec(nil), s.Resend...)
	for i := range s.Resend {
		s.Resend[i].Cohort = 8
	}
	fp := *s.Fingerprint
	fp.Repeats = 8
	s.Fingerprint = &fp
	s.fillWorker()
	return s
}

// AlertingDay3psSmokeV1 is alerting-day-smoke@v4 at the 3ps totals (3/s, 6/s
// in its 1-minute spikes at T+4 and T+7): the same path-proving controller
// lanes, the worker lane 2.66/s (5.46/s in a spike). Custom: never comparable.
func AlertingDay3psSmokeV1() Scenario {
	s := AlertingDaySmokeV4()
	s.ID, s.Version = "alerting-day-3ps-smoke", "v1"
	s.OfferedRate, s.OfferedSpikeRate = 3, 6
	s.fillWorker()
	return s
}

// AlertingDay3psV2 is alerting-day-3ps@v1 with smoothed correlation pacing
// (OSC-189): v1's documents start as credit allows and swing a minute's
// correlation offers by about one document (36 -8/+7), against a 5%
// offered-rate tolerance of 9 alerts at 3/s. v1 is released and frozen by
// hash, so the change is a new version.
//
// v2 also adds the maintenance-mode lane (OSC-189): a 5-minute window at
// T+70, between the spikes, after setup at T+66, probes every 20 s.
func AlertingDay3psV2() Scenario {
	s := AlertingDay3psV1()
	s.Version, s.SmoothCorrelationOffers = "v2", true
	s.Maintenance = &MaintenanceSpec{At: 66 * time.Minute, Start: 70 * time.Minute, For: 5 * time.Minute, Every: 20 * time.Second, Lag: 45 * time.Second}
	return s
}

// AlertingDay3psSmokeV2 is alerting-day-3ps-smoke@v1 with smoothed
// correlation pacing and a 3-minute maintenance window. Custom: never
// comparable.
func AlertingDay3psSmokeV2() Scenario {
	s := AlertingDay3psSmokeV1()
	s.Version, s.SmoothCorrelationOffers = "v2", true
	// After the T+4 and T+7 spikes: setup at T+6, window T+8 to T+11, after
	// probes until T+12:05 (the drain waits for the lane).
	s.Maintenance = &MaintenanceSpec{At: 6 * time.Minute, Start: 8 * time.Minute, For: 3 * time.Minute, Every: 10 * time.Second, Lag: 45 * time.Second}
	return s
}

// Builtins are the built-in campaign scenarios.
func Builtins() []Scenario {
	return []Scenario{AlertingDayV1(), AlertingDayV2(), AlertingDaySmokeV1(), AlertingDaySmokeV2(), DeliveryRetryV1(), ResendOnlyV1(), FunctionalV1(), FunctionalBurstV1(), FunctionalBurstFullV1(), AlertingDayControlV1(), AlertingDayV3(), AlertingDaySmokeV3(), ManagedAlertsSmokeV1(), AlertingDayV4(), AlertingDaySmokeV4(), AlertingDay3psV1(), AlertingDay3psSmokeV1(), AlertingDay3psV2(), AlertingDay3psSmokeV2()}
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

// RequiresComparisonProvenance identifies the long qualification plans.
func (s Scenario) RequiresComparisonProvenance() bool {
	return s.NativeWorkload != nil && !s.Custom
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

// SkippableLanes are the lanes a run may switch off by option (--skip-lane):
// the run records it, never counts as comparable, and the lane reads
// "skipped (by option)" rather than passing. The scenario and its hash are
// unchanged.
var SkippableLanes = map[string]func(Scenario) bool{
	"report":      func(s Scenario) bool { return s.Report != nil },
	"maintenance": func(s Scenario) bool { return s.Maintenance != nil },
}

// ValidateSkipLanes rejects an unknown lane, or one the scenario does not have.
func ValidateSkipLanes(s Scenario, lanes []string) error {
	for _, l := range lanes {
		has, ok := SkippableLanes[l]
		if !ok {
			return fmt.Errorf("lane %q cannot be skipped (skippable: report, maintenance)", l)
		}
		if !has(s) {
			return fmt.Errorf("%s@%s has no %s lane to skip", s.ID, s.Version, l)
		}
	}
	return nil
}
