package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const kindManagedOwner = "managed_provider_owner"

type originalOwner struct {
	Kind, History, Request, Operation, Target, Child, AID string
	Episode                                               int
}

func unknownEffect(e managed.Effect) bool {
	return e.ChildState == "unknown_requires_reconciliation" || e.TargetState == "unknown" || e.CallbackOutcome == "unknown"
}
func (m *managedLane) holdUnknown(ctx context.Context, f NativeEpisodeFixture, s managed.Snapshot) error {
	unknown := false
	for _, e := range s.Effects {
		if !unknownEffect(e) {
			continue
		}
		unknown = true
		o := originalOwner{Kind: "effect", History: f.History.ID, Operation: e.Operation, Target: e.Target, Child: e.Child}
		key := o.Operation + "/" + o.Target + "/" + o.Child
		if _, err := m.run.record(context.WithoutCancel(ctx), kindManagedOwner, key, o); err != nil {
			return err
		}
	}
	if unknown {
		return errors.New("original provider outcome requires reconciliation")
	}
	return nil
}
func terminalEffect(o originalOwner, s managed.Snapshot) bool {
	found := false
	for _, e := range s.Effects {
		if e.Operation != o.Operation || e.Target != o.Target || o.Child != "" && e.Child != o.Child {
			continue
		}
		found = true
		if unknownEffect(e) {
			return false
		}
		if e.TargetState == "no_effect" && e.Callback == "" && e.Child == "" {
			continue
		}
		if e.TargetState != "confirmed" || e.Callback == "" {
			return false
		}
		if e.CallbackOutcome == "no_effect" && (e.Child == "" || e.ChildState == "suppressed" || e.ChildState == "failed") {
			continue
		}
		if e.Child == "" || e.ConfirmedAt == "" || e.ChildState != "sent" || e.CallbackOutcome != "confirmed" {
			return false
		}
	}
	return found
}

// Cleanup only reads original evidence. A request without an attributable
// terminal result stays visible in the mutation ledger on every cleanup run.
func (r *run) recoverOriginal(ctx context.Context, mut domain.CampaignMutation) error {
	var o originalOwner
	if err := json.Unmarshal(mut.Prior, &o); err != nil {
		return err
	}
	deps := r.e.d.managed
	if deps == nil {
		return errors.New("original effect reader unavailable")
	}
	if o.Kind == "effect" {
		s, err := deps.config.Snapshot(ctx, o.History)
		if err != nil {
			return err
		}
		if terminalEffect(o, s) {
			return nil
		}
	}
	if o.Kind == "manual_resend" {
		op, err := deps.config.ResendOperation(ctx, o.History, o.Request)
		if err != nil {
			return err
		}
		status, err := deps.actor.ResendStatus(ctx, o.History, op)
		if err != nil {
			return err
		}
		if status.RequestID == o.Request && terminalResend(status) {
			return nil
		}
	}
	if o.Kind == "acknowledge" || o.Kind == "manual_ticket" {
		fact, err := deps.config.ActorFact(ctx, o.History, o.Request, o.Kind)
		if err != nil {
			return err
		}
		if o.Episode != fact.Episode {
			return errors.New("original actor episode differs")
		}
		if o.Kind == "acknowledge" {
			return nil
		}
		return r.closeOriginalTicket(ctx, o.AID, fact.Incident)
	}
	if o.Kind == "ticket_source" {
		m := managedLane{run: r, managedDeps: deps}
		rows, err := m.rows(ctx, o.AID)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].TicketID == "" {
			return errors.New("original automatic ticket identity unproven")
		}
		row := rows[0]
		snap, err := deps.config.Snapshot(ctx, row.ID)
		if err != nil {
			return err
		}
		proof, err := automaticTicket(NativeEpisodeFixture{History: historyEvidence(row)}, snap.Tickets, row.TicketID)
		if err != nil || proof.Flow == "" {
			return errors.New("original automatic workflow unverified")
		}
		return r.closeOriginalTicket(ctx, o.AID, proof.Incident)
	}
	return errors.New("original provider ownership unresolved: " + mut.ObjectID)
}
func (r *run) closeOriginalTicket(ctx context.Context, aid, number string) error {
	inc, err := r.e.d.managed.snow.Incident(ctx, number)
	if err != nil {
		return err
	}
	if aid == "" || inc.SysID == "" || inc.Number != number || !strings.Contains(inc.ShortDescription, aid) {
		return errors.New("original PDI incident unverified")
	}
	if inc.State == "6" || inc.State == "7" {
		return nil
	}
	return r.resolveIncident(ctx, number)
}
func terminalResend(r managed.CommandReceipt) bool {
	if r.Operation == "" || r.Operation != r.ResendID || r.NativeState != "confirmed" || r.TargetCounts["confirmed"] < 1 || r.Counts["confirmed"] < 1 {
		return false
	}
	for _, counts := range []map[string]int{r.TargetCounts, r.Counts} {
		for state, n := range counts {
			if n > 0 && state != "confirmed" && state != "no_effect" {
				return false
			}
		}
	}
	return true
}
