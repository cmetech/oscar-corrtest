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
)

const (
	growthWindow      = 20 // samples: 5 min at 15 s
	unknownMinOffered = 500
)

type Sample struct {
	At                         time.Time
	QueueDepth, QueueAge       map[string]float64
	Restarts, OOMs             int
	MySQLRefusals, RedisDenied float64
	Newest                     time.Time
}

type Counters struct {
	Offered, Unknown, TicketUnknown int
	WorkerHeartbeat                 time.Time
	// LaneState and LaneError come from the last worker heartbeat; a lane
	// that is not running before BaselineEnd stopped on its own.
	LaneState, LaneError string
	BaselineEnd          time.Time
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
	if len(window) >= growthWindow {
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
