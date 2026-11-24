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
	if got := o.OfferedBudget(b, 5, 10, 0); got != nil {
		t.Fatalf("on plan: %v", got)
	}
	o.PerMinute[5].Firing, o.PerMinute[7].Firing, o.PerMinute[6].Firing = 330, 270, 560
	got := o.OfferedBudget(b, 5, 10, 0)
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

// TestOfferedBudgetWaitsForFullLoad: minutes before the last paced lane
// reached full load (a late policy activation) are not under plan.
func TestOfferedBudgetWaitsForFullLoad(t *testing.T) {
	o := OfferedRate{}
	for m := 0; m < 20; m++ {
		o.PerMinute = append(o.PerMinute, OfferedMinute{Minute: m, Firing: 300})
	}
	o.PerMinute[6].Firing = 270 // cohorts not yet posting
	if got := o.OfferedBudget(scenario.BaselineSpec{}, 5, 10, 6*time.Minute+30*time.Second); got != nil {
		t.Fatalf("before full load: %v", got)
	}
	if got := o.OfferedBudget(scenario.BaselineSpec{}, 5, 10, 0); len(got) != 1 || !strings.Contains(got[0], "T+6") {
		t.Fatalf("at full load: %v", got)
	}
}

// TestTaskExecPlanned (OSC-156 AC-1, AC-4): the lane's planned runs are on the
// scorecard; fewer recorded runs than planned is missing evidence.
func TestTaskExecPlanned(t *testing.T) {
	s := TaskExecStats{Planned: 3, Runs: []TaskExecRun{{Status: "SUCCESS", Seconds: 5}, {Status: "SUCCESS", Seconds: 6}}}
	_, fails, incon := s.Judge(1, time.Minute)
	if len(fails) != 0 || len(incon) != 1 || !strings.Contains(incon[0], "2 of 3 planned") {
		t.Fatalf("fails %v incon %v", fails, incon)
	}
	s.Runs = append(s.Runs, TaskExecRun{Status: "SUCCESS", Seconds: 7})
	if _, fails, incon = s.Judge(1, time.Minute); len(fails)+len(incon) != 0 {
		t.Fatalf("every planned run recorded: %v %v", fails, incon)
	}
}

// TestJudgeHomelab (OSC-156 AC-2): the scheduled homelab job failure rate
// during load against the 30 minutes before T0.
func TestJudgeHomelab(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	loadEnd := t0.Add(150 * time.Minute)
	const conn, fs = "monitor:check_connection:homelab", "monitor:check_filesystem_space:homelab"
	run := func(job, status string, at time.Duration) targetops.TaskRun {
		return targetops.TaskRun{Job: job, Status: status, StartedAt: t0.Add(at), Scheduled: true}
	}
	// Baseline: 10 scheduled runs every 5 minutes, 1 failed (10%). Runs older
	// than 30 minutes and unscheduled (user or unattributed) runs are not counted.
	var pre []targetops.TaskRun
	for i := range 5 {
		at := -time.Duration(5*i+1) * time.Minute
		pre = append(pre, run(conn, "SUCCESS", at), run(fs, "SUCCESS", at))
	}
	pre[0].Status = "FAILURE"
	pre = append(pre, run(conn, "FAILURE", -40*time.Minute), targetops.TaskRun{Job: conn, Status: "FAILURE", StartedAt: t0.Add(-3 * time.Minute)})
	load := func(failEvery int, status string) []targetops.TaskRun {
		var out []targetops.TaskRun
		for i := range 30 {
			at := time.Duration(5*i+1) * time.Minute
			c, f := run(conn, "SUCCESS", at), run(fs, "SUCCESS", at)
			if failEvery > 0 && i%failEvery == 0 {
				c.Status = status
			}
			out = append(out, c, f)
		}
		// the lane's own runs (unscheduled, even without attribution) and runs in the drain are not load evidence
		return append(out, targetops.TaskRun{Job: conn, Status: "FAILURE", StartedAt: t0.Add(time.Minute)}, run(conn, "FAILURE", 151*time.Minute))
	}
	judge := func(pre, during []targetops.TaskRun) (HomelabHealth, []Headline, []string, []string) {
		return JudgeHomelab(pre, during, true, "", t0, loadEnd, 0.05)
	}

	h, heads, fails, incon := judge(pre, load(5, "FAILURE")) // 6 of 60 = 10%: no rise
	if len(fails)+len(incon) != 0 || h.BaselineRuns != 10 || h.BaselineFailed != 1 || h.LoadRuns != 60 || h.LoadFailed != 6 || len(heads) != 1 || !heads[0].Pass {
		t.Fatalf("no rise: %+v %v %v %v", h, heads, fails, incon)
	}
	for _, st := range []string{"FAILURE", "EXPIRED", "UNKNOWN", "REVOKED", "STARTED"} { // 10 of 60 = 16.7%
		if h, _, fails, _ = judge(pre, load(3, st)); len(fails) != 1 || !strings.Contains(fails[0], "homelab.fail_rise") {
			t.Fatalf("%s rows are failures; a rise above 5 points fails: %+v %v", st, h, fails)
		}
	}
	if _, _, fails, _ = judge(pre, load(4, "FAILURE")); len(fails) != 0 { // 8 of 60 = 13.3%: 3.3 points
		t.Fatalf("a rise within 5 points passes: %v", fails)
	}
	// Exactly 5 points is not more than 5, although 11/20 - 1/2 computes to
	// 0.050000000000000044 in float64.
	p2 := []targetops.TaskRun{run(conn, "FAILURE", -29*time.Minute), run(conn, "SUCCESS", -time.Minute)}
	var l20 []targetops.TaskRun
	for i := range 20 {
		l20 = append(l20, run(conn, map[bool]string{true: "FAILURE", false: "SUCCESS"}[i < 11], time.Duration(i)*7*time.Minute))
	}
	if _, _, fails, _ = judge(p2, l20); len(fails) != 0 {
		t.Fatalf("an exact 5-point rise passes: %v", fails)
	}

	// One baseline run fanned out to 11 hosts (rows seconds apart, or at the
	// same instant) is one run: its cadence is the whole baseline.
	for _, spread := range []time.Duration{time.Second, 0} {
		var batch []targetops.TaskRun
		for i := range 11 {
			batch = append(batch, run(fs, "SUCCESS", -7*time.Minute+time.Duration(i)*spread))
		}
		var healthy, quiet []targetops.TaskRun
		for i := range 10 {
			healthy = append(healthy, run(fs, "SUCCESS", time.Duration(15*i+8)*time.Minute))
			if i < 5 {
				quiet = append(quiet, run(fs, "SUCCESS", time.Duration(15*i+8)*time.Minute))
			}
		}
		if h, _, fails, _ = judge(batch, healthy); len(fails) != 0 || len(h.Stopped) != 0 {
			t.Fatalf("spread %s: one fanned-out baseline run is not a seconds cadence: %+v %v", spread, h, fails)
		}
		if h, _, fails, _ = judge(batch, quiet); len(h.Stopped) != 1 { // no run after T+68: 82 minutes > 60
			t.Fatalf("spread %s: still stopped past twice the baseline: %+v %v", spread, h, fails)
		}
	}

	// A class that stops for longer than twice its baseline cadence stopped,
	// at the end of load or in the middle.
	keep := func(drop func(targetops.TaskRun) bool) []targetops.TaskRun {
		var out []targetops.TaskRun
		for _, r := range load(0, "") {
			if !drop(r) {
				out = append(out, r)
			}
		}
		return out
	}
	tail := keep(func(r targetops.TaskRun) bool { return r.Job == fs && !r.StartedAt.Before(t0.Add(130*time.Minute)) })
	stall := keep(func(r targetops.TaskRun) bool {
		return r.Job == fs && r.StartedAt.After(t0.Add(30*time.Minute)) && r.StartedAt.Before(t0.Add(110*time.Minute))
	})
	for name, during := range map[string][]targetops.TaskRun{"tail": tail, "mid-load stall": stall} {
		if h, _, fails, _ = judge(pre, during); len(fails) != 1 || !strings.Contains(fails[0], fs) || len(h.Stopped) != 1 {
			t.Fatalf("%s: a stopped job class fails: %+v %v", name, h, fails)
		}
	}
	if _, _, fails, _ = judge(pre, nil); len(fails) == 0 {
		t.Fatal("no scheduled run during load: every class stopped")
	}

	// A 15-minute class on a 12-minute window cannot be judged stopped.
	short := t0.Add(28 * time.Minute)
	slowPre := []targetops.TaskRun{run(fs, "SUCCESS", -15*time.Minute), run(fs, "SUCCESS", -30*time.Second), run(conn, "SUCCESS", -5*time.Minute), run(conn, "SUCCESS", -10*time.Second)}
	slowLoad := []targetops.TaskRun{run(conn, "SUCCESS", 16*time.Minute), run(conn, "SUCCESS", 21*time.Minute), run(conn, "SUCCESS", 26*time.Minute)}
	if h, _, fails, incon = JudgeHomelab(slowPre, slowLoad, true, "", t0.Add(16*time.Minute), short, 0.05); len(fails)+len(incon) != 0 || len(h.NotJudged) != 1 || h.NotJudged[0] != fs {
		t.Fatalf("a class slower than the window is not judged: %+v %v %v", h, fails, incon)
	}

	// Missing evidence is never a pass.
	if _, heads, fails, incon = judge(nil, load(0, "")); len(fails) != 0 || len(heads) != 0 || len(incon) != 1 || !strings.Contains(incon[0], "baseline") {
		t.Fatalf("no baseline: %v %v %v", heads, fails, incon)
	}
	if _, _, fails, incon = JudgeHomelab(nil, nil, false, "task history: HTTP 502", t0, loadEnd, 0.05); len(fails) != 0 || len(incon) != 1 || !strings.Contains(incon[0], "HTTP 502") {
		t.Fatalf("not collected: %v %v", fails, incon)
	}
}
