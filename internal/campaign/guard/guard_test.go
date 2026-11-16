package guard

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func has(trips []Trip, g string) bool {
	for _, t := range trips {
		if t.Guard == g {
			return true
		}
	}
	return false
}

func series(n int, f func(i int) float64) []Sample {
	var w []Sample
	for i := 0; i < n; i++ {
		w = append(w, Sample{At: t0.Add(time.Duration(i) * 15 * time.Second),
			QueueDepth: map[string]float64{"q": f(i)}, Newest: t0.Add(time.Duration(i) * 15 * time.Second)})
	}
	return w
}

func TestQueueGrowthFiveMinutesTrips(t *testing.T) {
	base := Sample{QueueDepth: map[string]float64{"q": 5}}
	w := series(20, func(i int) float64 { return 10 + float64(i) })
	now := w[19].At
	if !has(Evaluate(now, w, Counters{WorkerHeartbeat: now}, base), GuardQueueGrowth) {
		t.Fatal("5 min growth should trip")
	}
	w = series(19, func(i int) float64 { return 10 + float64(i) })
	if has(Evaluate(w[18].At, w, Counters{WorkerHeartbeat: w[18].At}, base), GuardQueueGrowth) {
		t.Fatal("4m45s must not trip")
	}
	w = series(20, func(int) float64 { return 10 })
	if has(Evaluate(w[19].At, w, Counters{WorkerHeartbeat: w[19].At}, base), GuardQueueGrowth) {
		t.Fatal("flat must not trip")
	}
}

func TestRestartTrips(t *testing.T) {
	w := series(1, func(int) float64 { return 0 })
	w[0].Restarts = 1
	if !has(Evaluate(w[0].At, w, Counters{WorkerHeartbeat: w[0].At}, Sample{}), GuardRestart) {
		t.Fatal("restart should trip")
	}
}

func TestStaleMetricsTwoMinutes(t *testing.T) {
	w := []Sample{{Newest: t0}}
	hb := Counters{WorkerHeartbeat: t0.Add(121 * time.Second)}
	if !has(Evaluate(t0.Add(121*time.Second), w, hb, Sample{}), GuardMetricsStale) {
		t.Fatal("121s should trip")
	}
	if has(Evaluate(t0.Add(119*time.Second), w, hb, Sample{}), GuardMetricsStale) {
		t.Fatal("119s must not trip")
	}
}

func TestHeartbeatLost(t *testing.T) {
	w := []Sample{{Newest: t0}}
	if !has(Evaluate(t0.Add(31*time.Second), w, Counters{WorkerHeartbeat: t0}, Sample{}), GuardHeartbeatLost) {
		t.Fatal("31s should trip")
	}
	if has(Evaluate(t0.Add(30*time.Second), w, Counters{WorkerHeartbeat: t0}, Sample{}), GuardHeartbeatLost) {
		t.Fatal("30s must not trip")
	}
}

func TestUnknownRateGuardBoundary(t *testing.T) {
	w := []Sample{{Newest: t0}}
	e := func(u int) []Trip {
		return Evaluate(t0, w, Counters{Offered: 1000, Unknown: u, WorkerHeartbeat: t0}, Sample{})
	}
	if has(e(5), GuardUnknownRate) || !has(e(6), GuardUnknownRate) {
		t.Fatal("boundary wrong")
	}
	if has(Evaluate(t0, w, Counters{WorkerHeartbeat: t0}, Sample{}), GuardUnknownRate) {
		t.Fatal("offered 0 must not trip")
	}
	// Ruling C38: no evaluation before 500 sends.
	if has(Evaluate(t0, w, Counters{Offered: 499, Unknown: 10, WorkerHeartbeat: t0}, Sample{}), GuardUnknownRate) {
		t.Fatal("fewer than 500 sends must not trip")
	}
	if !has(Evaluate(t0, w, Counters{Offered: 500, Unknown: 3, WorkerHeartbeat: t0}, Sample{}), GuardUnknownRate) {
		t.Fatal("500 sends with 0.6% unknown must trip")
	}
}

func TestTicketUnknownAboveThree(t *testing.T) {
	w := []Sample{{Newest: t0}}
	e := func(n int) []Trip {
		return Evaluate(t0, w, Counters{TicketUnknown: n, WorkerHeartbeat: t0}, Sample{})
	}
	if has(e(3), GuardTicketUnknown) || !has(e(4), GuardTicketUnknown) {
		t.Fatal("ticket boundary wrong")
	}
}

func TestMySQLRedisOOM(t *testing.T) {
	w := []Sample{{Newest: t0, MySQLRefusals: 2, RedisDenied: 3, OOMs: 1}}
	tr := Evaluate(t0, w, Counters{WorkerHeartbeat: t0}, Sample{MySQLRefusals: 1, RedisDenied: 1})
	for _, g := range []string{GuardMySQLRefusals, GuardRedisDenied, GuardOOM} {
		if !has(tr, g) {
			t.Fatal(g)
		}
	}
}

func TestOrphanStoppedLaneIsHeartbeatLost(t *testing.T) {
	w := series(1, func(int) float64 { return 0 })
	now := w[0].At
	c := Counters{WorkerHeartbeat: now, LaneState: "orphan_stopped", BaselineEnd: now.Add(time.Minute)}
	trips := Evaluate(now, w, c, Sample{})
	if !has(trips, GuardHeartbeatLost) || has(trips, GuardLaneStopped) {
		t.Fatalf("orphan stop is a harness fault (heartbeat_lost), not lane_stopped: %+v", trips)
	}
}

func TestLaneStoppedBeforeBaselineEnd(t *testing.T) {
	w := series(1, func(int) float64 { return 0 })
	now := w[0].At
	c := Counters{WorkerHeartbeat: now, LaneState: "completed", LaneError: "ledger outcome seq 9: disk full", BaselineEnd: now.Add(time.Minute)}
	if !has(Evaluate(now, w, c, Sample{}), GuardLaneStopped) {
		t.Fatal("a lane completed before the baseline end must trip")
	}
	c.LaneState = "running"
	if has(Evaluate(now, w, c, Sample{}), GuardLaneStopped) {
		t.Fatal("a running lane must not trip")
	}
	c.LaneState, c.BaselineEnd = "completed", now
	if has(Evaluate(now, w, c, Sample{}), GuardLaneStopped) {
		t.Fatal("a lane done at the baseline end must not trip")
	}
	c.LaneState = ""
	c.BaselineEnd = now.Add(time.Minute)
	if has(Evaluate(now, w, c, Sample{}), GuardLaneStopped) {
		t.Fatal("no heartbeat yet (unknown state) must not trip")
	}
}

// creates builds a 15 s window whose History create rate is f(i); ok marks the
// metric as present.
func creates(n int, f func(i int) float64, ok bool) []Sample {
	w := series(n, func(int) float64 { return 0 })
	for i := range w {
		w[i].HistoryCreates, w[i].HistoryCreatesOK = f(i), ok
	}
	return w
}

// camp_681887ad6d96c6ff: History create commits fell from ~3/s to 0 at 21:34
// while the lane kept firing; nothing stopped the run for 29 minutes.
func TestHistoryStalledTripsAfterThreeMinutesOfZeroCreates(t *testing.T) {
	running := func(at time.Time) Counters {
		return Counters{WorkerHeartbeat: at, LaneState: "running", BaselineEnd: at.Add(time.Hour)}
	}
	stall := creates(20, func(i int) float64 { return map[bool]float64{true: 3, false: 0}[i < 8] }, true) // 12 zero samples
	if at := stall[19].At; !has(Evaluate(at, stall, running(at), Sample{}), GuardHistoryStalled) {
		t.Fatal("3 min of zero creates after flow must trip")
	}
	short := stall[:19] // only 11 zero samples (2m45s)
	if at := short[18].At; has(Evaluate(at, short, running(at), Sample{}), GuardHistoryStalled) {
		t.Fatal("under 3 min of zeros must not trip")
	}
	warmup := creates(20, func(int) float64 { return 0 }, true) // never flowed: startup, not a stall
	if at := warmup[19].At; has(Evaluate(at, warmup, running(at), Sample{}), GuardHistoryStalled) {
		t.Fatal("zero creates before any flow must not trip")
	}
	missing := creates(20, func(i int) float64 { return map[bool]float64{true: 3, false: 0}[i < 8] }, false)
	for i := 8; i < 20; i++ {
		missing[i].HistoryCreatesOK = false // series absent: the stale-metrics guard owns that
	}
	if at := missing[19].At; has(Evaluate(at, missing, running(at), Sample{}), GuardHistoryStalled) {
		t.Fatal("an absent metric must not trip the stall guard")
	}
	if at := stall[19].At; has(Evaluate(at, stall, Counters{WorkerHeartbeat: at, LaneState: "running", BaselineEnd: at}, Sample{}), GuardHistoryStalled) {
		t.Fatal("after the baseline end firings stop, so zero creates is expected")
	}
}
