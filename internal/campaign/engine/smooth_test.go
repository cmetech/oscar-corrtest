package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// smoothRun drives the scenario's correlation lane on fake time from its
// phase start, with correlate taking run(doc) and recording each started
// document's name, and returns the run and correlation offers per minute.
func smoothRun(t *testing.T, s scenario.Scenario, ctx context.Context, runTime func(doc corr.Scenario) time.Duration) (*run, map[int]int, *[]string) {
	t.Helper()
	return smoothRunOn(t, s, ctx, runTime, worker.RealClock{}) // under synctest, RealClock runs on the bubble's fake time
}

// lateClock wakes every SleepUntil 3 ms late, as a real clock does (plan review code F1).
type lateClock struct{ worker.RealClock }

func (c lateClock) SleepUntil(ctx context.Context, t time.Time) bool {
	return c.RealClock.SleepUntil(ctx, t.Add(3*time.Millisecond))
}

func smoothRunOn(t *testing.T, s scenario.Scenario, ctx context.Context, runTime func(doc corr.Scenario) time.Duration, clock interface {
	Now() time.Time
	SleepUntil(context.Context, time.Time) bool
}) (*run, map[int]int, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var started []string
	r := &run{s: s, t0: time.Now(), e: New(Deps{Store: linkStore{}, Correlate: func(_ context.Context, _ string, doc corr.Scenario, _ string) (domain.Run, error) {
		mu.Lock()
		started = append(started, doc.Name)
		mu.Unlock()
		time.Sleep(runTime(doc))
		return domain.Run{ID: "crt_slow"}, nil
	}}, clock)}
	time.Sleep(s.Phase("correlation").Start)
	r.correlation(ctx)
	perMinute := map[int]int{}
	for _, o := range r.offers {
		perMinute[int(o.At.Sub(r.t0)/time.Minute)]++
	}
	return r, perMinute, &started
}

// minutePlan is the correlation plan of minute m: Rate, or SpikeRate in a burst minute.
func minutePlan(s scenario.Scenario, m int) float64 {
	for _, b := range s.Baseline.Bursts {
		if time.Duration(m)*time.Minute >= b.At && time.Duration(m)*time.Minute < b.At+b.For {
			return s.Correlation.SpikeRate * 60
		}
	}
	return s.Correlation.Rate * 60
}

func runFor(plain, tickets time.Duration) func(corr.Scenario) time.Duration {
	return func(doc corr.Scenario) time.Duration {
		for _, c := range doc.Cases {
			if c.ParentTicket {
				return tickets
			}
		}
		return plain
	}
}

var slowRuns = []struct {
	name           string
	plain, tickets time.Duration
}{{"30s", 30 * time.Second, 30 * time.Second}, {"60s", time.Minute, time.Minute}, {"90s", 90 * time.Second, 90 * time.Second},
	{"90s-ticketed-240s", 90 * time.Second, 90*time.Second + setBTicketObservation}}

// TestAlertingDay3psV2SmoothsCorrelation (OSC-189 AC-1): with smoothing,
// every minute the offered-rate check judges for under-offering (T+5 to
// T+144) offers its plan (36, 72 in a spike) within -3/+3, the first minutes
// and T+145 at most plan + 3, and the lane total stays within 1% of plan.
// The same harness on v1's pacer swings beyond that (the contrast).
func TestAlertingDay3psV2SmoothsCorrelation(t *testing.T) {
	t.Parallel()
	for _, tc := range slowRuns {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, s := range []scenario.Scenario{scenario.AlertingDay3psV2(), scenario.AlertingDay3psV1()} {
				synctest.Test(t, func(t *testing.T) {
					_, perMinute, started := smoothRun(t, s, context.Background(), runFor(tc.plain, tc.tickets))
					if s.SmoothCorrelationOffers {
						fairMix(t, s, *started)
					}
					dist := map[int]int{}
					worst, offered, planned := 0.0, 0.0, 0.0
					for m := 1; m <= 145; m++ {
						got, plan := float64(perMinute[m]), minutePlan(s, m)
						offered, planned = offered+got, planned+plan
						if m >= 5 && m <= 144 {
							dist[int(got-plan)]++
							worst = max(worst, math.Abs(got-plan))
						} else if got > plan+3 {
							worst = max(worst, got-plan)
						}
					}
					t.Logf("%s@%s %s: per-minute deviation T+5..T+144 %v, total %.0f of %.0f", s.ID, s.Version, tc.name, dist, offered, planned)
					switch {
					case s.SmoothCorrelationOffers && (worst > 3 || math.Abs(offered-planned) > 0.01*planned):
						t.Errorf("smoothed: worst minute %.0f off plan, total %.0f of %.0f; per minute %v", worst, offered, planned, perMinute)
					case !s.SmoothCorrelationOffers && worst <= 3:
						t.Errorf("v1 pacer within +-3: the harness does not tell the pacers apart")
					}
				})
			}
		})
	}
}

// fairMix: every document runs about as often as the cycle lists it (within
// 10% of the mean per listing): reordering changes when documents run, not
// which (plan review N1).
func fairMix(t *testing.T, s scenario.Scenario, started []string) {
	t.Helper()
	listed := map[string]int{}
	for _, ref := range append(append([]scenario.CaseRef(nil), s.Correlation.SetA...), s.Correlation.SetB...) {
		doc, err := caseDoc(ref)
		if err != nil {
			t.Fatal(err)
		}
		listed[doc.Name]++
	}
	runs := map[string]int{}
	for _, n := range started {
		runs[n]++
	}
	total := 0
	for _, n := range listed {
		total += n
	}
	mean := float64(len(started)) / float64(total)
	for name, n := range listed {
		if per := float64(runs[name]) / float64(n); per < 0.9*mean-1 || per > 1.1*mean+1 {
			t.Errorf("%s ran %d times for %d listings, mean %.1f per listing", name, runs[name], n, mean)
		}
	}
}

// TestAlertingDay3psSmokeV2SmoothsCorrelation (OSC-189 AC-2): the smoke's two
// documents (10 and 6 alerts) at 12 a minute land T+1..T+7 within -2/+2 of
// plan (24 in a spike), run equally often, and the window offers at least 85%
// of plan: an even mix averages 8 alerts a start, so 96 of 108 is the most
// the -2/+2 bound allows (smoke@v1: 108 with minutes -6/+4).
func TestAlertingDay3psSmokeV2SmoothsCorrelation(t *testing.T) {
	t.Parallel()
	for _, tc := range slowRuns {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s := scenario.AlertingDay3psSmokeV2()
				_, perMinute, started := smoothRun(t, s, context.Background(), runFor(tc.plain, tc.tickets))
				fairMix(t, s, *started)
				offered, planned := 0.0, 0.0
				for m := 1; m <= 7; m++ {
					got, plan := float64(perMinute[m]), minutePlan(s, m)
					offered, planned = offered+got, planned+plan
					if math.Abs(got-plan) > 2 {
						t.Errorf("minute %d offered %.0f, plan %.0f (per minute %v)", m, got, plan, perMinute)
					}
				}
				if offered < 0.85*planned || offered > 1.1*planned {
					t.Errorf("window offered %.0f, plan %.0f", offered, planned)
				}
			})
		})
	}
}

// TestSmoothCorrelationStallDoesNotBurst (OSC-189 AC-4): runs block until
// T+20, so all 64 slots fill and the lane falls far behind plan; after the
// release no minute offers more than plan + 2.
func TestSmoothCorrelationStallDoesNotBurst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := scenario.AlertingDay3psV2()
		t0 := time.Now() // smoothRun's T0 (fake time stands still until it sleeps)
		release := 20 * time.Minute
		_, perMinute, _ := smoothRun(t, s, context.Background(), func(corr.Scenario) time.Duration {
			if d := t0.Add(release).Sub(time.Now()); d > 0 {
				return d
			}
			return 30 * time.Second
		})
		offered, planned := 0.0, 0.0
		for m := 1; m < 20; m++ {
			offered, planned = offered+float64(perMinute[m]), planned+minutePlan(s, m)
		}
		if planned-offered < 100 {
			t.Fatalf("no stall: offered %.0f of %.0f by T+20", offered, planned)
		}
		for m := 20; m <= 145; m++ {
			if got, plan := float64(perMinute[m]), minutePlan(s, m); got > plan+2 { // AC-4: at most 2 made up
				t.Errorf("minute %d offered %.0f after the stall, plan %.0f + 2", m, got, plan)
			}
		}
	})
}

// TestSmoothCorrelationNotRunBookkeeping (OSC-189, plan review F5): a lane
// cancelled during pass 0 records exactly the unstarted pass-0 set A
// documents as not run: those still in the window after out-of-order starts
// (cancelled at T+1m50s), and those never queued (cancelled at T+1m10s, one
// start in). Set A is the built-ins and the advanced examples here (16 unique
// document names, more than the window) and set B is empty, so every start is
// identifiable.
func TestSmoothCorrelationNotRunBookkeeping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cancel   time.Duration
		unqueued bool // the case needs set A documents never queued
	}{{"out-of-order", 110 * time.Second, false}, {"unqueued", 70 * time.Second, true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s := scenario.AlertingDay3psV2()
				var setA []scenario.CaseRef
				for _, ref := range s.Correlation.SetA {
					if ref.Level != "basic" { // basic examples repeat the built-in documents
						setA = append(setA, ref)
					}
				}
				s.Correlation.SetA, s.Correlation.SetB = setA, nil
				ctx, cancel := context.WithCancel(context.Background())
				go func() { time.Sleep(tc.cancel); cancel() }()
				r, _, started := smoothRun(t, s, ctx, func(corr.Scenario) time.Duration { return 30 * time.Second })
				pos, ref := map[string]int{}, map[string]string{}
				for i, x := range setA {
					doc, err := caseDoc(x)
					if err != nil {
						t.Fatal(err)
					}
					pos[doc.Name], ref[doc.Name] = i, "setA "+refName(x)
				}
				ran := map[string]bool{}
				outOfOrder, last := false, -1
				for _, name := range *started {
					i := pos[name]
					if ran[name] {
						t.Fatalf("set A %s started twice inside pass 0", name)
					}
					ran[name] = true
					outOfOrder = outOfOrder || i < last
					last = max(last, i)
				}
				var want []string
				for name := range pos {
					if !ran[name] {
						want = append(want, ref[name])
					}
				}
				if unqueued := len(ran) < len(setA)-smoothWindow; len(ran) == 0 || unqueued != tc.unqueued || !tc.unqueued && !outOfOrder {
					t.Fatalf("the case is not exercised: out of order %v, started %d of %d", outOfOrder, len(ran), len(setA))
				}
				sort.Strings(want)
				got := append([]string(nil), r.notRun...)
				sort.Strings(got)
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("not run %v, want %v", got, want)
				}
				if r.plannedCorr != len(setA) { // the phase plans pass-0 set A up front
					t.Errorf("plannedCorr %d, want %d", r.plannedCorr, len(setA))
				}
			})
		})
	}
}

// TestAlertingDay3psV2SmoothsOnALateClock (code review F1): with every wake
// 3 ms late, as on a real clock, no part of any minute goes unplanned: the
// lane total stays within 0.1% of plan and every minute within -3/+3.
func TestAlertingDay3psV2SmoothsOnALateClock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := scenario.AlertingDay3psV2()
		_, perMinute, _ := smoothRunOn(t, s, context.Background(), runFor(time.Minute, time.Minute), lateClock{})
		offered, planned := 0.0, 0.0
		for m := 1; m <= 145; m++ {
			got, plan := float64(perMinute[m]), minutePlan(s, m)
			offered, planned = offered+got, planned+plan
			if m >= 5 && m <= 144 && math.Abs(got-plan) > 3 {
				t.Errorf("minute %d offered %.0f, plan %.0f", m, got, plan)
			}
		}
		if math.Abs(offered-planned) > 0.001*planned {
			t.Errorf("late clock: total %.0f of %.0f", offered, planned)
		}
	})
}
