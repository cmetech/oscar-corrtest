package scenario_test

import (
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
)

// Live smoke 2026-11-15: policy activation took 83s (OSCAR cutover, unscaled),
// so cohorts started 1m23s into the resend phase and were posted once before
// ResendUntil. Every smoke policy must model at least one resend after a 90s
// activation, or the resend score is vacuous.
func TestSmokeResendModelsAResendAfterActivation(t *testing.T) {
	s := scenario.AlertingDaySmokeV1()
	origin := s.Phase("resend").Start + 90*time.Second
	for _, p := range s.Resend {
		var ds []resend.Delivery
		for at := origin; at < s.ResendUntil; at += s.Redelivery {
			ds = append(ds, resend.Delivery{At: at - origin, Fresh: p.Fresh})
		}
		if e, err := resend.Expect(p, ds); err != nil || len(e.Resends) == 0 {
			t.Errorf("policy %s models no resend (%d deliveries before %s)", p.Key, len(ds), s.ResendUntil)
		}
	}
}
