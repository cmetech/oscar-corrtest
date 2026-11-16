package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// testScenarioV2 is testScenario plus the v2 AT&T facets, minutes scaled to seconds.
func testScenarioV2() scenario.Scenario {
	s, sec := testScenario(), time.Second
	s.Phases = []scenario.Phase{{Name: "preflight", Start: -10 * sec}, {Name: "baseline", End: 30 * sec}, {Name: "correlation", Start: 30 * sec, End: 60 * sec},
		{Name: "ticketing", Start: 60 * sec, End: 90 * sec}, {Name: "resend", Start: 90 * sec, End: 120 * sec},
		{Name: "breaker", Start: 120 * sec, End: 130 * sec}, {Name: "drain", Start: 130 * sec, End: 150 * sec}}
	s.Standing = &scenario.StandingSpec{Size: 20, Instances: 4, Interval: 10 * sec, Until: 120 * sec, Concurrency: 4}
	s.Breaker = &scenario.BreakerSpec{QueueHigh: 100, QueueLow: 10, BurstConcurrency: 4, ResolveProbes: 3}
	return s
}

func newHarnessV2(t *testing.T) *harness {
	h := newHarness(t)
	h.s = testScenarioV2()
	h.oscar.s = h.s
	return h
}

func (h *harness) att() *score.ATT {
	h.t.Helper()
	var card struct{ ATT *score.ATT }
	if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil || card.ATT == nil {
		h.t.Fatalf("scorecard has no ATT section: %v", err)
	}
	return card.ATT
}

func (h *harness) assertLimitsRestored() {
	h.t.Helper()
	h.oscar.mu.Lock()
	defer h.oscar.mu.Unlock()
	var o struct{ Queue any }
	if err := json.Unmarshal(h.oscar.overrides, &o); err != nil || o.Queue != nil {
		h.t.Fatalf("queue override left in place: %s", h.oscar.overrides)
	}
}

func hasFinding(a *score.ATT, sub string) bool {
	for _, f := range a.Findings {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

// TestV2ReproducesTheATTBreakerAndRepeats: 26.4.4 behaviour (resolves refused while
// tripped) is reported; the fixed build (resolves admitted) is not; thresholds are
// restored and every standing alert is resolved either way.
func TestV2ReproducesTheATTBreakerAndRepeats(t *testing.T) {
	for _, rejectResolves := range []bool{true, false} {
		t.Run(map[bool]string{true: "26.4.4", false: "fixed"}[rejectResolves], func(t *testing.T) {
			h := newHarnessV2(t)
			// The scripted breaker refuses the rest of the burst and the first three
			// reopen probes, then admits the fourth. Reopen probes are paced by real
			// timers on the scaled clock; the old shut of 30 needed 16 sequential
			// probes (4 of the window's 8.75 s), which a slow runner could not fit.
			// The 3 spare refusals still keep the breaker tripped through the
			// resolve probes if a late cohort post lands in the window.
			trip := 5
			h.oscar.breakerTrip, h.oscar.breakerShut, h.oscar.rejectResolves = trip, h.s.Standing.Size-trip+3, rejectResolves
			h.oscar.queryValues = map[string]float64{"send_individual_notification": 100, `"tm.notifier.common"`: 300,
				`outcome="recovered"`: 100, `outcome="redispatched"`: 300, "notifications_failed_total": 40, "alertmanager_notifications_total": 200}
			if err := h.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if c := h.campaign(); c.Cleanup != domain.CleanupClean {
				t.Fatalf("cleanup %s", c.Cleanup)
			}
			h.assertLimitsRestored()
			a := h.att()
			br := a.Breaker
			if !br.Applied || !br.Tripped || !br.Restored || br.ResolveProbes != 3 || br.ReopenAfterSeconds <= 0 || br.Burst["rejected_503"] == 0 {
				t.Fatalf("breaker %+v", br)
			}
			if want := map[bool]int{true: 0, false: 3}[rejectResolves]; br.ResolveAdmitted != want {
				t.Fatalf("resolves admitted %d, want %d", br.ResolveAdmitted, want)
			}
			if got := hasFinding(a, "breaker rejected 3 of 3"); got != rejectResolves {
				t.Fatalf("rejected-resolves finding %v: %v", got, a.Findings)
			}
			if a.Standing.Members != 20 || a.Standing.Posts["ok"] < 20*11 || a.Standing.Resolves["ok"] != 20 {
				t.Fatalf("standing %+v", a.Standing)
			}
			if a.Budgets["notifier_jobs_per_alert"] != 3 || a.Budgets["handoff_copies"] != 4 ||
				!hasFinding(a, "hand-off re-queue: 4.00") || !hasFinding(a, "Alertmanager webhook failures in the breaker window: 40 of 200") {
				t.Fatalf("budgets %v findings %v", a.Budgets, a.Findings)
			}
			h.oscar.mu.Lock()
			defer h.oscar.mu.Unlock()
			for _, row := range h.oscar.history {
				if row.Labels["campaign_lane"] == standingLane && row.Status != "resolved" {
					t.Fatalf("standing alert %s still firing", row.AlertName)
				}
			}
		})
	}
}

// TestV2RefusedOverrideSkipsTheWindowAndChangesNothing: without manage:rate_limits
// the window is reported as not run, nothing is left to restore, cleanup is CLEAN.
func TestV2RefusedOverrideSkipsTheWindowAndChangesNothing(t *testing.T) {
	h := newHarnessV2(t)
	h.oscar.limitsRefused = true
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := h.campaign(); c.Cleanup != domain.CleanupClean {
		t.Fatalf("cleanup %s", c.Cleanup)
	}
	a := h.att()
	if a.Breaker.Applied || !strings.Contains(a.Breaker.Skipped, "override") || !hasFinding(a, "breaker window not run") {
		t.Fatalf("breaker %+v findings %v", a.Breaker, a.Findings)
	}
	if open, _ := h.db.OpenMutations(context.Background(), h.id); len(open) != 0 {
		t.Fatalf("open mutations %+v", open)
	}
}
