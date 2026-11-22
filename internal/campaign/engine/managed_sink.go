package engine

import (
	"context"
	"errors"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
)

// Match physical provider entries one to one, including ordinary deliveries.
// An extra receipt or overlapping candidate windows cannot be accepted as proof.
func matchSink(effects []managed.Effect, receipts []targetops.Receipt, notifier, correlation string) (bool, error) {
	claimed := map[int64]bool{}
	children := map[string]bool{}
	for _, effect := range effects {
		if effect.Notifier != notifier || effect.Action != "notification" || effect.ConfirmedAt == "" {
			continue
		}
		if effect.TargetState != "confirmed" || effect.Child == "" || effect.Callback == "" || children[effect.Child] {
			return false, errors.New("ambiguous physical target allocation")
		}
		children[effect.Child] = true
		start, err := time.Parse(time.RFC3339Nano, effect.EnteredAt)
		if err != nil {
			start, err = time.Parse(time.RFC3339Nano, effect.EnteredAt+"Z")
		}
		if err != nil {
			return false, errors.New("provider entry time unavailable")
		}
		end, err := time.Parse(time.RFC3339Nano, effect.ConfirmedAt)
		if err != nil || end.Before(start) {
			return false, errors.New("provider confirmation time unavailable")
		}
		var hit int64
		for _, receipt := range receipts {
			if receipt.ReceivedAt.Before(start) || receipt.ReceivedAt.After(end) {
				continue
			}
			for _, a := range receipt.Alerts {
				if a.Status == "firing" && a.Labels["severity"] == effect.Severity && a.Labels["correlation_key"] == correlation {
					if hit != 0 || claimed[receipt.ID] {
						return false, errors.New("ambiguous provider receipt identity")
					}
					hit = receipt.ID
				}
			}
		}
		if hit == 0 {
			return false, nil
		}
		claimed[hit] = true
	}
	if len(claimed) < len(receipts) {
		return false, errors.New("unattributed controlled provider contact")
	}
	return len(claimed) > 0, nil
}

func bulkIsolation(f, sibling NativeEpisodeFixture, root, notifier string, effects []managed.Effect, selected, other []targetops.Receipt) (bool, error) {
	var mine []managed.Effect
	for _, e := range effects {
		if e.Logical == root && e.Notifier == notifier && e.Action == "notification" {
			if e.Kind != "resend" || e.Target == "" || e.Child == "" || e.Operation == "" || e.ConfirmedAt == "" {
				return false, nil
			}
			mine = append(mine, e)
		}
	}
	if len(mine) != 1 {
		return false, errors.New("selected original has no unique physical notification child")
	}
	for _, r := range other {
		for _, a := range r.Alerts {
			if a.Status == "firing" && a.Labels["aid"] == sibling.History.Labels["aid"] {
				return false, errors.New("unselected sibling delivered during isolated bulk window")
			}
		}
	}
	start, err := time.Parse(time.RFC3339Nano, f.Source.Starts)
	if err != nil {
		return false, err
	}
	var firing []targetops.Receipt
	for _, r := range selected {
		for _, a := range r.Alerts {
			if a.Status != "firing" {
				continue
			}
			got, err := time.Parse(time.RFC3339Nano, a.StartsAt)
			if err != nil || !got.Equal(start) || a.HistoryID != "" && a.HistoryID != f.History.ID || a.Labels["aid"] != f.History.Labels["aid"] || a.Labels["source_id"] != f.Source.SourceID || a.Labels["node"] != f.Source.TID || a.Labels["correlation_key"] != f.Source.Correlation {
				return false, errors.New("bulk payload differs from selected episode")
			}
			firing = append(firing, r)
		}
	}
	return matchSink(mine, firing, notifier, f.Source.Correlation)
}

// Both members must have independently visible ordinary payloads before the
// bulk request. Otherwise an empty sibling capture cannot establish isolation.
func (m *managedLane) bulkBaseline(ctx context.Context, f NativeEpisodeFixture) error {
	start, err := time.Parse(time.RFC3339Nano, f.Source.Starts)
	if err != nil {
		return err
	}
	wait, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	aid := f.History.Labels["aid"]
	return poll(wait, func() (bool, error) {
		snap, err := m.config.Snapshot(wait, f.History.ID)
		if err != nil {
			return false, err
		}
		if err = m.holdUnknown(wait, f, snap); err != nil {
			return false, err
		}
		rs, err := m.sink.ManagedReceipts(wait, aid, start)
		if err != nil {
			return false, err
		}
		var mine []targetops.Receipt
		for _, r := range rs {
			for _, a := range r.Alerts {
				at, er := time.Parse(time.RFC3339Nano, a.StartsAt)
				if er == nil && at.Equal(start) && a.Status == "firing" && a.Labels["correlation_key"] == f.Source.Correlation {
					mine = append(mine, r)
					break
				}
			}
		}
		ok, err := matchSink(snap.Effects, mine, m.policy.notifierID, f.Source.Correlation)
		if err != nil || !ok {
			return false, err
		}
		return true, m.save(wait, "native.bulk.baseline", map[string]any{"subject": f, "receipts": mine})
	})
}
