package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Cohort route (C15): native Alertmanager suppresses unchanged reposts, so
// cohorts are posted as Alertmanager webhook deliveries straight to the
// Alertmanager Middleware (POST /api/v1/alerts), each a distinct delivery
// with the same fingerprint. This is the route the 2026-11-13 episode-resend
// local UAT used for occurrence milestones (oscar/docs/testing/
// 2026-11-13-episode-resend-local-uat.md, "Native Alertmanager suppresses a
// repost..."). The same route serves the correlation runner's injections.

const cohortEvent = "campaign.cohort"

type cohortPrior struct {
	Name     string            `json:"name"`
	Labels   map[string]string `json:"labels"`
	StartsAt time.Time         `json:"startsAt"`
}

// resendPhase activates the policies at T+90 (waiting for cutover), then
// re-delivers the cohorts every Redelivery until ResendUntil. An activation
// timeout or unavailable receiver makes resend INCONCLUSIVE and turns the
// cohorts off; the drafts are still cleaned up.
func (r *run) resendPhase(ctx context.Context) {
	if len(r.policies) == 0 || !r.e.clock.SleepUntil(ctx, r.at("resend", false)) {
		return
	}
	stable := context.WithoutCancel(ctx)
	for _, p := range r.policies {
		if _, err := r.record(stable, kindPolicyActivate, p.id, policyPrior{p.id, p.name, p.notifierID, p.spec.Action}); err != nil {
			r.mu.Lock()
			r.resendIncon = "resend: record activation: " + err.Error()
			r.mu.Unlock()
			return
		}
		var pol targetops.Policy
		err := r.withRevision(ctx, p.notifierID, p.spec.Action, func(rev int) (int, error) {
			var err error
			pol, err = r.e.d.Target.Activate(ctx, p.id, p.notifierID, p.spec.Action, rev, activationWait)
			return pol.Revision, err
		})
		if err != nil {
			why := "activation failed"
			if errors.Is(err, targetops.ErrActivationTimeout) || errors.Is(err, targetops.ErrReceiverUnavailable) {
				why = "activation unavailable"
			}
			r.mu.Lock()
			r.resendIncon = fmt.Sprintf("resend %s: %s: %v", p.spec.Key, why, err)
			r.mu.Unlock()
			r.event(stable, "campaign.resend", "warn", r.resendIncon+"; cohorts off", nil)
			return
		}
		p.active, p.activeAt = true, r.now()
	}
	origin := r.now()
	r.mu.Lock()
	for _, p := range r.policies {
		lane := laneFor(p.spec)
		for k := 1; k <= p.spec.Cohort; k++ {
			name := fmt.Sprintf("Campaign-%s-%s-%d", r.id, lane, k)
			labels := map[string]string{"alertname": name, "campaign_run": r.id, "campaign_lane": lane, "severity": "warning", "create_ticket": "false"}
			if p.spec.Action == "ticket" {
				labels["create_ticket"] = "snow"
			}
			r.cohorts[name] = &cohortMember{alert: Alert{Name: name, Labels: labels, StartsAt: origin}, policy: p, origin: origin}
		}
	}
	r.mu.Unlock()
	until := r.t0.Add(r.s.ResendUntil)
	every := r.s.Redelivery
	if every <= 0 {
		every = time.Minute
	}
	for tick := origin; tick.Before(until) && r.e.clock.SleepUntil(ctx, tick); tick = tick.Add(every) {
		for _, m := range r.members() {
			r.deliver(ctx, m, tick)
		}
	}
}

func (r *run) members() []*cohortMember {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*cohortMember, 0, len(r.cohorts))
	for _, m := range r.cohorts {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].alert.Name < out[j].alert.Name })
	return out
}

// deliver posts one cohort delivery. Occurrence and either cohorts are fresh
// (new startsAt); time and ticket cohorts repeat unchanged. A ticket member
// whose first delivery has an unknown outcome is never re-posted: a retry
// could create a duplicate incident.
func (r *run) deliver(ctx context.Context, m *cohortMember, tick time.Time) {
	if m.ambiguous || ctx.Err() != nil {
		return
	}
	fresh := m.policy.spec.Fresh
	if fresh {
		m.alert.StartsAt = tick
	}
	if !m.recorded { // recovery resolves cohorts from these events
		r.event(context.WithoutCancel(ctx), cohortEvent, "info", "cohort member "+m.alert.Name, cohortPrior{m.alert.Name, m.alert.Labels, m.alert.StartsAt})
		m.recorded = true
	}
	status, err := r.e.d.Target.PostAlert(ctx, m.alert)
	switch {
	case status == 0 && err != nil && ctx.Err() == nil:
		if m.policy.spec.Action == "ticket" && len(m.deliveries) == 0 {
			m.ambiguous = true
			r.mu.Lock()
			r.ticketUnknown++
			r.mu.Unlock()
			r.event(context.WithoutCancel(ctx), "campaign.ticket", "warn", "ambiguous ticket create for "+m.alert.Name+"; not retried", nil)
		}
	case err == nil && status/100 == 2:
		m.deliveries = append(m.deliveries, resend.Delivery{At: tick.Sub(m.origin), Fresh: fresh})
	}
}

// resolveCohorts resolves every delivered cohort member now.
func (r *run) resolveCohorts(ctx context.Context) {
	for _, m := range r.members() {
		if !m.recorded || m.resolved {
			continue
		}
		if err := r.resolveAlert(ctx, m.alert); err != nil {
			r.event(ctx, "campaign.cohort", "warn", err.Error(), nil)
			continue
		}
		m.resolved = true
	}
}

func (r *run) resolveAlert(ctx context.Context, a Alert) error {
	a.EndsAt = r.now()
	status, err := r.e.d.Target.PostAlert(ctx, a)
	if err == nil && status/100 != 2 {
		err = fmt.Errorf("resolve %s: HTTP %d", a.Name, status)
	}
	return err
}

// recoverCohorts resolves every cohort member recorded in the event log.
func (r *run) recoverCohorts(ctx context.Context) {
	evs, err := r.store().RecentCampaignEvents(ctx, r.id, maxEvents)
	if err != nil {
		r.event(ctx, "campaign.cohort", "error", "read cohort events: "+err.Error(), nil)
		return
	}
	for _, ev := range evs {
		var p cohortPrior
		if ev.Type != cohortEvent || json.Unmarshal(ev.Detail, &p) != nil {
			continue
		}
		if err := r.resolveAlert(ctx, Alert{Name: p.Name, Labels: p.Labels, StartsAt: p.StartsAt}); err != nil {
			r.event(ctx, "campaign.cohort", "warn", err.Error(), nil)
		}
	}
}
