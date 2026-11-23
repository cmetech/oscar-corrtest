package engine

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// Live OSCAR merges a fresh cohort's re-posts (new startsAt every Redelivery)
// into one History row that keeps the first startsAt; only the latest pair is
// open. Cleanup resolved with the row's startsAt and left all 20 resend-occ and
// resend-either alerts firing (camp_59f90e7f4c7c41b8, 2026-11-16).
func TestCleanupResolvesTheLatestPairOfFreshCohorts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		h.oscar.mergePairs = true
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c := h.campaign(); c.Cleanup != domain.CleanupClean {
			t.Fatalf("cleanup %s", c.Cleanup)
		}
		h.oscar.mu.Lock()
		defer h.oscar.mu.Unlock()
		for _, row := range h.oscar.history {
			if row.Status != "resolved" {
				t.Fatalf("%s still firing (row startsAt %s, open pair %s)", row.AlertName, row.StartsAt, h.oscar.openPair[row])
			}
		}
	})
}
