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
	Limits      Limits          `json:"limits"`
	Precheck    PrecheckLimits  `json:"precheck"`
	Seed        int64           `json:"seed"`
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
		Limits: Limits{HistoryP95: 40 * time.Second, Drain: 15 * min, ContainerSlopeMiBPerHour: 100,
			FrontOfficeP95: 2 * time.Second, FrontOfficeP99: 5 * time.Second, CorrelationSuccess: 1, TicketSuccess: 1, ResendSuccess: 1,
			RegressionPct: 20, QuerySetVersion: "q1"},
		Precheck: PrecheckLimits{IdleQueueDepth: 50, IdleQueueAge: 30 * time.Second, ClockSkew: 250 * time.Millisecond,
			TargetDiskFreePct: 20, TargetMemAvailPct: 25, RunnerDiskFreeGiB: 10, MetricsFreshness: 60 * time.Second, RestartWindow: 30 * min, HistoryRowsAdvisory: 5_000_000},
		Seed: 20261113,
	}
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

// Builtins are the built-in campaign scenarios.
func Builtins() []Scenario { return []Scenario{AlertingDayV1(), AlertingDaySmokeV1()} }

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
			name := strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0]
			m[name] = canon(v.Field(i))
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
