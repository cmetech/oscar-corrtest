package score

import (
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestOfferedPerMinuteCountsEveryLane(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	offers := []Offer{{At: t0.Add(time.Second), Lane: "baseline"}, {At: t0.Add(2 * time.Second), Lane: "resend"},
		{At: t0.Add(3 * time.Second), Lane: "baseline", Resolve: true}, {At: t0.Add(61 * time.Second), Lane: "fingerprint"},
		{At: t0.Add(-time.Second), Lane: "baseline"}, {At: t0.Add(2 * time.Minute), Lane: "baseline"}}
	got := OfferedPerMinute(t0, t0.Add(2*time.Minute), offers)
	if len(got.PerMinute) != 2 || got.PerMinute[0].Firing != 2 || got.PerMinute[0].Resolve != 1 || got.PerMinute[0].Lanes["resend"] != 1 ||
		got.PerMinute[1].Firing != 1 || got.PerMinute[1].Lanes["fingerprint"] != 1 {
		t.Fatalf("per minute %+v", got.PerMinute)
	}
	if got.PeakPerSecond != 2.0/60 || got.MeanPerSecond != 3.0/120 {
		t.Fatalf("peak %v mean %v", got.PeakPerSecond, got.MeanPerSecond)
	}
	rows := []domain.LaneRow{{Kind: "firing", SentAt: t0}, {Kind: "resolve", SentAt: t0}, {Kind: "firing"}}
	if o := LedgerOffers(rows, "baseline"); len(o) != 2 || o[0].Resolve || !o[1].Resolve {
		t.Fatalf("ledger offers %+v", o)
	}
}

func TestJudgeEscalation(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	after := 4 * time.Minute
	ep := EscalationEpisode{Alert: "E-1", StartsAt: t0, FirstAt: t0.Add(time.Second), End: t0.Add(15 * time.Minute)}
	rc := func(at time.Duration, sev string, starts time.Time) targetops.Receipt {
		return targetops.Receipt{ReceivedAt: t0.Add(at), Alerts: []targetops.SinkAlert{{AlertName: "E-1", Status: "firing",
			StartsAt: starts.Format(time.RFC3339Nano), Labels: map[string]string{"severity": sev}}}}
	}
	next := t0.Add(15 * time.Minute) // the next episode's receipts never count for this one
	for _, tc := range []struct {
		name     string
		receipts []targetops.Receipt
		want     Rate
		why      string
	}{
		{name: "rises once on time", receipts: []targetops.Receipt{rc(2*time.Second, "warning", t0), rc(4*time.Minute+20*time.Second, "critical", t0),
			rc(9*time.Minute, "critical", t0), rc(16*time.Minute, "warning", next)}, want: Rate{Expected: 1, Succeeded: 1}},
		{name: "never escalates", receipts: []targetops.Receipt{rc(2*time.Second, "warning", t0), rc(9*time.Minute, "warning", t0)},
			want: Rate{Expected: 1, Failed: 1}, why: "never reached critical"},
		{name: "too late", receipts: []targetops.Receipt{rc(2*time.Second, "warning", t0), rc(7*time.Minute, "critical", t0)},
			want: Rate{Expected: 1, Failed: 1}, why: "rose to critical"},
		{name: "too early", receipts: []targetops.Receipt{rc(2*time.Second, "warning", t0), rc(time.Minute, "critical", t0)},
			want: Rate{Expected: 1, Failed: 1}, why: "rose to critical"},
		{name: "critical from the first notification", receipts: []targetops.Receipt{rc(2*time.Second, "critical", t0)},
			want: Rate{Expected: 1, Failed: 1}, why: "rose to critical"},
		{name: "falls back", receipts: []targetops.Receipt{rc(2*time.Second, "warning", t0), rc(4*time.Minute+10*time.Second, "critical", t0),
			rc(9*time.Minute, "warning", t0)}, want: Rate{Expected: 1, Failed: 1}, why: "fell back"},
		{name: "no receipts", want: Rate{Expected: 1, Unknown: 1}, why: "no sink receipt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := JudgeEscalation([]EscalationEpisode{ep}, tc.receipts, "critical", after)
			got.Ratio = 0
			if got != tc.want || tc.why != "" && (len(why) != 1 || !strings.Contains(why[0], tc.why)) || tc.why == "" && len(why) != 0 {
				t.Fatalf("rate %+v why %v", got, why)
			}
		})
	}
	short := ep
	short.End = ep.FirstAt.Add(after + time.Minute)
	if got, _ := JudgeEscalation([]EscalationEpisode{short, {Alert: "E-2", StartsAt: t0, End: next}}, nil, "critical", after); got.Expected != 0 {
		t.Fatalf("an episode too short to escalate, or never accepted, is not judged: %+v", got)
	}
}

func TestTaskExecJudge(t *testing.T) {
	s := TaskExecStats{Runs: []TaskExecRun{{Status: "SUCCESS", Seconds: 10}, {Status: "SUCCESS", Seconds: 30}}}
	h, fails, incon := s.Judge(1, time.Minute)
	if len(h) != 2 || len(fails)+len(incon) != 0 || s.SuccessRatio != 1 || s.P95 != 30 {
		t.Fatalf("pass: %+v %v %v %+v", h, fails, incon, s)
	}
	s.Runs = append(s.Runs, TaskExecRun{Status: "timeout", Seconds: 240})
	if _, fails, _ = s.Judge(1, time.Minute); len(fails) != 1 || !strings.Contains(fails[0], "tasks.exec_success") {
		t.Fatalf("a timed-out run fails the success gate: %v", fails)
	}
	s.Runs = []TaskExecRun{{Status: "SUCCESS", Seconds: 90}}
	if _, fails, _ = s.Judge(1, time.Minute); len(fails) != 1 || !strings.Contains(fails[0], "tasks.exec_p95") {
		t.Fatalf("a slow run fails the latency gate: %v", fails)
	}
	if _, _, incon = (&TaskExecStats{NotRun: "no credential"}).Judge(1, time.Minute); len(incon) != 1 || !strings.Contains(incon[0], "no credential") {
		t.Fatalf("no run is inconclusive: %v", incon)
	}
}

func TestReportJudge(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	rows := []domain.LaneRow{{Kind: "firing", Identity: "A", Done: true, Status: 200, SentAt: t0.Add(10 * time.Second)}, {Kind: "firing", Identity: "B", Done: true, Status: 200, SentAt: t0.Add(5 * time.Minute)}}
	hist := []oscar.HistoryRecord{{AlertName: "A", CreatedAt: t0.Add(70 * time.Second)}, {AlertName: "B", CreatedAt: t0.Add(5*time.Minute + time.Second)}}
	s := ReportStats{Planned: 2, Runs: []ReportRun{{RunID: "r1", State: "artifact_ready", Verified: true, At: t0, End: t0.Add(time.Minute)}}}
	s.HistoryWithin(rows, hist)
	if r := s.Runs[0]; r.HistoryN != 1 || r.HistoryP95 != 60 {
		t.Fatalf("History within the report: %+v", r)
	}
	fails, incon := s.Judge(40 * time.Second)
	if len(fails) != 1 || !strings.Contains(fails[0], "History p95") || len(incon) != 1 || !strings.Contains(incon[0], "1 of 2") {
		t.Fatalf("fails %v incon %v", fails, incon)
	}
	s = ReportStats{Planned: 1, Runs: []ReportRun{{RunID: "r2", State: "artifact_failed", HistoryN: 1}}}
	if fails, _ = s.Judge(time.Minute); len(fails) != 1 || !strings.Contains(fails[0], "artifact_failed") {
		t.Fatalf("a failed report fails: %v", fails)
	}
	s.Runs[0].State, s.Runs[0].Err = "artifact_ready", "object storage: HTTP 403"
	if fails, incon = s.Judge(time.Minute); len(fails) != 0 || len(incon) != 1 || !strings.Contains(incon[0], "not read back") {
		t.Fatalf("an unverified artifact is inconclusive: %v %v", fails, incon)
	}
}

func TestOfferedBudget(t *testing.T) {
	b := scenario.BaselineSpec{Bursts: []scenario.BurstSpec{{At: 6 * time.Minute, For: time.Minute, Rate: 9}}}
	o := OfferedRate{}
	for m := 0; m < 13; m++ {
		o.PerMinute = append(o.PerMinute, OfferedMinute{Minute: m, Firing: 300})
	}
	o.PerMinute[0].Firing = 20 // warm-up: not under plan
	o.PerMinute[6].Firing = 600
	if got := o.OfferedBudget(b, 5, 10); got != nil {
		t.Fatalf("on plan: %v", got)
	}
	o.PerMinute[5].Firing, o.PerMinute[7].Firing, o.PerMinute[6].Firing = 330, 270, 560
	got := o.OfferedBudget(b, 5, 10)
	if len(got) != 2 || !strings.Contains(got[0], "over budget in 1 minutes: T+5 (5.50/s, budget 5/s)") ||
		!strings.Contains(got[1], "under plan in 2 minutes: T+6 (9.33/s, plan 10/s), T+7 (4.50/s, plan 5/s)") {
		t.Fatalf("off plan: %v", got)
	}
}

func TestTaskExecUnknownIsNotAFailure(t *testing.T) {
	s := TaskExecStats{Runs: []TaskExecRun{{Status: "SUCCESS", Seconds: 5}, {Status: "unknown", Err: "connection reset"}}}
	_, fails, incon := s.Judge(1, time.Minute)
	if len(fails) != 0 || len(incon) != 1 || !strings.Contains(incon[0], "1 of 2 runs with an unknown outcome") || s.SuccessRatio != 1 {
		t.Fatalf("fails %v incon %v stats %+v", fails, incon, s)
	}
}
