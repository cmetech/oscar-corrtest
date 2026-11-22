package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Every segment owns one real source per policy shape. Check the last segment
// as well as the first, retaining missing/duplicate/late evidence separately.
func (m *managedLane) sustained(ctx context.Context, p *policyState, span time.Duration) {
	r := m.run
	scope := "LOAD"
	if p.spec.Escalation == nil {
		scope = "REPEAT"
	}
	var previous string
	for i := 0; ; i++ {
		at := r.t0.Add(time.Duration(i) * span)
		end := r.t0.Add(r.s.LoadEnd)
		if !at.Before(end) || !r.e.clock.SleepUntil(ctx, at) {
			break
		}
		aid := managed.AID(r.id, "standing-"+scope, i)
		cond := m.condition(scope)
		if err := m.emit(ctx, aid, cond, "MJ"); err != nil {
			r.noteMissing(err.Error())
			return
		}
		if previous != "" {
			if err := m.emit(ctx, previous, cond, "CL"); err != nil {
				r.noteMissing(err.Error())
				return
			}
		}
		previous = aid
		f, err := m.observe(ctx, aid, "major", "")
		if err != nil {
			r.noteMissing(fmt.Sprintf("segment %s/%d source: %v", scope, i, err))
			continue
		}
		until := at.Add(span)
		if end.Before(until) {
			until = end
		}
		// Leave a bounded readback interval before the next source starts. The
		// remaining tail is explicitly reported for D3's final closure accounting.
		if !r.e.clock.SleepUntil(ctx, until.Add(-8*time.Second)) {
			break
		}
		snap, err := m.config.Snapshot(ctx, f.History.ID)
		if err != nil {
			r.noteMissing(fmt.Sprintf("segment %s/%d journal: %v", scope, i, err))
			continue
		}
		receipts, err := m.sink.ManagedReceipts(ctx, aid, at)
		if err != nil {
			r.noteMissing(fmt.Sprintf("segment %s/%d sink: %v", scope, i, err))
			continue
		}
		var owned []targetops.Receipt
		for _, receipt := range receipts {
			for _, alert := range receipt.Alerts {
				if alert.Labels["correlation_key"] == f.Source.Correlation && alert.Labels["node"] == m.config.TID && alert.Labels["source_id"] == m.config.SourceID && alert.Status == "firing" {
					owned = append(owned, receipt)
					break
				}
			}
		}
		err = segmentEvidence(p, f, snap, owned)
		reason := ""
		if err != nil {
			reason = err.Error()
			r.noteMissing(fmt.Sprintf("segment %s/%d: %v", scope, i, err))
		}
		if err = m.save(ctx, "native.managed.segment", map[string]any{"index": i, "lane": scope, "start": at, "end": until, "expectedPolicy": p.id, "interval": p.spec.Interval, "escalation": p.spec.Escalation, "fixture": f, "snapshot": snap, "receipts": owned, "reason": reason}); err != nil {
			r.noteMissing(err.Error())
		}
	}
	// clearOwned sends the final run-owned clear in the drain, even on stop.
}
func segmentEvidence(p *policyState, f NativeEpisodeFixture, s managed.Snapshot, receipts []targetops.Receipt) error {
	pinned := ""
	for _, b := range s.Bindings {
		if b.Notifier == p.notifierID && b.Action == "notification" && b.Rule == p.id {
			pinned = b.Revision
		}
	}
	if pinned == "" {
		return errors.New("expected policy binding absent")
	}
	var low, higher, esc int
	for _, e := range s.Effects {
		if e.TargetState == "unknown" || e.CallbackOutcome == "unknown" || e.ChildState == "unknown_requires_reconciliation" {
			return errors.New("original recipient outcome unknown")
		}
		if e.Notifier != p.notifierID || e.Action != "notification" || e.ScheduleRoot == "" {
			continue
		}
		if e.TargetState != "confirmed" || e.ConfirmedAt == "" || e.Revision != pinned || e.Logical != e.ScheduleRoot {
			return errors.New("unsettled or unbound scheduled recipient")
		}
		switch {
		case e.Kind == "resend" && e.Severity == "major" && esc == 0:
			low++
		case e.Kind == "escalate" && p.spec.Escalation != nil && e.Severity == p.spec.Escalation.TargetSeverity:
			esc++
		case e.Kind == "resend" && p.spec.Escalation != nil && esc == 1 && e.Severity == p.spec.Escalation.TargetSeverity:
			higher++
		default:
			return errors.New("unexpected scheduled action/recipient severity")
		}
	}
	// Every overdue ordinary slot must be represented. Confirmation resets the
	// clock; target/provider latency is retained separately. A missing late-run
	// resend must fail even when early counts were sufficient.
	var last time.Time
	for _, e := range s.Effects {
		if e.Notifier != p.notifierID || e.Action != "notification" || e.ConfirmedAt == "" {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
		if err != nil {
			return err
		}
		if e.ScheduleRoot != "" && !last.IsZero() {
			next := last.Add(p.spec.Interval)
			active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
			if err != nil {
				return err
			}
			if p.spec.Escalation != nil {
				raise := active.Add(p.spec.Escalation.After)
				if raise.After(last) && raise.Before(next) {
					next = raise
				}
			}
			callback, err := time.Parse(time.RFC3339Nano, e.ReceivedAt)
			if err != nil {
				return err
			}
			if callback.Before(next) || callback.After(next.Add(30*time.Second)) {
				return errors.New("scheduled callback outside independent segment deadline")
			}
		}
		if when.After(last) {
			last = when
		}
	}
	if last.IsZero() || s.MeasuredAt.After(last.Add(p.spec.Interval+30*time.Second)) {
		return errors.New("sustained policy has an overdue unobserved repeat")
	}
	if low < 1 || p.spec.Escalation != nil && (esc != 1 || higher < 1) {
		return errors.New("required repeat/escalation evidence absent")
	}
	ok, err := matchSink(s.Effects, receipts, p.notifierID, f.Source.Correlation)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("controlled sink reconciliation incomplete")
	}
	return nil
}
