package engine

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// TestStopKeepsResendExhaustion (OSC-231 AC-1, AC-2): a guard trip or an
// operator stop after the capped cohorts reached their caps, before the resend
// phase end, must still score their resends. A stop skips the phase-end wait
// and drain then closes the episodes; OSCAR's status lists only open episodes,
// so exhaustion must be read before drain (camp_dee1740eb5ba2fa2 scored R1, R3
// and R4 0/24 after a ticket_backlog trip).
func TestStopKeepsResendExhaustion(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		verdict      domain.Verdict
		trip         bool
	}{
		{"guard trip", "guard:restart", domain.VerdictFail, true},
		{"operator stop", "stopped: operator stop", domain.VerdictInconclusive, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t)
				s := time.Second
				// A resend phase long enough that the trip (the guard tick after the
				// caps, T+120) lands well before its end.
				h.s.Phases[4].End, h.s.Phases[5].Start, h.s.Phases[5].End, h.s.ResendUntil = 150*s, 150*s, 180*s, 148*s
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- h.run(ctx) }()
				waitFor(t, "capped cohorts at their caps", func() bool {
					h.oscar.mu.Lock()
					defer h.oscar.mu.Unlock()
					capped := 0
					for name := range h.oscar.cohort {
						if spec, _, exp := h.oscar.expected(name); spec.MaxResends != nil {
							if !exp.Exhausted {
								return false
							}
							capped++
						}
					}
					return capped == 3 // R1, R3, R4
				})
				if tc.trip {
					h.oscar.mu.Lock()
					h.oscar.restartAt = h.clock.Now()
					h.oscar.mu.Unlock()
				} else { // what runtime.StopCampaign does: STOPPING, then cancel
					if err := h.db.TransitionCampaign(context.Background(), h.id, domain.CampaignStopping, h.clock.Now(), "stop requested"); err != nil {
						t.Fatal(err)
					}
					cancel()
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				c, card := h.scorecard()
				if c.Verdict != tc.verdict || len(card.Reasons) == 0 || card.Reasons[0] != tc.reason {
					t.Fatalf("verdict %s reasons %v", c.Verdict, card.Reasons)
				}
				if c.Cleanup != domain.CleanupClean {
					t.Fatalf("cleanup %s", c.Cleanup)
				}
				if card.Resend.Expected != 4 || card.Resend.Succeeded != 4 {
					t.Fatalf("resend %+v, want all 4 policies' members succeeded", card.Resend)
				}
			})
		})
	}
}
