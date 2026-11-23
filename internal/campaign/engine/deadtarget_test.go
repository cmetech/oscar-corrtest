package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// TestDeliveryRetryDeadTargetIsCreatedJudgedAndRemoved: the dead webhook target
// exists for the run and is deleted by cleanup; the verdict follows publishes per
// delivery, and a run whose notifier queue never backed up is not a pass.
func TestDeliveryRetryDeadTargetIsCreatedJudgedAndRemoved(t *testing.T) {
	for _, tc := range []struct {
		name             string
		republished, age float64
		wantReason       string
		wantFail         bool
	}{
		{"looping build", 3000, 300, "delivery-retry: 4.00 publishes per delivery, limit 1.20", true},
		{"fixed build", 50, 300, "", false},
		{"no backlog", 0, 10, "the loop was not exercised", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t)
				h.s.DeadTarget = scenario.DeliveryRetryV1().DeadTarget
				h.oscar.s = h.s
				h.oscar.queryValues = map[string]float64{"claim_total": 1000, `outcome="republished"`: tc.republished,
					"count_over_time": 2, "max_over_time(oscar_celery_queue_oldest": tc.age}
				if err := h.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				c := h.campaign()
				if c.Cleanup != domain.CleanupClean {
					t.Fatalf("cleanup %s", c.Cleanup)
				}
				h.oscar.mu.Lock()
				created, left := h.oscar.deadCreated, h.oscar.notifiers
				h.oscar.mu.Unlock()
				if len(created) != 1 || !strings.HasPrefix(created[0], "CAMPAIGN_DEAD_") || !strings.HasSuffix(created[0], " https://load.home.arpa:9/dead") {
					t.Fatalf("dead target created %v", created)
				}
				for _, n := range left {
					if strings.HasPrefix(n.Name, "CAMPAIGN_DEAD_") {
						t.Fatalf("dead target %s left after cleanup", n.Name)
					}
				}
				var card struct {
					Verdict string
					Reasons []string
				}
				if err := json.Unmarshal(c.Scorecard, &card); err != nil {
					t.Fatal(err)
				}
				reasons := strings.Join(card.Reasons, "\n")
				if tc.wantReason == "" && strings.Contains(reasons, "delivery-retry") || tc.wantReason != "" && !strings.Contains(reasons, tc.wantReason) {
					t.Fatalf("reasons %v", card.Reasons)
				}
				if (card.Verdict == string(domain.VerdictFail)) != tc.wantFail && tc.wantFail {
					t.Fatalf("verdict %s", card.Verdict)
				}
				if !tc.wantFail && card.Verdict == string(domain.VerdictPass) && tc.age < 100 {
					t.Fatal("a run without a backlog must not pass")
				}
			})
		})
	}
}
