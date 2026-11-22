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
	aid := managed.AID(m.run.id, "main", 0)
	cond := m.condition("POL")
	if err := m.emit(ctx, aid, cond, "MN"); err != nil {
		return err
	}
	initial, err := m.observe(ctx, aid, "minor", "")
	if err != nil {
		return err
	}
	if err = m.emit(ctx, aid, cond, "MJ"); err != nil {
		return err
	}
	changed, err := m.observe(ctx, aid, "major", "")
	if err != nil {
		return err
	}
	a, b := initial.Source, changed.Source
	if a.Episode != b.Episode || a.Generation != b.Generation || a.ActiveSince != b.ActiveSince || a.WireEpisode != b.WireEpisode || a.ManagedKey != b.ManagedKey || a.Variant >= b.Variant || a.EventID == b.EventID || initial.History.Annotations["alarm_key"] != changed.History.Annotations["alarm_key"] {
		return errors.New("severity variant lacks exact verified original lineage")
	}
	elig, err := m.actor.Eligibility(ctx, changed.History.ID)
	if err != nil {
		return err
	}
	if !elig.Actions["resend"].Allowed || elig.Actions["clear"].Scope != "managed" {
		return errors.New("managed resend authority unavailable")
	}
	manual, err := m.command(ctx, "manual_resend", changed.History.ID)
	if err != nil {
		return err
	}
	if manual.ResendID == "" || manual.AlertID != changed.History.ID {
		return errors.New("manual resend admission unverified")
	}
	if err = m.waitActions(ctx, changed, manual.ResendID); err != nil {
		return err
	}
	// Read the original user operation, even after the timed operation has run.
	status, err := m.actor.ResendStatus(ctx, changed.History.ID, manual.ResendID)
	if err != nil {
		return err
	}
	if status.Operation != manual.ResendID || status.TargetCounts["confirmed"] < 1 {
		return errors.New("manual native target confirmation missing")
	}
	if err = m.save(ctx, "native.action.status", status); err != nil {
		return err
	}
	ack, err := m.command(ctx, "acknowledge", changed.History.ID)
	if err != nil {
		return err
	}
	if !ack.Record.Acknowledged || ack.Record.ID != changed.History.ID || ack.Record.Episode != changed.History.EpisodeNumber {
		return errors.New("acknowledgement readback mismatch")
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
	ids := []string{changed.History.ID, fresh.History.ID}
	requests := []string{uuid.NewString(), uuid.NewString()}
	if err = m.save(ctx, "native.bulk.intent", map[string]any{"histories": ids, "requests": requests, "sibling": sib.History.ID}); err != nil {
		return err
	}
	bulk, err := m.actor.Bulk(ctx, ids, requests)
	if se := m.save(context.WithoutCancel(ctx), "native.bulk.result", bulk); se != nil {
		return se
	}
	if err != nil || bulk.StatusCode != 202 || len(bulk.Results) != 2 {
		return errors.New("mixed bulk outcome unverified; retain original request IDs")
	}
	no, yes := bulk.Results[0], bulk.Results[1]
	if no.AlertID != ids[0] || no.RequestID != requests[0] || no.ItemStatus != 409 || no.Error.Code != "NATIVE_EPISODE_RESOLVED" || no.ResendID != "" || yes.AlertID != ids[1] || yes.RequestID != requests[1] || yes.ResendID == "" {
		return errors.New("mixed eligibility result does not match selected episodes")
	}
	wait, cancel := context.WithTimeout(ctx, time.Minute)
	err = poll(wait, func() (bool, error) {
		stat, err := m.actor.ResendStatus(wait, ids[1], yes.ResendID)
		if err != nil {
			return false, err
		}
		if stat.TargetCounts["confirmed"] == 0 {
			return false, nil
		}
		snap, err := m.config.Snapshot(wait, sib.History.ID)
		if err != nil {
			return false, err
		}
		for _, e := range snap.Effects {
			if e.Logical == yes.ResendID || e.Operation == yes.ResendID {
				return false, errors.New("selected operation reached unselected sibling")
			}
		}
		return true, m.save(wait, "native.bulk.isolation", map[string]any{"original": stat, "sibling": snap})
	})
	cancel()
	if err != nil {
		return err
	}
	if err = m.clear(ctx, sibling, siblingCond); err != nil {
		return err
	}
	if err = m.clear(ctx, aid, cond); err != nil {
		return err
	}
	for _, kind := range []string{"AUTO", "MANUAL"} {
		id := managed.AID(m.run.id, kind, 0)
		cond := m.condition(kind)
		m.unsettledTicket = id
		if err = m.emit(ctx, id, cond, "MJ"); err != nil {
			return err
		}
		f, err := m.observe(ctx, id, "major", "")
		if err != nil {
			return err
		}
		if kind == "MANUAL" {
			if f.History.TicketID != "" || f.History.Labels["create_ticket"] == "snow" {
				return errors.New("manual fixture already auto-ticketed")
			}
			result, err := m.command(ctx, "manual_ticket", f.History.ID)
			if err != nil {
				return err
			}
			if result.Outcome != "succeeded" || result.Operation == "" {
				return errors.New("original manual ticket outcome pending or unknown")
			}
		} else if f.History.Labels["create_ticket"] != "snow" {
			return errors.New("owned auto-ticket mapping missing")
		}
		if err = m.ticket(ctx, id); err != nil {
			return err
		}
		m.unsettledTicket = ""
		if err = m.clear(ctx, id, cond); err != nil {
			return err
		}
	}
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
