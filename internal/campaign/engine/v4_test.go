package engine

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// fakeUserOps is OSCAR's task and report API: every task succeeds, the report
// is approved and becomes ready on its third poll (20 s).
type fakeUserOps struct {
	mu                       sync.Mutex
	tasks, reports, verifies int
	polls                    map[string]int
	taskStatus               string
}

func (f *fakeUserOps) RunTask(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks++
	return "celery-" + string(rune('a'+f.tasks)), nil
}

func (f *fakeUserOps) TaskResult(context.Context, string) (string, bool, error) {
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
		starts := map[time.Time]bool{}
		for _, row := range h.oscar.history {
			if strings.Contains(row.AlertName, "native-escalate") {
				starts[row.StartsAt] = true
				if row.Status != "resolved" {
					t.Errorf("escalation episode %s@%s left firing", row.AlertName, row.StartsAt)
				}
			}
		}
		h.oscar.mu.Unlock()
		if len(starts) < 2 {
			t.Errorf("escalation episodes were not rotated: %d History episodes", len(starts))
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
