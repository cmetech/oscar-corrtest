package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Reconcile each offered slot to its closed History and provider children.
// Distinct source receipts can share a grouped physical sink request.
func (m *managedLane) rateSlot(ctx context.Context, slot int) error {
	aid := managed.AID(m.run.id, "mixed", slot)
	wait, stop := context.WithTimeout(ctx, 75*time.Second)
	defer stop()
	return poll(wait, func() (bool, error) {
		rows, err := m.rows(wait, aid)
		if err != nil {
			return false, err
		}
		if len(rows) == 0 {
			return false, nil
		}
		if len(rows) != 1 {
			return false, errors.New("mixed slot has multiple source Histories")
		}
		h := rows[0]
		if h.Status != "resolved" {
			return false, nil
		}
		snap, err := m.config.Snapshot(wait, h.ID)
		if err != nil {
			return false, err
		}
		var minor, major managed.SourceReceipt
		for _, s := range snap.Source {
			if s.History != h.ID || s.SourceID != m.config.SourceID || s.TID != m.config.TID || s.MappingRevision != m.config.MappingRevision || s.Correlation != h.Labels["correlation_key"] || s.Authority == "" || s.Episode == "" || s.Generation < 1 || s.EventID == "" || s.WireEpisode == "" {
				return false, errors.New("mixed source receipt identity mismatch")
			}
			switch s.SourceSeverity {
			case "minor":
				minor = s
			case "major":
				major = s
			}
		}
		if minor.EventID == "" || major.EventID == "" || minor.EventID == major.EventID || minor.Variant >= major.Variant || minor.Episode != major.Episode || minor.Authority != major.Authority || minor.Generation != major.Generation || minor.WireEpisode != major.WireEpisode {
			return false, nil
		}
		sourceEvents := map[string]bool{}
		for _, src := range snap.Source {
			if src.SourceSeverity == "minor" || src.SourceSeverity == "major" {
				sourceEvents[src.EventID] = true
			}
		}
		// Repeated policy contacts carry no new device occurrence. The two
		// firing PDUs have distinct event IDs and a single logical History.
		if len(sourceEvents) != 2 || h.OccurrenceCount != 2 {
			return false, errors.New("mixed firing input and source occurrence counts differ")
		}
		f := NativeEpisodeFixture{History: historyEvidence(h), Source: major}
		originals, err := m.proveClosedRoots(wait, f, snap, m.policy.notifierID)
		m.rememberClosed(aid, h.Labels["condition"], f, originals, nil)
		if err != nil {
			return false, err
		}
		if slot%2 != 0 {
			if _, err = ownedBinding(m.policy, snap.Bindings); err != nil {
				return false, err
			}
		} else {
			for _, b := range snap.Bindings {
				if b.Notifier == m.policy.notifierID && b.Rule != "" {
					return false, errors.New("no-policy mixed subject acquired a configured binding")
				}
			}
		}
		from, err := time.Parse(time.RFC3339Nano, minor.WireStart)
		if err != nil {
			return false, err
		}
		rs, err := m.sink.ManagedReceipts(wait, aid, from)
		if err != nil {
			return false, err
		}
		var firing []targetops.Receipt
		for _, r := range rs {
			for _, a := range r.Alerts {
				if a.Status != "firing" {
					continue
				}
				if a.Labels["node"] != major.TID || a.Labels["source_id"] != major.SourceID || a.Labels["correlation_key"] != major.Correlation {
					return false, errors.New("foreign mixed provider receipt")
				}
				firing = append(firing, r)
				break
			}
		}
		ok, err := matchManagedSink(snap, firing, m.policy.notifierID, m.run.e.d.CampaignNotifier, f)
		if err != nil || !ok {
			return false, err
		}
		m.rememberClosed(aid, h.Labels["condition"], f, originals, firing)
		return true, m.save(wait, "native.managed.rate_slot", map[string]any{"slot": slot, "aid": aid, "history": historyEvidence(h), "minor": minor, "major": major, "bindings": snap.Bindings, "effects": snap.Effects, "originals": originals, "receipts": rs, "queryDuration": snap.QueryDuration})
	})
}

// Observation runs apart from the source clock. A saturated observer produces
// an explicit missing slot and never delays or retries an offered source PDU.
func (m *managedLane) observeRateSlot(ctx context.Context, slot int, capacity chan struct{}) {
	select {
	case capacity <- struct{}{}:
		m.tails.Add(1)
		go func() {
			defer m.tails.Done()
			defer func() { <-capacity }()
			if err := m.rateSlot(context.WithoutCancel(ctx), slot); err != nil {
				m.run.noteMissing(fmt.Sprintf("TL1 rate slot %d: %v", slot, err))
			}
		}()
	default:
		m.run.noteMissing(fmt.Sprintf("TL1 rate slot %d: observer capacity exceeded", slot))
	}
}
