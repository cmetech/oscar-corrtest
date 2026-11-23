package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

type closedSubject struct {
	AID, Condition, History string
	Roots                   map[string]string
	Receipts                map[int64]bool
	Segment                 bool
}

func (m *managedLane) rememberClosed(aid, cond, history string, s managed.Snapshot, rs []targetops.Receipt, segment bool) {
	v := closedSubject{aid, cond, history, scheduledRoots(s), firingIDs(aid, rs), segment}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed == nil {
		m.closed = map[string]closedSubject{}
	}
	m.closed[aid] = v
}
func scheduledRoots(s managed.Snapshot) map[string]string {
	roots := map[string]string{}
	for _, d := range s.Dispatches {
		roots[d.Root] = d.Root
	}
	for _, e := range s.Effects {
		roots[e.Logical] = e.Operation
	}
	return roots
}
func firingIDs(aid string, rs []targetops.Receipt) map[int64]bool {
	ids := map[int64]bool{}
	for _, r := range rs {
		for _, a := range r.Alerts {
			if a.Labels["aid"] == aid && a.Status == "firing" {
				ids[r.ID] = true
			}
		}
	}
	return ids
}

// One paginated capture per run-owned condition. Compare closed members through
// queue drain so a late physical effect cannot disappear after a rotation.
func (m *managedLane) verifyDrain(ctx context.Context) error {
	if len(m.closed) == 0 {
		return errors.New("no closed policy/rate subjects")
	}
	byCond := map[string]map[string]map[int64]bool{}
	for _, v := range m.closed {
		if _, ok := byCond[v.Condition]; ok {
			continue
		}
		rs, err := m.sink.ManagedConditionReceipts(ctx, v.Condition, m.run.t0)
		if err != nil {
			return err
		}
		ids := map[string]map[int64]bool{}
		for _, r := range rs {
			for _, a := range r.Alerts {
				if a.Status != "firing" {
					continue
				}
				aid := a.Labels["aid"]
				if a.Labels["node"] != m.config.TID || a.Labels["source_id"] != m.config.SourceID {
					return errors.New("foreign member in owned condition capture")
				}
				if ids[aid] == nil {
					ids[aid] = map[int64]bool{}
				}
				if ids[aid][r.ID] {
					return errors.New("duplicate physical member in final capture")
				}
				ids[aid][r.ID] = true
			}
		}
		byCond[v.Condition] = ids
	}
	for _, v := range m.closed {
		if !maps.Equal(byCond[v.Condition][v.AID], v.Receipts) {
			return fmt.Errorf("final provider receipt set changed for %s", v.AID)
		}
		if v.Segment {
			s, err := m.config.Snapshot(ctx, v.History)
			if err != nil {
				return err
			}
			if err = m.holdUnknown(ctx, NativeEpisodeFixture{History: observedHistory{ID: v.History}}, s); err != nil {
				return err
			}
			if !maps.Equal(v.Roots, scheduledRoots(s)) {
				return errors.New("sentinel physical or logical root changed after clear")
			}
		}
	}
	return m.save(ctx, "native.managed.drain", map[string]any{"closedSubjects": len(m.closed), "checkedAt": m.run.now()})
}

func (m *managedLane) proveClosedRoots(ctx context.Context, f NativeEpisodeFixture, s managed.Snapshot, notifier string) (map[string]managed.OriginalReceipt, error) {
	originals := map[string]managed.OriginalReceipt{}
	for root, op := range scheduledRoots(s) {
		original, err := m.config.Original(ctx, f.History.ID, op)
		if err != nil {
			return nil, err
		}
		settle, ok := original.Settlement(f.History.ID, f.History.EpisodeNumber, root)
		if !ok {
			_, err = m.run.record(context.WithoutCancel(ctx), kindManagedOwner, root, originalOwner{Kind: "segment_root", History: f.History.ID, Episode: f.History.EpisodeNumber, Root: root, Operation: op})
			if err != nil {
				return nil, err
			}
			return nil, errors.New("closed subject has an unsettled original root")
		}
		for _, c := range settle.Children {
			if c.Successor != "" {
				continue
			}
			if c.State == "failed" {
				return nil, errors.New("original physical child failed")
			}
			if c.State == "sent" && c.Action == "notification" && c.Notifier != notifier && !containsFold(keepNotifiers, c.Name) {
				return nil, errors.New("unexpected external notification target")
			}
		}
		originals[root] = original
	}
	return originals, nil
}
