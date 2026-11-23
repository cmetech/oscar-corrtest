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
	Kind, History, Request, Root, Operation, Target, Child, AID string
	Episode                                                     int
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
		o := originalOwner{Kind: "effect", History: f.History.ID, Episode: f.History.EpisodeNumber, Root: e.Logical, Operation: e.Operation, Target: e.Target, Child: e.Child}
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
func terminalEffect(o originalOwner, r managed.OriginalReceipt) bool {
	root := o.Root
	episode := o.Episode
	// Older mutation entries have only the immutable History/operation pair.
	// Recover their recorded root and episode through that exact operation.
	for _, c := range r.Children {
		if c.Operation == o.Operation && c.History == o.History && c.Target == o.Target {
			if root == "" {
				root = c.Root
			}
			if episode == 0 {
				episode = c.Episode
			}
		}
	}
	if _, ok := r.Settlement(o.History, episode, root); !ok {
		return false
	}
	for _, c := range r.Children {
		if c.Operation == o.Operation && c.Target == o.Target && (o.Child == "" || c.Child == o.Child) {
			return true
		}
	}
	return false
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
	if o.Kind == "effect" || o.Kind == "segment_root" {
		if deps.originals == nil {
			return errors.New("original child reader unavailable")
		}
		s, err := deps.originals.Original(ctx, o.History, o.Operation)
		if err != nil {
			return err
		}
		_, whole := s.Settlement(o.History, o.Episode, o.Root)
		if o.Kind == "segment_root" && whole || o.Kind == "effect" && terminalEffect(o, s) {
			return (&managedLane{run: r}).save(ctx, "native.original.settled", map[string]any{"owner": o, "receipt": s})
		}
	}
	if o.Kind == "manual_resend" {
		if deps.actor == nil {
			return errors.New("original signed resend status reader unavailable")
		}
		if deps.originals == nil {
			return errors.New("original child reader unavailable")
		}
		op, err := deps.config.ResendOperation(ctx, o.History, o.Request)
		if err != nil {
			return err
		}
		status, err := deps.actor.ResendStatus(ctx, o.History, op)
		if err != nil {
			return err
		}
		receipt, err := deps.originals.Original(ctx, o.History, op)
		if err != nil {
			return err
		}
		episode := o.Episode
		if episode == 0 {
			for _, c := range receipt.Children {
				if c.Operation == op && c.History == o.History {
					episode = c.Episode
					break
				}
			}
		}
		if _, ok := receipt.Resend(status, o.History, episode, o.Request, op); ok {
			return (&managedLane{run: r}).save(ctx, "native.original.settled", map[string]any{"owner": o, "status": status, "receipt": receipt})
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
