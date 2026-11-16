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
