package engine

import (
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
