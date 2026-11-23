package scenario

import (
	"testing"
	"time"
)

// Live smoke 2026-11-15 (camp_144b717f577fdd83): OSCAR publishes an ordinary
// alert with Nats-Msg-Id fingerprint:status:startsAt, and the JetStream stream
// absorbs a duplicate within its 120s window. Time cohorts repost unchanged
// (same startsAt), so a 60s redelivery dropped every other post and the one
// landing at 120s went either way. Redelivery must clear the window.
func TestRedeliveryClearsTransportDedupWindow(t *testing.T) {
	for _, s := range Builtins() {
		if len(s.Resend) == 0 {
			continue // no resend cohorts: the redelivery loop never posts
		}
		if s.Redelivery < TransportDedupWindow+10*time.Second {
			t.Errorf("%s@%s redelivery %s must exceed the %s transport dedup window with margin",
				s.ID, s.Version, s.Redelivery, TransportDedupWindow)
		}
	}
}
