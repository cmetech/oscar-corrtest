package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hasReason(reasons []string, prefix string) bool {
	for _, r := range reasons {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func TestEngineHappyPathPass(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, card := h.scorecard()
	if c.Status != domain.CampaignCompleted || c.Cleanup != domain.CleanupClean {
		t.Fatalf("status %s cleanup %s", c.Status, c.Cleanup)
	}
	if c.Verdict != domain.VerdictPass {
		t.Fatalf("verdict %s, reasons %v", c.Verdict, card.Reasons)
	}
	h.assertRestored()
	names := map[string]bool{}
	for _, hl := range card.Headlines {
		names[hl.Name] = true
	}
	for _, want := range []string{"queue.drain_seconds", "latency.history_p95", "latency.sink_p95", "latency.correlation_p95",
		"correlation.success", "tickets.success", "resend.success", "frontoffice.p95", "frontoffice.p99"} {
		if !names[want] {
			t.Errorf("missing headline %s (have %v)", want, names)
		}
	}
	for _, n := range containerNames {
		if !names["memory."+n+".slope"] {
			t.Errorf("missing memory headline for %s", n)
		}
	}
	if card.Tickets.Expected == 0 {
		t.Error("no tickets expected; the scenario should ticket")
	}
}

func TestGuardTripStopsAndStillCleansUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.oscar.onCorr = func() { // phase 2 (correlation) has begun
		h.oscar.mu.Lock()
		if h.oscar.restartAt.IsZero() {
			h.oscar.restartAt = h.clock.Now()
		}
		h.oscar.mu.Unlock()
	}
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, card := h.scorecard()
	if c.Verdict != domain.VerdictFail || len(card.Reasons) == 0 || card.Reasons[0] != "guard:restart" {
		t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
	}
	if c.Cleanup != domain.CleanupClean {
		t.Fatalf("cleanup %s", c.Cleanup)
	}
	h.assertRestored()
	h.oscar.mu.Lock()
	defer h.oscar.mu.Unlock()
	limit := h.oscar.restartAt.Add(guardEvery + 2*time.Second) // one guard tick plus slack
	for _, at := range h.oscar.laneFires {
		if at.After(limit) {
			t.Fatalf("lane fired at %s, after the trip deadline %s", at, limit)
		}
	}
}

func TestWorkerLossIsInconclusive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.oscar.onCorr = func() { h.worker.down.Store(true) }
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, card := h.scorecard()
	if c.Verdict != domain.VerdictInconclusive || !hasReason(card.Reasons, "guard:heartbeat_lost") {
		t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
	}
	waitFor(t, "worker orphan stop", func() bool {
		st, err := h.worker.m.Status(context.Background(), h.id+"-baseline")
		return err == nil && st.State == "orphan_stopped" && st.OrphanStopAt != nil
	})
	waitFor(t, "orphan resolve-all", func() bool {
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		for name, r := range h.oscar.history {
			if strings.HasPrefix(name, "Campaign-"+h.id+"-") && !strings.Contains(name, "resend") && r.Status == "firing" {
				return false
			}
		}
		return true
	})
}

func TestEngineRecoveryAfterRestartRestoresEverything(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, kill := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	waitFor(t, "resend cohorts", func() bool {
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		return len(h.oscar.cohort) > 0 && len(h.oscar.cohort[h.anyCohort()]) >= 3
	})
	kill() // runtime Close: status stays RUNNING, so no drain here
	if err := <-done; err == nil {
		t.Fatal("killed run returned nil")
	}
	if c := h.campaign(); c.Status != domain.CampaignRunning {
		t.Fatalf("status after kill %s", c.Status)
	}
	h.oscar.mu.Lock()
	firings := h.oscar.allFirings
	h.oscar.mu.Unlock()
	// The runtime marks the campaign INTERRUPTED and recovers on restart.
	if err := h.db.TransitionCampaign(context.Background(), h.id, domain.CampaignInterrupted, h.clock.Now(), "restart"); err != nil {
		t.Fatal(err)
	}
	if err := New(h.deps, h.clock).Recover(context.Background(), h.id); err != nil {
		t.Fatal(err)
	}
	c, card := h.scorecard()
	if c.Status != domain.CampaignCompleted || c.Verdict != domain.VerdictInconclusive {
		t.Fatalf("status %s verdict %s reasons %v", c.Status, c.Verdict, card.Reasons)
	}
	if c.Cleanup != domain.CleanupClean {
		t.Fatalf("cleanup %s", c.Cleanup)
	}
	h.assertRestored()
	h.oscar.mu.Lock()
	defer h.oscar.mu.Unlock()
	if h.oscar.allFirings != firings {
		t.Fatalf("%d firings sent after the kill", h.oscar.allFirings-firings)
	}
}

// anyCohort returns one cohort alertname (caller holds oscar.mu).
func (h *harness) anyCohort() string {
	for name := range h.oscar.cohort {
		return name
	}
	return ""
}

func TestAmbiguousTicketCreateNotRetried(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	name := "Campaign-" + h.id + "-resend-ticket-1"
	h.oscar.ambiguous = name
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.oscar.mu.Lock()
	posts := h.oscar.posts[name]
	h.oscar.mu.Unlock()
	if posts != 1 {
		t.Fatalf("ambiguous ticket create posted %d times, want exactly 1", posts)
	}
	c, card := h.scorecard()
	if card.Tickets.Unknown < 1 || c.Verdict == domain.VerdictPass {
		t.Fatalf("verdict %s tickets %+v; the ambiguous create must count as unknown", c.Verdict, card.Tickets)
	}
	if c.Cleanup != domain.CleanupClean {
		t.Fatalf("cleanup %s", c.Cleanup)
	}
}

func TestSetAOverrunMarksNotRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.s.Correlation.SetA = append(h.s.Correlation.SetA, h.s.Correlation.SetA[0])
	h.oscar.slow = 20 * time.Second // 3 cases x 20 s cannot fit the 30 s window
	if err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, card := h.scorecard()
	if c.Verdict != domain.VerdictInconclusive || !hasReason(card.Reasons, "not run: setA") {
		t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
	}
	if !hasReason(card.Reasons, "correlation: no evidence") {
		t.Fatalf("correlation gate not INCONCLUSIVE: %v", card.Reasons)
	}
}
