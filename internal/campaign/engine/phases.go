package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/campaign/guard"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// Mutation kinds in the campaign mutation log.
const (
	kindNotifier = "notifier_disable"
	kindSink     = "sink_enable"
	// kindNotifierEnable is pre-check B5's smoke: ObjectID is the notifier
	// NAME (CAMPAIGN_SINK), Prior {"status":"disabled"}.
	kindNotifierEnable = "notifier_enable"
	// policy_create is recorded twice: an intent keyed by name before the
	// create (Prior.ID empty), then the created policy keyed by id.
	kindPolicyCreate   = "policy_create"
	kindPolicyActivate = "policy_activate"
	kindIncident       = "incident_close"
	// kindDeadNotifier is the delivery-retry dead target: ObjectID is its name
	// (recorded before the create, so the id is not known yet); restore deletes it.
	kindDeadNotifier = "dead_notifier_create"
)

// keepNotifiers stay enabled during a campaign (besides CAMPAIGN_SINK).
var keepNotifiers = []string{"ALERTHISTORY", "LOGGER", "CORRELATION_EVENTS"}

func (r *run) record(ctx context.Context, kind, object string, prior any) (int64, error) {
	raw, _ := json.Marshal(prior)
	return r.store().RecordMutation(ctx, domain.CampaignMutation{CampaignID: r.id, Kind: kind, ObjectID: object, Prior: raw, RecordedAt: r.now()})
}

func (r *run) restored(ctx context.Context, id int64) error {
	return r.store().MarkMutationRestored(ctx, id, r.now())
}

type notifierPrior struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// isolate disables every external notifier and enables CAMPAIGN_SINK, each
// recorded before it happens and verified by read-back.
func (r *run) isolate(ctx context.Context) error {
	stable := context.WithoutCancel(ctx)
	all, err := r.e.d.Target.Notifiers(ctx)
	if err != nil {
		return err
	}
	r.notifiers = map[string]string{}
	keep := append(append([]string{}, keepNotifiers...), r.e.d.CampaignNotifier)
	var external []targetops.Notifier
	for _, n := range all {
		r.notifiers[strings.ToUpper(n.Name)] = n.ID
		if strings.EqualFold(n.Name, r.e.d.CampaignNotifier) {
			r.sinkID = n.ID
		}
		if n.Status != "disabled" && !containsFold(keep, n.Name) {
			external = append(external, n)
		}
	}
	if r.sinkID == "" {
		return fmt.Errorf("notifier %s not found", r.e.d.CampaignNotifier)
	}
	for _, n := range external {
		if err := r.setNotifier(stable, kindNotifier, n, false); err != nil {
			return err
		}
	}
	if err := r.setNotifier(stable, kindSink, targetops.Notifier{ID: r.sinkID, Name: r.e.d.CampaignNotifier, Status: "disabled"}, true); err != nil {
		return err
	}
	return r.createDeadTarget(stable)
}

// createDeadTarget adds the delivery-retry facet's webhook notifier, enabled,
// so every alert gets a delivery that fails before send.
func (r *run) createDeadTarget(ctx context.Context) error {
	d := r.s.DeadTarget
	if d == nil {
		return nil
	}
	name := d.Name + "_" + strings.TrimPrefix(r.id, "camp_")
	if _, err := r.record(ctx, kindDeadNotifier, name, notifierPrior{Name: name}); err != nil {
		return err
	}
	n, err := r.e.d.Target.CreateWebhookNotifier(ctx, name, d.URL)
	if err != nil {
		return err
	}
	if n.Status != "enabled" {
		return fmt.Errorf("dead target %s read back %q, want enabled", name, n.Status)
	}
	r.event(ctx, "campaign.isolation", "info", "dead target "+name+" created", map[string]string{"url": d.URL})
	return nil
}

func (r *run) setNotifier(ctx context.Context, kind string, n targetops.Notifier, on bool) error {
	mid, err := r.record(ctx, kind, n.ID, notifierPrior{n.Name, n.Status})
	if err != nil {
		return err
	}
	got, err := r.e.d.Target.SetNotifier(ctx, n.ID, on)
	if err != nil {
		return err
	}
	if want := statusWord(on); got.Status != want {
		return fmt.Errorf("notifier %s read back %q, want %q", n.Name, got.Status, want)
	}
	_ = mid // restored by cleanup
	return nil
}

func statusWord(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// laneFor maps a policy to its cohort lane label.
func laneFor(p scenario.PolicySpec) string {
	if p.Action == "ticket" {
		return "resend-ticket"
	}
	switch p.Mode {
	case "occurrences":
		return "resend-occ"
	case "either":
		return "resend-either"
	}
	return "resend-time"
}

type policyPrior struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	NotifierID string `json:"notifier_id"`
	Action     string `json:"action"`
}

// createDrafts creates the resend policies disabled. Revisions are per
// notifier/action, so every call uses the latest one we have seen.
func (r *run) createDrafts(ctx context.Context) error {
	stable := context.WithoutCancel(ctx)
	for i, p := range r.s.Resend {
		nid := r.notifiers[strings.ToUpper(p.Notifier)]
		if nid == "" {
			return fmt.Errorf("policy %s: notifier %s not found", p.Key, p.Notifier)
		}
		name := fmt.Sprintf("Campaign-%s-%s", r.id, p.Key)
		def := targetops.PolicyDefinition{NotifierID: nid, Name: name, Action: p.Action, Mode: p.Mode, MaxResends: p.MaxResends,
			Priority: i + 1, Enabled: false,
			Condition: fmt.Sprintf(`campaign_run == %q and campaign_lane == %q`, r.id, laneFor(p))}
		if p.Escalation != nil {
			if p.Escalation.After < time.Second || p.Escalation.After%time.Second != 0 {
				return fmt.Errorf("policy %s escalation must use whole positive seconds", p.Key)
			}
			def.Escalation = &targetops.Escalation{AfterSeconds: int(p.Escalation.After / time.Second), TargetSeverity: p.Escalation.TargetSeverity}
		}
		if p.Interval > 0 {
			s := int(p.Interval / time.Second)
			def.IntervalSeconds = &s
		}
		if p.Every > 0 {
			e := p.Every
			def.EveryOccurrences = &e
		}
		intent, err := r.record(stable, kindPolicyCreate, name, policyPrior{"", name, nid, p.Action})
		if err != nil {
			return err
		}
		var pol targetops.Policy
		err = r.withRevision(ctx, nid, p.Action, func(rev int) (int, error) {
			var err error
			pol, err = r.e.d.Target.CreateDraft(ctx, def, rev)
			return pol.Revision, err
		})
		if err != nil {
			if definite4xx(err) { // the create was refused, so nothing exists to clean up
				_ = r.restored(stable, intent)
			}
			return fmt.Errorf("draft %s: %w", p.Key, err)
		}
		if _, err := r.record(stable, kindPolicyCreate, pol.ID, policyPrior{pol.ID, name, nid, p.Action}); err != nil {
			return err
		}
		if err := r.restored(stable, intent); err != nil {
			return err
		}
		r.policies = append(r.policies, &policyState{spec: p, id: pol.ID, name: name, notifierID: nid})
	}
	return nil
}

// definite4xx reports a request the server definitely refused (4xx).
func definite4xx(err error) bool {
	var me *oscar.MachineError
	var conflict *targetops.ConflictError
	return errors.As(err, &conflict) || errors.Is(err, targetops.ErrRevisionMismatch) ||
		errors.As(err, &me) && me.StatusCode >= 400 && me.StatusCode < 500
}

// withRevision runs op with the notifier/action's current revision. A stale
// revision surfaces as ConflictError: re-read the status and retry once.
func (r *run) withRevision(ctx context.Context, nid, action string, op func(rev int) (int, error)) error {
	st, err := r.e.d.Target.PolicyStatus(ctx, nid, action, "")
	if err != nil {
		return err
	}
	_, err = op(st.Revision)
	var conflict *targetops.ConflictError
	if errors.As(err, &conflict) || errors.Is(err, targetops.ErrRevisionMismatch) {
		if st, err = r.e.d.Target.PolicyStatus(ctx, nid, action, ""); err != nil {
			return err
		}
		_, err = op(st.Revision)
	}
	return err
}

// startLane starts the remote baseline lane at T0.
func (r *run) startLane(ctx context.Context) error {
	stable := context.WithoutCancel(ctx)
	r.monoStart = time.Now()
	r.t0 = r.now()
	r.lastHeartbeat = r.t0
	r.event(stable, "campaign.t0", "info", "campaign T0", map[string]time.Time{"T0": r.t0})
	params, _ := json.Marshal(workerapi.BaselineParams{TargetURL: r.e.d.IngestURL, Seed: r.s.Seed, Baseline: r.s.Baseline,
		Duration: r.s.Phase("drain").Start, StartAt: r.t0})
	_, err := r.e.d.Worker.Start(ctx, workerapi.LaneSpec{ID: r.laneID, CampaignID: r.id, Kind: "baseline", Params: params})
	return err
}

// heartbeats keeps the worker lane alive every 5 s until ctx ends.
func (r *run) heartbeats(ctx context.Context) {
	for next := r.now().Add(heartbeatEvery); r.e.clock.SleepUntil(ctx, next); next = next.Add(heartbeatEvery) {
		if st, err := r.e.d.Worker.Heartbeat(ctx, r.laneID); err == nil {
			r.mu.Lock()
			r.lastHeartbeat, r.laneState, r.laneErr = r.now(), st.State, st.Error
			r.mu.Unlock()
		}
	}
}

// guards samples every 15 s and trips stop(). It never evaluates before the
// first sample, which is also the baseline.
func (r *run) guards(ctx context.Context, stop context.CancelFunc) {
	var window []guard.Sample
	var baseline guard.Sample
	for next := r.now(); r.e.clock.SleepUntil(ctx, next); next = next.Add(guardEvery) {
		s, err := sampleGuard(ctx, r.e.d.Metrics, r.e.d.Probe, r.now())
		if err == nil {
			if len(window) == 0 {
				baseline = s
			}
			window = append(window, s)
		}
		if len(window) == 0 {
			continue
		}
		st, _ := r.e.d.Worker.Status(ctx, r.laneID)
		r.mu.Lock()
		c := guard.Counters{Offered: st.Sent + st.Failed + st.Unknown, Unknown: st.Unknown, TicketUnknown: r.ticketUnknown, WorkerHeartbeat: r.lastHeartbeat,
			LaneState: r.laneState, LaneError: r.laneErr, BaselineEnd: r.at("drain", false)}
		if r.s.Breaker != nil { // the forced breaker stalls History and grows queues on purpose
			_, _, tail := r.breakerTiming()
			c.QuietFrom, c.QuietUntil = r.at("breaker", false), r.at("breaker", true).Add(tail)
		}
		r.mu.Unlock()
		trips := guard.Evaluate(r.now(), window, c, baseline)
		if len(trips) == 0 {
			continue
		}
		r.mu.Lock()
		r.trip = trips[0].Guard
		r.workerLost = r.trip == guard.GuardHeartbeatLost
		r.mu.Unlock()
		r.event(context.WithoutCancel(ctx), "campaign.guard", "error", "guard tripped: "+trips[0].Guard, trips)
		stop()
		return
	}
}

func (r *run) frontOffice(ctx context.Context) {
	if len(r.e.d.FrontOfficeUsers) == 0 {
		return
	}
	frontoffice.Run(ctx, r.e.d.FrontOfficeURL, r.e.d.FrontOfficeHTTP, r.e.d.FrontOfficeUsers, r.s.Baseline.Instances, r.s.Seed,
		r.s.FrontOffice, r.e.clock, func(a frontoffice.ActionResult) {
			r.mu.Lock()
			r.fo[a.Action] = append(r.fo[a.Action], score.FOSample{Seconds: float64(a.LatencyMillis) / 1000, Err: a.Err != ""})
			r.mu.Unlock()
		})
}

// caseDoc builds the correlation document for one case reference.
func caseDoc(ref scenario.CaseRef) (corr.Scenario, error) {
	var doc corr.Scenario
	if ref.Level == "builtin" {
		doc = corr.Builtin(ref.Pattern)
	} else {
		ex, ok := corr.LookupExample(ref.Pattern, ref.Level)
		if !ok {
			return doc, fmt.Errorf("no example %s:%s", ref.Pattern, ref.Level)
		}
		doc = ex.Scenario
	}
	return doc, nil
}

func refName(ref scenario.CaseRef) string { return ref.Pattern + ":" + ref.Level }

// correlation runs set A from T+30 (anything not started by T+60 is
// not_run), then loops set B from T+60 until the drain phase.
func (r *run) correlation(ctx context.Context) {
	setA, setB := r.s.Correlation.SetA, r.s.Correlation.SetB
	if len(setA)+len(setB) == 0 || !r.e.clock.SleepUntil(ctx, r.at("correlation", false)) {
		r.mu.Lock()
		for _, ref := range setA {
			r.notRun = append(r.notRun, "setA "+refName(ref))
		}
		r.plannedCorr += len(setA)
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.plannedCorr += len(setA)
	r.mu.Unlock()
	end := r.at("correlation", true)
	for i, ref := range setA {
		if ctx.Err() != nil || !r.now().Before(end) {
			r.mu.Lock()
			for _, rest := range setA[i:] {
				r.notRun = append(r.notRun, "setA "+refName(rest))
			}
			r.mu.Unlock()
			break
		}
		r.correlate(ctx, ref, "setA", false)
	}
	if len(setB) == 0 || !r.e.clock.SleepUntil(ctx, r.at("ticketing", false)) {
		return
	}
	if r.s.NativeWorkload != nil && r.s.NativeWorkload.ManagedTL1 && r.s.ID == "alerting-day" {
		if err := r.syntheticTicket(ctx); err != nil {
			r.noteMissing("synthetic parent: " + err.Error())
		}
	}
	stopAt := r.at("drain", false)
	if r.s.Breaker != nil { // set B runs must finish before the forced breaker window
		stopAt = r.at("breaker", false).Add(-setBBreakerMargin)
	}
	for ctx.Err() == nil {
		ran := false
		for _, ref := range setB {
			if ctx.Err() != nil || !r.now().Before(stopAt) {
				return
			}
			ran = r.correlate(ctx, ref, "setB", containsFold(r.s.Correlation.TicketedPatterns, ref.Pattern)) || ran
		}
		if !ran && !r.e.clock.SleepUntil(ctx, r.now().Add(setBBackoff)) { // a pass that started no run must not spin
			return
		}
	}
}

// setBBackoff spaces set B passes in which every run failed to start.
const setBBackoff = 30 * time.Second

// setBBreakerMargin stops starting set B runs this long before the breaker
// window, so their injections are not refused by the tripped breaker.
const setBBreakerMargin = 5 * time.Minute

// correlate executes one case document through the runtime and links it.
//
// Ticket path (C15, ruling C27): the compiler's synthetic parent carries only
// the fixed identityLabels (compiler.go RulePlan.EmitLabels), so rule plans
// cannot put labels on the parent. For the ticketed patterns (flood,
// threshold) create_ticket=snow goes on ONE source alert per trigger case:
// its first firing event. That bounds PDI volume. Ticketed runs are linked
// as "setB_ticket:<pattern>" so drain and recovery can find their incidents
// by the run's short token.
// It reports whether the runtime started a run.
func (r *run) correlate(ctx context.Context, ref scenario.CaseRef, kind string, ticket bool) bool {
	stable := context.WithoutCancel(ctx)
	r.mu.Lock()
	if kind == "setB" {
		r.plannedCorr++
	}
	r.mu.Unlock()
	doc, err := caseDoc(ref)
	cr := score.CorrRun{Verdict: domain.VerdictError, Cleanup: domain.CleanupDirty}
	started := false
	if err == nil {
		if ticket {
			ticketFirstTrigger(&doc)
			kind = setBTicketRun + ref.Pattern
		}
		var res domain.Run
		res, err = r.e.d.Correlate(ctx, r.e.d.TargetID, doc, correlationRun)
		started = err == nil && res.ID != "" // a run that failed validation still has an ID
		if res.ID != "" {
			_ = r.store().LinkCampaignRun(stable, r.id, res.ID, kind)
			cr.Verdict, cr.Cleanup = res.Verdict, res.CleanupStatus
			if res.StartedAt != nil && res.EndedAt != nil {
				// ponytail: run wall time stands in for source-accepted -> parent-in-History; add History timing when the runner exposes it.
				cr.LatencySeconds = res.EndedAt.Sub(*res.StartedAt).Seconds()
			}
		}
	}
	if err != nil {
		r.event(stable, "campaign.correlation", "warn", fmt.Sprintf("%s %s: %v", kind, refName(ref), err), nil)
	}
	r.mu.Lock()
	r.corrRuns = append(r.corrRuns, cr)
	r.mu.Unlock()
	return started
}

// setBTicketObservation is how long a ticketed set B case observes: OSCAR
// holds ticketed alerts (anti-flap), so their History rows land well after the
// 45 s default (49-79 s at 5/s, max 91 s; Corey, 2026-11-18).
const setBTicketObservation = 150 * time.Second

// ticketFirstTrigger labels the first firing event of each trigger
// (positive) case with create_ticket=snow, lets that case observe for
// setBTicketObservation and grows the run budget to match.
func ticketFirstTrigger(doc *corr.Scenario) {
	for i := range doc.Cases {
		c := &doc.Cases[i]
		if c.Polarity != "positive" {
			continue
		}
		c.ObservationWindow = setBTicketObservation
		doc.MaxDuration += setBTicketObservation
		if len(c.Events) == 0 { // Repeat cases: expand exactly as the compiler does
			for range c.Repeat {
				c.Events = append(c.Events, corr.Event{Role: c.Role, Status: "firing"})
			}
			c.Repeat, c.Role = 0, ""
		}
		for j := range c.Events {
			if strings.EqualFold(c.Events[j].Status, "resolved") {
				continue
			}
			labels := map[string]string{"create_ticket": "snow"}
			for k, v := range c.Events[j].Labels {
				labels[k] = v
			}
			c.Events[j].Labels = labels
			break
		}
	}
}
