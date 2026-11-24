package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runtime"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// fakeUserOps is OSCAR's task and report API: every task succeeds, the report
// is approved and becomes ready on its third poll (20 s).
type fakeUserOps struct {
	mu                       sync.Mutex
	tasks, reports, verifies int
	polls                    map[string]int
	taskStatus               string
	// taskPolls keeps each run pending for that many result polls (the lane
	// polls every second); keys, pending and maxInFlight record the requests.
	taskPolls             int
	keys                  map[string]bool
	pending               map[string]int
	inFlight, maxInFlight int
}

func (f *fakeUserOps) RunTask(_ context.Context, _, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks++
	if f.keys == nil {
		f.keys, f.pending = map[string]bool{}, map[string]int{}
	}
	f.keys[key] = true
	id := fmt.Sprintf("celery-%d", f.tasks)
	f.pending[id] = f.taskPolls
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	return id, nil
}

func (f *fakeUserOps) TaskResult(_ context.Context, id string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.pending[id]; ok && n > 0 {
		f.pending[id]--
		return "STARTED", false, nil
	}
	if _, ok := f.pending[id]; ok {
		delete(f.pending, id)
		f.inFlight--
	}
	s := f.taskStatus
	if s == "" {
		s = "SUCCESS"
	}
	return s, s == "SUCCESS" || s == "FAILURE", nil
}

func (f *fakeUserOps) Report(context.Context, string) (targetops.ReportInfo, error) {
	return targetops.ReportInfo{ID: "rep-1", Key: "core-alarm-history-dump", SetupState: "approved"}, nil
}

func (f *fakeUserOps) RunReport(_ context.Context, _, id string, start, end time.Time) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports++
	if f.polls == nil {
		f.polls = map[string]int{}
	}
	f.polls[id] = 0
	return id, "artifact_admitted", nil
}

func (f *fakeUserOps) ReportRunState(_ context.Context, id string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls[id]++
	if f.polls[id] < 3 {
		return "artifact_running", "", nil
	}
	return "artifact_ready", "", nil
}

func (f *fakeUserOps) ReportArtifacts(context.Context, string) (targetops.ReportManifest, error) {
	var m targetops.ReportManifest
	m.Manifest.ID, m.Manifest.SHA256, m.Manifest.SizeBytes = "man-1", "abc", 120
	m.Items = append(m.Items, struct {
		ID        string `json:"id"`
		SizeBytes int64  `json:"size_bytes"`
	}{"part-1", 4096})
	return m, nil
}

func (f *fakeUserOps) VerifyManifest(context.Context, string, targetops.ReportManifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifies++
	return nil
}

func v4Harness(t *testing.T) (*harness, *fakeUserOps) {
	h := newHarness(t)
	s := time.Second
	// Long enough for the cycling correlation to make several passes before its
	// drain margin.
	h.s.Phases = []scenario.Phase{{Name: "preflight", Start: -10 * s}, {Name: "baseline", End: 420 * s}, {Name: "correlation", Start: 30 * s, End: 420 * s},
		{Name: "ticketing", Start: 60 * s, End: 420 * s}, {Name: "resend", Start: 90 * s, End: 400 * s}, {Name: "drain", Start: 420 * s, End: 450 * s}}
	h.s.ResendUntil, h.s.Baseline.TicketTo = 398*s, 400*s
	h.s.Paced = true
	h.s.Correlation.Cycle, h.s.Correlation.ParentTickets = true, true
	h.s.Correlation.Rate, h.s.Correlation.Parallel = 5, 3 // fast enough that 3 s runs overlap
	h.s.Fingerprint = &scenario.FingerprintSpec{At: 10 * s, Repeats: 3, Converge: 5 * s, Verify: 15 * s, Every: 20 * s}
	h.s.Resend = append(h.s.Resend, scenario.PolicySpec{Key: "R5", Action: "notification", Mode: "time", Interval: 4 * s, Cohort: 1,
		Notifier: "CAMPAIGN_SINK", Escalation: &scenario.EscalationSpec{After: 10 * s, TargetSeverity: "critical"}, Episode: 9 * s})
	h.s.Tasks = &scenario.TaskExecSpec{Job: "monitor:check_connection:homelab", At: 5 * s, Every: 20 * s, Timeout: 10 * s}
	h.s.Report = &scenario.ReportSpec{Key: "core-alarm-history-dump", At: []time.Duration{40 * s}, Timeout: 30 * s}
	h.s.Limits.TaskSuccess, h.s.Limits.TaskP95 = 1, 10*s
	h.oscar.s = h.s
	ops := &fakeUserOps{}
	h.deps.UserOps = ops
	h.deps.nativeSender = lane.NewAlertmanagerSenderClient(h.oscar.srv.URL+ingestPath, h.oscar.client)
	return h, ops
}

// TestV4LanesRunConcurrently: correlation cycles set A and set B and tickets
// the synthetic parents, the fingerprint policy is verified every round, the
// escalation cohort rotates episodes, tasks run every Every and the report is
// read back, and the scorecard carries the offered rate of every lane.
func TestV4LanesRunConcurrently(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, ops := v4Harness(t)
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c := h.campaign()
		var card score.Scorecard
		if err := json.Unmarshal(c.Scorecard, &card); err != nil {
			t.Fatal(err)
		}
		for _, r := range card.Reasons {
			for _, lane := range []string{"correlation", "tickets", "fingerprint", "task", "report", "not run"} {
				if strings.Contains(r, lane) {
					t.Errorf("unexpected %s reason: %s", lane, r)
				}
			}
		}
		if c.Cleanup != domain.CleanupClean {
			t.Errorf("cleanup %s", c.Cleanup)
		}
		// Set A (2) and set B (1) cycle: more runs than one pass, every one scored.
		if card.Correlation.Expected <= 3 || card.Correlation.Succeeded != card.Correlation.Expected {
			t.Errorf("correlation %+v", card.Correlation)
		}
		if m := h.oscar.corrMaxInFlight.Load(); m < 2 || m > 3 {
			t.Errorf("correlation runs in flight at once: %d, want 2-3 (Parallel 3)", m)
		}
		h.oscar.mu.Lock()
		parents := 0
		for _, inc := range h.oscar.corrInc {
			if strings.HasSuffix(inc.ShortDescription, "correlation parent") {
				parents++
			}
		}
		h.oscar.mu.Unlock()
		if parents < 2 || card.Tickets.Duplicate+card.Tickets.Failed+card.Tickets.Unknown != 0 {
			t.Errorf("parent tickets %d, tickets %+v", parents, card.Tickets)
		}
		if fp := card.Fingerprint; fp == nil || len(fp.Rounds) < 2 || !fp.Reverted {
			t.Errorf("fingerprint rounds %+v", fp)
		}
		if te := card.TaskExec; te == nil || len(te.Runs) < 3 || te.Succeeded != len(te.Runs) || ops.tasks != len(te.Runs) {
			t.Errorf("task executions %+v (requested %d)", te, ops.tasks)
		}
		if rp := card.Reports; rp == nil || len(rp.Runs) != 1 || !rp.Runs[0].Verified || rp.Runs[0].State != "artifact_ready" || ops.verifies != 1 {
			t.Errorf("reports %+v", rp)
		}
		lanes := map[string]int{}
		for _, m := range card.Offered.PerMinute {
			for l, n := range m.Lanes {
				lanes[l] += n
			}
		}
		// Paced: correlation offers at most Rate over its window (plus one document).
		if max := 5*float64(h.s.Phase("drain").Start-h.s.Phase("correlation").Start)/float64(time.Second) + 10; float64(lanes["correlation"]) > max || lanes["correlation"] < 100 {
			t.Errorf("correlation offered %d alerts, want paced near Rate (at most %.0f)", lanes["correlation"], max)
		}
		for _, l := range []string{"baseline", "resend", "fingerprint", "correlation"} {
			if lanes[l] == 0 {
				t.Errorf("offered rate lacks lane %s: %+v", l, lanes)
			}
		}
		// The escalation cohort opened several episodes (each resolved before
		// the next); none lasts long enough here to judge, which is reported.
		if card.Escalation == nil || card.Escalation.Expected != 0 || !hasReason(card.Reasons, "escalation: no episode long enough") {
			t.Errorf("escalation %+v reasons %v", card.Escalation, card.Reasons)
		}
		h.oscar.mu.Lock()
		episodes := map[string]bool{} // each episode is its own alert, so Alertmanager cannot merge it into the last
		for _, row := range h.oscar.history {
			if strings.Contains(row.AlertName, "native-escalate") {
				episodes[row.AlertName] = true
				if row.Status != "resolved" {
					t.Errorf("escalation episode %s@%s left firing", row.AlertName, row.StartsAt)
				}
			}
		}
		h.oscar.mu.Unlock()
		if len(episodes) < 2 || !episodes["Campaign-"+h.id+"-native-escalate-1-e2"] {
			t.Errorf("escalation episodes were not rotated into their own alerts: %v", episodes)
		}
	})
}

// TestV4EscalationScoredPerEpisode: R5 goes through the native route; each
// episode long enough to escalate is judged from its sink receipts. A target
// that escalates on time passes every episode; one that never escalates FAILs.
func TestV4EscalationScoredPerEpisode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		escalate time.Duration
	}{{"escalates", 10 * time.Second}, {"never escalates", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, _ := v4Harness(t)
				for i := range h.s.Resend {
					if h.s.Resend[i].Key == "R5" {
						h.s.Resend[i].Episode = 150 * time.Second // > After + escalationLate: judged
					}
				}
				h.oscar.s = h.s
				h.oscar.escalate = tc.escalate
				if err := h.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				var card score.Scorecard
				if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil {
					t.Fatal(err)
				}
				e := card.Escalation
				if e == nil || e.Expected < 2 {
					t.Fatalf("escalation %+v reasons %v", e, card.Reasons)
				}
				if tc.escalate > 0 && (e.Succeeded != e.Expected || hasReason(card.Reasons, "escalation")) {
					t.Fatalf("on-time escalations: %+v %v", e, card.Reasons)
				}
				if tc.escalate == 0 && (e.Failed != e.Expected || card.Verdict != domain.VerdictFail || !hasReason(card.Reasons, "escalation:")) {
					t.Fatalf("missing escalations must FAIL: %+v %s %v", e, card.Verdict, card.Reasons)
				}
			})
		})
	}
}

// TestV4SkipReportLane: --skip-lane report switches the report lane off for
// the run: no report is requested, B13 is advisory and skipped, the scorecard
// says "skipped (by option)" (never a pass) and the run is not comparable.
func TestV4SkipReportLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, ops := v4Harness(t)
		pctx := context.WithValue(context.Background(), skipLanesKey{}, []string{"report"})
		var b13 *precheck.Result
		for _, r := range v4Prechecks(pctx, h.deps, h.s) {
			if r.ID == "B13" {
				b13 = &r
			}
		}
		if b13 == nil || b13.Tier != precheck.Advisory || b13.Status != precheck.Skipped {
			t.Fatalf("B13 with the report lane skipped: %+v", b13)
		}
		err := New(h.deps, h.clock).Run(context.Background(), h.id, runtime.CampaignOptions{Scenario: h.s, Trigger: "cli", RunBy: "agent", SkipLanes: []string{"report"}})
		if err != nil {
			t.Fatal(err)
		}
		var card score.Scorecard
		if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil {
			t.Fatal(err)
		}
		if ops.reports != 0 || card.Reports == nil || card.Reports.Skipped != "skipped (by option)" || len(card.Reports.Runs) != 0 {
			t.Fatalf("report lane: requested %d, section %+v", ops.reports, card.Reports)
		}
		if len(card.SkippedLanes) != 1 || card.SkippedLanes[0] != "report: skipped (by option)" || card.Comparable || hasReason(card.Reasons, "alert report") {
			t.Fatalf("skipped lanes %v comparable %t reasons %v", card.SkippedLanes, card.Comparable, card.Reasons)
		}
		if card.TaskExec == nil || len(card.TaskExec.Runs) == 0 {
			t.Fatal("the other v4 lanes still run")
		}
	})
}

// linkStore accepts the run links a correlation run writes; nothing else is called.
type linkStore struct{ Store }

func (linkStore) LinkCampaignRun(context.Context, string, string, string) error { return nil }

// TestV4SmokeCorrelationHoldsPlanWithSlowRuns (OSC-155 AC-1): with
// correlation runs taking up to 90 s (OSCAR slow), and a ticketed set B run
// using its whole 150 s ticket observation on top, alerting-day-smoke@v4
// still offers its planned correlation rate within 10% over its cycling
// window (T+1 to drain - cycleMargin) and in each spike minute.
//
// Each document offers its alerts at once (flood 10, threshold 6), so a
// spike minute passes when three documents start in it. That phase is fixed
// by the lane starting at T+1 with no credit; a change to document size,
// creditCap or the start time can move a spike minute by one document.
func TestV4SmokeCorrelationHoldsPlanWithSlowRuns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		plain, tickets time.Duration // run time of an unticketed / ticketed document
	}{{"30s", 30 * time.Second, 30 * time.Second}, {"60s", time.Minute, time.Minute}, {"90s", 90 * time.Second, 90 * time.Second},
		{"90s-ticketed-240s", 90 * time.Second, 90*time.Second + setBTicketObservation}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s := scenario.AlertingDaySmokeV4()
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
				time.Sleep(s.Phase("correlation").Start) // the phase runner starts the lane here
				r.cycleCorrelation(context.Background())
				perMinute := map[int]int{}
				for _, o := range r.offers {
					perMinute[int(o.At.Sub(r.t0)/time.Minute)]++
				}
				// Plan over the cycling window: Rate per minute, SpikeRate in a
				// spike minute, from the correlation start to the last start.
				spike := map[int]bool{}
				for _, b := range s.Baseline.Bursts {
					for m := b.At; m < b.At+b.For; m += time.Minute {
						spike[int(m/time.Minute)] = true
					}
				}
				within := func(got, plan float64) bool { return got >= 0.9*plan && got <= 1.1*plan }
				var offered, planned float64
				checked := 0
				for m := int(s.Phase("correlation").Start / time.Minute); m < int((s.Phase("drain").Start-cycleMargin)/time.Minute); m++ {
					plan := s.Correlation.Rate * 60
					if spike[m] {
						plan, checked = s.Correlation.SpikeRate*60, checked+1
						if !within(float64(perMinute[m]), plan) {
							t.Errorf("spike minute %d offered %d, plan %.0f (per minute %v)", m, perMinute[m], plan, perMinute)
						}
					}
					offered, planned = offered+float64(perMinute[m]), planned+plan
				}
				if !within(offered, planned) {
					t.Errorf("cycling window offered %.0f, plan %.0f (per minute %v)", offered, planned, perMinute)
				}
				if checked != len(spike) {
					t.Errorf("only %d of spike minutes %v fall inside the cycling window", checked, spike)
				}
			})
		})
	}
}

// TestV4TaskLaneOverlapsAndBursts (OSC-156 AC-1, AC-4): a request every
// Every does not wait for the previous run, every 5th request issues 3
// concurrent runs, each request carries its own idempotency key, every
// planned run is counted on the scorecard and none is an offered alert.
func TestV4TaskLaneOverlapsAndBursts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, ops := v4Harness(t)
		s := time.Second
		h.s.Tasks = &scenario.TaskExecSpec{Job: "monitor:check_connection:homelab", At: 5 * s, Every: 20 * s, Timeout: 40 * s, BurstEvery: 5, Burst: 3}
		h.s.Limits.TaskP95 = time.Minute
		h.oscar.s = h.s
		ops.taskPolls = 30 // about 30 s, longer than Every: runs overlap only if the lane does not wait
		if err := h.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		var card score.Scorecard
		if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil {
			t.Fatal(err)
		}
		planned := 0
		for _, r := range h.s.Tasks.Requests(h.s.Phase("drain").Start) {
			planned += r.Runs
		}
		te := card.TaskExec
		if te == nil || planned < 20 || te.Planned != planned || len(te.Runs) != planned || te.Succeeded != planned || ops.tasks != planned {
			t.Fatalf("task executions %+v: planned %d, requested %d", te, planned, ops.tasks)
		}
		if len(ops.keys) != planned {
			t.Errorf("%d distinct idempotency keys for %d requests", len(ops.keys), planned)
		}
		if ops.maxInFlight < 4 { // a 3-run burst on top of the previous, still pending, run
			t.Errorf("at most %d runs in flight: the lane waited for runs or did not burst", ops.maxInFlight)
		}
		for _, m := range card.Offered.PerMinute {
			for l := range m.Lanes {
				if strings.Contains(l, "task") {
					t.Errorf("task requests counted as offered alerts: minute %d lane %s", m.Minute, l)
				}
			}
		}
		if hasReason(card.Reasons, "task") {
			t.Errorf("reasons %v", card.Reasons)
		}
	})
}

// TestV4HomelabHealthGate (OSC-156 AC-2): scheduled homelab job runs during
// load are judged against the 30 minutes before T0.
func TestV4HomelabHealthGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during string
		fail   bool
	}{{"steady", "SUCCESS", false}, {"failing under load", "FAILURE", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, _ := v4Harness(t)
				h.s.Limits.HomelabFailRise = 0.05
				h.oscar.s = h.s
				jobs := []string{"monitor:check_connection:homelab", "monitor:check_filesystem_space:homelab"}
				h.deps.TaskRuns = func(_ context.Context, start, end time.Time) ([]targetops.TaskRun, error) {
					var out []targetops.TaskRun
					for _, j := range jobs {
						if end.Sub(start) == time.Hour { // before T0
							out = append(out, targetops.TaskRun{Job: j, Status: "SUCCESS", StartedAt: end.Add(-10 * time.Minute), Scheduled: true},
								targetops.TaskRun{Job: j, Status: "SUCCESS", StartedAt: end.Add(-5 * time.Minute), Scheduled: true})
							continue
						}
						out = append(out, targetops.TaskRun{Job: j, Status: "SUCCESS", StartedAt: start.Add(time.Minute), Scheduled: true},
							targetops.TaskRun{Job: j, Status: tc.during, StartedAt: start.Add(5 * time.Minute), Scheduled: true})
					}
					return out, nil
				}
				if err := h.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				var card score.Scorecard
				if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil {
					t.Fatal(err)
				}
				hh := card.HomelabHealth
				if hh == nil || hh.BaselineRuns != 4 || hh.LoadRuns != 4 {
					t.Fatalf("homelab health %+v", hh)
				}
				if got := hasReason(card.Reasons, "homelab"); got != tc.fail || tc.fail && card.Verdict != domain.VerdictFail {
					t.Fatalf("verdict %s reasons %v", card.Verdict, card.Reasons)
				}
			})
		})
	}
}

// TestV4HomelabWindowEndsAtEarlyStop (OSC-156 AC-2): on an operator stop the
// homelab load window ends where the lane stopped, so job classes are not
// judged stopped for the minutes the run never reached.
func TestV4HomelabWindowEndsAtEarlyStop(t *testing.T) {
	t.Parallel()
	h, ops := v4Harness(t)
	h.s.Limits.HomelabFailRise = 0.05
	h.oscar.s = h.s
	h.deps.TaskRuns = func(_ context.Context, start, end time.Time) ([]targetops.TaskRun, error) {
		job := "monitor:check_connection:homelab"
		if end.Sub(start) == time.Hour { // a 2-minute cadence before T0: 4 minutes without a run stops it
			return []targetops.TaskRun{{Job: job, Status: "SUCCESS", StartedAt: end.Add(-4 * time.Minute), Scheduled: true},
				{Job: job, Status: "SUCCESS", StartedAt: end.Add(-2 * time.Minute), Scheduled: true}}, nil
		}
		return []targetops.TaskRun{{Job: job, Status: "SUCCESS", StartedAt: start.Add(time.Second), Scheduled: true}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	waitFor(t, "first task run", func() bool { ops.mu.Lock(); defer ops.mu.Unlock(); return ops.tasks > 0 })
	if err := h.db.TransitionCampaign(context.Background(), h.id, domain.CampaignStopping, h.clock.Now(), "stop requested"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var card score.Scorecard
	if err := json.Unmarshal(h.campaign().Scorecard, &card); err != nil {
		t.Fatal(err)
	}
	if hh := card.HomelabHealth; hh == nil || len(hh.Stopped) != 0 || len(hh.NotJudged) != 1 || hasReason(card.Reasons, "homelab jobs") {
		t.Fatalf("homelab health %+v reasons %v", hh, card.Reasons)
	}
}
