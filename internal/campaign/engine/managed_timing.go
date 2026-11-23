package engine

import (
	"context"
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

func (m *managedLane) waitActions(ctx context.Context, f NativeEpisodeFixture, manualID string) (string, error) {
	active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
	if err != nil {
		return "", errors.New("invalid native active age")
	}
	model := slotClock{receiver: f.Source.Receiver, active: active, source: f.Source.SourceSeverity}
	// Policy clock is measured on the same target as native active age. Use zero
	// early allowance. Never subtract a configured maximum as if it were measured.
	until, stop := context.WithDeadline(ctx, active.Add(m.policy.spec.Escalation.After+3*m.policy.spec.Interval+time.Minute))
	defer stop()
	seen := map[string]bool{}
	var timing dispatchSamples
	seenSource := map[string]bool{}

	var ordinary, higher, escalated bool
	var manualChild string
	err = poll(until, func() (bool, error) {
		snap, err := m.config.Snapshot(until, f.History.ID)
		if err != nil {
			return false, err
		}
		var manual *managed.Effect
		timing.add(snap)
		for i := range snap.Effects {
			e := &snap.Effects[i]
			if e.Notifier == m.policy.notifierID && e.Action == "notification" && e.Logical == manualID && e.ConfirmedAt != "" && e.TargetState == "confirmed" {
				manual = e
			}
		}
		if err = m.holdUnknown(until, f, snap); err != nil {
			return false, err
		}
		if manual == nil {
			return false, nil
		}
		binding, err := ownedBinding(m.policy, snap.Bindings)
		if err != nil {
			return false, err
		}
		if model.last.IsZero() {
			model.last, err = time.Parse(time.RFC3339Nano, manual.ConfirmedAt)
			if err != nil {
				return false, err
			}
		}
		for _, e := range snap.Effects {
			if e.ScheduleRoot == "" && e.Logical != manualID && e.Callback != "" && !seenSource[e.Callback] {
				when, err := time.Parse(time.RFC3339Nano, e.ReceivedAt)
				if err != nil {
					return false, err
				}
				if when.After(model.last) && !escalated {
					return false, errors.New("independent timer window received an additional native callback")
				}
				seenSource[e.Callback] = true
			}
			if e.ScheduleRoot == "" || seen[e.Child] || e.Notifier != m.policy.notifierID || e.Action != "notification" || e.ConfirmedAt == "" {
				continue
			}
			due, kind, severity := model.next(m.policy)
			window := timing.Windows[e.Logical]
			if err = model.accept(m.policy, binding, e, window); err != nil {
				return false, err
			}
			if kind == "escalate" {
				escalated = true
			} else if escalated {
				higher = true
			} else {
				ordinary = true
			}
			seen[e.Child] = true
			if err = m.save(until, "native.timer_due", map[string]any{"history": f.History.ID, "expectedKind": kind, "expectedDue": due, "expectedSeverity": severity, "binding": binding, "effect": e, "dispatch": window, "confirmed": model.last, "queryDuration": snap.QueryDuration}); err != nil {
				return false, err
			}
		}

		if !ordinary || !escalated || !higher {
			return false, nil
		}
		receipts, err := m.sink.ManagedReceipts(until, f.History.Labels["aid"], active)
		if err != nil {
			return false, err
		}

		var selected []targetops.Receipt
		for _, receipt := range receipts {
			for _, alert := range receipt.Alerts {
				if alert.Labels["source_id"] != m.config.SourceID || alert.Labels["node"] != m.config.TID || alert.Labels["correlation_key"] != f.History.Labels["correlation_key"] || alert.Status != "firing" {
					continue
				}

				if receipt.ID <= 0 {
					return false, errors.New("provider receipt ID unavailable")
				}

				selected = append(selected, receipt)
				break
			}
		}
		ok, err := matchOriginalSink(snap.Effects, selected, m.policy.notifierID, f.History.Labels["correlation_key"], manual.Child, f)
		if err != nil || !ok {
			return false, err
		}
		// The durable floor, History source count and native validity must all
		// survive the quiet period. No amount of provider traffic proves these.
		current, err := m.rows(until, f.History.Labels["aid"])
		if err != nil {
			return false, err
		}
		proved := false
		for _, row := range current {
			if row.ID == f.History.ID && row.SourceSeverity == "major" && row.EffectiveSeverity == "critical" && row.OccurrenceCount == f.History.OccurrenceCount {
				proved = true
			}
		}
		sourceVariant := false
		for _, src := range snap.Source {
			if src.Authority == f.Source.Authority && src.Episode == f.Source.Episode && src.Generation == f.Source.Generation && src.Variant == f.Source.Variant && src.Fingerprint == f.Source.Fingerprint && src.Starts == f.Source.Starts && src.SourceSeverity == "major" && src.Floor == "critical" {
				sourceVariant = true
			}
		}
		if !proved || !sourceVariant {
			return false, nil
		}
		reader := m.run.e.d.nativeSender.(nativeReader)
		live, err := reader.Members(until, map[string]string{"aid": f.History.Labels["aid"], "source_id": m.config.SourceID, "node": m.config.TID})
		if err != nil {
			return false, err
		}
		originalDeadline, err := time.Parse(time.RFC3339Nano, f.Source.RefreshDeadline)
		if err != nil {
			return false, err
		}
		refreshed := false
		var projected any
		for _, v := range live {
			if observedMember(v, f.Source, "critical", m.run.now()) && v.EndsAt.After(originalDeadline) {
				refreshed = true
				projected = memberEvidence(v)
			}
		}
		if !refreshed {
			return false, errors.New("original source validity unavailable at higher-floor resend")
		}
		if err = m.save(until, "native.managed.refresh", map[string]any{"history": f.History.ID, "member": projected}); err != nil {
			return false, err
		}
		if err = m.save(until, "native.managed.effects", map[string]any{"history": f.History.ID, "source": snap.Source, "effects": snap.Effects, "sink": selected, "queryDuration": snap.QueryDuration}); err != nil {
			return false, err
		}
		manualChild = manual.Child
		return true, nil
	})
	return manualChild, err
}

// The acknowledgement is a local action. The next physical notification is
// still due from the preceding provider confirmation on its pinned binding.
func (m *managedLane) afterAck(ctx context.Context, f NativeEpisodeFixture) error {
	before, err := m.config.Snapshot(ctx, f.History.ID)
	if err != nil {
		return err
	}
	binding, err := ownedBinding(m.policy, before.Bindings)
	if err != nil {
		return err
	}
	active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
	if err != nil {
		return err
	}
	clock := slotClock{receiver: f.Source.Receiver, active: active, source: f.Source.SourceSeverity, raised: true}
	for _, e := range before.Effects {
		if e.Notifier == m.policy.notifierID && e.Action == "notification" && e.ConfirmedAt != "" {
			at, er := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
			if er != nil {
				return er
			}
			if at.After(clock.last) {
				clock.last = at
			}
		}
	}
	if clock.last.IsZero() {
		return errors.New("missing pre-ack contact anchor")
	}
	due, _, _ := clock.next(m.policy)
	if !before.MeasuredAt.Before(due) {
		return errors.New("acknowledgement overlaps timed slot")
	}
	prior := map[string]bool{}
	for _, root := range before.DispatchRoots {
		prior[root] = true
	}
	timing := dispatchSamples{}
	timing.add(before)
	wait, cancel := context.WithDeadline(ctx, due.Add(45*time.Second))
	defer cancel()
	return poll(wait, func() (bool, error) {
		snap, er := m.config.Snapshot(wait, f.History.ID)
		if er != nil {
			return false, er
		}
		timing.add(snap)
		if er = m.holdUnknown(wait, f, snap); er != nil {
			return false, er
		}
		sameAge := false
		for _, v := range snap.Source {
			if v.Episode == f.Source.Episode && v.Authority == f.Source.Authority && v.Generation == f.Source.Generation && v.ActiveSince == f.Source.ActiveSince && v.Variant == f.Source.Variant && v.SourceSeverity == "major" && v.Floor == "critical" {
				sameAge = true
			}
		}
		if !sameAge {
			return false, errors.New("acknowledgement changed original active age or variant")
		}
		b, er := ownedBinding(m.policy, snap.Bindings)
		if er != nil || b != binding {
			return false, errors.New("acknowledgement changed pinned binding")
		}
		for _, e := range snap.Effects {
			if e.Notifier != m.policy.notifierID || e.Action != "notification" || e.ScheduleRoot == "" || prior[e.ScheduleRoot] || e.ConfirmedAt == "" {
				continue
			}
			sample := clock
			if er = sample.accept(m.policy, b, e, timing.Windows[e.ScheduleRoot]); er != nil {
				return false, er
			}
			rows, er := m.rows(wait, f.History.Labels["aid"])
			if er != nil {
				return false, er
			}
			acked := false
			for _, r := range rows {
				if r.ID == f.History.ID && r.EpisodeNumber == f.History.EpisodeNumber && r.Acknowledged && r.SourceSeverity == f.Source.SourceSeverity && r.EffectiveSeverity == "critical" {
					acked = true
				}
			}
			if !acked {
				return false, nil
			}
			elig, er := m.actor.Eligibility(wait, f.History.ID)
			if er != nil {
				return false, er
			}
			if elig.Episode != f.History.EpisodeNumber || !elig.Actions["resend"].Allowed {
				return false, errors.New("acknowledged original lost resend eligibility")
			}
			rs, er := m.sink.ManagedReceipts(wait, f.History.Labels["aid"], before.MeasuredAt)
			if er != nil {
				return false, er
			}
			ok, er := matchSink([]managed.Effect{e}, rs, m.policy.notifierID, f.Source.Correlation)
			if er != nil || !ok {
				return false, er
			}
			return true, m.save(wait, "native.acknowledged.repeat", map[string]any{"subject": f, "acknowledged": acked, "eligibility": elig, "due": due, "dispatch": timing.Windows[e.ScheduleRoot], "effect": e, "sink": rs})
		}
		return false, nil
	})
}
