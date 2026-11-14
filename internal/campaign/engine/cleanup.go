package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// drainAndFinish is step 8 then 9 for a run that reached traffic. On a guard
// trip or Stop the lane is stopped and resolved at once; otherwise the lane
// finishes its scheduled resolves first. ctx must not be cancellable.
func (r *run) drainAndFinish(ctx context.Context, stopped bool) error {
	if stopped && r.tripped() == "" {
		r.mu.Lock()
		r.stopped = true
		r.mu.Unlock()
	}
	if err := r.transition(ctx, domain.CampaignDraining, "drain"); err != nil {
		return err
	}
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go r.heartbeats(hbCtx)
	if !stopped {
		r.waitLane(ctx, r.at("drain", true))
	}
	stopHB()
	r.resolveLane(ctx)
	r.resolveCohorts(ctx)
	return r.finishAfterDrain(ctx, "", "")
}

// finish is recovery's drain and cleanup: no lane restart, no cohorts.
func (r *run) finish(ctx context.Context, why string) error {
	r.resolveLane(ctx)
	r.recoverCohorts(ctx)
	return r.finishAfterDrain(ctx, domain.VerdictInconclusive, why)
}

// waitLane polls the lane until it completes or deadline passes.
func (r *run) waitLane(ctx context.Context, deadline time.Time) {
	for next := r.now(); next.Before(deadline) && r.e.clock.SleepUntil(ctx, next); next = next.Add(heartbeatEvery) {
		st, err := r.e.d.Worker.Status(ctx, r.laneID)
		if err != nil || st.State != "running" {
			return
		}
	}
}

// resolveAllBudget bounds how long the controller repeats resolve-all.
const resolveAllBudget = 15 * time.Minute

// resolveLane stops the lane and has the worker resolve everything it fired
// (idempotent, ledger-driven), repeating until the worker reports nothing
// still open or the 15 min budget is spent; then cleanup is DIRTY (ruling
// C36). A lost worker orphan-stops and resolves itself, but the controller
// cannot confirm that, so it stays DIRTY.
func (r *run) resolveLane(ctx context.Context) {
	deadline := r.now().Add(resolveAllBudget)
	backoff := time.Second
	for {
		left, err := r.resolveLaneOnce(ctx)
		if err == nil && left == 0 {
			return
		}
		if !r.now().Before(deadline) {
			why := fmt.Sprintf("lane resolve-all: %d identities still open after %s", left, resolveAllBudget)
			if err != nil {
				why = "lane stop/resolve-all failed: " + err.Error()
			}
			r.addDirty(why)
			return
		}
		r.e.clock.SleepUntil(ctx, r.now().Add(backoff))
		backoff = min(2*backoff, guardEvery)
	}
}

func (r *run) resolveLaneOnce(ctx context.Context) (remaining int, err error) {
	if _, err := r.e.d.Worker.Stop(ctx, r.laneID); err != nil && !errors.Is(err, workerapi.ErrLaneNotFound) {
		return 0, err
	}
	res, err := r.e.d.Worker.ResolveAll(ctx, r.laneID)
	if errors.Is(err, workerapi.ErrLaneNotFound) { // no ledger rows: the lane never fired
		return 0, nil
	}
	return res.Remaining, err
}

// addDirty records a cleanup failure that is not an open mutation.
func (r *run) addDirty(why string) {
	r.mu.Lock()
	r.dirty = append(r.dirty, why)
	r.mu.Unlock()
}

// haltLane stops the lane without resolving (Close path; recovery resolves).
func (r *run) haltLane(ctx context.Context) {
	r.mu.Lock()
	if r.laneStopAt.IsZero() {
		r.laneStopAt = r.now() // the baseline window ends here (scoring)
	}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = r.e.d.Worker.Stop(ctx, r.laneID)
}

// finishAfterDrain waits for queues, verifies and closes incidents, cleans up,
// scores and completes. forced, when set, overrides the verdict.
func (r *run) finishAfterDrain(ctx context.Context, forced domain.Verdict, why string) error {
	if !r.t0.IsZero() {
		r.waitQueues(ctx)
	}
	incidents := r.closeIncidents(ctx)
	end := r.now()
	if err := r.transition(ctx, domain.CampaignCleaningUp, "cleanup"); err != nil {
		return err
	}
	cleanup := r.cleanup(ctx)
	if r.foDone != nil {
		<-r.foDone // the front office runs to the drain end
	}
	card, report := r.scoreRun(ctx, end, incidents, cleanup == domain.CleanupClean, forced, why)
	return r.store().CompleteCampaign(ctx, r.id, card.Verdict, cleanup, r.comparable && cleanup == domain.CleanupClean, mustJSON(card), report, r.now())
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// waitQueues waits (<= 30 min) for every queue to return to the idle depth.
func (r *run) waitQueues(ctx context.Context) {
	deadline := r.now().Add(maxQueueWait)
	for next := r.now(); next.Before(deadline) && r.e.clock.SleepUntil(ctx, next); next = next.Add(guardEvery) {
		v, err := r.e.d.Metrics.Query(ctx, queueDepthQuery, r.now())
		if err != nil {
			continue
		}
		idle := true
		for _, s := range v {
			idle = idle && r.s.Precheck.QueueIdle(s.Value)
		}
		if idle {
			return
		}
	}
	r.event(ctx, "campaign.drain", "warn", "queues did not return to idle within 30m", nil)
}

const (
	commentWait   = 5 * time.Minute
	resolveRetry  = 3
	restoreTries  = 3
	setBTicketRun = "setB_ticket:" // link kind prefix; the pattern follows
)

// corrTicketStats is the correlation-ticket figure (set B), kept apart from
// the baseline Tickets score.
type corrTicketStats struct {
	Runs, Incidents, Closed, Open int
}

type corrRun struct{ pattern, token string }

// ticketedRuns re-derives the ticketed set B runs from the link rows, so
// recovery finds them too.
func (r *run) ticketedRuns(ctx context.Context) []corrRun {
	links, err := r.store().CampaignRuns(ctx, r.id)
	if err != nil {
		r.noteMissing("campaign run links: " + err.Error())
		return nil
	}
	var out []corrRun
	for _, l := range links {
		if p, ok := strings.CutPrefix(l.Kind, setBTicketRun); ok {
			out = append(out, corrRun{p, domain.RunID(l.RunID).Short()})
		}
	}
	return out
}

func (r *run) noteMissing(why string) {
	r.mu.Lock()
	r.missing = append(r.missing, why)
	r.mu.Unlock()
}

// closeIncidents waits (<= 5 min, every 15 s) for OSCAR's resolution comment
// on this run's incidents (campaign alerts and ticketed set B runs), then
// records, resolves and reads back each one. Cohort resolves are re-posted
// up to 3 times while waiting. Incidents still open at the deadline make
// cleanup DIRTY. It returns the campaign incidents as last read, for scoring.
func (r *run) closeIncidents(ctx context.Context) []targetops.Incident {
	if r.e.d.SNOW == nil {
		return nil
	}
	corr := r.ticketedRuns(ctx)
	campaign := map[string]targetops.Incident{}
	corrSeen := map[string]bool{}
	closed := map[string]bool{}
	deadline := r.now().Add(commentWait)
	backoff := time.Second
	for retry := 0; ; retry++ {
		var pending []string
		var listErr error // a failed listing is never "nothing pending" (ruling C35)
		visit := func(inc targetops.Incident) {
			if closed[inc.Number] {
				return
			}
			if !resolvedComment(inc) {
				pending = append(pending, inc.Number)
				return
			}
			mid, err := r.record(ctx, kindIncident, inc.Number, map[string]string{"state": inc.State})
			if err == nil && r.resolveIncident(ctx, inc.Number) == nil {
				_ = r.restored(ctx, mid)
				closed[inc.Number] = true
				return
			}
			pending = append(pending, inc.Number)
		}
		incs, err := r.e.d.SNOW.OpenCampaignIncidents(ctx, r.id)
		if err != nil {
			listErr = fmt.Errorf("incident listing: %w", err)
		}
		for _, inc := range incs {
			campaign[inc.Number] = inc
			visit(inc)
		}
		for _, c := range corr {
			incs, err := r.e.d.SNOW.OpenCorrTestIncidents(ctx, c.pattern, c.token)
			if err != nil {
				listErr = fmt.Errorf("correlation incident listing: %w", err)
			}
			for _, inc := range incs {
				corrSeen[inc.Number] = true
				visit(inc)
			}
		}
		if listErr == nil {
			backoff = time.Second
		}
		if len(pending) == 0 && listErr == nil || !r.now().Before(deadline) {
			r.mu.Lock()
			r.openIncidents = pending
			if listErr != nil {
				r.missing = append(r.missing, listErr.Error())
				r.dirty = append(r.dirty, listErr.Error()+" (incidents not verified)")
			}
			r.corrTickets = corrTicketStats{Runs: len(corr), Incidents: len(corrSeen)}
			for n := range corrSeen {
				if closed[n] {
					r.corrTickets.Closed++
				} else {
					r.corrTickets.Open++
				}
			}
			r.mu.Unlock()
			break
		}
		if retry < resolveRetry && len(pending) > 0 {
			r.reresolveCohorts(ctx)
		}
		wait := guardEvery
		if listErr != nil { // retry a failed listing with backoff, up to the 15 s cadence
			wait, backoff = backoff, min(2*backoff, guardEvery)
		}
		r.e.clock.SleepUntil(ctx, r.now().Add(wait))
	}
	out := make([]targetops.Incident, 0, len(campaign))
	for _, inc := range campaign {
		out = append(out, inc)
	}
	return out
}

func (r *run) resolveIncident(ctx context.Context, number string) error {
	got, err := r.e.d.SNOW.Resolve(ctx, number, "Closed by CorrTest campaign "+r.id+" after OSCAR resolution")
	if err == nil && got.State != "6" && got.State != "7" {
		err = fmt.Errorf("incident %s read back state %s", number, got.State)
	}
	return err
}

// cleanup walks the open mutations in reverse and restores each one.
func (r *run) cleanup(ctx context.Context) domain.CleanupStatus {
	r.restoreKinds(ctx) // all kinds
	open, err := r.store().OpenMutations(ctx, r.id)
	r.mu.Lock()
	incidents := append([]string{}, r.openIncidents...)
	dirty := append([]string{}, r.dirty...)
	var cohorts []string
	for n := range r.cohortOpen {
		cohorts = append(cohorts, n)
	}
	r.mu.Unlock()
	for _, m := range r.members() {
		if m.recorded && !m.resolved {
			cohorts = append(cohorts, m.alert.Name)
		}
	}
	if len(cohorts) > 0 {
		sort.Strings(cohorts)
		dirty = append(dirty, "cohort alerts not resolved: "+strings.Join(cohorts, ","))
	}
	if len(incidents) > 0 {
		r.event(ctx, "campaign.cleanup", "error", "incidents still open: "+strings.Join(incidents, ","), incidents)
	}
	for _, d := range dirty {
		r.event(ctx, "campaign.cleanup", "error", d, nil)
	}
	if err != nil || len(open) > 0 || len(incidents) > 0 || len(dirty) > 0 {
		return domain.CleanupDirty
	}
	return domain.CleanupClean
}

// restoreKinds restores open mutations of the given kinds (all when none),
// newest first.
func (r *run) restoreKinds(ctx context.Context, kinds ...string) {
	open, err := r.store().OpenMutations(ctx, r.id)
	if err != nil {
		r.event(ctx, "campaign.cleanup", "error", "open mutations: "+err.Error(), nil)
		return
	}
	for i := len(open) - 1; i >= 0; i-- {
		m := open[i]
		if len(kinds) > 0 && !containsFold(kinds, m.Kind) {
			continue
		}
		if err := r.restoreWithRetry(ctx, m); err != nil {
			r.event(ctx, "campaign.cleanup", "warn", fmt.Sprintf("restore %s %s: %v", m.Kind, m.ObjectID, err), nil)
			continue
		}
		_ = r.restored(ctx, m.ID)
	}
}

// restoreWithRetry tries a restore 3 times with 1 s, 2 s backoff.
func (r *run) restoreWithRetry(ctx context.Context, m domain.CampaignMutation) error {
	var err error
	for i := 0; i < restoreTries; i++ {
		if i > 0 {
			r.e.clock.SleepUntil(ctx, r.now().Add(time.Duration(i)*time.Second))
		}
		if err = r.restore(ctx, m); err == nil {
			return nil
		}
	}
	return err
}

// notifierID finds a notifier's id by exact (case-insensitive) name.
func (r *run) notifierID(ctx context.Context, name string) (string, error) {
	all, err := r.e.d.Target.Notifiers(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range all {
		if strings.EqualFold(n.Name, name) {
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("notifier %s not found", name)
}

func (r *run) restore(ctx context.Context, m domain.CampaignMutation) (err error) {
	switch m.Kind {
	case kindIncident:
		return r.resolveIncident(ctx, m.ObjectID)
	case kindPolicyCreate, kindPolicyActivate:
		var p policyPrior
		if err := json.Unmarshal(m.Prior, &p); err != nil {
			return err
		}
		if p.ID == "" {
			return r.removeByName(ctx, p)
		}
		if m.Kind == kindPolicyActivate {
			return r.deactivatePolicy(ctx, p)
		}
		return r.withRevision(ctx, p.NotifierID, p.Action, func(rev int) (int, error) {
			return r.e.d.Target.Delete(ctx, p.ID, p.NotifierID, p.Action, rev)
		})
	case kindSink, kindNotifier, kindNotifierEnable:
		var p notifierPrior
		if err := json.Unmarshal(m.Prior, &p); err != nil {
			return err
		}
		id := m.ObjectID
		if m.Kind == kindNotifierEnable { // keyed by name: resolve the id now
			p.Name = m.ObjectID
			if id, err = r.notifierID(ctx, m.ObjectID); err != nil {
				return err
			}
		}
		on := p.Status == "enabled"
		got, err := r.e.d.Target.SetNotifier(ctx, id, on)
		if err != nil {
			return err
		}
		if got.Status != statusWord(on) {
			return fmt.Errorf("read back %q", got.Status)
		}
		if on {
			// Enabling a notifier re-enables tasks matching notifier:<name>% server side.
			r.event(ctx, "campaign.restore", "info", "notifier "+p.Name+" re-enabled", map[string]string{
				"notifier": p.Name, "side_effect": "server re-enabled tasks matching notifier:" + p.Name + "%"})
		}
		return nil
	}
	return fmt.Errorf("unknown mutation kind %q", m.Kind)
}

// deactivatePolicy deactivates a policy if it is enabled, with the latest revision.
func (r *run) deactivatePolicy(ctx context.Context, p policyPrior) error {
	st, err := r.e.d.Target.PolicyStatus(ctx, p.NotifierID, p.Action, p.ID)
	if err != nil || !st.Enabled {
		return err
	}
	return r.withRevision(ctx, p.NotifierID, p.Action, func(rev int) (int, error) {
		return r.e.d.Target.Deactivate(ctx, p.ID, p.NotifierID, p.Action, rev)
	})
}

// resolvedComment reports OSCAR's resolution comment on an incident.
func resolvedComment(i targetops.Incident) bool {
	for _, c := range i.Comments {
		if strings.Contains(c.Value, "[OSCAR AUTO-UPDATE") && strings.Contains(c.Value, "Alert has been resolved") {
			return true
		}
	}
	return false
}

// removeByName resolves a create intent that never got an id: an exact-name
// match is deactivated and deleted; no match means the create never landed.
func (r *run) removeByName(ctx context.Context, p policyPrior) error {
	items, err := r.e.d.Target.ListPolicies(ctx, p.NotifierID, p.Action, p.Name)
	if err != nil {
		return err
	}
	var match []targetops.PolicyItem
	for _, it := range items {
		if it.Name == p.Name {
			match = append(match, it)
		}
	}
	switch len(match) {
	case 0:
		return nil
	case 1:
		p.ID = match[0].ID
		if err := r.deactivatePolicy(ctx, p); err != nil {
			return err
		}
		return r.withRevision(ctx, p.NotifierID, p.Action, func(rev int) (int, error) {
			return r.e.d.Target.Delete(ctx, p.ID, p.NotifierID, p.Action, rev)
		})
	}
	return fmt.Errorf("%d policies named %s; resolve by hand", len(match), p.Name)
}
