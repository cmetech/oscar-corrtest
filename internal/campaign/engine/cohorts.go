package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
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
	r.laneStarts = append(r.laneStarts, origin.Add(r.s.Redelivery)) // every member has posted once
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
		ms := r.members()
		for i, m := range ms {
			slot := tick
			if r.s.Paced { // member i at its own offset: no burst of every member at once
				slot = tick.Add(time.Duration(i) * every / time.Duration(len(ms)))
				if !r.e.clock.SleepUntil(ctx, slot) {
					break
				}
			}
			r.deliver(ctx, m, slot)
		}
	}
	r.endEpisodes(context.WithoutCancel(ctx))
	// OSCAR's status lists only open episodes, and a scenario may resolve the
	// cohorts (drain) before scoring: read exhaustion now, after the last
	// resends had until the phase end to land.
	if !r.e.clock.SleepUntil(ctx, r.at("resend", true)) {
		return
	}
	for _, p := range r.policies {
		if st, err := r.e.d.Target.PolicyStatus(ctx, p.notifierID, p.spec.Action, p.id); err == nil {
			r.mu.Lock()
			p.status = &st
			r.mu.Unlock()
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
	if m.policy.spec.Episode > 0 {
		r.deliverEpisode(ctx, m, tick)
		return
	}
	fresh := m.policy.spec.Fresh
	if fresh {
		m.alert.StartsAt = tick
	}
	if !m.recorded { // evidence of the cohort; cleanup resolves from History
		r.event(context.WithoutCancel(ctx), cohortEvent, "info", "cohort member "+m.alert.Name, cohortPrior{m.alert.Name, m.alert.Labels, m.alert.StartsAt})
		m.recorded = true
	}
	start := r.now()
	status, err := r.e.d.Target.PostAlert(ctx, m.alert)
	r.sourceOffer("ordinary_resend", start, status)
	r.offer("resend", false, 1)
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

// deliverEpisode is deliver for an episode cohort (PolicySpec.Episode).
// Escalation acts on native episodes (OSCAR's design doc: the webhook bypass
// never proves native admission), so these members go through native
// Alertmanager's public v2 route, like the worker lane: a repost with the
// same startsAt refreshes the alert (native keeps it firing for its
// resolve_timeout after each), and every Episode the member is resolved there
// and fired again with startsAt tick, opening a new episode and a new
// escalation. The lane label starts with "native-", so cleanup leaves the
// clears to this source; endEpisodes resolves the last episodes.
func (r *run) deliverEpisode(ctx context.Context, m *cohortMember, tick time.Time) {
	if r.e.d.nativeSender == nil {
		if !m.recorded {
			r.noteMissing("native v2 sender unavailable for escalation cohort member " + m.alert.Name)
			m.recorded = true
		}
		return
	}
	if n := len(m.episodes); n > 0 && tick.Sub(m.episodes[n-1].StartsAt) >= m.policy.spec.Episode {
		r.resolveEpisode(ctx, m)
	}
	if n := len(m.episodes); n == 0 || !m.episodes[n-1].End.IsZero() {
		// Each episode is its own alert (name suffix -e<k>): Alertmanager merges
		// a re-fire of the same labels whose startsAt overlaps the resolved alert
		// back into it (keeping the first startsAt), so it would open no new
		// native episode. startsAt is the wall time of its first post, when the
		// escalation clock (native active_since) starts.
		m.episodes = append(m.episodes, score.EscalationEpisode{Alert: fmt.Sprintf("%s-e%d", m.alert.Name, n+1), StartsAt: r.now().Truncate(time.Millisecond)})
	}
	if !m.recorded {
		r.event(context.WithoutCancel(ctx), cohortEvent, "info", "cohort member "+m.alert.Name, cohortPrior{m.alert.Name, m.alert.Labels, m.alert.StartsAt})
		m.recorded = true
	}
	ep := &m.episodes[len(m.episodes)-1]
	if ep.FirstAt.IsZero() { // not yet accepted: the episode starts at the post that is
		ep.StartsAt = r.now().Truncate(time.Millisecond)
	}
	start := r.now()
	status, err := r.e.d.nativeSender.Send(ctx, episodeEvent(m, ep, lane.Firing), r.id, m.alert.Labels["campaign_lane"])
	r.offer("resend", false, 1)
	if err == nil && status/100 == 2 {
		m.deliveries = append(m.deliveries, resend.Delivery{At: tick.Sub(m.origin)})
		if ep.FirstAt.IsZero() {
			ep.FirstAt = start
		}
	}
}

func episodeEvent(m *cohortMember, ep *score.EscalationEpisode, kind lane.EventKind) lane.Event {
	return lane.Event{Identity: ep.Alert, Instance: "corrtest-escalate", Severity: m.alert.Labels["severity"], StartsAt: ep.StartsAt, Kind: kind}
}

// resolveEpisode resolves a member's current episode at the native source
// and ends it. A failed resolve is logged: native resolve_timeout clears it.
func (r *run) resolveEpisode(ctx context.Context, m *cohortMember) {
	n := len(m.episodes)
	if n == 0 || !m.episodes[n-1].End.IsZero() {
		return
	}
	status, err := r.e.d.nativeSender.Send(ctx, episodeEvent(m, &m.episodes[n-1], lane.Resolve), r.id, m.alert.Labels["campaign_lane"])
	r.offer("resend", true, 1)
	if err == nil && status/100 != 2 {
		err = fmt.Errorf("HTTP %d", status)
	}
	if err != nil {
		r.event(context.WithoutCancel(ctx), cohortEvent, "warn", "episode resolve "+m.episodes[n-1].Alert+": "+err.Error(), nil)
	}
	m.episodes[n-1].End = r.now()
}

// endEpisodes resolves every episode cohort member's last episode when the
// cohorts stop, even on a stop.
func (r *run) endEpisodes(ctx context.Context) {
	for _, m := range r.members() {
		if m.policy.spec.Episode > 0 && r.e.d.nativeSender != nil {
			r.resolveEpisode(ctx, m)
		}
	}
}

// resolveFiring reads the campaign's History and resolves every occurrence
// still firing, each by its own native fingerprint and startsAt (fresh cohorts
// leave one occurrence per delivery). The worker resolves its lane through
// native Alertmanager, so lane occurrences are included only when
// includeLane is set (cleanup's last resort). It returns the occurrences that
// were firing when read; an unreadable History is an error, never "none".
func (r *run) resolveFiring(ctx context.Context, includeLane bool) ([]oscar.HistoryRecord, error) {
	if r.t0.IsZero() { // no traffic was sent
		return nil, nil
	}
	rows, err := r.e.d.Target.History(ctx, r.id, r.t0.Add(-time.Minute), r.now().Add(time.Minute))
	if err != nil {
		return nil, err
	}
	var firing []oscar.HistoryRecord
	for _, row := range rows {
		if row.Labels["campaign_run"] != r.id || strings.EqualFold(row.Status, "resolved") || (!includeLane && row.Labels["campaign_lane"] == r.laneID) {
			continue
		}
		firing = append(firing, row)
		if strings.HasPrefix(row.Labels["campaign_lane"], "native-") || row.Labels["integration"] == "tl1_probe" || row.Labels["campaign_lane"] == "receivers" { // receivers: cleanupReceivers closes them in native Alertmanager
			continue
		} // only the actual source owns these clears
		req, err := oscar.BuildHistoryResolve(row, r.id)
		if err == nil {
			// The latest re-post first: it is the pair that keeps the row open.
			for _, at := range append(r.freshStarts(row), row.StartsAt) {
				req.Alerts[0].StartsAt = at.UTC().Format(time.RFC3339Nano)
				var status int
				start := r.now()
				status, err = r.e.d.Target.PostAlertGroup(ctx, req)
				r.sourceOffer("controller_clear", start, status)
				if err == nil && status/100 != 2 {
					err = fmt.Errorf("HTTP %d", status)
				}
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			r.event(ctx, cohortEvent, "warn", fmt.Sprintf("resolve %s (history %s): %v", row.AlertName, row.ID, err), nil)
		}
	}
	return firing, nil
}

// freshStarts are the later startsAt values of a fresh cohort member, newest
// first: every Redelivery after the row's startsAt, before ResendUntil. OSCAR
// merges a fresh re-post into the open History row (which keeps the first
// startsAt) as its open pair, so only a resolve of the latest one closes the
// row. Derived from the plan, so a cleanup re-run after a restart still has it;
// a resolve of a pair that is closed or was never posted changes nothing.
func (r *run) freshStarts(row oscar.HistoryRecord) []time.Time {
	fresh := false
	for _, p := range r.s.Resend {
		fresh = fresh || p.Fresh && laneFor(p) == row.Labels["campaign_lane"]
	}
	every, until := r.s.Redelivery, r.t0.Add(r.s.ResendUntil)
	if !fresh || every <= 0 || r.t0.IsZero() {
		return nil
	}
	var out []time.Time
	for at := row.StartsAt.Add(every); at.Before(until); at = at.Add(every) {
		out = append([]time.Time{at}, out...)
	}
	return out
}

// historyResolveBudget bounds cleanup's History read-back (polled every
// heartbeatEvery), after the drain and the incident wait already gave
// resolves time to land.
const historyResolveBudget = 2 * time.Minute

// verifyHistoryResolved is cleanup's verdict input: CLEAN needs every campaign
// occurrence to read back resolved. Firing ones are resolved each round; any
// still firing at the budget are named as a cleanup failure.
func historyNames(rows []oscar.HistoryRecord) string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, fmt.Sprintf("%s (history %s, startsAt %s)", row.AlertName, row.ID, row.StartsAt.UTC().Format(time.RFC3339Nano)))
	}
	return strings.Join(names, ", ")
}

func (r *run) verifyHistoryResolved(ctx context.Context) {
	deadline := r.now().Add(historyResolveBudget)
	for {
		firing, err := r.resolveFiring(ctx, true)
		if err == nil && len(firing) == 0 {
			return
		}
		if !r.now().Before(deadline) || !r.e.clock.SleepUntil(ctx, r.now().Add(heartbeatEvery)) {
			if err != nil {
				r.addDirty("History read-back failed (alerts not verified resolved): " + err.Error())
				return
			}
			r.addDirty(fmt.Sprintf("%d alerts still firing in History after resolve: %s", len(firing), historyNames(firing)))
			return
		}
	}
}
