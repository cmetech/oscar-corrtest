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
// ("builtin", "basic", "advanced"); Case optionally names a case code.
type CaseRef struct {
	Pattern string `json:"pattern"`
	Level   string `json:"level"`
	Case    string `json:"case,omitempty"`
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
	HistoryP95                time.Duration `json:"historyP95"`
	Drain                     time.Duration `json:"drain"`
	ParentAnonSlopeMiBPerHour float64       `json:"parentAnonSlopeMiBPerHour"`
	ContainerSlopeMiBPerHour  float64       `json:"containerSlopeMiBPerHour"`
	FrontOfficeP95            time.Duration `json:"frontOfficeP95"`
	FrontOfficeP99            time.Duration `json:"frontOfficeP99"`
	CorrelationSuccess        float64       `json:"correlationSuccess"`
	TicketSuccess             float64       `json:"ticketSuccess"`
	ResendSuccess             float64       `json:"resendSuccess"`
	RegressionPct             float64       `json:"regressionPct"`
	QuerySetVersion           string        `json:"querySetVersion"`
}

type PrecheckLimits struct {
	IdleQueueDepth    int           `json:"idleQueueDepth"`
	IdleQueueAge      time.Duration `json:"idleQueueAge"`
	ClockSkew         time.Duration `json:"clockSkew"`
	TargetDiskFreePct float64       `json:"targetDiskFreePct"`
	TargetMemAvailPct float64       `json:"targetMemAvailPct"`
	RunnerDiskFreeGiB float64       `json:"runnerDiskFreeGiB"`
	MetricsFreshness  time.Duration `json:"metricsFreshness"`
	RestartWindow     time.Duration `json:"restartWindow"`
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

func AlertingDayV1() Scenario {
	min := time.Minute
	var setA, setB []CaseRef
	for _, b := range corr.AllBuiltins() {
		setA = append(setA, CaseRef{Pattern: b.Pattern, Level: "builtin"})
		setB = append(setB, CaseRef{Pattern: b.Pattern, Level: "builtin", Case: "P01"})
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
			{"R1", "notification", "time", 120 * time.Second, 0, n(5), false, 10, "CAMPAIGN_SINK"},
			{"R2", "notification", "occurrences", 0, 3, nil, true, 10, "CAMPAIGN_SINK"},
			{"R3", "notification", "either", 300 * time.Second, 5, n(2), true, 10, "CAMPAIGN_SINK"},
			{"R4", "ticket", "time", 600 * time.Second, 0, n(2), false, 10, "ALERTHISTORY"},
		},
		Redelivery: 60 * time.Second, ResendUntil: 118 * min,
		FrontOffice: FrontOfficeSpec{Users: 4, Interval: 15 * time.Second, Jitter: 3 * time.Second, SearchEvery: 2 * min},
		Limits: Limits{HistoryP95: 40 * time.Second, Drain: 15 * min, ParentAnonSlopeMiBPerHour: 25, ContainerSlopeMiBPerHour: 100,
			FrontOfficeP95: 2 * time.Second, FrontOfficeP99: 5 * time.Second, CorrelationSuccess: 1, TicketSuccess: 1, ResendSuccess: 1,
			RegressionPct: 20, QuerySetVersion: "q1"},
		Precheck: PrecheckLimits{IdleQueueDepth: 50, IdleQueueAge: 30 * time.Second, ClockSkew: 250 * time.Millisecond,
			TargetDiskFreePct: 20, TargetMemAvailPct: 25, RunnerDiskFreeGiB: 10, MetricsFreshness: 60 * time.Second, RestartWindow: 30 * min},
		Seed: 20261113,
	}
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
