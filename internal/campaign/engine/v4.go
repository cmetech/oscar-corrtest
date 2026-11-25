package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// UserOps is the v4 task and report surface, called as a user account
// (targetops.UserAPI with the first front-office user's key).
type UserOps interface {
	RunTask(ctx context.Context, job, idempotencyKey string) (string, error)
	TaskResult(ctx context.Context, id string) (status string, done bool, err error)
	Report(ctx context.Context, key string) (targetops.ReportInfo, error)
	RunReport(ctx context.Context, id, requestID string, start, end time.Time) (runID, state string, err error)
	ReportRunState(ctx context.Context, runID string) (state, errorCode string, err error)
	ReportArtifacts(ctx context.Context, runID string) (targetops.ReportManifest, error)
	VerifyManifest(ctx context.Context, runID string, m targetops.ReportManifest) error
}

// laneSkipped is how a lane switched off by --skip-lane reads in the scorecard.
const laneSkipped = "skipped (by option)"

// Poll cadences of the v4 lanes (TaskResult long-polls 5 s itself).
const (
	taskPoll   = time.Second
	reportPoll = 10 * time.Second
)

// offer records one alert post of a controller lane for the per-minute
// offered rate (the worker lane's come from its ledger).
func (r *run) offer(lane string, resolve bool, n int) {
	at := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for range n {
		r.offers = append(r.offers, score.Offer{At: at, Lane: lane, Resolve: resolve})
	}
}

// docAlerts is what one correlation run offers: its firing source alerts plus
// the runtime's label-survival probe alert (diagnose injects one per run).
func docAlerts(doc corr.Scenario) int { return firingEvents(doc) + 1 }

// firingEvents counts a correlation document's firing source alerts (the
// synthetic parent is OSCAR's, not offered).
func firingEvents(doc corr.Scenario) int {
	n := 0
	for _, c := range doc.Cases {
		if len(c.Events) == 0 { // the compiler expands Repeat only without Events
			n += c.Repeat
		}
		for _, e := range c.Events {
			if !strings.EqualFold(e.Status, "resolved") {
				n++
			}
		}
	}
	return n
}

// taskPhase runs the homelab task through OSCAR's task API on the spec's
// schedule until the drain: each request starts its runs at once, without
// waiting for earlier runs, and the phase returns when every run has ended.
func (r *run) taskPhase(ctx context.Context) {
	spec := r.s.Tasks
	if spec == nil {
		return
	}
	note := func(why string) {
		r.mu.Lock()
		r.tasks.NotRun = why
		r.mu.Unlock()
	}
	reqs := spec.Requests(r.s.Phase("drain").Start)
	r.mu.Lock()
	r.tasks.Job = spec.Job
	for _, q := range reqs {
		r.tasks.Planned += q.Runs
	}
	r.mu.Unlock()
	if r.e.d.UserOps == nil {
		note("no front-office user credential for the task API")
		return
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, q := range reqs {
		if !r.e.clock.SleepUntil(ctx, r.t0.Add(q.At)) {
			return
		}
		for range q.Runs {
			wg.Go(func() {
				run := r.execTask(ctx, spec.Job, spec.Timeout)
				if ctx.Err() != nil && run.Status == "" {
					return // stopped mid-run: not evidence either way
				}
				r.mu.Lock()
				r.tasks.Runs = append(r.tasks.Runs, run)
				r.mu.Unlock()
			})
		}
	}
}

// execTask requests one run with its own idempotency key and polls it to a
// terminal state within timeout.
func (r *run) execTask(ctx context.Context, job string, timeout time.Duration) score.TaskExecRun {
	start := r.now()
	out := score.TaskExecRun{At: start, Key: newUUID()}
	id, err := r.e.d.UserOps.RunTask(ctx, job, out.Key)
	if err != nil {
		out.Status, out.Err = "unknown", err.Error() // transport error: the run may or may not exist
		var se *targetops.StatusError
		if errors.As(err, &se) {
			out.Status = "not_started" // OSCAR refused it
		}
		if ctx.Err() != nil {
			out.Status = ""
		}
		return out
	}
	out.ID = id
	for deadline := start.Add(timeout); ; {
		status, done, err := r.e.d.UserOps.TaskResult(ctx, id)
		if done {
			out.Status, out.Seconds = status, r.now().Sub(start).Seconds()
			return out
		}
		var se *targetops.StatusError
		unread := err != nil && !errors.As(err, &se)
		if err != nil {
			out.Err = err.Error()
		}
		if !r.now().Before(deadline) {
			out.Status, out.Seconds = "timeout", r.now().Sub(start).Seconds()
			if unread {
				out.Status = "unknown" // the result could not be read: not proof of a slow task
			}
			return out
		}
		if !r.e.clock.SleepUntil(ctx, r.now().Add(taskPoll)) {
			return out
		}
	}
}

// reportPhase requests the alert report at each At and verifies its artifact.
func (r *run) reportPhase(ctx context.Context) {
	spec := r.s.Report
	if spec == nil {
		return
	}
	if slices.Contains(r.opts.SkipLanes, "report") {
		r.mu.Lock()
		r.reports.Skipped = laneSkipped
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.reports.Planned = len(spec.At)
	r.mu.Unlock()
	if r.e.d.UserOps == nil {
		return // Judge: 0 of n requested
	}
	for _, at := range spec.At {
		if !r.e.clock.SleepUntil(ctx, r.t0.Add(at)) {
			return
		}
		rr := r.runReport(ctx, spec)
		r.mu.Lock()
		r.reports.Runs = append(r.reports.Runs, rr)
		r.mu.Unlock()
		r.event(context.WithoutCancel(ctx), "campaign.report", "info", fmt.Sprintf("alert report %s: %s in %.0fs", rr.RunID, rr.State, rr.Seconds), rr)
	}
}

// runReport requests one manual artifact run over [T0, now) and waits for a
// terminal state within Timeout, then reads the artifact back.
func (r *run) runReport(ctx context.Context, spec *scenario.ReportSpec) (rr score.ReportRun) {
	start := r.now()
	rr = score.ReportRun{At: start, RunID: newUUID()}
	defer func() { rr.End = r.now() }() // named result: the History window ends when the run did
	info, err := r.e.d.UserOps.Report(ctx, spec.Key)
	if err != nil {
		rr.Err = "find report: " + err.Error()
		return rr
	}
	runID, _, err := r.e.d.UserOps.RunReport(ctx, info.ID, rr.RunID, r.t0.Truncate(time.Minute), start.Truncate(time.Minute))
	if err != nil {
		rr.Err = "request: " + err.Error()
		var se *targetops.StatusError
		if errors.As(err, &se) {
			rr.State = "refused"
		}
		return rr
	}
	rr.RunID = runID
	for deadline := start.Add(spec.Timeout); ; {
		state, code, err := r.e.d.UserOps.ReportRunState(ctx, rr.RunID)
		rr.Seconds = r.now().Sub(start).Seconds()
		if err == nil {
			rr.State, rr.Err = state, code
		}
		switch {
		case err == nil && reportTerminal(state):
			if state == "artifact_ready" {
				m, err := r.e.d.UserOps.ReportArtifacts(ctx, rr.RunID)
				if err == nil {
					rr.ManifestBytes, rr.Parts = m.Manifest.SizeBytes, len(m.Items)
					err = r.e.d.UserOps.VerifyManifest(ctx, rr.RunID, m)
				}
				rr.Verified = err == nil
				if err != nil {
					rr.Err = err.Error()
				}
			}
			return rr
		case !r.now().Before(deadline):
			rr.State = "timeout (last " + rr.State + ")"
			return rr
		}
		if !r.e.clock.SleepUntil(ctx, r.now().Add(reportPoll)) {
			rr.State, rr.Err = "unknown", "campaign ended while the report ran"
			return rr
		}
	}
}

func reportTerminal(state string) bool {
	switch state {
	case "artifact_ready", "artifact_failed", "artifact_cancelled", "artifact_expired", "artifact_deleted":
		return true
	}
	return false
}

// newUUID is a random RFC 4122 version 4 UUID (the report request id).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// v4Score adds the offered rate (Paced scenarios) and the v4 lanes: the
// escalation episodes, the on-demand task runs and the alert reports.
func (r *run) v4Score(card *score.Scorecard, in score.Inputs, report map[string]any) {
	r.mu.Lock()
	offers := append(score.LedgerOffers(in.Ledger, "baseline"), r.offers...)
	tasks, reports := r.tasks, r.reports
	tasks.Runs = append([]score.TaskExecRun(nil), r.tasks.Runs...)
	reports.Runs = append([]score.ReportRun(nil), r.reports.Runs...)
	r.mu.Unlock()
	if r.s.Paced { // v4: every lane is counted (older scenarios have uncounted lanes)
		offered := score.OfferedPerMinute(r.t0, r.at("drain", false), offers)
		card.Offered = &offered
		report["offered"] = offered
		r.mu.Lock()
		var fullFrom time.Duration // the last paced lane's full-load start: earlier minutes are not under plan
		for _, t := range r.laneStarts {
			fullFrom = max(fullFrom, t.Sub(r.t0))
		}
		r.mu.Unlock()
		judge(card, nil, offered.OfferedBudget(r.s.Baseline, scenario.V4TotalRate, scenario.V4SpikeRate, fullFrom))
	}

	r.escalationScore(card, in.Receipts, report)
	if r.s.Tasks != nil {
		h, fails, incon := tasks.Judge(r.s.Limits.TaskSuccess, r.s.Limits.TaskP95)
		card.Headlines = append(card.Headlines, h...)
		card.TaskExec = &tasks
		report["taskExec"] = tasks
		judge(card, fails, incon)
	}
	if r.s.Limits.HomelabFailRise > 0 {
		loadEnd := r.at("drain", false)
		r.mu.Lock()
		if stop := r.laneStopAt; !stop.IsZero() && stop.Before(loadEnd) { // the run ended early
			loadEnd = stop
		}
		r.mu.Unlock()
		hh, heads, fails, incon := score.JudgeHomelab(in.PreTasks, in.Tasks, in.TasksCollected, in.TasksNotCollected, r.t0, loadEnd, r.s.Limits.HomelabFailRise)
		card.Headlines = append(card.Headlines, heads...)
		card.HomelabHealth = &hh
		report["homelabHealth"] = hh
		judge(card, fails, incon)
	}
	if slices.Contains(r.opts.SkipLanes, "report") {
		reports.Skipped = laneSkipped
		card.SkippedLanes = append(card.SkippedLanes, "report: "+laneSkipped)
	}
	if r.s.Report != nil {
		reports.HistoryWithin(in.Ledger, in.History)
		card.Reports = &reports
		report["reports"] = reports
		fails, incon := reports.Judge(r.s.Limits.HistoryP95)
		judge(card, fails, incon)
	}
}

// escalationScore judges the episodes of every escalation cohort
// (PolicySpec.Episode with Escalation) against ResendSuccess: a failed
// escalation FAILs, an unknown or missing one keeps a PASS from passing.
func (r *run) escalationScore(card *score.Scorecard, receipts []targetops.Receipt, report map[string]any) {
	var total score.Rate
	var why []string
	planned := false
	for _, p := range r.s.Resend {
		planned = planned || p.Episode > 0 && p.Escalation != nil
	}
	if !planned {
		return
	}
	for _, p := range r.policies {
		esc := p.spec.Escalation
		if p.spec.Episode <= 0 || esc == nil {
			continue
		}
		var eps []score.EscalationEpisode
		for _, m := range r.members() {
			if m.policy != p {
				continue
			}
			for _, ep := range m.episodes {
				if ep.End.IsZero() { // still firing until the drain resolves it
					ep.End = r.at("drain", false)
				}
				eps = append(eps, ep)
			}
		}
		rate, w := score.JudgeEscalation(eps, receipts, esc.TargetSeverity, esc.After)
		total.Expected += rate.Expected
		total.Succeeded += rate.Succeeded
		total.Failed += rate.Failed
		total.Unknown += rate.Unknown
		why = append(why, w...)
	}
	if total.Expected > 0 {
		total.Ratio = float64(total.Succeeded) / float64(total.Expected)
	}
	card.Escalation = &total
	report["escalation"] = map[string]any{"rate": total, "reasons": why}
	if total.Expected == 0 {
		judge(card, nil, []string{"escalation: no episode long enough to escalate was observed"})
		return
	}
	h := score.Headline{Name: "escalation.success", Unit: "ratio", Value: total.Ratio, Limit: &r.s.Limits.ResendSuccess,
		HigherBetter: true, Pass: total.Ratio >= r.s.Limits.ResendSuccess}
	card.Headlines = append(card.Headlines, h)
	var fails, incon []string
	if total.Failed > 0 {
		fails = append(fails, fmt.Sprintf("escalation: %d of %d episodes failed", total.Failed, total.Expected))
	}
	if total.Unknown > 0 {
		incon = append(incon, fmt.Sprintf("escalation: %d of %d episodes without evidence", total.Unknown, total.Expected))
	}
	judge(card, fails, incon)
	card.Reasons = append(card.Reasons, why...) // the first few bad episodes, for the operator
}
