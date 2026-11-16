package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/guard"
	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

var (
	queueDepthQuery = score.Queries["queue_depth"]
	queueAgeQuery   = score.Queries["queue_oldest_age"]
)

// scoreRun gathers the evidence, computes the scorecard and the report.
func (r *run) scoreRun(ctx context.Context, end time.Time, incidents []targetops.Incident, clean bool, forced domain.Verdict, why string) (score.Scorecard, json.RawMessage) {
	r.mu.Lock()
	trip, lost, resendIncon := r.trip, r.workerLost, r.resendIncon
	r.mu.Unlock()
	report := map[string]any{"campaign": r.id, "t0": r.t0, "end": end, "laneId": r.laneID}
	if r.t0.IsZero() || len(r.s.Phases) == 0 { // never reached traffic, or scenario unknown
		card := score.Scorecard{ScenarioHash: r.s.Hash(), Trigger: r.opts.Trigger, RunBy: r.opts.RunBy, CleanupClean: clean,
			Verdict: forced, Reasons: []string{why}}
		if card.Verdict == "" {
			card.Verdict = domain.VerdictInconclusive
		}
		report["reasons"] = card.Reasons
		return card, mustJSON(report)
	}
	ledger, ledgerErr := r.fetchLedger(ctx)
	if ledgerErr != nil {
		r.noteMissing("ledger: " + ledgerErr.Error())
	} else {
		r.crossCheckLedger(ctx, ledger)
	}
	in := score.Inputs{PlannedBaseline: r.plannedBaseline(), ScenarioHash: r.s.Hash(), Trigger: r.opts.Trigger, RunBy: r.opts.RunBy, Comparable: r.comparable, CleanupClean: clean,
		Start: r.t0, End: end, Phases: r.s.Phases, Ledger: ledger, Correlation: r.corrRuns, NotRun: r.notRun,
		PlannedCorrelation: r.plannedCorr, FrontOffice: r.fo,
		GuardStopped: trip != "" && !lost, Precheck: r.s.Precheck}
	in.Tasks, in.PreTasks, in.TasksNotCollected = r.taskRuns(ctx, end)
	in.TasksCollected = in.TasksNotCollected == ""
	var err error
	if in.History, err = r.e.d.Target.History(ctx, r.id, r.t0, end); err != nil {
		r.noteMissing("history: " + err.Error())
	}
	if r.e.d.Sink != nil {
		if in.Receipts, err = r.e.d.Sink.Receipts(ctx, "campaign", r.id, r.t0); err != nil {
			r.noteMissing("sink receipts: " + err.Error())
		}
	}
	in.QueueDepth = r.series(ctx, queueDepthQuery, "queue", end)
	in.QueueAge = r.series(ctx, queueAgeQuery, "queue", end)
	if m, err := r.e.d.Metrics.QueryRange(ctx, score.Queries["container_workingset"], r.t0, end, metricStep); err == nil {
		in.Containers = score.ContainerSeries(m)
	}
	if snap, err := r.e.d.Metrics.Snapshot(ctx, score.Queries, r.t0, end, metricStep); err == nil {
		report["metrics"] = snap
	}
	in.Tickets, in.PlannedTickets = r.ticketInputs(ledger, in.History, incidents)
	if resendIncon == "" {
		in.Resend = r.resendCases(ctx, in.Receipts, incidents)
	}
	report["tickets"], report["resend"], report["frontoffice"], report["cohorts"] = in.Tickets, in.Resend, r.foSnapshot(), r.cohortSnapshot()
	for _, p := range r.s.Resend {
		in.PlannedResend += p.Cohort
	}
	card := score.Compute(in, r.s.Limits)
	if r.s.Standing != nil || r.s.Breaker != nil { // v2: AT&T shape, report-only
		card.ATT = r.attStats(ctx)
		report["att"] = card.ATT
	}
	if d := r.s.DeadTarget; d != nil { // delivery-retry: gated, over T0 to the end of drain
		card.ATT = r.deliveryStats(ctx, end)
		report["att"] = card.ATT
		fails, incon := score.JudgeDeliveryRetry(card.ATT.Budgets, d.MaxCopies, d.MinQueueAge.Seconds())
		if len(fails) > 0 {
			card.Verdict = domain.VerdictFail
		} else if len(incon) > 0 && card.Verdict == domain.VerdictPass {
			card.Verdict = domain.VerdictInconclusive
		}
		card.Reasons = append(append(card.Reasons, fails...), incon...)
	}
	r.mu.Lock()
	missing, stopped, open, corrT := append([]string{}, r.missing...), r.stopped, r.openIncidents, r.corrTickets
	r.mu.Unlock()
	report["correlationTickets"] = corrT
	if len(open) > 0 {
		card.Reasons = append(card.Reasons, "incidents still open: "+strings.Join(open, ","))
		if card.Verdict == domain.VerdictPass {
			card.Verdict = domain.VerdictInconclusive
		}
	}
	for _, m := range missing { // no PASS on missing evidence
		card.Reasons = append(card.Reasons, "missing evidence: "+m)
		if card.Verdict == domain.VerdictPass {
			card.Verdict = domain.VerdictInconclusive
		}
	}
	switch {
	case forced != "":
		card.Verdict, card.Reasons = forced, append([]string{why}, card.Reasons...)
	case lost:
		card.Verdict, card.Reasons = domain.VerdictInconclusive, append([]string{"guard:" + guard.GuardHeartbeatLost + ": worker lost"}, card.Reasons...)
	case trip != "":
		card.Verdict, card.Reasons = domain.VerdictFail, append([]string{"guard:" + trip}, card.Reasons...)
	case stopped:
		card.Verdict, card.Reasons = domain.VerdictInconclusive, append([]string{"stopped: operator stop"}, card.Reasons...)
	case resendIncon != "" && card.Verdict == domain.VerdictPass:
		card.Verdict, card.Reasons = domain.VerdictInconclusive, []string{resendIncon}
	}
	report["reasons"] = card.Reasons
	return card, mustJSON(report)
}

// taskRuns reads the homelab pack's runs for the campaign window and for the
// hour before T0, or says why they were not collected.
func (r *run) taskRuns(ctx context.Context, end time.Time) (during, pre []targetops.TaskRun, notCollected string) {
	if r.e.d.TaskRuns == nil {
		return nil, nil, "no task history client"
	}
	pre, err := r.e.d.TaskRuns(ctx, r.t0.Add(-time.Hour), r.t0)
	if err == nil {
		during, err = r.e.d.TaskRuns(ctx, r.t0, end)
	}
	if err != nil {
		return nil, nil, "task history: " + err.Error()
	}
	return during, pre, ""
}

// laneGrace: plan events due this close to the lane stop may have been in
// flight; they are left out of the planned count.
const laneGrace = 2 * time.Second

// plannedBaseline is the number of lane plan events in the window the lane
// actually ran: the whole plan on a normal end, the events due before the
// controller stopped the lane otherwise, 0 (unknown) when neither is known.
// The plan is deterministic (same run id, seed, spec and duration as
// startLane), sorted by schedule, so the window is a seq prefix.
func (r *run) plannedBaseline() int {
	r.mu.Lock()
	normal, stopAt := r.normalEnd, r.laneStopAt
	r.mu.Unlock()
	if !normal && stopAt.IsZero() {
		return 0
	}
	plan := lane.Compile(r.id, r.s.Seed, r.s.Baseline, r.s.Phase("drain").Start)
	if normal {
		return len(plan.Events)
	}
	cut := stopAt.Sub(r.t0) - laneGrace
	return sort.Search(len(plan.Events), func(i int) bool { return plan.Events[i].At > cut })
}

// series runs a range query at the 15 s step, keyed by label.
func (r *run) series(ctx context.Context, q, label string, end time.Time) map[string][]score.Point {
	m, err := r.e.d.Metrics.QueryRange(ctx, q, r.t0, end, metricStep)
	if err != nil {
		return nil
	}
	out := map[string][]score.Point{}
	for _, s := range m {
		out[s.Metric[label]] = append(out[s.Metric[label]], s.Values...)
	}
	return out
}

// fetchLedger streams the lane ledger into a temp file, renames it only on
// success (partial rows stay in the temp file on error) and parses it.
func (r *run) fetchLedger(ctx context.Context) ([]domain.LaneRow, error) {
	dir := r.e.d.ReportDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, r.laneID+"-*.jsonl.part")
	if err != nil {
		return nil, err
	}
	werr := r.e.d.Worker.Ledger(ctx, r.laneID, f)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	path := f.Name()
	if werr == nil {
		final := filepath.Join(dir, r.laneID+".ledger.jsonl")
		if err := os.Rename(path, final); err == nil {
			path = final
		}
	}
	rows, perr := readLedger(path)
	if werr != nil {
		return rows, werr
	}
	return rows, perr
}

func readLedger(path string) ([]domain.LaneRow, error) {
	f, err := os.Open(path) // #nosec G304 -- ledger path is built from the campaign data dir, not request input.
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []domain.LaneRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var row domain.LaneRow
		if json.Unmarshal(sc.Bytes(), &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows, sc.Err()
}

// ticketInputs derives expected incidents from the accepted ledger (ticketed
// firings) plus the ticket cohort, and matches incidents by exact alertname.
func (r *run) ticketInputs(ledger []domain.LaneRow, hist []oscar.HistoryRecord, incs []targetops.Incident) (score.TicketInputs, int) {
	in := score.TicketInputs{Incidents: map[string][]targetops.Incident{}, Bound: map[string]bool{}}
	seen := map[string]bool{}
	for _, row := range ledger {
		// Accepted ticketed firings, plus ones with an unknown outcome (they may
		// have created an incident; with none found they score as Unknown).
		if row.Kind == "firing" && row.Ticket && (row.Done && row.Status/100 == 2 || unknownOutcome(row)) && !seen[row.Identity] {
			seen[row.Identity] = true
			in.Expected = append(in.Expected, row.Identity)
		}
	}
	for _, m := range r.members() {
		if m.policy.spec.Action == "ticket" && (len(m.deliveries) > 0 || m.ambiguous) {
			in.Expected = append(in.Expected, m.alert.Name)
		}
	}
	r.mu.Lock()
	corr := append([]corrTicket(nil), r.corrTicketed...)
	r.mu.Unlock()
	expected := in.Expected
	in.Expected = append([]string(nil), expected...)
	for _, c := range corr { // set B tickets count in the rate; a missing one is a miss
		in.Expected = append(in.Expected, c.id)
		in.Bound[c.id] = true // the incident carries the run token: OSCAR bound it
		if !c.unknown {
			in.Incidents[c.id] = c.incidents
		}
	}
	for _, id := range expected {
		for _, inc := range incs {
			if mentions(inc.ShortDescription, id) {
				in.Incidents[id] = append(in.Incidents[id], inc)
			}
		}
	}
	// ponytail: "bound" = History holds the ticketed alert (create_ticket=snow); switch to the History ticket-binding field once the client exposes it.
	for _, h := range hist {
		if h.Labels["create_ticket"] == "snow" {
			in.Bound[h.AlertName] = true
		}
	}
	return in, len(in.Expected)
}

// mentions reports whether s contains id as a whole token (Campaign-x-1 must
// not match Campaign-x-10).
func mentions(s, id string) bool {
	for i := strings.Index(s, id); i >= 0; {
		j := i + len(id)
		if j == len(s) || !(s[j] >= '0' && s[j] <= '9' || s[j] == '-') {
			return true
		}
		k := strings.Index(s[i+1:], id)
		if k < 0 {
			return false
		}
		i += 1 + k
	}
	return false
}

// resendCases builds each cohort identity's observed resends: sink receipts
// for notification policies, OSCAR's "still firing" incident comments for
// the ticket policy (R4).
func (r *run) resendCases(ctx context.Context, receipts []targetops.Receipt, incs []targetops.Incident) []score.ResendCase {
	r.mu.Lock()
	read := r.incidentsRead
	r.mu.Unlock()
	exhausted := map[*policyState]bool{}
	for _, p := range r.policies {
		if st, err := r.e.d.Target.PolicyStatus(ctx, p.notifierID, p.spec.Action, p.id); err == nil {
			// ponytail: status is per policy, so exhaustion is "every member exhausted"; per-identity episodes need an identity-keyed status.
			exhausted[p] = st.Episodes.Exhausted >= p.spec.Cohort
		}
	}
	var out []score.ResendCase
	for _, m := range r.members() {
		c := score.ResendCase{Policy: m.policy.spec, Deliveries: m.deliveries, Exhausted: exhausted[m.policy]}
		var times []time.Time
		if m.policy.spec.Action == "ticket" {
			c.Observed, c.Unread, c.Violation = ticketResends(m, incs, read)
			if c.Violation != "" {
				r.event(ctx, "campaign.resend", "warn", "resend "+m.policy.spec.Key+" "+m.alert.Name+": "+c.Violation, nil)
			}
		} else {
			var all []time.Time
			for _, rc := range receipts {
				for _, a := range rc.Alerts {
					if a.AlertName == m.alert.Name && a.Status != "resolved" {
						all = append(all, rc.ReceivedAt)
					}
				}
			}
			sort.Slice(all, func(i, j int) bool { return all[i].Before(all[j]) })
			if len(all) > 0 {
				times = all[1:] // the first receipt is the initial notification
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		for _, t := range times {
			c.Observed = append(c.Observed, t.Sub(m.origin))
		}
		out = append(out, c)
	}
	return out
}

// resendComment is OSCAR's ticket-policy resend comment ("OSCAR alert still
// firing: episode <n>, occurrence <m>.").
const resendComment = "OSCAR alert still firing"

// ticketResends reads one R4 member's evidence from its incident(s): each
// resend comment as an offset from the member's origin. read is whether the
// campaign incidents were listed at all; no listing, or no incident for the
// member, is unread (Unknown). More resend comments than max_resends, or one
// after the resolution comment, is a violation.
func ticketResends(m *cohortMember, incs []targetops.Incident, read bool) (observed []time.Duration, unread, violation string) {
	if !read {
		return nil, "campaign incidents not read", ""
	}
	found := false
	for _, inc := range incs {
		if !mentions(inc.ShortDescription, m.alert.Name) {
			continue
		}
		found = true
		resolved := false
		for _, cm := range inc.Comments {
			switch {
			case isResolution(cm.Value):
				resolved = true
			case strings.Contains(cm.Value, resendComment):
				at, err := time.Parse("2006-01-02 15:04:05", cm.CreatedOn)
				if err != nil {
					return nil, fmt.Sprintf("incident %s: unreadable comment time %q", inc.Number, cm.CreatedOn), ""
				}
				observed = append(observed, at.Sub(m.origin))
				if resolved {
					violation = "incident " + inc.Number + ": resend comment after resolution"
				}
			}
		}
	}
	if !found {
		return nil, "no incident for " + m.alert.Name, ""
	}
	if mx := m.policy.spec.MaxResends; mx != nil && len(observed) > *mx && violation == "" {
		violation = fmt.Sprintf("%d resend comments, max_resends %d", len(observed), *mx)
	}
	sort.Slice(observed, func(i, j int) bool { return observed[i] < observed[j] })
	return observed, "", violation
}

// unknownOutcome is a ledger row sent without a definite answer.
func unknownOutcome(row domain.LaneRow) bool {
	switch row.ErrorClass {
	case lane.ErrClassTimeout, lane.ErrClassNetwork, lane.ErrClassCancelled:
		return true
	}
	return !row.SentAt.IsZero() && row.Status == 0 && !row.Missed
}

// crossCheckLedger compares accepted ledger rows with the worker's own
// accepted count (Status.Sent + Status.Resolved): the planned tickets come
// from the ledger, so a short ledger must not pass.
func (r *run) crossCheckLedger(ctx context.Context, ledger []domain.LaneRow) {
	st, err := r.e.d.Worker.Status(ctx, r.laneID)
	if err != nil {
		r.noteMissing("worker status for the ledger cross-check: " + err.Error())
		return
	}
	accepted := 0
	for _, row := range ledger {
		if row.Done && row.Status/100 == 2 {
			accepted++
		}
	}
	if accepted != st.Sent+st.Resolved {
		r.noteMissing(fmt.Sprintf("ledger has %d accepted rows, worker reports %d", accepted, st.Sent+st.Resolved))
	}
}
