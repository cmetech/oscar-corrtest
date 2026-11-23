package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/google/uuid"
)

// The source may refresh its native validity while no ordinary callback occurs.
// Only a new signed ordinary callback invalidates the quiet timer interval.
func (m *managedLane) exercise(ctx context.Context) error {
	if err := exerciseManagedSubjects(ctx, m); err != nil {
		return err
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
