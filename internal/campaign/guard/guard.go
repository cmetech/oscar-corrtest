// Package guard decides whether a campaign must stop new traffic now.
package guard

import (
	"fmt"
	"time"
)

const (
	GuardQueueGrowth   = "queue_growth"
	GuardRestart       = "restart"
	GuardOOM           = "oom"
	GuardMySQLRefusals = "mysql_refusals"
	GuardRedisDenied   = "redis_denied"
	GuardMetricsStale  = "metrics_stale"
	GuardHeartbeatLost = "heartbeat_lost"
	GuardUnknownRate   = "unknown_rate"
	GuardTicketUnknown = "ticket_unknown"
	GuardLaneStopped   = "lane_stopped"
	// GuardHistoryStalled: History create commits fell to zero while the
	// baseline lane keeps firing (camp_681887ad6d96c6ff ran 29 min that way).
	GuardHistoryStalled = "history_stalled"
)

const (
	growthWindow      = 20 // samples: 5 min at 15 s
	stallWindow       = 3 * time.Minute
	unknownMinOffered = 500
)

type Sample struct {
	At                         time.Time
	QueueDepth, QueueAge       map[string]float64
	Restarts, OOMs             int
	MySQLRefusals, RedisDenied float64
	Newest                     time.Time
	// HistoryCreates is OSCAR's History create-commit rate (/s); OK when the
	// series was present in the sample.
	HistoryCreates   float64
	HistoryCreatesOK bool
}

type Counters struct {
	Offered, Unknown, TicketUnknown int
	WorkerHeartbeat                 time.Time
	// LaneState and LaneError come from the last worker heartbeat; a lane
	// that is not running before BaselineEnd stopped on its own.
	LaneState, LaneError string
	BaselineEnd          time.Time
	// QuietFrom..QuietUntil is a deliberate overload window (v2 forced
	// breaker): History stalls and queue growth there are the test, not a fault.
	QuietFrom, QuietUntil time.Time
}

func (c Counters) quiet(now time.Time) bool {
	return !c.QuietFrom.IsZero() && !now.Before(c.QuietFrom) && now.Before(c.QuietUntil)
}

type Trip struct {
	Guard, Detail string
	At            time.Time
}

// Evaluate returns every guard currently tripped.
func Evaluate(now time.Time, window []Sample, c Counters, baseline Sample) []Trip {
	var trips []Trip
	add := func(g, f string, a ...any) { trips = append(trips, Trip{g, fmt.Sprintf(f, a...), now}) }

	if len(window) == 0 || now.Sub(window[len(window)-1].Newest) > 2*time.Minute {
		add(GuardMetricsStale, "no fresh metrics for over 2m")
	}
	// The worker orphan-stops itself after 30s without heartbeats; a 15s guard
	// tick can miss that window, so its own report is the same harness fault.
	if now.Sub(c.WorkerHeartbeat) > 30*time.Second || c.LaneState == "orphan_stopped" {
		add(GuardHeartbeatLost, "worker heartbeat lost (lane %s)", c.LaneState)
	}
	// Only sends with no definite answer are unknown (ruling C38); a handful
	// of sends is no rate, so evaluate from 500 offered.
	if c.Offered >= unknownMinOffered && c.Unknown*1000 > c.Offered*5 {
		add(GuardUnknownRate, "unknown %d of %d offered", c.Unknown, c.Offered)
	}
	if c.LaneState != "" && c.LaneState != "running" && c.LaneState != "orphan_stopped" && now.Before(c.BaselineEnd) {
		add(GuardLaneStopped, "baseline lane %s before the planned end: %s", c.LaneState, c.LaneError)
	}
	if c.TicketUnknown > 3 {
		add(GuardTicketUnknown, "%d ticket creates with unknown outcome", c.TicketUnknown)
	}
	if len(window) == 0 {
		return trips
	}
	if c.LaneState == "running" && now.Before(c.BaselineEnd) && !c.quiet(now) && historyStalled(window) {
		add(GuardHistoryStalled, "no History creates for %s while the lane fires", stallWindow)
	}
	last := window[len(window)-1]
	if last.Restarts > baseline.Restarts {
		add(GuardRestart, "restarts %d > baseline %d", last.Restarts, baseline.Restarts)
	}
	if last.OOMs > baseline.OOMs {
		add(GuardOOM, "ooms %d > baseline %d", last.OOMs, baseline.OOMs)
	}
	if last.MySQLRefusals > baseline.MySQLRefusals {
		add(GuardMySQLRefusals, "mysql refusals increased")
	}
	if last.RedisDenied > baseline.RedisDenied {
		add(GuardRedisDenied, "redis write denials increased")
	}
	if len(window) >= growthWindow && !c.quiet(now) {
		w := window[len(window)-growthWindow:]
		for _, kind := range []struct {
			name string
			get  func(Sample) map[string]float64
		}{{"depth", func(s Sample) map[string]float64 { return s.QueueDepth }},
			{"age", func(s Sample) map[string]float64 { return s.QueueAge }}} {
			for q := range kind.get(w[0]) {
				if rising(w, baseline, kind.get, q) {
					add(GuardQueueGrowth, "queue %s %s rising for 5m", q, kind.name)
				}
			}
		}
	}
	return trips
}

// historyStalled: the window ends in a run of present, zero-create samples
// spanning stallWindow (12 ticks), and creates flowed before that run.
func historyStalled(w []Sample) bool {
	i := len(w)
	for i > 0 && w[i-1].HistoryCreatesOK && w[i-1].HistoryCreates == 0 {
		i--
	}
	if i == len(w) || w[len(w)-1].At.Sub(w[i].At) < stallWindow-guardTick {
		return false
	}
	for _, s := range w[:i] {
		if s.HistoryCreatesOK && s.HistoryCreates > 0 {
			return true
		}
	}
	return false
}

// guardTick is the controller's sampling cadence.
const guardTick = 15 * time.Second

// rising: every sample above baseline and least-squares slope > 0.
func rising(w []Sample, base Sample, get func(Sample) map[string]float64, q string) bool {
	n := float64(len(w))
	var sx, sy, sxy, sxx float64
	for i, s := range w {
		y, ok := get(s)[q]
		if !ok || y <= get(base)[q] {
			return false
		}
		x := float64(i)
		sx, sy, sxy, sxx = sx+x, sy+y, sxy+x*y, sxx+x*x
	}
	return n*sxy-sx*sy > 0
}
