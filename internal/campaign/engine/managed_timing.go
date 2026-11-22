package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

func (m *managedLane) waitActions(ctx context.Context, f NativeEpisodeFixture, manualID string) error {
	active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
	if err != nil {
		return errors.New("invalid native active age")
	}
	ref := resend.SourceRef{Scope: f.Source.Authority, ProblemKey: f.Source.ManagedKey, Episode: f.Source.Episode, Generation: f.Source.Generation, Starts: f.Source.ActiveSince}
	model := resend.NativeClock{Policy: m.policy.spec, Target: m.policy.notifierID}
	if _, err = model.Apply(resend.NativeEvent{Kind: "new_episode", At: active, Source: ref, Severity: f.Source.SourceSeverity}); err != nil {
		return err
	}
	// Policy clock is measured on the same target as native active age. Use zero
	// early allowance. Never subtract a configured maximum as if it were measured.
	until, stop := context.WithDeadline(ctx, active.Add(m.policy.spec.Escalation.After+3*m.policy.spec.Interval+time.Minute))
	defer stop()
	seen := map[string]bool{}
	firstSeen := map[string]time.Time{}
	seenSource := map[string]bool{}
	knownBefore := map[string]time.Time{}
	var prevPoll time.Time
	var ordinary, higher, escalated bool
	return poll(until, func() (bool, error) {
		snap, err := m.config.Snapshot(until, f.History.ID)
		if err != nil {
			return false, err
		}
		var manual *managed.Effect
		for _, root := range snap.DispatchRoots {
			if !firstSeen[root].IsZero() {
				continue
			}
			firstSeen[root] = snap.MeasuredAt
			knownBefore[root] = prevPoll
		}
		for i := range snap.Effects {
			e := &snap.Effects[i]
			if e.Notifier == m.policy.notifierID && e.Action == "notification" && e.Logical == manualID && e.ConfirmedAt != "" && e.TargetState == "confirmed" {
				manual = e
			}
		}
		for _, e := range snap.Effects {
			if e.ChildState == "unknown_requires_reconciliation" || e.TargetState == "unknown" || e.CallbackOutcome == "unknown" {
				if err = m.save(context.WithoutCancel(until), "native.original_unknown", map[string]any{"source": f.Source, "effect": e, "manual": manualID}); err != nil {
					return false, err
				}
				return false, fmt.Errorf("original effect requires reconciliation %s/%s", e.Operation, e.Target)
			}
		}
		if manual == nil {
			prevPoll = snap.MeasuredAt.Add(-snap.QueryDuration)
			return false, nil
		}
		if model.LastContact.IsZero() {
			_, err = model.Apply(resend.NativeEvent{Kind: "manual_resend", At: active, Source: ref, Token: manualID})
			if err != nil {
				return false, err
			}
			t, err := time.Parse(time.RFC3339Nano, manual.ConfirmedAt)
			if err != nil {
				return false, err
			}
			_, err = model.Apply(resend.NativeEvent{Kind: "provider_outcome", At: t, Source: ref, Target: model.Target, Token: manualID, Outcome: "confirmed"})
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
				if when.After(model.LastContact) && !escalated {
					return false, errors.New("independent timer window received an additional native callback")
				}
				seenSource[e.Callback] = true
			}
			if e.ScheduleRoot == "" || seen[e.Child] || e.Notifier != model.Target || e.Action != "notification" || e.ConfirmedAt == "" {
				continue
			}
			if e.TargetState != "confirmed" || e.Child == "" || e.Callback == "" || e.Revision == "" || e.ScheduleRoot != e.Logical || e.ReceivedAt == "" {
				return false, errors.New("scheduled physical recipient evidence incomplete")
			}
			confirmed, err := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
			if err != nil {
				return false, err
			}
			due := model.LastContact.Add(model.Policy.Interval)
			kind := "repeat"
			if !escalated && !active.Add(model.Policy.Escalation.After).After(due) {
				due = active.Add(model.Policy.Escalation.After)
				kind = "escalation"
			}
			// Supplied timer input comes from the independently computed deadline.
			expected, err := model.Apply(resend.NativeEvent{Kind: "timer_due", At: due, Source: ref, Token: e.Logical})
			if err != nil || expected == nil || expected.Kind != kind {
				return false, errors.New("unexpected timed recipient action")
			}
			if expected.Severity != e.Severity || (kind == "escalation") != (e.Kind == "escalate") || kind == "repeat" && e.Kind != "resend" {
				return false, errors.New("scheduled action kind or effective severity mismatch")
			}
			// Bracket the first durable dispatch row using adjacent consistent DB
			// snapshots. A bracket overlapping the due instant is inconclusive.
			low, high := knownBefore[e.Logical], firstSeen[e.Logical]
			if low.IsZero() || low.Before(due) {
				return false, errors.New("dispatch bracket crosses earliest deadline; exact dispatch timestamp required")
			}
			if err = resend.CheckDispatch(due, time.Time{}, high, 0); err != nil {
				return false, err
			}
			if _, err = model.Apply(resend.NativeEvent{Kind: "provider_outcome", At: confirmed, Source: ref, Target: model.Target, Token: e.Logical, Outcome: "confirmed"}); err != nil {
				return false, err
			}
			if kind == "escalation" {
				escalated = true
			} else if escalated {
				higher = true
			} else {
				ordinary = true
			}
			seen[e.Child] = true
			if err = m.save(until, "native.timer_due", map[string]any{"history": f.History.ID, "expected": expected, "effect": e, "earliestObserved": low, "latestObserved": high, "confirmed": confirmed, "queryDuration": snap.QueryDuration}); err != nil {
				return false, err
			}
		}
		prevPoll = snap.MeasuredAt.Add(-snap.QueryDuration)
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
			}
		}
		ok, err := matchSink(snap.Effects, selected, m.policy.notifierID, f.History.Labels["correlation_key"])
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
		if !proved {
			return false, nil
		}
		reader := m.run.e.d.nativeSender.(nativeReader)
		live, err := reader.Members(until, map[string]string{"aid": f.History.Labels["aid"], "source_id": m.config.SourceID, "node": m.config.TID})
		if err != nil {
			return false, err
		}
		refreshed := false
		for _, v := range live {
			if v.Fingerprint == f.Source.Fingerprint && v.Labels["severity"] == "major" && v.EndsAt.After(m.run.now()) {
				refreshed = true
			}
		}
		if !refreshed {
			return false, errors.New("original source validity unavailable at higher-floor resend")
		}
		if err = m.save(until, "native.managed.refresh", map[string]any{"history": f.History.ID, "members": live}); err != nil {
			return false, err
		}
		if err = m.save(until, "native.managed.effects", map[string]any{"history": f.History.ID, "source": snap.Source, "effects": snap.Effects, "sink": selected, "queryDuration": snap.QueryDuration}); err != nil {
			return false, err
		}
		return true, nil
	})
}
