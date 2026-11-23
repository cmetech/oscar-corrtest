package engine

import (
	"context"
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Observe beyond the pre-boundary snapshot without shifting the next raise.
// The final subject's clear is issued by clearOwned on the original session.
func (m *managedLane) segmentTail(ctx context.Context, p *policyState, f NativeEpisodeFixture, aid string, index int, lane string, start, end time.Time, timing dispatchSamples) error {
	return poll(ctx, func() (bool, error) {
		snap, err := m.config.Snapshot(ctx, f.History.ID)
		if err != nil {
			return false, err
		}
		timing.add(snap)
		cleared, ok := m.clearsAt.Load(aid)
		if !ok {
			if err = m.holdUnknown(ctx, f, snap); err != nil {
				return false, err
			}
			return false, m.validity(ctx, f, &timing, end.Add(10*time.Second))
		}
		clearAt := cleared.(time.Time)
		if clearAt.After(timing.ValidUntil) {
			return false, errors.New("source validity does not reach its actual clear")
		}
		if clearAt.Before(end) {
			return false, errors.New("sentinel cleared before segment end")
		}
		rows, err := m.rows(ctx, aid)
		if err != nil {
			return false, err
		}
		closed := false
		for _, row := range rows {
			if row.ID == f.History.ID && row.EpisodeNumber == f.History.EpisodeNumber && row.Status == "resolved" {
				closed = true
			}
		}
		if !closed {
			return false, nil
		}
		// Include operations without a callback or target, and schedule roots that
		// never reached native admission. Settlement cannot infer a successful call.
		originals, err := m.proveClosedRoots(ctx, f, snap, p.notifierID)
		m.rememberClosed(aid, m.condition(lane), f, originals, nil)
		if err != nil {
			return false, err
		}
		rs, err := m.sink.ManagedReceipts(ctx, aid, start)
		if err != nil {
			return false, err
		}
		var firing []targetops.Receipt
		for _, r := range rs {
			for _, a := range r.Alerts {
				if a.Status == "firing" {
					if !selectedMember(f, a) {
						return false, errors.New("foreign sentinel receipt member")
					}
					firing = append(firing, r)
					break
				}
			}
		}
		if err = segmentEvidence(p, f, snap, firing, timing, clearAt, m.run.e.d.CampaignNotifier); err != nil {
			return false, err
		}
		m.rememberClosed(aid, m.condition(lane), f, originals, rs)
		return true, m.save(ctx, "native.managed.segment_closed", map[string]any{"index": index, "lane": lane, "start": start, "end": end, "clearedAt": clearAt, "fixture": f, "snapshot": snap, "dispatch": timing, "originals": originals, "receipts": rs})
	})
}
