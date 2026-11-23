package managed

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// AIDs use the complete campaign identity digested into a bounded device token.
func AID(id, lane string, idx int) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("CT-%x-%s-%06d", sum[:12], lane, idx)
}

type Offer struct {
	At   time.Duration
	Slot int
	Code string
}

// Each slot raises, changes source severity, then clears. Round down to complete
// lifecycles. The selected rate counts every wire PDU, including clear.
func MixedOffer(index int, rate float64, end time.Duration) (Offer, bool) {
	if rate <= 0 || rate > 100 || end <= 0 || index < 0 {
		return Offer{}, false
	}
	// Interleave 64 devices so scheduled policy traffic has time to act where
	// the configured rate and repeat duration permit it.
	count := int(end.Seconds()*rate) / 3 * 3
	if index >= count {
		return Offer{}, false
	}
	base := index / 192 * 192
	width := min(64, (count-base)/3)
	cycle := index - base
	return Offer{time.Duration(float64(index) / rate * float64(time.Second)), index/192*64 + cycle%width, []string{"MN", "MJ", "CL"}[cycle/width]}, true
}
