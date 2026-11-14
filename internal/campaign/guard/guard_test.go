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
