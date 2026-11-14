package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// drainAndFinish is step 8 then 9 for a run that reached traffic. On a guard
// trip or Stop the lane is stopped and resolved at once; otherwise the lane
// finishes its scheduled resolves first. ctx must not be cancellable.
func (r *run) drainAndFinish(ctx context.Context) error {
	trip := r.tripped()
	if err := r.transition(ctx, domain.CampaignDraining, "drain"); err != nil {
		return err
	}
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go r.heartbeats(hbCtx)
	c, _ := r.store().GetCampaign(ctx, r.id)
	stopped := trip != "" || c.Status == domain.CampaignStopping
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

// resolveLane stops the lane and has the worker resolve everything it fired
// (idempotent, ledger-driven). A lost worker orphan-stops and resolves itself.
func (r *run) resolveLane(ctx context.Context) {
	_, err := r.e.d.Worker.Stop(ctx, r.laneID)
	if err == nil || errors.Is(err, workerapi.ErrLaneNotFound) {
		_, err = r.e.d.Worker.ResolveAll(ctx, r.laneID)
	}
	if err != nil && !errors.Is(err, workerapi.ErrLaneNotFound) {
		r.event(ctx, "campaign.lane", "warn", "lane stop/resolve-all failed: "+err.Error(), nil)
	}
}

// haltLane stops the lane without resolving (Close path; recovery resolves).
func (r *run) haltLane(ctx context.Context) {
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
	card, report := r.scoreRun(ctx, end, incidents, cleanup == domain.CleanupClean, forced, why)
	return r.store().CompleteCampaign(ctx, r.id, card.Verdict, cleanup, r.comparable && cleanup == domain.CleanupClean, mustJSON(card), report, r.now())
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// waitQueues waits (<= 30 min) for queue depth to return to the idle limit.
func (r *run) waitQueues(ctx context.Context) {
	limit := float64(r.s.Precheck.IdleQueueDepth)
	deadline := r.now().Add(maxQueueWait)
	for next := r.now(); next.Before(deadline) && r.e.clock.SleepUntil(ctx, next); next = next.Add(guardEvery) {
		v, err := r.e.d.Metrics.Query(ctx, queueDepthQuery, r.now())
		if err != nil {
			continue
		}
		total := 0.0
		for _, s := range v {
			total += s.Value
		}
		if total <= limit {
			return
		}
	}
	r.event(ctx, "campaign.drain", "warn", "queues did not return to idle within 30m", nil)
}

// closeIncidents reads this run's open incidents and closes the ones with
// OSCAR's resolution comment (recorded, read back by number). It returns the
// incidents as read before closing, for scoring.
func (r *run) closeIncidents(ctx context.Context) []targetops.Incident {
	if r.e.d.SNOW == nil {
		return nil
	}
	incs, err := r.e.d.SNOW.OpenCampaignIncidents(ctx, r.id)
	if err != nil {
		r.event(ctx, "campaign.tickets", "warn", "incident listing failed: "+err.Error(), nil)
		return nil
	}
	for _, inc := range incs {
		if !resolvedComment(inc) {
			continue
		}
		mid, err := r.record(ctx, kindIncident, inc.Number, map[string]string{"state": inc.State})
		if err != nil {
			continue
		}
		if r.resolveIncident(ctx, inc.Number) == nil {
			_ = r.restored(ctx, mid)
		}
	}
	return incs
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
	if err != nil || len(open) > 0 {
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
		if err := r.restore(ctx, m); err != nil {
			r.event(ctx, "campaign.cleanup", "warn", fmt.Sprintf("restore %s %s: %v", m.Kind, m.ObjectID, err), nil)
			continue
		}
		_ = r.restored(ctx, m.ID)
	}
}

func (r *run) restore(ctx context.Context, m domain.CampaignMutation) error {
	switch m.Kind {
	case kindIncident:
		return r.resolveIncident(ctx, m.ObjectID)
	case kindPolicyCreate, kindPolicyActivate:
		var p policyPrior
		if err := json.Unmarshal(m.Prior, &p); err != nil {
			return err
		}
		if p.ID == "" {
			return errors.New("draft create outcome unknown; check the target for " + p.Name)
		}
		if m.Kind == kindPolicyActivate {
			return r.deactivatePolicy(ctx, p)
		}
		return r.withRevision(ctx, p.NotifierID, p.Action, func(rev int) (int, error) {
			return r.e.d.Target.Delete(ctx, p.ID, p.NotifierID, p.Action, rev)
		})
	case kindSink, kindNotifier:
		var p notifierPrior
		if err := json.Unmarshal(m.Prior, &p); err != nil {
			return err
		}
		on := p.Status == "enabled"
		got, err := r.e.d.Target.SetNotifier(ctx, m.ObjectID, on)
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
