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
		timing := dispatchSamples{ExpectedDue: map[string]time.Time{}}
		timing.ValidUntil, _ = time.Parse(time.RFC3339Nano, f.Source.RefreshDeadline)
		cut := until.Add(-8 * time.Second)
		for r.now().Before(cut) {
			if err := m.validity(ctx, f, &timing, until); err != nil {
				r.noteMissing(fmt.Sprintf("segment lifetime %s/%d: %v", scope, i, err))
				break
			}
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
		rows, err := m.rows(ctx, aid)
		sameCount := false
		for _, row := range rows {
			if row.ID == f.History.ID && row.Status != "resolved" && row.EpisodeNumber == f.History.EpisodeNumber && row.OccurrenceCount == f.History.OccurrenceCount {
				sameCount = true
			}
		}
		if err != nil || !sameCount {
			r.noteMissing(fmt.Sprintf("segment %s/%d original source count changed or unavailable", scope, i))
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

		err = segmentEvidence(p, f, snap, owned, timing, snap.MeasuredAt, m.run.e.d.CampaignNotifier)
		reason := ""
		if err != nil {
			reason = err.Error()
			r.noteMissing(fmt.Sprintf("segment %s/%d: %v", scope, i, err))
		}
		if err = m.save(ctx, "native.managed.segment", map[string]any{"index": i, "lane": scope, "start": at, "end": until, "expectedPolicy": p.id, "interval": p.spec.Interval, "escalation": p.spec.Escalation, "fixture": f, "snapshot": snap, "dispatch": timing, "receipts": owned, "reason": reason}); err != nil {
			r.noteMissing(err.Error())
		}
		m.tails.Add(1)
		go func() {
			defer m.tails.Done()
			tail, stop := context.WithDeadline(context.WithoutCancel(ctx), until.Add(90*time.Second))
			defer stop()
			if err := m.segmentTail(tail, p, f, aid, i, scope, at, until, timing); err != nil {
				r.noteMissing(fmt.Sprintf("segment tail %s/%d: %v", scope, i, err))
			}
		}()

	}
	// clearOwned sends the final run-owned clear in the drain, even on stop.
}
func segmentEvidence(p *policyState, f NativeEpisodeFixture, s managed.Snapshot, receipts []targetops.Receipt, timing dispatchSamples, activeThrough time.Time, captureName string) error {
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
		due, _, _ := clock.next(p)
		if err = clock.accept(p, b, e, timing.Windows[e.Logical]); err != nil {
			return err
		}
		timing.ExpectedDue[e.Operation] = due
		if e.Kind == "escalate" {
			esc++
		} else if clock.raised {
			higher++
		} else {
			low++
		}
	}
	due, _, _ := clock.next(p)
	if activeThrough.After(due.Add(30 * time.Second)) {
		return errors.New("sustained policy has an overdue unobserved action")
	}
	if low < 1 || p.spec.Escalation != nil && (esc != 1 || higher < 1) {
		return errors.New("required repeat/escalation evidence absent")
	}
	ok, err := matchManagedSink(s, receipts, p.notifierID, captureName, f)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("controlled sink reconciliation incomplete")
	}
	return nil
}

// Check each observed expiry before trusting the next refresh. A later healthy
// member cannot fill an expired interval or substitute another generation.
func (m *managedLane) validity(ctx context.Context, f NativeEpisodeFixture, t *dispatchSamples, until time.Time) error {
	now := m.run.now()
	if !t.ValidUntil.After(now) {
		return errors.New("sentinel source validity expired before refresh observation")
	}
	if t.ValidUntil.After(until) || t.ValidUntil.Sub(now) > 15*time.Second {
		return nil
	}
	reader, ok := m.run.e.d.nativeSender.(nativeReader)
	if !ok {
		return errors.New("native lifetime reader unavailable")
	}
	live, err := reader.Members(ctx, map[string]string{"aid": f.History.Labels["aid"], "source_id": f.Source.SourceID, "node": f.Source.TID})
	if err != nil {
		return err
	}
	now = m.run.now()
	if !t.ValidUntil.After(now) {
		return errors.New("unobserved sentinel source lifetime interval")
	}
	for _, v := range live {
		if (observedMember(v, f.Source, "major", now) || observedMember(v, f.Source, "critical", now)) && len(v.Receivers) == 1 && v.Receivers[0].Name == f.Source.Receiver {
			t.ValidUntil = v.EndsAt
			t.ValiditySamples++
			return nil
		}
	}
	return errors.New("sentinel original source unavailable")
}
