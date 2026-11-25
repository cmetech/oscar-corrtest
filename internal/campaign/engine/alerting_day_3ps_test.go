package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// TestV4OfferedBudgetUsesScenarioTotals (OSC-184 AC-3): the offered-rate
// check judges against the scenario's own totals, and v4's 5/s when it sets
// none. The harness offers well over 1/s, so a 1/s total reads over budget.
func TestV4OfferedBudgetUsesScenarioTotals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		total, peak float64
		want        string
	}{{"scenario", 1, 2, "/s, budget 1/s)"}, {"v4-default", 0, 0, " 5/s)"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, _ := v4Harness(t)
				h.s.OfferedRate, h.s.OfferedSpikeRate = tc.total, tc.peak
				h.oscar.s = h.s
				if err := h.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				var card score.Scorecard
				if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, r := range card.Reasons {
					if strings.HasPrefix(r, "offered rate") {
						found = found || strings.Contains(r, tc.want)
						if tc.total == 0 && strings.Contains(r, " 1/s)") {
							t.Errorf("v4 default judged against 1/s: %s", r)
						}
					}
				}
				if !found {
					t.Errorf("no offered-rate reason containing %q: %v", tc.want, card.Reasons)
				}
			})
		})
	}
}

// TestAlertingDay3psCorrelationPerMinute (OSC-184, plan review F2) documents
// the per-minute granularity of the 3ps correlation lane: documents are
// counted when they start and the pacing credit holds up to one document, so
// each minute's offers vary around the planned 36 (0.6/s). Measured at
// d7f5a64: 36 -8/+7 with 30-90 s runs. With the other lanes (worker 116-117,
// resend 18-19, fingerprint 9 a minute) a minute totals 171-188, inside the
// 3ps check's 171-189 (5% of 180), with no margin on the low side: a wider
// swing reads INCONCLUSIVE ("offered rate under plan"), never FAIL. Spike
// minutes stay within 10% of 72 (1.2/s).
func TestAlertingDay3psCorrelationPerMinute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		plain, tickets time.Duration // run time of an unticketed / ticketed document
	}{{"30s", 30 * time.Second, 30 * time.Second}, {"60s", time.Minute, time.Minute}, {"90s", 90 * time.Second, 90 * time.Second},
		{"90s-ticketed-240s", 90 * time.Second, 90*time.Second + setBTicketObservation}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s, _ := scenario.Lookup("alerting-day-3ps@v1")
				r := &run{s: s, t0: time.Now(), e: New(Deps{Store: linkStore{}, Correlate: func(_ context.Context, _ string, doc corr.Scenario, _ string) (domain.Run, error) {
					d := tc.plain
					for _, c := range doc.Cases {
						if c.ParentTicket {
							d = tc.tickets
						}
					}
					time.Sleep(d)
					return domain.Run{ID: "crt_slow"}, nil
				}}, worker.RealClock{})} // under synctest, RealClock runs on the bubble's fake time
				time.Sleep(s.Phase("correlation").Start)
				r.cycleCorrelation(context.Background())
				perMinute := map[int]int{}
				for _, o := range r.offers {
					perMinute[int(o.At.Sub(r.t0)/time.Minute)]++
				}
				spike := map[int]bool{}
				for _, b := range s.Baseline.Bursts {
					spike[int(b.At/time.Minute)] = true
				}
				plan, peak := s.Correlation.Rate*60, s.Correlation.SpikeRate*60
				for m := 5; m < 140; m++ {
					got := float64(perMinute[m])
					if spike[m] {
						if got < 0.9*peak || got > 1.1*peak {
							t.Errorf("spike minute %d offered %.0f, plan %.0f", m, got, peak)
						}
					} else if got < plan-8 || got > plan+8 {
						t.Errorf("minute %d offered %.0f, outside %.0f +-8", m, got, plan)
					}
				}
			})
		})
	}
}
