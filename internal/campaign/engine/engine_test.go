package engine

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runtime"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
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
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		start := h.clock.Now()
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.oscar.mu.Lock()
		lastFO := h.oscar.lastFO
		h.oscar.mu.Unlock()
		if d := lastFO.Sub(start); d < 145*time.Second { // spec 6.1: front office runs 0-150 min (scaled: 0-150 s)
			t.Errorf("front office stopped at T+%s, want it running to the drain end", d)
		}
		c, card := h.scorecard()
		if c.Status != domain.CampaignCompleted || c.Cleanup != domain.CleanupClean {
			t.Fatalf("status %s cleanup %s", c.Status, c.Cleanup)
		}
		if c.Verdict != domain.VerdictPass {
			t.Fatalf("verdict %s, reasons %v", c.Verdict, card.Reasons)
		}
		if c.Comparable { // the test scenario is custom (ruling C39: custom never enters the reference)
			t.Fatal("a custom scenario completed comparable")
		}
		h.assertRestored()
		names := map[string]bool{}
		for _, hl := range card.Headlines {
			names[hl.Name] = true
		}
		for _, want := range []string{"queue.drain_seconds", "latency.history_p95", "latency.sink_p95", "latency.correlation_run_wall_p95",
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
		if card.Resend.Expected != 4 || card.Resend.Succeeded != 4 { // R1-R3 from sink receipts, R4 from incident comments
			t.Errorf("resend %+v, want all 4 policies' members succeeded", card.Resend)
		}
		if card.Tickets.Expected == 0 {
			t.Error("no tickets expected; the scenario should ticket")
		}
		if card.Baseline.Planned == 0 || card.Baseline.Offered < card.Baseline.Planned*98/100 {
			t.Errorf("baseline planned %d offered %d", card.Baseline.Planned, card.Baseline.Offered)
		}
		if hl := card.Homelab; !hl.Collected || hl.Completed != 2 || hl.Failed != 1 || hl.PreP95 == 0 {
			t.Errorf("homelab task stats %+v", hl)
		}
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		tokens := map[string]int{}
		for _, inc := range h.oscar.corrInc {
			tokens[inc.ShortDescription[strings.Index(inc.ShortDescription, "[TRIGGER][")+10:][:8]]++
		}
		if len(tokens) == 0 {
			t.Fatal("ticketed set B runs opened no incidents")
		}
		for tok, n := range tokens {
			if n != 1 {
				t.Errorf("run %s ticketed %d sources, want exactly 1", tok, n)
			}
		}
	})
}

func TestGuardTripStopsAndStillCleansUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// TestLaneStoppedEarlyTripsGuard: a lane that stops on its own while the
// campaign is RUNNING (before the planned baseline end) is a guard trip, not a
// silent under-delivery (ruling C34a).
func TestLaneStoppedEarlyTripsGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.onCorr = func() { _ = h.worker.m.Stop(context.Background(), h.id+"-baseline") }
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Verdict != domain.VerdictFail || len(card.Reasons) == 0 || card.Reasons[0] != "guard:lane_stopped" {
			t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
		}
		if c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
	})
}

func TestWorkerLossIsInconclusive(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
			for _, r := range h.oscar.history {
				if name := r.AlertName; strings.HasPrefix(name, "Campaign-"+h.id+"-") && !strings.Contains(name, "resend") && r.Status == "firing" {
					return false
				}
			}
			return true
		})
	})
}

func TestEngineRecoveryAfterRestartRestoresEverything(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		if len(h.oscar.corrInc) == 0 {
			t.Fatal("no set B incidents existed before the kill; recovery closing them is untested")
		}
		if h.oscar.allFirings != firings {
			t.Fatalf("%d firings sent after the kill", h.oscar.allFirings-firings)
		}
	})
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
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestSetAOverrunMarksNotRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestStopHaltsLaneAndCleansUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		var mu sync.Mutex
		var firstStop time.Time
		h.worker.onStop = func() {
			mu.Lock()
			if firstStop.IsZero() {
				firstStop = h.clock.Now()
			}
			mu.Unlock()
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- h.run(ctx) }()
		waitFor(t, "correlation phase", func() bool { return h.oscar.correlates.Load() > 0 })
		// What runtime.StopCampaign does: STOPPING, then cancel.
		stopAt := h.clock.Now()
		if err := h.db.TransitionCampaign(context.Background(), h.id, domain.CampaignStopping, stopAt, "stop requested"); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		if firstStop.IsZero() || firstStop.Sub(stopAt) > guardEvery {
			t.Fatalf("worker Stop at %s, want within one tick of %s", firstStop, stopAt)
		}
		mu.Unlock()
		c, card := h.scorecard()
		if c.Status != domain.CampaignCompleted || c.Cleanup != domain.CleanupClean {
			t.Fatalf("status %s cleanup %s", c.Status, c.Cleanup)
		}
		if c.Verdict != domain.VerdictInconclusive || card.Reasons[0] != "stopped: operator stop" {
			t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
		}
		if card.Baseline.Planned == 0 || hasReason(card.Reasons, "baseline_under_delivered") {
			t.Fatalf("stopped window: baseline %+v reasons %v", card.Baseline, card.Reasons)
		}
		h.assertRestored()
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		for _, at := range h.oscar.laneFires {
			if at.After(stopAt.Add(guardEvery)) {
				t.Fatalf("lane fired at %s after the stop at %s", at, stopAt)
			}
		}
	})
}

func TestIncidentWithoutResolutionCommentIsDirty(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.withholdComments = true
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Cleanup != domain.CleanupDirty || c.Verdict == domain.VerdictPass || !hasReason(card.Reasons, "incidents still open: INC") {
			t.Fatalf("cleanup %s verdict %s reasons %v", c.Cleanup, c.Verdict, card.Reasons)
		}
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		for _, inc := range h.oscar.incidents {
			if inc.State == "6" {
				t.Fatalf("incident %s closed without OSCAR's resolution comment", inc.Number)
			}
		}
	})
}

func TestMissingEvidenceIsNotPass(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.sinkErr = true
		h.deps.TaskRuns = nil // no task history client: "not collected", never 0/0
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Verdict == domain.VerdictPass || !hasReason(card.Reasons, "missing evidence: sink receipts") {
			t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
		}
		if card.Homelab.Collected || card.Homelab.NotCollected == "" {
			t.Fatalf("homelab %+v", card.Homelab)
		}
	})
}

func TestRestoreRetriesTransientFailures(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.failEnable = 2 // the first two re-enables fail
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
		h.assertRestored()
	})
}

func TestDraftRefusedMarksIntentRestored(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.draftErr = &oscar.MachineError{Operation: "POST /api/v1/resend-policies", StatusCode: 422, Code: "http_error", Detail: "invalid"}
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, _ := h.scorecard()
		if c.Verdict != domain.VerdictError || c.Cleanup != domain.CleanupClean {
			t.Fatalf("verdict %s cleanup %s", c.Verdict, c.Cleanup)
		}
		h.assertRestored()
	})
}

func TestRestoreIntentWithoutIDByName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		r := &run{e: New(h.deps, h.clock), id: h.id, laneID: h.id + "-baseline", cohorts: map[string]*cohortMember{}}
		intent := func(name string) domain.CampaignMutation {
			return domain.CampaignMutation{Kind: kindPolicyCreate, ObjectID: name, Prior: mustJSON(policyPrior{Name: name, NotifierID: "n5", Action: "notification"})}
		}
		mk := func(name string, active bool) {
			p, err := h.oscar.CreateDraft(ctx, targetops.PolicyDefinition{NotifierID: "n5", Name: name, Action: "notification"}, h.oscar.revs["n5/notification"])
			if err != nil {
				t.Fatal(err)
			}
			h.oscar.policies[p.ID].enabled = active
		}
		mk("Campaign-x-R1", true)
		if err := r.restore(ctx, intent("Campaign-x-R1")); err != nil {
			t.Fatalf("one match: %v", err)
		}
		for id, p := range h.oscar.policies {
			if !p.deleted || p.enabled {
				t.Fatalf("policy %s not deactivated and deleted", id)
			}
		}
		if err := r.restore(ctx, intent("Campaign-x-R2")); err != nil {
			t.Fatalf("no match must count as restored: %v", err)
		}
		mk("Campaign-x-R3", false)
		mk("Campaign-x-R3", false)
		if err := r.restore(ctx, intent("Campaign-x-R3")); err == nil {
			t.Fatal("two exact matches must be an error")
		}
	})
}

// TestRestoreCreateByIDWhenAlreadyGone: a policy deleted outside CorrTest must
// count as restored, while a live one is still deactivated and deleted.
func TestRestoreCreateByIDWhenAlreadyGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		r := &run{e: New(h.deps, h.clock), id: h.id, laneID: h.id + "-baseline", cohorts: map[string]*cohortMember{}}
		create := func(name string) domain.CampaignMutation {
			p, err := h.oscar.CreateDraft(ctx, targetops.PolicyDefinition{NotifierID: "n5", Name: name, Action: "notification"}, h.oscar.revs["n5/notification"])
			if err != nil {
				t.Fatal(err)
			}
			h.oscar.policies[p.ID].enabled = true
			return domain.CampaignMutation{Kind: kindPolicyCreate, ObjectID: p.ID, Prior: mustJSON(policyPrior{p.ID, name, "n5", "notification"})}
		}
		live := create("Campaign-y-R1")
		if err := r.restore(ctx, live); err != nil {
			t.Fatalf("live policy: %v", err)
		}
		if p := h.oscar.policies[live.ObjectID]; !p.deleted || p.enabled {
			t.Fatal("live policy not deactivated and deleted")
		}
		gone := create("Campaign-y-R2")
		h.oscar.policies[gone.ObjectID].deleted = true // removed by hand
		if err := r.restore(ctx, gone); err != nil {
			t.Fatalf("already-deleted policy must count as restored: %v", err)
		}
	})
}

func TestUnknownOutcome(t *testing.T) {
	sent := time.Now()
	for _, tc := range []struct {
		row  domain.LaneRow
		want bool
	}{
		{domain.LaneRow{SentAt: sent, ErrorClass: "timeout"}, true},
		{domain.LaneRow{SentAt: sent, ErrorClass: "network"}, true},
		{domain.LaneRow{SentAt: sent}, true}, // sent, no answer recorded
		{domain.LaneRow{SentAt: sent, Done: true, Status: 500, ErrorClass: "http"}, false},
		{domain.LaneRow{Missed: true, ErrorClass: "missed"}, false},
		{domain.LaneRow{}, false}, // never sent
	} {
		if got := unknownOutcome(tc.row); got != tc.want {
			t.Errorf("%+v: got %v", tc.row, got)
		}
	}
}

// TestTicketFirstTriggerCompiles checks ruling C27 against the real compiler:
// each ticketed pattern still validates and compiles, and exactly one source
// alert per trigger case carries create_ticket=snow.
func TestTicketFirstTriggerCompiles(t *testing.T) {
	for _, pattern := range []string{"flood", "threshold"} {
		doc := corr.Builtin(pattern)
		ticketFirstTrigger(&doc)
		if err := corr.Validate(doc); err != nil {
			t.Fatalf("%s: %v", pattern, err)
		}
		plan, err := compiler.Compile(domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "01234567"}, doc, compiler.Capabilities{PipelineMode: correlationRun})
		if err != nil {
			t.Fatalf("%s: %v", pattern, err)
		}
		for _, c := range plan.Cases {
			n := 0
			for _, a := range c.Alerts {
				if a.Labels["create_ticket"] == "snow" {
					n++
					if !strings.HasPrefix(a.Annotations["summary"], "[CORRTEST]["+pattern+"][TRIGGER][01234567]") {
						t.Errorf("%s: ticketed alert summary %q", pattern, a.Annotations["summary"])
					}
				}
			}
			want := 0
			if strings.Contains(c.Name, "emits") || c.Polarity == "positive" {
				want = 1
			}
			if n != want {
				t.Errorf("%s case %s: %d ticketed alerts, want %d", pattern, c.Name, n, want)
			}
			// OSCAR holds ticketed alerts (anti-flap): History shows the ticketed
			// source 49-79 s after start under 5/s (camp_3b45fed18acaacee), after
			// the 45 s default window. Only the ticketed cases wait longer.
			if want == 1 && c.ObservationWindow < setBTicketObservation {
				t.Errorf("%s case %s: observation %s, want at least %s", pattern, c.Name, c.ObservationWindow, setBTicketObservation)
			}
			if want == 0 && c.ObservationWindow >= setBTicketObservation {
				t.Errorf("%s case %s: untickted case observes %s", pattern, c.Name, c.ObservationWindow)
			}
		}
		if plan.MaxDuration < corr.Builtin(pattern).MaxDuration+setBTicketObservation {
			t.Errorf("%s: plan budget %s does not cover the longer observation", pattern, plan.MaxDuration)
		}
	}
}

// TestIncidentListingFailureIsDirty: a SNOW listing that never succeeds is
// not "nothing pending"; cleanup is DIRTY with the reason (ruling C35).
func TestIncidentListingFailureIsDirty(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.listErr = -1
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Cleanup != domain.CleanupDirty || c.Verdict == domain.VerdictPass {
			t.Fatalf("cleanup %s verdict %s reasons %v", c.Cleanup, c.Verdict, card.Reasons)
		}
		evs, _ := h.db.RecentCampaignEvents(context.Background(), h.id, 1000)
		found := false
		for _, ev := range evs {
			found = found || ev.Type == "campaign.cleanup" && strings.Contains(ev.Summary, "incident listing")
		}
		if !found {
			t.Fatal("no cleanup event names the failed incident listing")
		}
	})
}

// TestIncidentListingRetried: transient listing failures are retried with
// backoff until the close deadline (ruling C35).
func TestIncidentListingRetried(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.listErr = 2
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s reasons %v", c.Cleanup, card.Reasons)
		}
		h.assertRestored()
	})
}

// TestResolveAllRepeatedUntilNoneRemain: the controller repeats resolve-all
// until the worker reports nothing still open (ruling C36).
func TestResolveAllRepeatedUntilNoneRemain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.worker.stuck.Store(2)
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
		if n := h.worker.resolves.Load(); n < 3 {
			t.Fatalf("resolve-all called %d times, want it repeated past the 2 stuck answers", n)
		}
		h.assertRestored()
	})
}

// TestResolveAllStillOpenIsDirty: identities still open after the resolve-all
// budget make cleanup DIRTY (ruling C36).
func TestResolveAllStillOpenIsDirty(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.worker.stuck.Store(-1)
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupDirty {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
	})
}

// TestCohortResolveFailureIsDirty: a cohort alert that could not be resolved
// makes cleanup DIRTY (ruling C36).
func TestCohortResolveFailureIsDirty(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.s.Resend = h.s.Resend[:3] // no ticket cohort: an open incident must not be what makes it DIRTY
		h.oscar.failResolves = true
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupDirty {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
	})
}

// TestCohortStillFiringInHistoryIsDirty: an accepted resolve is not cleanup;
// occurrences still firing in History make cleanup DIRTY and are named.
func TestCohortStillFiringInHistoryIsDirty(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.s.Resend = h.s.Resend[:3] // no ticket cohort: an open incident must not be what makes it DIRTY
		h.oscar.ignoreResolves = true
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupDirty {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
		evs, _ := h.db.RecentCampaignEvents(context.Background(), h.id, 400)
		named := false
		for _, ev := range evs {
			named = named || (ev.Type == "campaign.cleanup" && strings.Contains(ev.Summary, "still firing in History") && strings.Contains(ev.Summary, "Campaign-"+h.id))
		}
		if !named {
			t.Fatal("cleanup did not name the occurrences still firing")
		}
	})
}

// Every resolve is History-built: the native fingerprint and the exact startsAt
// of a recorded occurrence (fresh cohorts have several per alertname).
func TestCohortResolvesEchoHistoryOccurrences(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertRestored()
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		occurrences := map[string]int{}
		for _, row := range h.oscar.history {
			occurrences[row.AlertName]++
		}
		multi := false
		for _, n := range occurrences {
			multi = multi || n > 1
		}
		if !multi || len(h.oscar.resolvePosts) == 0 {
			t.Fatalf("fixture lacks repeated occurrences or History resolves: %v posts=%d", occurrences, len(h.oscar.resolvePosts))
		}
		for _, post := range h.oscar.resolvePosts {
			matched := false
			for _, row := range h.oscar.history {
				matched = matched || (row.Labels["am_fingerprint"] == post.Fingerprint && row.StartsAt.Format(time.RFC3339Nano) == post.StartsAt)
			}
			if !matched || post.Labels["am_fingerprint"] != "" {
				t.Fatalf("resolve not built from a History occurrence: %+v", post)
			}
		}
	})
}

// sinkEnableMutation records what the B5 smoke records: CAMPAIGN_SINK by
// name, prior status disabled. The sink is left enabled.
func (h *harness) sinkEnableMutation() {
	h.t.Helper()
	if _, err := h.db.RecordMutation(context.Background(), domain.CampaignMutation{CampaignID: h.id, Kind: "notifier_enable", ObjectID: "CAMPAIGN_SINK",
		Prior: []byte(`{"status":"disabled"}`), RecordedAt: h.clock.Now()}); err != nil {
		h.t.Fatal(err)
	}
	h.oscar.mu.Lock()
	h.oscar.notifiers["n5"].Status = "enabled"
	h.oscar.mu.Unlock()
}

// Ruling C40: restore knows the pre-check's notifier_enable (by name, readback).
func TestRestoreNotifierEnableByName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.sinkEnableMutation()
		r := &run{e: New(h.deps, h.clock), id: h.id, laneID: h.id + "-baseline", cohorts: map[string]*cohortMember{}}
		open, _ := h.db.OpenMutations(context.Background(), h.id)
		if err := r.restore(context.Background(), open[0]); err != nil {
			t.Fatal(err)
		}
		if st := h.oscar.notifiers["n5"].Status; st != "disabled" {
			t.Fatalf("CAMPAIGN_SINK %s after restore", st)
		}
	})
}

// A pre-check-only campaign that leaves a mutation open restores it and
// reports the cleanup it did, not NOT_REQUIRED.
func TestPrecheckOnlyRestoresLeftoverMutation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.deps.Precheck = func(context.Context, scenario.Scenario) []precheck.Result {
			h.sinkEnableMutation() // the smoke failed to restore
			return []precheck.Result{{ID: "B1", Tier: precheck.Blocking, Status: precheck.Pass}}
		}
		if err := New(h.deps, h.clock).Run(context.Background(), h.id, runtime.CampaignOptions{Scenario: h.s, PrecheckOnly: true}); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupClean || c.Verdict != domain.VerdictPass {
			t.Fatalf("cleanup %s verdict %s", c.Cleanup, c.Verdict)
		}
		h.assertRestored()
	})
}

// Ruling C40: cleanup of a COMPLETED campaign re-runs restore for its open
// mutations, records the outcome, never changes the verdict, and may move
// DIRTY to CLEAN.
func TestCleanupCompletedCampaign(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		e := New(h.deps, h.clock)
		if _, err := e.Cleanup(ctx, h.id); err == nil {
			t.Fatal("cleanup of a campaign that is not COMPLETED must be refused")
		}
		h.sinkEnableMutation()
		if err := h.db.CompleteCampaign(ctx, h.id, domain.VerdictInconclusive, domain.CleanupDirty, false, nil, nil, h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		st, err := e.Cleanup(ctx, h.id)
		if err != nil || st != domain.CleanupClean {
			t.Fatalf("cleanup %s %v", st, err)
		}
		c := h.campaign()
		if c.Cleanup != domain.CleanupClean || c.Verdict != domain.VerdictInconclusive || c.Status != domain.CampaignCompleted {
			t.Fatalf("campaign %s %s %s", c.Status, c.Verdict, c.Cleanup)
		}
		h.assertRestored()
		evs, _ := h.db.RecentCampaignEvents(ctx, h.id, 100)
		found := false
		for _, ev := range evs {
			found = found || ev.Type == "campaign.cleanup" && strings.Contains(ev.Summary, "DIRTY -> CLEAN")
		}
		if !found {
			t.Fatal("no audit event for the cleanup re-run")
		}
	})
}

// Ruling C41: recovering a campaign that never reached traffic skips the
// lane, the queue wait and scoring, completes INCONCLUSIVE "no traffic",
// still restores its mutations, and exports without a ledger.
func TestRecoverWithoutTraffic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		// isolation ran (one notifier disabled), then the process died before T0
		if _, err := h.db.RecordMutation(ctx, domain.CampaignMutation{CampaignID: h.id, Kind: kindNotifier, ObjectID: "n1",
			Prior: mustJSON(notifierPrior{"EMAIL", "enabled"}), RecordedAt: h.clock.Now()}); err != nil {
			t.Fatal(err)
		}
		h.oscar.notifiers["n1"].Status = "disabled"
		m := &queueMetrics{depths: []float64{500}} // never idle: a queue wait would poll for 30 min
		h.deps.Metrics = m
		h.worker.down.Store(true) // a lane resolve would retry for 15 min
		e := New(h.deps, h.clock)
		if err := e.Recover(ctx, h.id); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Status != domain.CampaignCompleted || c.Verdict != domain.VerdictInconclusive || !hasReason(card.Reasons, "no traffic") {
			t.Fatalf("status %s verdict %s reasons %v", c.Status, c.Verdict, card.Reasons)
		}
		if c.Cleanup != domain.CleanupClean || m.calls != 0 || h.worker.resolves.Load() != 0 {
			t.Fatalf("cleanup %s queue polls %d resolve-all calls %d", c.Cleanup, m.calls, h.worker.resolves.Load())
		}
		h.assertRestored()
		if _, err := e.ExportCampaign(ctx, h.id, filepath.Join(t.TempDir(), "c.zip")); err != nil {
			t.Fatalf("export without a ledger: %v", err)
		}
	})
}

// Finding 12: an operator stop before traffic (here: during isolation) is
// INCONCLUSIVE "stopped before traffic", not ERROR, and cleanup still runs.
func TestStopBeforeTrafficIsInconclusive(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		var once sync.Once
		h.oscar.onSetNotifier = func() {
			once.Do(func() { // what runtime.StopCampaign does: STOPPING, then cancel
				if err := h.db.TransitionCampaign(context.Background(), h.id, domain.CampaignStopping, h.clock.Now(), "stop requested"); err != nil {
					t.Error(err)
				}
				cancel()
			})
		}
		if err := h.run(ctx); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Verdict != domain.VerdictInconclusive || len(card.Reasons) == 0 || card.Reasons[0] != "stopped before traffic" {
			t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
		}
		if c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
		h.assertRestored()
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		if len(h.oscar.laneFires) != 0 {
			t.Fatalf("%d lane firings after a stop before traffic", len(h.oscar.laneFires))
		}
	})
}

// Finding 12c: a ticketed set B run whose incident never appears is a miss
// in the ticket success rate, not just a separate figure.
func TestMissingSetBTicketIsAMiss(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.corrNoTicket = true
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if card.Tickets.Failed == 0 || c.Verdict != domain.VerdictFail || !hasReason(card.Reasons, "tickets.success") {
			t.Fatalf("verdict %s tickets %+v reasons %v", c.Verdict, card.Tickets, card.Reasons)
		}
	})
}

// TestSlowIncidentPassRelistsAndVisitsSetB: camp_3b45fed18acaacee closed 540
// incidents in one pass (03:22:47-03:29), longer than commentWait. The pass's
// only listing predated OSCAR's resolution comments, so the cohort incidents
// were scored failed and left open, and the deadline skipped every set B
// listing (unknown). A pass that starts before the deadline must finish, and
// one that runs past it must be followed by a fresh listing.
func TestSlowIncidentPassRelistsAndVisitsSetB(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.resolveDelay = commentWait / 2
		h.oscar.staleListings = 1
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c, card := h.scorecard()
		if c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s reasons %v", c.Cleanup, card.Reasons)
		}
		if card.Tickets.Failed != 0 || card.Tickets.Unknown != 0 {
			t.Fatalf("tickets %+v, want every incident judged on a fresh read", card.Tickets)
		}
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		for _, inc := range h.oscar.corrInc {
			if inc.State != "6" {
				t.Errorf("set B incident %s left open", inc.Number)
			}
		}
	})
}
