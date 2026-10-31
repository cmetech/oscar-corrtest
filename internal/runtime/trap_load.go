package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/snmpload"
)

type TrapLoadOptions = snmpload.Options

type trapLoadSend struct {
	Phase        string    `json:"phase"`
	Episode      int       `json:"episode"`
	Kind         string    `json:"kind"`
	PreparedAt   time.Time `json:"preparedAt"`
	CompletedAt  time.Time `json:"completedAt"`
	LagMillis    int64     `json:"lagMillis"`
	LocalSuccess bool      `json:"localSuccess"`
	StartedAt    time.Time `json:"startedAt"`
	Attempted    bool      `json:"attempted"`
	ScheduledAt  time.Time `json:"scheduledAt"`
}
type trapLoadPreflight struct {
	Family            string `json:"family"`
	Episode           int    `json:"episode"`
	FiringAfterRepeat bool   `json:"firingAfterRepeat"`
	FiringObserved    bool   `json:"firingObserved"`
	ResolvedObserved  bool   `json:"resolvedObserved"`
}
type trapLoadReport struct {
	Preflight              []trapLoadPreflight `json:"preflight"`
	PlannedOffer           int                 `json:"plannedOffer"`
	PlannedTail            int                 `json:"plannedTail"`
	TailAttempts           int                 `json:"tailAttempts"`
	LocalFailures          int                 `json:"localFailures"`
	UnattemptedPlanPackets int                 `json:"unattemptedPlanPackets"`

	Coverage                 map[string]string      `json:"coverage"`
	Outcome                  string                 `json:"outcome"`
	OfferStartedAt           time.Time              `json:"offerStartedAt"`
	OfferCompletedAt         time.Time              `json:"offerCompletedAt"`
	OfferAttempts            int                    `json:"offerAttempts"`
	AchievedPacketsPerSecond float64                `json:"achievedPacketsPerSecond"`
	MaxLagMillis             int64                  `json:"maxLagMillis"`
	Sends                    []trapLoadSend         `json:"sends"`
	Observations             []snmpload.Observation `json:"observations"`
	ObservationErrors        int                    `json:"observationErrors"`
}

func (r *Runtime) StartTrapLoad(ctx context.Context, targetID string, o TrapLoadOptions) (domain.Run, error) {
	if err := o.Validate(); err != nil {
		return domain.Run{}, err
	}
	target, err := r.GetTarget(ctx, targetID)
	if err != nil {
		return domain.Run{}, err
	}
	parsed, err := url.Parse(target.BaseURL)
	if err != nil || parsed.Hostname() == "" {
		return domain.Run{}, fmt.Errorf("invalid target host")
	}
	client, err := r.newOSCARClient(target)
	if err != nil {
		return domain.Run{}, err
	}
	sender, err := snmpload.NewSender(parsed.Hostname(), o.Port, r.getenv("CORRTEST_SNMP_COMMUNITY"))
	if err != nil {
		return domain.Run{}, err
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if r.closing || len(r.active) > 0 {
		sender.Close()
		return domain.Run{}, fmt.Errorf("wait for the active run and its cleanup before starting an SNMP load")
	}
	run, err := r.history.CreateRun(ctx, targetID, "", r.version)
	if err != nil {
		sender.Close()
		return domain.Run{}, err
	}
	plan, err := snmpload.Compile(run.ID, o)
	if err != nil {
		sender.Close()
		return domain.Run{}, err
	}
	preflight := []snmpload.Episode{}
	seen := map[string]bool{}
	for _, e := range plan.Episodes {
		if !seen[e.Family] {
			f, err := snmpload.Fixture(run.ID+"-pre", len(preflight), e.Family)
			if err != nil {
				sender.Close()
				return domain.Run{}, err
			}
			preflight = append(preflight, f)
			seen[e.Family] = true
		}
	}
	document, _ := json.Marshal(struct {
		snmpload.Plan
		Preflight  []snmpload.Episode `json:"preflight"`
		TargetHost string             `json:"targetHost"`
	}{plan, preflight, parsed.Hostname()})
	if err := r.database.SetRunExecutionDocuments(ctx, run.ID, json.RawMessage(`{"inputPath":"snmp-load"}`), document, time.Now().UTC()); err != nil {
		sender.Close()
		return domain.Run{}, err
	}
	executionCtx, cancel := context.WithCancel(r.rootCtx)
	r.active[run.ID] = cancel
	r.runWG.Add(1)
	go func() {
		defer r.runWG.Done()
		defer sender.Close()
		defer cancel()
		defer func() { r.activeMu.Lock(); delete(r.active, run.ID); r.activeMu.Unlock() }()
		r.runMu.Lock()
		defer r.runMu.Unlock()
		if err := r.executeTrapLoad(executionCtx, run, client, sender, plan, preflight); err != nil {
			// Prepared send events survive storage failures; never invent successful cleanup.
			_ = r.database.TransitionRun(context.WithoutCancel(executionCtx), run.ID, domain.RunInterrupted, time.Now().UTC(), "SNMP load interrupted; reconcile prepared sends before retry")
			r.logger.Error("SNMP load evidence persistence failed", "run_id", run.ID, "error_type", fmt.Sprintf("%T", err))
		}
	}()
	return run, nil
}

func (r *Runtime) executeTrapLoad(ctx context.Context, run domain.Run, client *oscar.Client, sender *snmpload.Sender, plan snmpload.Plan, preflight []snmpload.Episode) error {
	stable := context.WithoutCancel(ctx)
	start := time.Now().UTC()
	o := plan.Options
	report := trapLoadReport{Coverage: map[string]string{"inputPath": "snmp-load", "transport": "UNACKNOWLEDGED_UDP", "rate": "NOMINAL_LOCAL_INVOCATIONS_NOT_WIRE_ARRIVALS", "receiverAdmission": "HISTORY_ONLY", "repeatReceipt": "NOT_PROVEN", "clearAttribution": "RECEIVER_REPORTED_MATCH_NOT_INDEPENDENT_AUDIT", "notificationDelivery": "NOT_TESTED", "capacityQualification": "NOT_TESTED"}}
	report.PlannedOffer = plan.OfferPackets
	report.PlannedTail = len(plan.Packets) - plan.OfferPackets
	transition := func(state domain.RunStatus, text string) error {
		return r.database.TransitionRun(stable, run.ID, state, time.Now().UTC(), text)
	}
	event := func(kind string, value any) error {
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = r.database.AppendRunEvent(stable, domain.RunEvent{RunID: run.ID, Type: kind, Level: "info", OccurredAt: time.Now().UTC(), Summary: kind, DetailJSON: string(body)})
		return err
	}
	// Retain every attempted problem, including ambiguous local failures, for scoped cleanup.
	attempted := map[int]bool{}
	clearAttempted := map[int]bool{}
	all := append(append([]snmpload.Episode{}, preflight...), plan.Episodes...)
	period := time.Second / time.Duration(o.Rate)
	var lastStarted time.Time
	problemStarted := map[int]time.Time{}
	repeatStarted := map[int]time.Time{}
	send := func(sendCtx context.Context, id int, kind, phase string, scheduled time.Time) (bool, error) {
		entry := trapLoadSend{Phase: phase, Episode: id, Kind: kind, PreparedAt: time.Now().UTC(), ScheduledAt: scheduled}
		prepared, err := json.Marshal(entry)
		if err != nil {
			return false, err
		}
		if err := r.database.PrepareRunInput(stable, run.ID, entry.PreparedAt, string(prepared)); err != nil {
			return false, err
		}
		due := lastStarted.Add(period)
		if scheduled.After(due) {
			due = scheduled
		}
		if kind == "clear" && (phase == "offer" || phase == "tail-clear") {
			if minimum := problemStarted[id].Add(time.Duration(o.ClearDelay) * time.Second); minimum.After(due) {
				due = minimum
			}
			if minimum := repeatStarted[id].Add(time.Second); minimum.After(due) {
				due = minimum
			}
		}
		ready := waitTrapUntil(sendCtx, due)
		if ready && !scheduled.IsZero() {
			entry.LagMillis = time.Since(scheduled).Milliseconds()
			if entry.LagMillis > report.MaxLagMillis {
				report.MaxLagMillis = entry.LagMillis
			}
			// Bound cumulative scheduler jitter without sending catch-up bursts.
			lagBudget := max(time.Second, scheduled.Sub(report.OfferStartedAt)/20)
			if entry.LagMillis > lagBudget.Milliseconds() {
				ready = false
				report.Outcome = "sender exceeded scheduling tolerance (one second or 5% elapsed); load is not valid"
			}
		}
		if ready {
			entry.StartedAt = time.Now().UTC()
			lastStarted = entry.StartedAt
			entry.Attempted = true
			if kind == "problem" {
				attempted[id] = true
				problemStarted[id] = entry.StartedAt
			}
			if kind == "repeat" {
				repeatStarted[id] = entry.StartedAt
			}
			if phase == "offer" {
				report.OfferAttempts++
			}
			if phase == "tail-clear" {
				report.TailAttempts++
			}
			entry.LocalSuccess = sender.Send(sendCtx, all[id], kind) == nil
			if !entry.LocalSuccess {
				report.LocalFailures++
			}
			// Even a killed subprocess may have emitted UDP; retain ambiguity without blind replay.
			if kind == "clear" {
				clearAttempted[id] = true
			}
		}
		entry.CompletedAt = time.Now().UTC()
		report.Sends = append(report.Sends, entry)
		if phase == "offer" && entry.Attempted {
			report.OfferCompletedAt = entry.CompletedAt
		}
		if err := event("trap.result", entry); err != nil {
			return false, err
		}
		return entry.LocalSuccess, nil
	}

	if err := transition(domain.RunPreflight, "Checking each selected migrated problem/clear pair before load"); err != nil {
		return err
	}
	failed := false
	for id := range preflight {
		if ctx.Err() != nil {
			failed = true
			report.Outcome = "cancelled before load"
			break
		}
		ok, err := send(ctx, id, "problem", "preflight", time.Time{})
		if err != nil {
			return err
		}
		observed := false
		if ok {
			observed = waitLoadState(ctx, client, []snmpload.Episode{all[id]}, start, "firing", min(o.DrainTimeout, 60), &report)
		}
		repeated := false
		if observed && waitTrapUntil(ctx, problemStarted[id].Add(time.Second)) {
			repeatOK, err := send(ctx, id, "repeat", "preflight", time.Time{})
			if err != nil {
				return err
			}
			if repeatOK {
				repeated = waitLoadState(ctx, client, []snmpload.Episode{all[id]}, start, "firing", min(o.DrainTimeout, 60), &report)
			}
		}
		clearOK := false
		if attempted[id] {
			clearCtx, cancel := context.WithTimeout(stable, 10*time.Second)
			waitTrapUntil(clearCtx, repeatStarted[id].Add(time.Second))
			clearOK, err = send(clearCtx, id, "clear", "preflight", time.Time{})
			cancel()
			if err != nil {
				return err
			}
		}
		resolved := false
		if clearOK && observed {
			resolved = waitLoadState(ctx, client, []snmpload.Episode{all[id]}, start, "resolved", min(o.DrainTimeout, 60), &report)
		}
		report.Preflight = append(report.Preflight, trapLoadPreflight{Family: all[id].Family, Episode: id, FiringObserved: observed, FiringAfterRepeat: repeated, ResolvedObserved: resolved})
		if !observed || !repeated || !resolved {
			failed = true
			report.Outcome = all[id].Family + " preflight failed; no bulk traffic sent"
			break
		}
	}
	if err := transition(domain.RunSettingUp, "Owned packet plan and preflight retained"); err != nil {
		return err
	}
	if err := transition(domain.RunInjecting, "Sending paced problems, repeats and clears"); err != nil {
		return err
	}
	if !failed {
		report.OfferStartedAt = time.Now().UTC()
		for index, p := range plan.Packets {
			if ctx.Err() != nil {
				failed = true
				report.Outcome = "cancelled; new problems stopped"
				break
			}
			phase := "offer"
			if index >= plan.OfferPackets {
				phase = "tail-clear"
			}
			ok, err := send(ctx, p.Episode+len(preflight), p.Kind, phase, report.OfferStartedAt.Add(time.Duration(p.AtMillis)*time.Millisecond))
			if err != nil {
				return err
			}
			if !ok {
				failed = true
				if report.Outcome == "" {
					report.Outcome = "local send failed or cancelled; UDP outcome remains unknown"
				}
				break
			}
		}
	}
	if !report.OfferStartedAt.IsZero() {
		elapsed := max(period.Seconds(), report.OfferCompletedAt.Sub(report.OfferStartedAt).Seconds()+period.Seconds())
		report.AchievedPacketsPerSecond = float64(report.OfferAttempts) / elapsed
	}
	if err := transition(domain.RunObserving, "Offer and tail counts recorded; observing owned History"); err != nil {
		return err
	}
	if err := transition(domain.RunAsserting, "Reconciling source identities without inferring UDP acknowledgements"); err != nil {
		return err
	}
	if err := transition(domain.RunCleaningUp, "Paced cleanup of attempted owned problems, then History drain"); err != nil {
		return err
	}
	report.UnattemptedPlanPackets = len(plan.Packets) - report.OfferAttempts - report.TailAttempts
	drainSeconds := o.DrainTimeout
	if ctx.Err() != nil {
		drainSeconds = min(drainSeconds, 30)
	}
	cleanupCtx, cancel := context.WithTimeout(stable, time.Duration(len(attempted)/o.Rate+drainSeconds+30)*time.Second)
	defer cancel()
	owned := []snmpload.Episode{}
	ownedIndices := []int{}
	for id, e := range all {
		if !attempted[id] {
			continue
		}
		owned = append(owned, e)
		ownedIndices = append(ownedIndices, id)
	}
	for _, id := range ownedIndices {
		if clearAttempted[id] {
			continue
		}
		if !waitTrapUntil(cleanupCtx, lastStarted.Add(period)) {
			failed = true
			break
		}
		ok, err := send(cleanupCtx, id, "clear", "cleanup", time.Time{})
		if err != nil {
			return err
		}
		if !ok {
			failed = true
		}
	}
	clean := false
	if len(owned) > 0 {
		clean = waitLoadState(cleanupCtx, client, owned, start, "resolved", drainSeconds, &report)
	}
	for i := range report.Observations {
		report.Observations[i].Episode = ownedIndices[report.Observations[i].Episode]
	}
	verdict, cleanup := domain.VerdictInconclusive, domain.CleanupUnknown
	if len(owned) == 0 {
		cleanup = domain.CleanupNotRequired
	} else if clean {
		cleanup = domain.CleanupClean
	} else {
		for _, v := range report.Observations {
			if v.ProblemFiring {
				cleanup = domain.CleanupDirty
			}
		}
	}
	if !failed && ctx.Err() == nil && clean && report.OfferAttempts == plan.OfferPackets {
		verdict = domain.VerdictPass
		report.Outcome = "paced load completed; exact owned History lifecycles resolved"
	} else if report.Outcome == "" {
		report.Outcome = "owned History missing, mismatched, duplicated or unresolved"
	}
	for _, v := range report.Observations {
		if v.State == "duplicate" || v.State == "mismatch" {
			verdict = domain.VerdictFail
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return r.database.CompleteRun(stable, run.ID, verdict, cleanup, encoded, "", time.Now().UTC())
}

func waitTrapUntil(ctx context.Context, due time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(time.Until(due))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
func waitLoadState(ctx context.Context, client *oscar.Client, episodes []snmpload.Episode, start time.Time, state string, seconds int, report *trapLoadReport) bool {
	report.Observations = snmpload.Reconcile(episodes, nil)
	observeCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	var settled []snmpload.Observation
	for {
		rows := []oscar.HistoryRecord{}
		seen := map[string]bool{}
		readOK := true
		for _, e := range episodes {
			if seen[e.AlertName] {
				continue
			}
			seen[e.AlertName] = true
			found, err := client.FindHistory(observeCtx, oscar.HistoryQuery{AlertName: e.AlertName, Start: start.Add(-5 * time.Second), End: time.Now().UTC().Add(time.Minute)})
			if err != nil {
				report.ObservationErrors++
				readOK = false
				break
			}
			rows = append(rows, found...)
		}
		if readOK {
			report.Observations = snmpload.Reconcile(episodes, rows)
			complete := true
			for _, v := range report.Observations {
				complete = complete && v.State == state
			}
			if complete {
				if state != "resolved" || reflect.DeepEqual(settled, report.Observations) {
					return true
				}
				settled = append([]snmpload.Observation{}, report.Observations...)
			} else {
				settled = nil
			}
		}
		if !readOK {
			settled = nil
		}
		if !waitTrapUntil(observeCtx, time.Now().Add(2*time.Second)) {
			return false
		}
	}
}
