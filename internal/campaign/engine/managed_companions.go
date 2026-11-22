package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

func (m *managedLane) policyChanges(ctx context.Context) error {
	// A second owned rule can be edited/stopped without touching the sustained
	// workload rule. Keep original mutation intents for idempotent recovery.
	author := &managedLane{run: m.run, managedDeps: m.managedDeps}
	spec := m.policy.spec
	spec.Key = "EDIT"
	aid := managed.AID(m.run.id, "edit", 0)
	cond := m.condition("EDIT")
	if err := m.emit(ctx, aid, cond, "MJ"); err != nil {
		return err
	}
	f, err := m.observe(ctx, aid, "major", "")
	if err != nil {
		return err
	}
	if err = author.activate(ctx, spec); err != nil {
		return err
	}
	r := m.run
	p := author.policy
	binding := func() (managed.Binding, error) {
		wait, stop := context.WithTimeout(ctx, 90*time.Second)
		defer stop()
		var hit managed.Binding
		err := poll(wait, func() (bool, error) {
			s, e := m.config.Snapshot(wait, f.History.ID)
			if e != nil {
				return false, e
			}
			for _, b := range s.Bindings {
				if b.Rule == p.id && b.Notifier == p.notifierID && b.Action == p.spec.Action && b.Revision != "" {
					hit = b
					return true, nil
				}
			}
			return false, nil
		})
		return hit, err
	}
	before, err := binding()
	if err != nil {
		return err
	}
	edit := author.definition
	later := int(p.spec.Interval.Seconds()) + 30
	edit.IntervalSeconds = &later
	edit.Enabled = true
	err = r.withRevision(ctx, p.notifierID, p.spec.Action, func(rev int) (int, error) {
		got, e := targetops.Edit(ctx, m.oscar, p.id, edit, rev)
		return got.Revision, e
	})
	if err != nil {
		return err
	}
	pinned, err := binding()
	if err != nil || pinned.ID != before.ID || pinned.Revision != before.Revision {
		return errors.New("existing episode lost original pinned revision after edit")
	}
	err = r.withRevision(ctx, p.notifierID, p.spec.Action, func(rev int) (int, error) {
		return r.e.d.Target.Deactivate(ctx, p.id, p.notifierID, p.spec.Action, rev)
	})
	if err != nil {
		return err
	}
	drafts, err := r.e.d.Target.ListPolicies(ctx, p.notifierID, p.spec.Action, p.name)
	if err != nil {
		return err
	}
	foundDraft := false
	for _, row := range drafts {
		if row.ID == p.id && row.Status == "draft" {
			foundDraft = true
		}
	}
	if !foundDraft {
		return errors.New("disabled rule was not read back as draft")
	}
	paused, err := r.e.d.Target.PolicyStatus(ctx, p.notifierID, p.spec.Action, p.id)
	if err != nil {
		return err
	}
	err = r.withRevision(ctx, p.notifierID, p.spec.Action, func(rev int) (int, error) {
		got, e := r.e.d.Target.Activate(ctx, p.id, p.notifierID, p.spec.Action, rev, activationWait)
		return got.Revision, e
	})
	if err != nil {
		return err
	}
	resumed, err := binding()
	if err != nil || resumed.ID != before.ID || resumed.Revision != before.Revision {
		return errors.New("original binding changed on resume")
	}
	err = r.withRevision(ctx, p.notifierID, p.spec.Action, func(rev int) (int, error) {
		return r.e.d.Target.Deactivate(ctx, p.id, p.notifierID, p.spec.Action, rev)
	})
	if err != nil {
		return err
	}
	err = r.withRevision(ctx, p.notifierID, p.spec.Action, func(rev int) (int, error) { return r.e.d.Target.Delete(ctx, p.id, p.notifierID, p.spec.Action, rev) })
	if err != nil {
		return err
	}
	listed, err := r.e.d.Target.ListPolicies(ctx, p.notifierID, p.spec.Action, p.name)
	if err != nil {
		return err
	}
	for _, row := range listed {
		if row.ID == p.id {
			return errors.New("owned rule still present after delete")
		}
	}
	open, err := r.store().OpenMutations(ctx, r.id)
	if err != nil {
		return err
	}
	for _, item := range open {
		if item.ObjectID == p.id && (item.Kind == kindPolicyActivate || item.Kind == kindPolicyCreate) {
			if err = r.restored(ctx, item.ID); err != nil {
				return err
			}
		}
	}
	if err = m.save(ctx, "native.policy.changes", map[string]any{"history": f.History.ID, "before": before, "afterEdit": pinned, "paused": paused, "resumed": resumed, "deleted": p.id}); err != nil {
		return err
	}
	return m.clear(ctx, aid, cond)
}
func (m *managedLane) suppression(ctx context.Context) error {
	reader, ok := m.run.e.d.nativeSender.(nativeReader)
	if !ok {
		return errors.New("suppression reader unavailable")
	}
	for _, kind := range []string{"silence", "inhibit"} {
		aid := managed.AID(m.run.id, kind, 0)
		cond := m.condition(kind)
		// The operator owns an isolated matcher that suppresses only Major for
		// this fixture. The Minor source first establishes the native reference.
		if err := m.emit(ctx, aid, cond, "MN"); err != nil {
			return err
		}
		f, err := m.observe(ctx, aid, "minor", "")
		if err != nil {
			return err
		}
		if err = m.emit(ctx, aid, cond, "MJ"); err != nil {
			return err
		}
		wait, cancel := context.WithTimeout(ctx, time.Minute)
		err = poll(wait, func() (bool, error) {
			members, err := reader.Members(wait, map[string]string{"aid": aid, "node": m.config.TID, "source_id": m.config.SourceID, "severity": "major"})
			if err != nil {
				return false, err
			}
			if len(members) != 1 || kind == "silence" && len(members[0].Status.SilencedBy) == 0 || kind == "inhibit" && len(members[0].Status.InhibitedBy) == 0 {
				return false, nil
			}
			selection, err := m.actor.Eligibility(wait, f.History.ID)
			if err != nil {
				return false, err
			}
			resend := selection.Actions["resend"]
			if resend.Allowed || resend.Reason != "NATIVE_SUPPRESSED" || !selection.Actions["acknowledge"].Allowed || !selection.Actions["ticket_create"].Allowed {
				return false, nil
			}
			return true, m.save(wait, "native.suppression_changed", map[string]any{"kind": kind, "source": members[0], "eligibility": selection})
		})
		cancel()
		if err != nil {
			return err
		}
		if err = m.clear(ctx, aid, cond); err != nil {
			return err
		}
	}
	return nil
}

// Exercise a genuine emitted parent under the same serial correlation lane.
// Normal set-B tickets stay on their existing source alerts.
func (r *run) syntheticTicket(ctx context.Context) error {
	doc, err := caseDoc(scenario.CaseRef{Pattern: "flood", Level: "builtin"})
	if err != nil {
		return err
	}
	for i := range doc.Cases {
		if doc.Cases[i].Polarity == "positive" {
			doc.Cases[i].ParentTicket = true
			doc.Cases[i].ObservationWindow = setBTicketObservation
			doc.MaxDuration += setBTicketObservation
		}
	}
	result, err := r.e.d.Correlate(ctx, r.e.d.TargetID, doc, correlationRun)
	if result.ID == "" {
		return errors.New("synthetic companion run unavailable")
	}
	if er := r.store().LinkCampaignRun(context.WithoutCancel(ctx), r.id, result.ID, setBTicketRun+"flood"); er != nil {
		return er
	}
	if err != nil {
		return err
	}
	m := r.e.d.managed
	if m == nil {
		return errors.New("managed observation unavailable")
	}
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return poll(wait, func() (bool, error) {
		rows, err := m.oscar.HistoryByLabel(wait, "oscar_test_run_id", result.ID, r.t0, r.now().Add(time.Second))
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			if row.Labels["oscar_test_run_id"] != result.ID || row.Labels["oscar_synthetic_source"] != "correlator" || row.Labels["oscar_test_alert_role"] != "synthetic_parent" || row.Labels["create_ticket"] != "snow" || row.TicketID == "" {
				continue
			}
			inc, err := m.snow.Incident(wait, row.TicketID)
			if err != nil {
				return false, err
			}
			if inc.Number != row.TicketID || !strings.Contains(inc.ShortDescription, strings.ToUpper(result.ShortToken)) {
				return false, errors.New("synthetic provider ticket ownership unproved")
			}
			elig, err := m.actor.Eligibility(wait, row.ID)
			if err != nil {
				return false, err
			}
			for _, k := range []string{"resend", "ticket_create", "acknowledge"} {
				d, ok := elig.Actions[k]
				if !ok || d.Allowed || d.Reason != "SYNTHETIC_ACTION_DISABLED" {
					return false, errors.New("synthetic manual action refusal unproved")
				}
			}
			snap, err := m.config.Snapshot(wait, row.ID)
			if err != nil {
				return false, err
			}
			for _, binding := range snap.Bindings {
				if binding.Rule != "" || binding.Revision != "" {
					return false, errors.New("synthetic parent acquired a policy revision")
				}
			}
			return true, r.managed.save(wait, "native.synthetic_ticket", map[string]any{"history": row.ID, "run": result.ID, "ticket": inc.Number, "providerID": inc.SysID, "eligibility": elig, "bindings": snap.Bindings})
		}
		return false, nil
	})
}
