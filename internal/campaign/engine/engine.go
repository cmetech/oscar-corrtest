// Package engine runs a campaign: pre-checks, notifier isolation, policy
// drafts, the remote baseline lane, correlation sets, resend cohorts, guards,
// drain, cleanup and scoring. It also recovers interrupted campaigns.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runtime"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// MinVersion is the oldest OSCAR build a campaign accepts.
const MinVersion = "26.4.6"

// Fixed engine cadences (spec section 8).
const (
	guardEvery     = 15 * time.Second
	heartbeatEvery = 5 * time.Second
	metricStep     = 15 * time.Second
	maxQueueWait   = 30 * time.Minute
	activationWait = 5 * time.Minute
	correlationRun = "phase_b_dispatch"
)

// Store is the campaign repository subset the engine uses (*sqlite.Database).
type Store interface {
	GetCampaign(ctx context.Context, id string) (domain.Campaign, error)
	AppendCampaignEvent(ctx context.Context, id, typ, level, summary string, detail json.RawMessage, at time.Time) error
	RecentCampaignEvents(ctx context.Context, id string, limit int) ([]domain.CampaignEvent, error)
	TransitionCampaign(ctx context.Context, id string, next domain.CampaignStatus, at time.Time, summary string) error
	RecordPrecheck(ctx context.Context, id string, r domain.PrecheckResult) error
	LinkCampaignRun(ctx context.Context, campaignID, runID, kind string) error
	RecordMutation(ctx context.Context, m domain.CampaignMutation) (int64, error)
	MarkMutationRestored(ctx context.Context, id int64, at time.Time) error
	OpenMutations(ctx context.Context, campaignID string) ([]domain.CampaignMutation, error)
	CompleteCampaign(ctx context.Context, id string, v domain.Verdict, cleanup domain.CleanupStatus, comparable bool, scorecard, report json.RawMessage, at time.Time) error
}

// Worker is the controller's view of the remote worker (*workerclient.Client).
type Worker interface {
	Start(ctx context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error)
	Heartbeat(ctx context.Context, id string) error
	Status(ctx context.Context, id string) (workerapi.LaneStatus, error)
	Stop(ctx context.Context, id string) (workerapi.LaneStatus, error)
	ResolveAll(ctx context.Context, id string) (int, error)
	Ledger(ctx context.Context, id string, w io.Writer) error
}

// Alert is one controller-sent alert (cohort member).
type Alert struct {
	Name     string
	Labels   map[string]string
	StartsAt time.Time
	EndsAt   time.Time // zero = firing
}

// Target is the OSCAR-side surface: notifiers, resend policies, the cohort
// route and History. The real implementation binds *oscar.Client.
type Target interface {
	Notifiers(ctx context.Context) ([]targetops.Notifier, error)
	SetNotifier(ctx context.Context, id string, enabled bool) (targetops.Notifier, error)
	CreateDraft(ctx context.Context, def targetops.PolicyDefinition, rev int) (targetops.Policy, error)
	Activate(ctx context.Context, id, notifierID, action string, rev int, wait time.Duration) (targetops.Policy, error)
	Deactivate(ctx context.Context, id, notifierID, action string, rev int) (int, error)
	Delete(ctx context.Context, id, notifierID, action string, rev int) (int, error)
	PolicyStatus(ctx context.Context, notifierID, action, policyID string) (targetops.PolicyStatus, error)
	// PostAlert sends one alert on the cohort route. status is the HTTP
	// status, 0 when the outcome is unknown (timeout, transport error).
	PostAlert(ctx context.Context, a Alert) (status int, err error)
	History(ctx context.Context, campaignRun string, start, end time.Time) ([]oscar.HistoryRecord, error)
}

// SNOW is the PDI surface (*targetops.SNOW).
type SNOW interface {
	OpenCampaignIncidents(ctx context.Context, campaignRun string) ([]targetops.Incident, error)
	Resolve(ctx context.Context, number, note string) (targetops.Incident, error)
}

// Sink is the receipt capture (*targetops.Sink).
type Sink interface {
	Receipts(ctx context.Context, capture, campaignRun string, since time.Time) ([]targetops.Receipt, error)
}

// Metrics is the PromQL surface (*targetops.Metrics).
type Metrics interface {
	Query(ctx context.Context, promql string, at time.Time) (targetops.Vector, error)
	QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (targetops.Matrix, error)
	Snapshot(ctx context.Context, queries map[string]string, start, end time.Time, step time.Duration) (json.RawMessage, error)
}

// Deps wires the engine to its collaborators.
type Deps struct {
	Store   Store
	Worker  Worker
	Target  Target
	SNOW    SNOW
	Sink    Sink
	Metrics Metrics
	Probe   func(ctx context.Context) (targetops.ProbeResult, error)
	// Precheck runs the C10 checks (precheck.Run with the real adapter).
	Precheck func(ctx context.Context, s scenario.Scenario) []precheck.Result
	// Correlate is the runtime's executeScenarioForCampaign.
	Correlate func(ctx context.Context, targetID string, s corr.Scenario, mode string) (domain.Run, error)

	TargetID         string
	IngestURL        string // target base URL the worker lane posts to
	CampaignNotifier string // CAMPAIGN_SINK
	FrontOfficeURL   string
	FrontOfficeHTTP  *http.Client
	FrontOfficeUsers []frontoffice.User
	ReportDir        string // ledger files; default os.TempDir()
}

// Engine implements runtime.CampaignEngine.
type Engine struct {
	d     Deps
	clock lane.Clock
}

var _ runtime.CampaignEngine = (*Engine)(nil)

// New returns an engine. clock drives every phase, cadence and timestamp.
func New(d Deps, clock lane.Clock) *Engine {
	if d.CampaignNotifier == "" {
		d.CampaignNotifier = "CAMPAIGN_SINK"
	}
	return &Engine{d: d, clock: clock}
}

// run is the state of one campaign execution.
type run struct {
	e      *Engine
	id     string
	s      scenario.Scenario
	opts   runtime.CampaignOptions
	t0     time.Time
	laneID string

	comparable bool
	sinkID     string
	notifiers  map[string]string // name -> id

	mu            sync.Mutex
	trip          string // guard name that tripped
	workerLost    bool
	lastHeartbeat time.Time
	ticketUnknown int
	policies      []*policyState
	resendIncon   string
	cohorts       map[string]*cohortMember // alertname -> member
	corrRuns      []score.CorrRun
	notRun        []string
	plannedCorr   int
	fo            map[string][]score.FOSample
	preTasks      []targetops.TaskRun
}

type policyState struct {
	spec       scenario.PolicySpec
	id, name   string
	notifierID string
	rev        int
	active     bool
	activeAt   time.Time
}

type cohortMember struct {
	alert      Alert
	policy     *policyState
	origin     time.Time
	deliveries []resend.Delivery
	ambiguous  bool
	recorded   bool
	resolved   bool
}

func (r *run) store() Store   { return r.e.d.Store }
func (r *run) now() time.Time { return r.e.clock.Now().UTC() }

func (r *run) at(name string, end bool) time.Time {
	p := r.s.Phase(name)
	if end {
		return r.t0.Add(p.End)
	}
	return r.t0.Add(p.Start)
}

func (r *run) event(ctx context.Context, typ, level, summary string, detail any) {
	var raw json.RawMessage
	if detail != nil {
		raw, _ = json.Marshal(detail)
	}
	_ = r.store().AppendCampaignEvent(ctx, r.id, typ, level, summary, raw, r.now())
}

func (r *run) transition(ctx context.Context, next domain.CampaignStatus, why string) error {
	return r.store().TransitionCampaign(ctx, r.id, next, r.now(), why)
}

func (r *run) tripped() string { r.mu.Lock(); defer r.mu.Unlock(); return r.trip }

// Run executes a campaign. Drain and cleanup run on a non-cancellable
// context: ctx is cancelled on Stop and on Close, and the stored status
// (STOPPING vs RUNNING) tells them apart.
func (e *Engine) Run(ctx context.Context, id string, o runtime.CampaignOptions) error {
	stable := context.WithoutCancel(ctx)
	r := &run{e: e, id: id, s: o.Scenario, opts: o, laneID: id + "-baseline", cohorts: map[string]*cohortMember{}, fo: map[string][]score.FOSample{}}
	sc, _ := json.Marshal(o.Scenario)
	_ = e.d.Store.AppendCampaignEvent(stable, id, scenarioEvent, "info", "campaign scenario recorded", sc, r.now())

	results := e.d.Precheck(context.WithValue(ctx, campaignIDKey{}, id), o.Scenario)
	ok, comparable, reason := precheck.Startable(results, o.Acknowledged)
	r.comparable = comparable
	detail, _ := json.Marshal(map[string]any{"results": results, "reason": reason, "comparable": comparable})
	if err := e.d.Store.RecordPrecheck(stable, id, domain.PrecheckResult{Passed: ok, Detail: detail, At: r.now()}); err != nil {
		return fmt.Errorf("record pre-checks: %w", err)
	}
	if ctx.Err() != nil { // stopped during pre-checks: StopCampaign set INTERRUPTED
		return e.Recover(stable, id)
	}
	if !ok || o.PrecheckOnly {
		v := domain.VerdictPass
		if !ok {
			v = domain.VerdictInconclusive
		}
		rep, _ := json.Marshal(map[string]any{"precheckOnly": o.PrecheckOnly, "startable": ok, "reason": reason, "results": results})
		return e.d.Store.CompleteCampaign(stable, id, v, domain.CleanupNotRequired, comparable, nil, rep, r.now())
	}
	if err := r.transition(stable, domain.CampaignRunning, "pre-checks passed"); err != nil {
		return err
	}
	return r.execute(ctx)
}

const (
	scenarioEvent = "campaign.scenario"
	maxEvents     = 1 << 20 // read the whole event log
)

// Recover drains and cleans up an interrupted campaign with verdict
// INCONCLUSIVE. It never restarts the lane or the cohorts.
func (e *Engine) Recover(ctx context.Context, id string) error {
	ctx = context.WithoutCancel(ctx)
	r := &run{e: e, id: id, laneID: id + "-baseline", cohorts: map[string]*cohortMember{}, fo: map[string][]score.FOSample{}}
	c, err := e.d.Store.GetCampaign(ctx, id)
	if err != nil {
		return err
	}
	if c.Status == domain.CampaignCompleted {
		return nil
	}
	if c.Status != domain.CampaignInterrupted && c.Status != domain.CampaignRecovering {
		if err := r.transition(ctx, domain.CampaignInterrupted, "recovery requested"); err != nil {
			return err
		}
	}
	if c.Status != domain.CampaignRecovering {
		if err := r.transition(ctx, domain.CampaignRecovering, "recovering interrupted campaign"); err != nil {
			return err
		}
	}
	r.t0 = c.StartedAt
	r.opts = runtime.CampaignOptions{Trigger: c.Trigger, RunBy: c.RunBy}
	if s, ok := r.recordedScenario(ctx); ok {
		r.s = s
	}
	if t0, ok := r.recordedT0(ctx); ok {
		r.t0 = t0
	}
	return r.finish(ctx, "interrupted: recovered after restart")
}

func (r *run) recordedScenario(ctx context.Context) (scenario.Scenario, bool) {
	evs, err := r.store().RecentCampaignEvents(ctx, r.id, maxEvents)
	if err != nil {
		return scenario.Scenario{}, false
	}
	for _, ev := range evs {
		if ev.Type == scenarioEvent {
			var s scenario.Scenario
			if json.Unmarshal(ev.Detail, &s) == nil {
				return s, true
			}
		}
	}
	return scenario.Scenario{}, false
}

func (r *run) recordedT0(ctx context.Context) (time.Time, bool) {
	evs, err := r.store().RecentCampaignEvents(ctx, r.id, maxEvents)
	if err != nil {
		return time.Time{}, false
	}
	for _, ev := range evs {
		if ev.Type == "campaign.t0" {
			var d struct{ T0 time.Time }
			if json.Unmarshal(ev.Detail, &d) == nil {
				return d.T0, true
			}
		}
	}
	return time.Time{}, false
}

// execute runs steps 2-9 once the campaign is RUNNING.
func (r *run) execute(ctx context.Context) error {
	stable := context.WithoutCancel(ctx)
	phaseCtx, stopPhases := context.WithCancel(ctx)
	defer stopPhases()

	if err := r.isolate(phaseCtx); err != nil {
		r.event(stable, "campaign.error", "error", "isolation failed: "+err.Error(), nil)
		return r.abort(ctx, "isolation failed")
	}
	if err := r.createDrafts(phaseCtx); err != nil {
		r.event(stable, "campaign.error", "error", "policy drafts failed: "+err.Error(), nil)
		return r.abort(ctx, "policy drafts failed")
	}
	if err := r.startLane(phaseCtx); err != nil {
		r.event(stable, "campaign.error", "error", "lane start failed: "+err.Error(), nil)
		return r.abort(ctx, "lane start failed")
	}

	var bg sync.WaitGroup
	bg.Add(3)
	go func() { defer bg.Done(); r.heartbeats(phaseCtx) }()
	go func() { defer bg.Done(); r.guards(phaseCtx, stopPhases) }()
	go func() { defer bg.Done(); r.frontOffice(phaseCtx) }()

	var work sync.WaitGroup
	work.Add(2)
	go func() { defer work.Done(); r.correlation(phaseCtx) }()
	go func() { defer work.Done(); r.resendPhase(phaseCtx) }()
	work.Wait()
	// Firings end at the drain phase start (or earlier on a trip/stop).
	r.e.clock.SleepUntil(phaseCtx, r.at("drain", false))
	stopPhases()
	bg.Wait()

	if ctx.Err() != nil && r.tripped() == "" {
		c, err := r.store().GetCampaign(stable, r.id)
		if err != nil || c.Status != domain.CampaignStopping {
			// Close: halt our own traffic and leave drain to recovery.
			r.haltLane(stable)
			return errors.New("runtime closing; campaign left for recovery")
		}
	}
	return r.drainAndFinish(stable)
}

// abort ends a campaign that failed before traffic: no lane, straight to cleanup.
func (r *run) abort(ctx context.Context, why string) error {
	stable := context.WithoutCancel(ctx)
	if err := r.transition(stable, domain.CampaignDraining, why); err != nil {
		return err
	}
	return r.finishAfterDrain(stable, domain.VerdictError, why)
}
