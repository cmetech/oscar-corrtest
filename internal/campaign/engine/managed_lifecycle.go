package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/google/uuid"
)

// The source may refresh its native validity while no ordinary callback occurs.
// Only a new signed ordinary callback invalidates the quiet timer interval.
func (m *managedLane) exercise(ctx context.Context) error {
	var cross NativeEpisodeFixture
	if m.run.managedOnly() {
		if m.config.CrossRouteReceiver != "" {
			aid := managed.AID(m.run.id, "CROSS", 0)
			if err := m.save(ctx, "native.cross_route.requested", map[string]string{"aid": aid, "criticalReceiver": m.config.CrossRouteReceiver}); err != nil {
				return err
			}
			if err := m.emit(ctx, aid, m.condition("POL"), "MJ"); err != nil {
				return err
			}
			var err error
			cross, err = m.observe(ctx, aid, "major", "")
			if err != nil {
				return err
			}
			if cross.Source.Receiver == m.config.CrossRouteReceiver {
				return errors.New("cross-route fixture requires distinct Major and Critical receivers")
			}
		} else if err := m.save(ctx, "native.cross_route.uncommissioned", map[string]string{"case": "Major-A/Critical-B"}); err != nil {
			return err
		}
	}
	if err := exerciseManagedSubjects(ctx, m); err != nil {
		return err
	}
	if cross.History.ID != "" {
		if err := m.checkCrossRoute(ctx, cross); err != nil {
			return err
		}
	}
	var err error
	if !m.run.managedOnly() && m.run.s.ID == "alerting-day" {
		if err = m.policyChanges(ctx); err != nil {
			return err
		}
		if err = m.suppression(ctx); err != nil {
			return err
		}
	}
	return m.save(ctx, "native.managed.functional", map[string]bool{"completed": true})
}

// The extra source shares the existing smoke window and controlled provider.
func (m *managedLane) checkCrossRoute(ctx context.Context, f NativeEpisodeFixture) error {
	aid := f.History.Labels["aid"]
	active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = poll(wait, func() (bool, error) {
		snap, err := m.config.Snapshot(wait, f.History.ID)
		if err != nil {
			return false, err
		}
		if err = m.holdUnknown(wait, f, snap); err != nil {
			return false, err
		}
		binding, err := ownedBinding(m.policy, snap.Bindings)
		if err != nil {
			return false, err
		}
		effect, err := crossRouteEffect(f, snap, binding, m.config.CrossRouteReceiver, m.run.sinkID)
		if err != nil || effect.Child == "" {
			return false, err
		}
		receipts, err := m.sink.ManagedReceipts(wait, aid, active)
		if err != nil {
			return false, err
		}
		ok, err := matchProvider(snap.Effects, receipts, m.run.sinkID, f.Source.Correlation,
			func(e managed.Effect, r targetops.Receipt, a targetops.SinkAlert) bool {
				return r.Notifier == m.run.e.d.CampaignNotifier && selectedMember(f, a)
			})
		if err != nil || !ok {
			return false, err
		}
		return true, m.save(wait, "native.cross_route.confirmed", map[string]any{
			"source": f, "criticalReceiver": m.config.CrossRouteReceiver, "binding": binding,
			"effect": effect, "sourceWrites": snap.Source, "dispatches": snap.Dispatches, "captured": receipts,
		})
	})
	if err != nil {
		return err
	}
	return m.clear(ctx, aid, m.condition("POL"))
}

func crossRouteEffect(f NativeEpisodeFixture, s managed.Snapshot, binding managed.Binding, receiver, notifier string) (managed.Effect, error) {
	var hit managed.Effect
	root := ""
	for _, e := range s.Effects {
		if e.Kind != "escalate" {
			continue
		}
		if e.Target == "" {
			continue
		} // Reserved, prior to native plan.
		if receiver == "" || f.Source.Receiver == "" || f.Source.Receiver == receiver || e.Receiver != receiver ||
			!routedScope(e, notifier, "notification") || e.BindingID != binding.ID || e.Revision != binding.Revision ||
			e.Logical == "" || e.ScheduleRoot != e.Logical || root != "" && e.Logical != root {
			return hit, errors.New("escalation differs from commissioned Critical-only route or original binding")
		}
		root = e.Logical
		if e.Notifier != notifier || e.Action != "notification" || e.ConfirmedAt == "" {
			continue
		}
		if hit.Child != "" || e.Child == "" || e.ChildState != "sent" || e.TargetState != "confirmed" ||
			e.Callback == "" || e.Operation == "" || e.Variant != f.Source.Variant || e.Severity != "critical" {
			return hit, errors.New("ambiguous cross-route physical notification")
		}
		for _, src := range s.Source {
			if src.WriteID != "" && e.SourceWrite == src.WriteID && src.History == f.History.ID && src.Episode == f.Source.Episode &&
				src.Authority == f.Source.Authority && src.Generation == f.Source.Generation && src.Variant == e.Variant &&
				src.Receiver == f.Source.Receiver && src.SourceSeverity == "major" && src.Floor == "critical" {
				hit = e
			}
		}
	}
	for _, d := range s.Dispatches {
		if d.Root == root && d.Attempt == hit.Operation && d.Kind == "escalate" && (d.State == "confirmed" || d.State == "partial") {
			return hit, nil
		}
	}
	return managed.Effect{}, nil
}

// Each step consumes the verified original subject. Refire and bulk use the
// resolved selection as the refused member before independent ticket episodes.
type managedSubjects interface {
	raiseOriginal(context.Context) (NativeEpisodeFixture, error)
	timedAndResolve(context.Context, NativeEpisodeFixture) error
	refireBulk(context.Context, NativeEpisodeFixture) error
	ticketSubjects(context.Context) error
}

func exerciseManagedSubjects(ctx context.Context, m managedSubjects) error {
	f, err := m.raiseOriginal(ctx)
	if err != nil {
		return err
	}
	if err = m.timedAndResolve(ctx, f); err != nil {
		return err
	}
	if err = m.refireBulk(ctx, f); err != nil {
		return err
	}
	return m.ticketSubjects(ctx)
}
func (m *managedLane) raiseOriginal(ctx context.Context) (NativeEpisodeFixture, error) {
	aid := managed.AID(m.run.id, "main", 0)
	cond := m.condition("POL")
	if err := m.emit(ctx, aid, cond, "MN"); err != nil {
		return NativeEpisodeFixture{}, err
	}
	initial, err := m.observe(ctx, aid, "minor", "")
	if err != nil {
		return NativeEpisodeFixture{}, err
	}
	if err = m.emit(ctx, aid, cond, "MJ"); err != nil {
		return NativeEpisodeFixture{}, err
	}
	changed, err := m.observe(ctx, aid, "major", "")
	if err != nil {
		return NativeEpisodeFixture{}, err
	}
	a, b := initial.Source, changed.Source
	if a.Authority != b.Authority || initial.History.ID != changed.History.ID || initial.History.EpisodeNumber != changed.History.EpisodeNumber || a.Episode != b.Episode || a.Generation != b.Generation || a.ActiveSince != b.ActiveSince || a.WireEpisode != b.WireEpisode || a.ManagedKey != b.ManagedKey || a.Variant >= b.Variant || a.EventID == b.EventID || initial.History.AlarmKey != changed.History.AlarmKey {
		return NativeEpisodeFixture{}, errors.New("severity variant lacks exact verified original lineage")
	}
	return changed, nil
}
func (m *managedLane) timedAndResolve(ctx context.Context, changed NativeEpisodeFixture) error {
	aid, cond := changed.History.Labels["aid"], m.condition("POL")
	elig, err := m.actor.Eligibility(ctx, changed.History.ID)
	if err != nil {
		return err
	}
	if elig.Episode != changed.History.EpisodeNumber || !elig.Actions["resend"].Allowed || elig.Actions["clear"].Scope != "managed" {
		return errors.New("managed resend authority unavailable")
	}
	manual, err := m.command(ctx, "manual_resend", changed)
	if err != nil {
		return err
	}
	if manual.ResendID == "" || manual.AlertID != changed.History.ID {
		return errors.New("manual resend admission unverified")
	}
	manualChild, err := m.waitActions(ctx, changed, manual.ResendID)
	if err != nil {
		return err
	}
	// Read the original user operation, even after the timed operation has run.
	status, err := m.actor.ResendStatus(ctx, changed.History.ID, manual.ResendID)
	if err != nil {
		return err
	}
	original, err := m.config.Original(ctx, changed.History.ID, manual.ResendID)
	if err != nil {
		return err
	}
	proof, ok := original.Resend(status, changed.History.ID, changed.History.EpisodeNumber, manual.IssuedRequest, manual.ResendID)
	if !ok || manualChild == "" || proof.DisabledTicketNotification(m.policy.notifierID) != manualChild {
		return errors.New("manual native target confirmation missing")
	}
	if err = m.save(ctx, "native.action.children", original); err != nil {
		return err
	}
	if err = m.save(ctx, "native.action.status", status); err != nil {
		return err
	}
	if err = m.run.restored(ctx, manual.Owner); err != nil {
		return err
	}
	ack, err := m.command(ctx, "acknowledge", changed)
	if err != nil {
		return err
	}
	if !ack.Record.Acknowledged || ack.Record.ID != changed.History.ID || ack.Record.Episode != changed.History.EpisodeNumber {
		return errors.New("acknowledgement readback mismatch")
	}
	if err = m.run.restored(ctx, ack.Owner); err != nil {
		return err
	}
	if err = m.afterAck(ctx, changed); err != nil {
		return err
	}
	if err = m.clear(ctx, aid, cond); err != nil {
		return err
	}
	post, err := m.actor.Eligibility(ctx, changed.History.ID)
	if err != nil {
		return err
	}
	for _, k := range []string{"resend", "acknowledge", "ticket_create"} {
		dec, present := post.Actions[k]
		if !present || dec.Allowed || dec.Reason != "NATIVE_EPISODE_RESOLVED" {
			return fmt.Errorf("post-resolution %s refusal unproved: %s", k, dec.Reason)
		}
	}
	if err = m.save(ctx, "native.resolved.eligibility", post); err != nil {
		return err
	}
	return nil
}
func (m *managedLane) refireBulk(ctx context.Context, changed NativeEpisodeFixture) error {
	var err error
	a := changed.Source
	aid, cond := changed.History.Labels["aid"], m.condition("POL")
	if err = m.emit(ctx, aid, cond, "MJ"); err != nil {
		return err
	}
	fresh, err := m.observe(ctx, aid, "major", a.Episode)
	if err != nil {
		return err
	}
	if fresh.Source.WireEpisode == a.WireEpisode || fresh.Source.ActiveSince == a.ActiveSince || fresh.Source.ManagedKey == a.ManagedKey || fresh.Source.Floor != "" {
		return errors.New("fresh refire inherited original episode or floor")
	}
	// Submit the resolved original beside its active refire. The related
	// unselected sibling has a different AID on the same test source.
	sibling := managed.AID(m.run.id, "sibling", 0)
	siblingCond := m.condition("SIB")
	if err = m.emit(ctx, sibling, siblingCond, "MJ"); err != nil {
		return err
	}
	sib, err := m.observe(ctx, sibling, "major", "")
	if err != nil {
		return err
	}
	if sib.Source.ManagedKey == fresh.Source.ManagedKey {
		return errors.New("unrelated lookalike merged")
	}
	for _, member := range []NativeEpisodeFixture{fresh, sib} {
		if err = m.bulkBaseline(ctx, member); err != nil {
			return err
		}
	}
	ids := []string{changed.History.ID, fresh.History.ID}
	requests := []string{uuid.NewString(), uuid.NewString()}
	if err = m.save(ctx, "native.bulk.intent", map[string]any{"histories": ids, "requests": requests, "sibling": sib.History.ID}); err != nil {
		return err
	}
	var owners []int64
	episodes := []int{changed.History.EpisodeNumber, fresh.History.EpisodeNumber}
	for i, id := range ids {
		owner, er := m.run.record(ctx, kindManagedOwner, requests[i], originalOwner{Kind: "manual_resend", History: id, Episode: episodes[i], Request: requests[i]})
		if er != nil {
			return er
		}
		owners = append(owners, owner)
	}
	bulkStart := m.run.now()
	bulk, err := m.actor.Bulk(ctx, ids, requests)
	if se := m.save(context.WithoutCancel(ctx), "native.bulk.result", bulk); se != nil {
		return se
	}
	if err != nil || bulk.StatusCode != 202 || len(bulk.Results) != 2 {
		return errors.New("mixed bulk outcome unverified; retain original request IDs")
	}
	no, yes := bulk.Results[0], bulk.Results[1]
	if no.AlertID != ids[0] || no.RequestID != requests[0] || no.ItemStatus != 409 || no.Error.Code != "NATIVE_EPISODE_RESOLVED" || no.ResendID != "" || no.Operation != "" || yes.AlertID != ids[1] || yes.RequestID != requests[1] || yes.ResendID == "" {
		return errors.New("mixed eligibility result does not match selected episodes")
	}
	if err = m.run.restored(ctx, owners[0]); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, time.Minute)
	err = poll(wait, func() (bool, error) {
		stat, err := m.actor.ResendStatus(wait, ids[1], yes.ResendID)
		if err != nil {
			return false, err
		}
		original, err := m.config.Original(wait, ids[1], yes.ResendID)
		if err != nil {
			return false, err
		}
		proof, ok := original.Resend(stat, ids[1], fresh.History.EpisodeNumber, requests[1], yes.ResendID)
		child := proof.DisabledTicketNotification(m.policy.notifierID)
		if !ok || child == "" {
			return false, nil
		}
		snap, err := m.config.Snapshot(wait, fresh.History.ID)
		if err != nil {
			return false, err
		}
		if err = m.holdUnknown(wait, fresh, snap); err != nil {
			return false, err
		}
		selected, err := m.sink.ManagedReceipts(wait, aid, bulkStart)
		if err != nil {
			return false, err
		}
		other, err := m.sink.ManagedReceipts(wait, sibling, bulkStart)
		if err != nil {
			return false, err
		}
		ok, err = bulkIsolation(fresh, sib, yes.ResendID, m.policy.notifierID, child, snap.Effects, selected, other)
		if err != nil || !ok {
			return false, err
		}
		return true, m.save(wait, "native.bulk.isolation", map[string]any{"original": stat, "children": original, "refused": no, "selected": fresh, "sibling": sib, "effects": snap.Effects, "since": bulkStart, "selectedReceipts": selected, "siblingReceipts": other})
	})
	cancel()
	if err != nil {
		return err
	}
	if err = m.run.restored(ctx, owners[1]); err != nil {
		return err
	}
	if err = m.clear(ctx, sibling, siblingCond); err != nil {
		return err
	}
	if err = m.clear(ctx, aid, cond); err != nil {
		return err
	}
	return nil
}
func (m *managedLane) ticketSubjects(ctx context.Context) error {
	var err error
	for _, kind := range []string{"AUTO", "MANUAL"} {
		id := managed.AID(m.run.id, kind, 0)
		cond := m.condition(kind)
		var owner int64
		if kind == "AUTO" {
			owner, err = m.run.record(ctx, kindManagedOwner, id, originalOwner{Kind: "ticket_source", AID: id})
			if err != nil {
				return err
			}
		}
		if err = m.emit(ctx, id, cond, "MJ"); err != nil {
			return err
		}
		f, err := m.observe(ctx, id, "major", "")
		if err != nil {
			return err
		}
		var result managed.CommandReceipt
		if kind == "MANUAL" {
			if f.History.TicketID != "" || f.History.Labels["create_ticket"] == "snow" {
				return errors.New("manual fixture already auto-ticketed")
			}
			result, err = m.command(ctx, "manual_ticket", f)
			if err != nil {
				return err
			}
			if result.Operation == "" || result.Outcome == "succeeded" && result.TicketID == "" || result.Outcome != "succeeded" && result.Outcome != "pending" && result.Outcome != "unknown" {
				return errors.New("original manual ticket admission incomplete")
			}
		} else if f.History.Labels["create_ticket"] != "snow" {
			return errors.New("owned auto-ticket mapping missing")
		}
		if err = m.ticket(ctx, f, result); err != nil {
			return err
		}
		if result.Owner != 0 {
			if err = m.run.restored(ctx, result.Owner); err != nil {
				return err
			}
		}
		if owner != 0 {
			if err = m.run.restored(ctx, owner); err != nil {
				return err
			}
		}
		if err = m.clear(ctx, id, cond); err != nil {
			return err
		}
	}
	return nil
}
