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
	scope := "POL"
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
		var timing dispatchSamples
		cut := until.Add(-8 * time.Second)
		for r.now().Before(cut) {
			sample, er := m.config.RootSamples(ctx, f.History.ID)
			if er != nil {
				r.noteMissing("sustained dispatch observation unavailable")
				break
			}
			timing.add(sample)
			next := r.now().Add(2 * time.Second)
			if next.After(cut) {
				next = cut
			}
			if !r.e.clock.SleepUntil(ctx, next) {
				return
			}
		}
		snap, err := m.config.Snapshot(ctx, f.History.ID)
		if err != nil {
			r.noteMissing(fmt.Sprintf("segment %s/%d journal: %v", scope, i, err))
			continue
		}
		if err = m.holdUnknown(ctx, f, snap); err != nil {
			r.noteMissing(err.Error())
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
		timing.add(snap)

		err = segmentEvidence(p, f, snap, owned, timing)
		reason := ""
		if err != nil {
			reason = err.Error()
			r.noteMissing(fmt.Sprintf("segment %s/%d: %v", scope, i, err))
		}
		if err = m.save(ctx, "native.managed.segment", map[string]any{"index": i, "lane": scope, "start": at, "end": until, "expectedPolicy": p.id, "interval": p.spec.Interval, "escalation": p.spec.Escalation, "fixture": f, "snapshot": snap, "dispatch": timing, "receipts": owned, "reason": reason}); err != nil {
			r.noteMissing(err.Error())
		}
	}
	// clearOwned sends the final run-owned clear in the drain, even on stop.
}
func segmentEvidence(p *policyState, f NativeEpisodeFixture, s managed.Snapshot, receipts []targetops.Receipt, timing dispatchSamples) error {
	b, err := ownedBinding(p, s.Bindings)
	if err != nil {
		return err
	}
	active, err := time.Parse(time.RFC3339Nano, f.Source.ActiveSince)
	if err != nil {
		return err
	}
	clock := slotClock{receiver: f.Source.Receiver, active: active, source: f.Source.SourceSeverity}
	var low, higher, esc int
	seen := map[string]bool{}
	for _, e := range s.Effects {
		if unknownEffect(e) {
			return errors.New("original recipient outcome unknown")
		}
		if e.Notifier != p.notifierID || e.Action != p.spec.Action {
			continue
		}
		if e.ScheduleRoot == "" {
			if e.ConfirmedAt == "" {
				continue
			}
			confirmed, err := time.Parse(time.RFC3339Nano, e.ConfirmedAt)
			if err != nil {
				return err
			}
			if e.Kind != "ordinary" || low+higher+esc != 0 || !clock.last.IsZero() {
				return errors.New("ambiguous extra native contact within quiet segment")
			}
			clock.last = confirmed
			continue
		}
		if seen[e.ScheduleRoot] {
			return errors.New("duplicate scheduled recipient")
		}
		seen[e.ScheduleRoot] = true
		if err = clock.accept(p, b, e, timing.Windows[e.Logical]); err != nil {
			return err
		}
		if e.Kind == "escalate" {
			esc++
		} else if clock.raised {
			higher++
		} else {
			low++
		}
	}
	due, _, _ := clock.next(p)
	if s.MeasuredAt.After(due.Add(30 * time.Second)) {
		return errors.New("sustained policy has an overdue unobserved action")
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
