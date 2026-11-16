package score

import (
	"strings"
	"testing"
)

func TestATTDeriveStatesTheReproducedFailureModes(t *testing.T) {
	a := ATT{Budgets: map[string]float64{"alerts": 100, "notifier_jobs": 300, "recovered": 100, "redispatched": 970,
		"am_webhook_failed": 900, "am_webhook_sent": 1000, "history_updates": 50, "history_creates": 150},
		Breaker: BreakerStats{Applied: true, Tripped: true, ResolveProbes: 50, ResolveAdmitted: 0, ReopenAfterSeconds: -1}}
	a.Derive()
	for _, want := range []string{"hand-off re-queue: 10.70", "breaker rejected 50 of 50", "breaker did not reopen",
		"retry storm", "queue threshold override NOT restored", "notifier jobs per alert: 3.00"} {
		if !strings.Contains(strings.Join(a.Findings, "\n"), want) {
			t.Errorf("missing %q in %v", want, a.Findings)
		}
	}
	if a.Budgets["repeat_share"] != 0.25 || a.Budgets["handoff_copies"] != 10.7 {
		t.Fatalf("ratios %v", a.Budgets)
	}
	healthy := ATT{Budgets: map[string]float64{"recovered": 100, "redispatched": 1},
		Breaker: BreakerStats{Applied: true, Tripped: true, ResolveProbes: 50, ResolveAdmitted: 50, ReopenAfterSeconds: 12, Restored: true}}
	healthy.Derive()
	if len(healthy.Findings) != 0 {
		t.Fatalf("a fixed build reports nothing: %v", healthy.Findings)
	}
	if Outcome(0) != "unknown" || Outcome(202) != "ok" || Outcome(503) != "rejected_503" || Outcome(429) != "error" {
		t.Fatal("outcome buckets")
	}
}

// AT&T loop on the delivery queue (2026-11-16 v2 run): notification-delivery recovery
// republished each retryable delivery every 2 s while the notifier queue was backlogged.
func TestATTDeriveCountsDeliveryCopies(t *testing.T) {
	looping := ATT{Budgets: map[string]float64{"delivery_requests": 1000, "delivery_republished": 3000},
		Breaker: BreakerStats{Applied: true, Tripped: true, ResolveProbes: 1, ResolveAdmitted: 1, ReopenAfterSeconds: 1, Restored: true}}
	looping.Derive()
	if looping.Budgets["delivery_copies"] != 4 || !strings.Contains(strings.Join(looping.Findings, "\n"), "delivery re-queue: 4.00 publishes per delivery") {
		t.Fatalf("copies %v findings %v", looping.Budgets, looping.Findings)
	}
	fixed := ATT{Budgets: map[string]float64{"delivery_requests": 1000, "delivery_republished": 5},
		Breaker: BreakerStats{Applied: true, Tripped: true, ResolveProbes: 1, ResolveAdmitted: 1, ReopenAfterSeconds: 1, Restored: true}}
	fixed.Derive()
	if fixed.Budgets["delivery_copies"] != 1.005 || len(fixed.Findings) != 0 {
		t.Fatalf("copies %v findings %v", fixed.Budgets, fixed.Findings)
	}
	for _, k := range []string{"delivery_requests", "delivery_republished"} {
		if !strings.Contains(BudgetQueries[k], "%s") {
			t.Fatalf("budget query %s missing", k)
		}
	}
}
