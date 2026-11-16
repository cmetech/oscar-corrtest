package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// AT&T shape (scenario v2, 2026-11-16 capacity findings on OSCAR 26.4.4).
//
// Standing pool: Size alerts on hosts that are not in inventory, posted once
// across the first Interval and then re-delivered unchanged every Interval, as
// native Alertmanager re-sends a group on repeat_interval. They use the cohort
// route (Alertmanager webhook delivery to the Alertmanager Middleware) because
// native Alertmanager suppresses unchanged reposts.
//
// Breaker window: the queue thresholds are overridden (a recorded mutation,
// restored at the window end and by cleanup), the whole pool is posted at once
// to trip the intake breaker, resolve-only posts probe whether resolves are
// admitted while it is tripped, and one firing post every reopenProbeEvery
// times the reopen. The baseline lane keeps posting through native
// Alertmanager, whose webhook retries are the storm the scorecard counts.

const (
	kindQueueOverride   = "queue_override"
	queueOverrideObject = "alerts/ratelimits"
	standingLane        = "standing"
	reopenProbeEvery    = 5 * time.Second
	// overrideSettle lets the thresholds reach every ingest process before the burst.
	overrideSettle = 15 * time.Second
	// breakerQuietTail keeps the History-stall and queue-growth guards quiet while
	// the burst and the Alertmanager retries drain after the window.
	breakerQuietTail = 5 * time.Minute
)

// breakerTiming caps the settle, reopen-probe and quiet-tail times at a
// fraction of the breaker window (they equal the constants for a 10-minute one).
func (r *run) breakerTiming() (settle, probeEvery, quietTail time.Duration) {
	span := r.s.Phase("breaker").End - r.s.Phase("breaker").Start
	return min(overrideSettle, span/8), min(reopenProbeEvery, span/40), min(breakerQuietTail, span/2)
}

type standingMember struct {
	alert Alert
}

func (r *run) standingMembers() []standingMember {
	st := r.s.Standing
	sev := []string{"warning", "minor", "major", "critical"}
	out := make([]standingMember, st.Size)
	for k := range out {
		name := fmt.Sprintf("Campaign-%s-standing-%d", r.id, k+1)
		labels := map[string]string{"alertname": name, "campaign_run": r.id, "campaign_lane": standingLane,
			"severity": sev[k%len(sev)], "instance": fmt.Sprintf("campaign-device-%03d", k%max(st.Instances, 1)+1), "create_ticket": "false"}
		out[k] = standingMember{alert: Alert{Name: name, Labels: labels, StartsAt: r.t0}}
	}
	return out
}

// standingPhase runs the pool from T0, then the breaker window, then resolves
// the pool at the drain start. On a stop or trip it returns early; cleanup's
// History read-back resolves whatever is still firing.
func (r *run) standingPhase(ctx context.Context) {
	st := r.s.Standing
	if st == nil || st.Size <= 0 || st.Interval <= 0 {
		return
	}
	members := r.standingMembers()
	r.mu.Lock()
	r.standing = score.StandingStats{Members: len(members), Posts: map[string]int{}, Resolves: map[string]int{}}
	r.mu.Unlock()
	sem := make(chan struct{}, max(st.Concurrency, 1))
	var inflight sync.WaitGroup
	until := r.t0.Add(st.Until)
	for k := 0; ; k++ {
		at := r.t0.Add(time.Duration(k) * st.Interval / time.Duration(st.Size))
		if !at.Before(until) || !r.e.clock.SleepUntil(ctx, at) {
			break
		}
		m := members[k%len(members)]
		sem <- struct{}{}
		inflight.Add(1)
		go func() {
			defer func() { <-sem; inflight.Done() }()
			status, _ := r.e.d.Target.PostAlert(ctx, m.alert)
			r.countStanding("post", status)
		}()
	}
	inflight.Wait()
	if r.s.Breaker != nil {
		r.breakerWindow(ctx, members)
	}
	if !r.e.clock.SleepUntil(ctx, r.at("drain", false)) {
		return
	}
	r.resolveStanding(context.WithoutCancel(ctx), members, sem)
}

func (r *run) countStanding(kind string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if kind == "resolve" {
		r.standing.Resolves[score.Outcome(status)]++
		return
	}
	r.standing.Posts[score.Outcome(status)]++
}

func (r *run) resolveStanding(ctx context.Context, members []standingMember, sem chan struct{}) {
	var wg sync.WaitGroup
	for _, m := range members {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			r.countStanding("resolve", r.postStandingResolve(ctx, m))
		}()
	}
	wg.Wait()
}

func (r *run) postStandingResolve(ctx context.Context, m standingMember) int {
	req, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: m.alert.Name, Status: "resolved", Labels: m.alert.Labels,
		Annotations: map[string]string{"summary": m.alert.Name}, StartsAt: m.alert.StartsAt, EndsAt: r.now()})
	if err != nil {
		return 400
	}
	status, _ := r.e.d.Target.PostAlertGroup(ctx, req)
	return status
}

// breakerWindow forces the intake breaker for the breaker phase and always
// restores the thresholds it changed, even when ctx ends early.
func (r *run) breakerWindow(ctx context.Context, members []standingMember) {
	b := r.s.Breaker
	r.mu.Lock()
	r.breaker = score.BreakerStats{Burst: map[string]int{}, ReopenAfterSeconds: -1}
	r.mu.Unlock()
	skip := func(why string) {
		r.mu.Lock()
		r.breaker.Skipped = why
		r.mu.Unlock()
		r.event(context.WithoutCancel(ctx), "campaign.breaker", "warn", "breaker window skipped: "+why, nil)
	}
	if !r.e.clock.SleepUntil(ctx, r.at("breaker", false)) {
		skip("campaign ended before the breaker phase")
		return
	}
	stable := context.WithoutCancel(ctx)
	prior, err := r.e.d.Target.AlertLimits(ctx)
	if err != nil {
		skip("read rate limits: " + err.Error())
		return
	}
	forced, err := targetops.WithQueue(prior.Overrides, b.QueueHigh, b.QueueLow)
	if err != nil {
		skip(err.Error())
		return
	}
	id, err := r.record(stable, kindQueueOverride, queueOverrideObject, prior.Overrides)
	if err != nil {
		skip("record override: " + err.Error())
		return
	}
	if _, err := r.e.d.Target.SetAlertLimits(ctx, prior.Revision, forced); err != nil {
		// A refused PUT (no manage:rate_limits, stale revision) changed nothing.
		if cur, rerr := r.e.d.Target.AlertLimits(stable); rerr == nil && targetops.SameOverrides(cur.Overrides, prior.Overrides) {
			_ = r.restored(stable, id)
		}
		skip("override queue thresholds: " + err.Error())
		return
	}
	r.mu.Lock()
	r.breaker.Applied = true
	r.mu.Unlock()
	r.event(stable, "campaign.breaker", "info", fmt.Sprintf("queue thresholds forced to %d/%d", b.QueueHigh, b.QueueLow), nil)
	defer r.restoreQueueOverride(stable, id)

	end := r.at("breaker", true)
	settle, probeEvery, _ := r.breakerTiming()
	if !r.e.clock.SleepUntil(ctx, r.now().Add(settle)) {
		return
	}
	start := r.now()
	var tripped sync.Once
	trippedAt := make(chan time.Time, 1)
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(b.BurstConcurrency, 1))
	probes := members[:min(b.ResolveProbes, len(members))]
	for _, m := range members {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			status, _ := r.e.d.Target.PostAlert(ctx, m.alert)
			r.mu.Lock()
			r.breaker.Burst[score.Outcome(status)]++
			r.mu.Unlock()
			if status == 503 {
				tripped.Do(func() { trippedAt <- r.now() })
			}
		}()
	}
	wg.Wait()
	var at time.Time
	select {
	case at = <-trippedAt:
	default:
		return // never tripped: nothing to probe
	}
	r.mu.Lock()
	r.breaker.Tripped, r.breaker.TripAfterSeconds = true, at.Sub(start).Seconds()
	r.mu.Unlock()
	// Resolve-only posts while tripped: AT&T's door rejected resolves too.
	var admitted int
	var mu sync.Mutex
	for _, m := range probes {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if s := r.postStandingResolve(ctx, m); s/100 == 2 {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	r.breaker.ResolveProbes, r.breaker.ResolveAdmitted = len(probes), admitted
	r.mu.Unlock()
	// Reopen: one firing repeat every reopenProbeEvery until one is admitted.
	rest := members[len(probes):]
	for i := 0; len(rest) > 0 && r.now().Before(end); i++ {
		if status, _ := r.e.d.Target.PostAlert(ctx, rest[i%len(rest)].alert); status/100 == 2 {
			r.mu.Lock()
			r.breaker.ReopenAfterSeconds = r.now().Sub(at).Seconds()
			r.mu.Unlock()
			break
		}
		if !r.e.clock.SleepUntil(ctx, r.now().Add(probeEvery)) {
			break
		}
	}
	r.e.clock.SleepUntil(ctx, end)
}

// restoreQueueOverride puts the overrides read before the window back.
func (r *run) restoreQueueOverride(ctx context.Context, id int64) {
	open, err := r.store().OpenMutations(ctx, r.id)
	if err != nil {
		return // cleanup retries every open mutation
	}
	for _, m := range open {
		if m.ID != id {
			continue
		}
		if err := r.restoreWithRetry(ctx, m); err != nil {
			r.event(ctx, "campaign.breaker", "error", "restore queue thresholds: "+err.Error(), nil)
			return
		}
		_ = r.restored(ctx, id)
		r.mu.Lock()
		r.breaker.Restored = true
		r.mu.Unlock()
		r.event(ctx, "campaign.breaker", "info", "queue thresholds restored", nil)
	}
}

// restoreAlertLimits is the queue_override restore: put the recorded overrides
// back at the current revision, then read back.
func (r *run) restoreAlertLimits(ctx context.Context, prior json.RawMessage) error {
	cur, err := r.e.d.Target.AlertLimits(ctx)
	if err != nil {
		return err
	}
	if targetops.SameOverrides(cur.Overrides, prior) {
		return nil
	}
	got, err := r.e.d.Target.SetAlertLimits(ctx, cur.Revision, prior)
	if err != nil {
		return err
	}
	if !targetops.SameOverrides(got.Overrides, prior) {
		return fmt.Errorf("read back overrides differ from the recorded ones")
	}
	return nil
}

// attStats is the v2 scorecard section: pool and breaker counts from this run,
// plus per-alert budgets read from VictoriaMetrics over the steady window and
// the breaker window (to the end of the guards' quiet tail).
func (r *run) attStats(ctx context.Context) *score.ATT {
	r.mu.Lock()
	a := &score.ATT{Standing: r.standing, Breaker: r.breaker, Budgets: map[string]float64{}}
	r.mu.Unlock()
	steadyEnd := r.at("drain", false)
	if r.s.Breaker != nil {
		steadyEnd = r.at("breaker", false)
	}
	r.scalars(ctx, a.Budgets, score.BudgetQueries, r.t0, steadyEnd)
	if r.s.Breaker != nil {
		_, _, tail := r.breakerTiming()
		r.scalars(ctx, a.Budgets, score.BreakerQueries, r.at("breaker", false), r.at("breaker", true).Add(tail))
	}
	a.Derive()
	return a
}

// deliveryStats is the delivery-retry section: the per-alert budgets plus the
// delivery queries, over T0 to end (copies keep being made through the drain).
func (r *run) deliveryStats(ctx context.Context, end time.Time) *score.ATT {
	a := &score.ATT{Budgets: map[string]float64{}, Breaker: score.BreakerStats{Skipped: "not part of this scenario"}}
	r.scalars(ctx, a.Budgets, score.BudgetQueries, r.t0, end)
	r.scalars(ctx, a.Budgets, score.DeliveryQueries, r.t0, end)
	a.Derive()
	return a
}

// scalars evaluates each windowed query at end; a missing series is left out.
func (r *run) scalars(ctx context.Context, into map[string]float64, queries map[string]string, start, end time.Time) {
	if !end.After(start) {
		return
	}
	window := fmt.Sprintf("%ds", int(end.Sub(start).Seconds()))
	for name, q := range queries {
		if v, err := r.e.d.Metrics.Query(ctx, fmt.Sprintf(q, window), end); err == nil && len(v) > 0 {
			into[name] = v[0].Value
		}
	}
}
