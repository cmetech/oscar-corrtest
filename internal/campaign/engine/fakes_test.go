package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/resend"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runtime"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// speed is how much faster than wall time the scaled clock runs.
const speed = 50

// scaledClock is the fake clock: campaign time runs speed x wall time, so
// a scenario with phases in seconds finishes in a few wall seconds.
type scaledClock struct{ base, start time.Time }

func newClock() *scaledClock {
	return &scaledClock{base: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), start: time.Now()}
}

func (c *scaledClock) Now() time.Time {
	return c.base.Add(time.Since(c.start) * speed)
}

func (c *scaledClock) SleepUntil(ctx context.Context, t time.Time) bool {
	d := t.Sub(c.Now())
	if d <= 0 {
		return ctx.Err() == nil
	}
	tm := time.NewTimer(d / speed)
	defer tm.Stop()
	select {
	case <-tm.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// laneClock is the scaled clock as the worker lane sees it. The scaled clock
// multiplies timer overshoot and per-send processing time (sqlite, HTTP) by
// speed, so a parallel test run would record pacing misses a real clock never
// produces. Worker time is therefore the last instant the lane waited for:
// Now() never runs ahead of the latest SleepUntil target. (Pacing misses are
// covered by the lane package's own tests.)
type laneClock struct {
	*scaledClock
	mu    sync.Mutex
	floor time.Time
}

func (c *laneClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.scaledClock.Now()
	if !c.floor.IsZero() && c.floor.Before(now) {
		return c.floor
	}
	return now
}

func (c *laneClock) SleepUntil(ctx context.Context, t time.Time) bool {
	ok := c.scaledClock.SleepUntil(ctx, t)
	c.mu.Lock()
	if ok && t.After(c.floor) {
		c.floor = t
	}
	c.mu.Unlock()
	return ok
}

// testScenario is a Custom scenario with minutes scaled to seconds.
func testScenario() scenario.Scenario {
	s := time.Second
	n := func(i int) *int { return &i }
	return scenario.Scenario{ID: "engine-test", Version: "v1", Custom: true,
		Phases: []scenario.Phase{{Name: "preflight", Start: -10 * s}, {Name: "baseline", End: 30 * s}, {Name: "correlation", Start: 30 * s, End: 60 * s},
			{Name: "ticketing", Start: 60 * s, End: 90 * s}, {Name: "resend", Start: 90 * s, End: 120 * s}, {Name: "drain", Start: 120 * s, End: 150 * s}},
		Baseline: scenario.BaselineSpec{Rate: 0.25, UniqueRatio: 0.9, PoolSize: 5, Instances: 3, Severity: map[string]float64{"warning": 1},
			ResolveAfter: 5 * s, TicketEveryNth: 5, TicketFrom: 60 * s, TicketTo: 120 * s},
		Correlation: scenario.CorrelationSpec{
			SetA:             []scenario.CaseRef{{Pattern: "flood", Level: "builtin"}, {Pattern: "threshold", Level: "builtin"}},
			SetB:             []scenario.CaseRef{{Pattern: "flood", Level: "builtin", Case: "P01"}},
			TicketedPatterns: []string{"flood", "threshold"}},
		Resend: []scenario.PolicySpec{
			{Key: "R1", Action: "notification", Mode: "time", Interval: 4 * s, MaxResends: n(2), Cohort: 1, Notifier: "CAMPAIGN_SINK"},
			{Key: "R2", Action: "notification", Mode: "occurrences", Every: 3, Fresh: true, Cohort: 1, Notifier: "CAMPAIGN_SINK"},
			{Key: "R3", Action: "notification", Mode: "either", Interval: 6 * s, Every: 5, MaxResends: n(2), Fresh: true, Cohort: 1, Notifier: "CAMPAIGN_SINK"},
			{Key: "R4", Action: "ticket", Mode: "time", Interval: 8 * s, MaxResends: n(2), Cohort: 1, Notifier: "ALERTHISTORY"},
		},
		Redelivery: 2 * s, ResendUntil: 118 * s,
		FrontOffice: scenario.FrontOfficeSpec{Users: 2, Interval: 2 * s, Jitter: s / 2, SearchEvery: 10 * s},
		Limits: scenario.Limits{HistoryP95: 5 * s, Drain: 60 * s, ContainerSlopeMiBPerHour: 100, FrontOfficeP95: 2 * s, FrontOfficeP99: 5 * s,
			CorrelationSuccess: 1, TicketSuccess: 1, ResendSuccess: 1, RegressionPct: 20, QuerySetVersion: "q1"},
		Precheck: scenario.PrecheckLimits{IdleQueueDepth: 50},
		Seed:     7,
	}
}

type fakePolicy struct {
	def             targetops.PolicyDefinition
	enabled, active bool
	deleted         bool
}

type delivery struct {
	at     time.Time
	labels map[string]string
}

// fakeOSCAR models the target: native ingest and front-office HTTP, plus the
// Target, SNOW, Sink and Metrics interfaces. Firings reach History and the
// sink; create_ticket=snow opens an incident; a resolve adds OSCAR's comment.
type fakeOSCAR struct {
	t     *testing.T
	clock *scaledClock
	s     scenario.Scenario
	srv   *httptest.Server

	mu         sync.Mutex
	notifiers  map[string]*targetops.Notifier
	policies   map[string]*fakePolicy
	revs       map[string]int
	history    map[string]*oscar.HistoryRecord
	receipts   []targetops.Receipt
	incidents  map[string]*targetops.Incident // by alertname
	cohort     map[string][]delivery          // controller cohort deliveries
	posts      map[string]int                 // cohort firing posts per alertname
	laneFires  []time.Time
	allFirings int
	restartAt  time.Time             // probe reports a restart from here
	ambiguous  string                // first cohort post of this alertname has an unknown outcome
	corrInc    []*targetops.Incident // set B source-alert incidents
	lastFO     time.Time             // last front-office request
	// fault injection
	withholdComments bool   // resolves never get OSCAR's comment
	listErr          int    // next N campaign incident listings fail (-1: always)
	failResolves     bool   // cohort resolve posts get HTTP 500
	onSetNotifier    func() // called before each notifier change
	sinkErr          bool   // Receipts fails
	failEnable       int    // next N notifier enables fail
	draftErr         error  // CreateDraft fails with this
	db               *storage.Database
	correlates       atomic.Int32
	slow             time.Duration
	onCorr           func()
}

func newFakeOSCAR(t *testing.T, clock *scaledClock, s scenario.Scenario) *fakeOSCAR {
	f := &fakeOSCAR{t: t, clock: clock, s: s, policies: map[string]*fakePolicy{}, revs: map[string]int{},
		history: map[string]*oscar.HistoryRecord{}, incidents: map[string]*targetops.Incident{}, cohort: map[string][]delivery{}, posts: map[string]int{},
		notifiers: map[string]*targetops.Notifier{}}
	for _, n := range []targetops.Notifier{{ID: "n1", Name: "EMAIL", Status: "enabled"}, {ID: "n2", Name: "SLACK", Status: "enabled"},
		{ID: "n3", Name: "ALERTHISTORY", Status: "enabled"}, {ID: "n4", Name: "LOGGER", Status: "enabled"},
		{ID: "n5", Name: "CAMPAIGN_SINK", Status: "disabled"}, {ID: "n6", Name: "PAGER", Status: "disabled"}} {
		f.notifiers[n.ID] = &n
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOSCAR) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/api/v2/alerts") {
		var alerts []struct {
			Labels map[string]string `json:"labels"`
			EndsAt string            `json:"endsAt"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &alerts); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		for _, a := range alerts {
			f.ingest(a.Labels, a.EndsAt != "", true)
		}
		w.WriteHeader(200)
		return
	}
	f.mu.Lock()
	f.lastFO = f.clock.Now()
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"records":[],"total_records":0}`)) // front office
}

// ingest applies one alert to the model.
func (f *fakeOSCAR) ingest(labels map[string]string, resolved, lane bool) {
	now := f.clock.Now().UTC()
	name := labels["alertname"]
	f.mu.Lock()
	defer f.mu.Unlock()
	if resolved {
		if h := f.history[name]; h != nil {
			h.Status = "resolved"
		}
		if inc := f.incidents[name]; inc != nil && !f.withholdComments {
			inc.Comments = append(inc.Comments, targetops.Comment{Value: "[OSCAR AUTO-UPDATE 1] Alert has been resolved"})
		}
		return
	}
	f.allFirings++
	if lane {
		f.laneFires = append(f.laneFires, now)
	}
	if f.history[name] == nil {
		f.history[name] = &oscar.HistoryRecord{AlertName: name, Status: "firing", CreatedAt: now.Add(50 * time.Millisecond), Labels: labels}
		if f.notifiers["n5"].Status == "enabled" {
			f.receipts = append(f.receipts, targetops.Receipt{ReceivedAt: now.Add(100 * time.Millisecond),
				Alerts: []targetops.SinkAlert{{AlertName: name, Status: "firing", Labels: labels}}})
		}
		if labels["create_ticket"] == "snow" && f.incidents[name] == nil {
			f.incidents[name] = &targetops.Incident{Number: fmt.Sprintf("INC%07d", len(f.incidents)+1), SysID: name, State: "1",
				ShortDescription: "OSCAR " + name}
		}
	}
	f.history[name].Status = "firing"
}

// --- Target ---

func (f *fakeOSCAR) Notifiers(context.Context) ([]targetops.Notifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []targetops.Notifier
	for _, n := range f.notifiers {
		out = append(out, *n)
	}
	return out, nil
}

func (f *fakeOSCAR) SetNotifier(ctx context.Context, id string, on bool) (targetops.Notifier, error) {
	if f.onSetNotifier != nil {
		f.onSetNotifier()
	}
	if ctx.Err() != nil { // like the real client: a cancelled request fails
		return targetops.Notifier{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.notifiers[id]
	if n == nil {
		return targetops.Notifier{}, errors.New("no notifier")
	}
	if on && f.failEnable > 0 {
		f.failEnable--
		return targetops.Notifier{}, errors.New("transient 502")
	}
	n.Status = statusWord(on)
	return *n, nil
}

func (f *fakeOSCAR) bump(nid, action string, rev int) (int, error) {
	k := nid + "/" + action
	if rev != f.revs[k] {
		return 0, &targetops.ConflictError{Code: "RESEND_POLICY_REQUEST_FAILED", Detail: "stale revision"}
	}
	f.revs[k]++
	return f.revs[k], nil
}

func (f *fakeOSCAR) CreateDraft(_ context.Context, def targetops.PolicyDefinition, rev int) (targetops.Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.draftErr != nil {
		return targetops.Policy{}, f.draftErr
	}
	nr, err := f.bump(def.NotifierID, def.Action, rev)
	if err != nil {
		return targetops.Policy{}, err
	}
	id := fmt.Sprintf("pol-%d", len(f.policies)+1)
	f.policies[id] = &fakePolicy{def: def}
	return targetops.Policy{ID: id, Revision: nr, Status: "draft"}, nil
}

func (f *fakeOSCAR) Activate(_ context.Context, id, nid, action string, rev int, _ time.Duration) (targetops.Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	nr, err := f.bump(nid, action, rev)
	if err != nil {
		return targetops.Policy{}, err
	}
	f.policies[id].enabled, f.policies[id].active = true, true
	return targetops.Policy{ID: id, Revision: nr, Status: "active"}, nil
}

func (f *fakeOSCAR) Deactivate(_ context.Context, id, nid, action string, rev int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	nr, err := f.bump(nid, action, rev)
	if err == nil {
		f.policies[id].enabled = false
	}
	return nr, err
}

func (f *fakeOSCAR) Delete(_ context.Context, id, nid, action string, rev int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	nr, err := f.bump(nid, action, rev)
	if err == nil {
		f.policies[id].deleted = true
	}
	return nr, err
}

func (f *fakeOSCAR) specFor(lane string) scenario.PolicySpec {
	for _, p := range f.s.Resend {
		if laneFor(p) == lane {
			return p
		}
	}
	return scenario.PolicySpec{}
}

// expected runs the resend model over a cohort member's deliveries.
func (f *fakeOSCAR) expected(name string) (scenario.PolicySpec, time.Time, resend.Expected) {
	ds := f.cohort[name]
	spec := f.specFor(ds[0].labels["campaign_lane"])
	var model []resend.Delivery
	for _, d := range ds {
		model = append(model, resend.Delivery{At: d.at.Sub(ds[0].at), Fresh: spec.Fresh})
	}
	exp, err := resend.Expect(spec, model)
	if err != nil {
		f.t.Errorf("resend model: %v", err)
	}
	return spec, ds[0].at, exp
}

func (f *fakeOSCAR) PolicyStatus(_ context.Context, nid, action, policyID string) (targetops.PolicyStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := targetops.PolicyStatus{Revision: f.revs[nid+"/"+action], Enrollment: "enrolled"}
	if p := f.policies[policyID]; p != nil {
		st.Enabled = p.enabled && !p.deleted
		for name := range f.cohort {
			if spec, _, exp := f.expected(name); spec.Key != "" && strings.HasSuffix(p.def.Name, "-"+spec.Key) && exp.Exhausted {
				st.Episodes.Exhausted++
			}
		}
	}
	return st, nil
}

func (f *fakeOSCAR) PostAlert(_ context.Context, a Alert) (int, error) {
	f.mu.Lock()
	firing := a.EndsAt.IsZero()
	if firing {
		f.posts[a.Name]++
	}
	if !firing && f.failResolves {
		f.mu.Unlock()
		return 500, errors.New("HTTP 500")
	}
	amb := firing && a.Name == f.ambiguous && f.posts[a.Name] == 1
	if !amb && a.EndsAt.IsZero() {
		f.cohort[a.Name] = append(f.cohort[a.Name], delivery{at: f.clock.Now().UTC(), labels: a.Labels})
	}
	f.mu.Unlock()
	if amb {
		return 0, errors.New("context deadline exceeded")
	}
	f.ingest(a.Labels, !a.EndsAt.IsZero(), false)
	return 200, nil
}

func (f *fakeOSCAR) History(_ context.Context, run string, _, _ time.Time) ([]oscar.HistoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []oscar.HistoryRecord
	for _, h := range f.history {
		if h.Labels["campaign_run"] == run {
			out = append(out, *h)
		}
	}
	return out, nil
}

// --- SNOW ---

func (f *fakeOSCAR) OpenCampaignIncidents(context.Context, string) ([]targetops.Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != 0 {
		if f.listErr > 0 {
			f.listErr--
		}
		return nil, errors.New("snow: 503 instance hibernating")
	}
	var out []targetops.Incident
	for name, inc := range f.incidents {
		if inc.State == "6" || inc.State == "7" {
			continue
		}
		c := *inc
		if ds := f.cohort[name]; len(ds) > 0 { // R4 resends land as incident comments
			_, origin, exp := f.expected(name)
			for _, d := range exp.Resends {
				c.Comments = append(c.Comments, targetops.Comment{Value: "resend_comment", CreatedOn: origin.Add(d).Format("2006-01-02 15:04:05")})
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeOSCAR) OpenCorrTestIncidents(_ context.Context, pattern, token string) ([]targetops.Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []targetops.Incident
	for _, kind := range []string{"TRIGGER", "NON-TRIGGER"} {
		prefix := "[CORRTEST][" + pattern + "][" + kind + "][" + strings.ToUpper(token) + "]"
		for _, inc := range f.corrInc {
			if strings.HasPrefix(inc.ShortDescription, prefix) && inc.State != "6" && inc.State != "7" {
				out = append(out, *inc)
			}
		}
	}
	return out, nil
}

func (f *fakeOSCAR) ListPolicies(_ context.Context, nid, action, name string) ([]targetops.PolicyItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []targetops.PolicyItem
	for id, p := range f.policies {
		if !p.deleted && p.def.NotifierID == nid && p.def.Action == action && strings.Contains(p.def.Name, name) {
			st := "draft"
			if p.enabled {
				st = "active"
			}
			out = append(out, targetops.PolicyItem{ID: id, Name: p.def.Name, Status: st})
		}
	}
	return out, nil
}

func (f *fakeOSCAR) Resolve(_ context.Context, number, _ string) (targetops.Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inc := range f.corrInc {
		if inc.Number == number {
			inc.State = "6"
			return *inc, nil
		}
	}
	for _, inc := range f.incidents {
		if inc.Number == number {
			inc.State = "6"
			return *inc, nil
		}
	}
	return targetops.Incident{}, errors.New("no incident")
}

// --- Sink ---

func (f *fakeOSCAR) Receipts(context.Context, string, string, time.Time) ([]targetops.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sinkErr {
		return nil, errors.New("sink unavailable")
	}
	out := append([]targetops.Receipt{}, f.receipts...)
	for name := range f.cohort {
		spec, origin, exp := f.expected(name)
		if spec.Action != "notification" {
			continue
		}
		for _, d := range exp.Resends {
			out = append(out, targetops.Receipt{ReceivedAt: origin.Add(d), Alerts: []targetops.SinkAlert{{AlertName: name, Status: "firing"}}})
		}
	}
	return out, nil
}

// --- Metrics ---

var containerNames = []string{"oscar-alertmanager_worker-1", "oscar-notifier-1", "oscar-handoff-1", "oscar-history-1", "oscar-taskmanager-1"}

func (f *fakeOSCAR) Query(_ context.Context, q string, at time.Time) (targetops.Vector, error) {
	ts := float64(at.UnixMilli()) / 1000
	m := map[string]string{"queue": "alerts"}
	return targetops.Vector{{Metric: m, Time: ts, Value: 0}}, nil
}

func (f *fakeOSCAR) QueryRange(_ context.Context, q string, start, end time.Time, step time.Duration) (targetops.Matrix, error) {
	var pts []targetops.Point
	value := 0.0
	if strings.Contains(q, "container_memory") {
		value = 100 << 20
	}
	for t := start; !t.After(end); t = t.Add(step) {
		pts = append(pts, targetops.Point{Time: float64(t.Unix()), Value: value})
	}
	pts = append(pts, targetops.Point{Time: float64(end.Unix()), Value: value})
	if strings.Contains(q, "container_memory") {
		var m targetops.Matrix
		for _, n := range containerNames {
			m = append(m, targetops.Series{Metric: map[string]string{"name": n}, Values: pts})
		}
		return m, nil
	}
	return targetops.Matrix{{Metric: map[string]string{"queue": "alerts"}, Values: pts}}, nil
}

func (f *fakeOSCAR) Snapshot(context.Context, map[string]string, time.Time, time.Time, time.Duration) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (f *fakeOSCAR) probe(context.Context) (targetops.ProbeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	restarts := 0
	if !f.restartAt.IsZero() && !f.clock.Now().Before(f.restartAt) {
		restarts = 1
	}
	return targetops.ProbeResult{Containers: []targetops.ProbeContainer{{Name: "oscar-notifier-1", RestartCount: restarts}}}, nil
}

// correlate stands in for the runtime runner: it stores a real run row, opens
// an incident per create_ticket source (compiler summary format) and adds
// OSCAR's resolution comment when the run's cleanup resolves its sources.
func (f *fakeOSCAR) correlate(ctx context.Context, _ string, doc corr.Scenario, _ string) (domain.Run, error) {
	n := f.correlates.Add(1)
	if f.onCorr != nil {
		f.onCorr()
	}
	start := f.clock.Now()
	id := fmt.Sprintf("crt_%08dABCDEFGHJKMNPQRS", n)
	short := id[4:12]
	if err := f.db.CreateRun(context.WithoutCancel(ctx), domain.Run{ID: id, ShortToken: short, Status: domain.RunQueued,
		CleanupStatus: domain.CleanupNotRequired, HarnessVersion: "test", CreatedAt: start, UpdatedAt: start}); err != nil {
		f.t.Errorf("create run: %v", err)
	}
	var mine []*targetops.Incident
	f.mu.Lock()
	for _, c := range doc.Cases {
		for _, e := range c.Events {
			if e.Labels["create_ticket"] == "snow" {
				inc := &targetops.Incident{Number: fmt.Sprintf("INC9%06d", len(f.corrInc)+1), State: "1",
					ShortDescription: fmt.Sprintf("[CORRTEST][%s][TRIGGER][%s] %s: source alert 1 of 2", doc.Pattern, short, c.Name)}
				f.corrInc = append(f.corrInc, inc)
				mine = append(mine, inc)
			}
		}
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		for _, inc := range mine {
			inc.Comments = append(inc.Comments, targetops.Comment{Value: "[OSCAR AUTO-UPDATE 1] Alert has been resolved"})
		}
		f.mu.Unlock()
	}()
	d := 3 * time.Second
	if f.slow > 0 {
		d = f.slow
	}
	f.clock.SleepUntil(ctx, start.Add(d))
	end := f.clock.Now()
	return domain.Run{ID: id, ShortToken: short, Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean, StartedAt: &start, EndedAt: &end}, nil
}

// fakeWorker is the in-process worker: the real C7 Manager, reachable until
// down is set (then every call fails like a lost network).
type fakeWorker struct {
	m      *worker.Manager
	down   atomic.Bool
	onStop func()
	// stuck: the next N resolve-all calls report one identity still open
	// without resolving anything (-1: always).
	stuck    atomic.Int32
	resolves atomic.Int32 // resolve-all calls
}

var errDown = errors.New("dial tcp: connection refused")

func (w *fakeWorker) Start(ctx context.Context, s workerapi.LaneSpec) (workerapi.LaneStatus, error) {
	if w.down.Load() {
		return workerapi.LaneStatus{}, errDown
	}
	return w.m.Start(ctx, s)
}
func (w *fakeWorker) Heartbeat(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	if w.down.Load() {
		return workerapi.LaneStatus{}, errDown
	}
	return w.m.Heartbeat(ctx, id)
}
func (w *fakeWorker) Status(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	if w.down.Load() {
		return workerapi.LaneStatus{}, errDown
	}
	return w.m.Status(ctx, id)
}
func (w *fakeWorker) Stop(ctx context.Context, id string) (workerapi.LaneStatus, error) {
	if w.down.Load() {
		return workerapi.LaneStatus{}, errDown
	}
	if w.onStop != nil {
		w.onStop()
	}
	if err := w.m.Stop(ctx, id); err != nil {
		return workerapi.LaneStatus{}, err
	}
	return w.m.Status(ctx, id)
}
func (w *fakeWorker) ResolveAll(ctx context.Context, id string) (workerapi.ResolveResult, error) {
	w.resolves.Add(1)
	if w.down.Load() {
		return workerapi.ResolveResult{}, errDown
	}
	if n := w.stuck.Load(); n != 0 {
		if n > 0 {
			w.stuck.Add(-1)
		}
		return workerapi.ResolveResult{Remaining: 1, Error: "resolve x: status 503"}, nil
	}
	return w.m.ResolveAll(ctx, id)
}
func (w *fakeWorker) Ledger(ctx context.Context, id string, out io.Writer) error {
	if w.down.Load() {
		return errDown
	}
	return w.m.Ledger(ctx, id, out)
}

// harness wires an engine to the fakes over a real sqlite store.
type harness struct {
	t      *testing.T
	clock  *scaledClock
	db     *storage.Database
	oscar  *fakeOSCAR
	worker *fakeWorker
	deps   Deps
	s      scenario.Scenario
	id     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := newClock()
	s := testScenario()
	f := newFakeOSCAR(t, clock, s)
	f.db = db
	target := domain.Target{ID: "t1", BaseURL: f.srv.URL}
	m := worker.NewManager(laneStore{storage.NewLaneLedger(db), db}, target, &laneClock{scaledClock: clock}, 30*time.Second)
	t.Cleanup(m.Shutdown)
	w := &fakeWorker{m: m}
	h := &harness{t: t, clock: clock, db: db, oscar: f, worker: w, s: s, id: "camp_" + strings.ReplaceAll(t.Name(), "/", "_")}
	h.deps = Deps{Store: db, Worker: w, Target: f, SNOW: f, Sink: f, Metrics: f, Probe: f.probe,
		Precheck: func(context.Context, scenario.Scenario) []precheck.Result {
			return []precheck.Result{{ID: "B1", Tier: precheck.Blocking, Status: precheck.Pass}}
		},
		TaskRuns: func(_ context.Context, start, end time.Time) ([]targetops.TaskRun, error) {
			if end.Sub(start) == time.Hour { // the pre-run hour
				return []targetops.TaskRun{{Job: "monitor:check_connection:homelab", Status: "SUCCESS", Duration: time.Second}}, nil
			}
			return []targetops.TaskRun{{Job: "monitor:check_connection:homelab", Status: "SUCCESS", Duration: 2 * time.Second},
				{Job: "monitor:check_connection:homelab", Status: "SUCCESS", Duration: 3 * time.Second},
				{Job: "monitor:check_filesystem_space:homelab", Status: "FAILURE", Duration: time.Second}}, nil
		},
		Correlate: f.correlate, TargetID: "t1", IngestURL: f.srv.URL, FrontOfficeURL: f.srv.URL, FrontOfficeHTTP: f.srv.Client(),
		FrontOfficeUsers: []frontoffice.User{{Name: "fo-01", APIKey: "k1"}, {Name: "fo-02", APIKey: "k2"}}, ReportDir: t.TempDir()}
	if err := db.CreateCampaign(ctx, domain.Campaign{ID: h.id, ScenarioID: s.ID, ScenarioHash: s.Hash(), Custom: true, Trigger: "cli",
		RunBy: "agent", Status: domain.CampaignPrecheck, StartedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	return h
}

type laneStore struct {
	lane.Ledger
	db *storage.Database
}

func (s laneStore) LaneLedger(ctx context.Context, id string) ([]domain.LaneRow, error) {
	return s.db.LaneLedger(ctx, id)
}

func (h *harness) run(ctx context.Context) error {
	return New(h.deps, h.clock).Run(ctx, h.id, runtime.CampaignOptions{Scenario: h.s, Trigger: "cli", RunBy: "agent"})
}

func (h *harness) campaign() domain.Campaign {
	c, err := h.db.GetCampaign(context.Background(), h.id)
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

func (h *harness) scorecard() (c domain.Campaign, card struct {
	Verdict   domain.Verdict
	Reasons   []string
	Headlines []struct{ Name string }
	Tickets   struct{ Expected, Succeeded, Unknown int }
	Baseline  struct{ Planned, Offered int }
	Homelab   struct {
		Completed, Failed int
		PreP95            float64
		Collected         bool
		NotCollected      string
	}
}) {
	c = h.campaign()
	if c.Verdict != domain.VerdictPass {
		evs, _ := h.db.RecentCampaignEvents(context.Background(), h.id, 400)
		for _, ev := range evs {
			if ev.Level != "info" {
				h.t.Logf("event %s %s: %s", ev.Level, ev.Type, ev.Summary)
			}
		}
	}
	if err := json.Unmarshal(c.Scorecard, &card); err != nil {
		h.t.Fatalf("scorecard: %v (%s)", err, c.Scorecard)
	}
	return c, card
}

// assertRestored checks notifiers, policies and incidents after cleanup.
func (h *harness) assertRestored() {
	h.t.Helper()
	f := h.oscar
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]string{"n1": "enabled", "n2": "enabled", "n3": "enabled", "n4": "enabled", "n5": "disabled", "n6": "disabled"}
	for id, st := range want {
		if f.notifiers[id].Status != st {
			h.t.Errorf("notifier %s is %s, want %s", f.notifiers[id].Name, f.notifiers[id].Status, st)
		}
	}
	for id, p := range f.policies {
		if !p.deleted {
			h.t.Errorf("policy %s (%s) left on the target", id, p.def.Name)
		}
	}
	for name, inc := range f.incidents {
		if inc.State != "6" && inc.State != "7" {
			h.t.Errorf("incident %s for %s not closed (state %s)", inc.Number, name, inc.State)
		}
	}
	for _, inc := range f.corrInc {
		if inc.State != "6" {
			h.t.Errorf("set B incident %s (%s) not closed", inc.Number, inc.ShortDescription)
		}
	}
	for name, h2 := range f.history {
		if h2.Status != "resolved" {
			h.t.Errorf("alert %s still firing", name)
		}
	}
	open, err := h.db.OpenMutations(context.Background(), h.id)
	if err != nil || len(open) > 0 {
		h.t.Errorf("open mutations: %v %+v", err, open)
	}
}
