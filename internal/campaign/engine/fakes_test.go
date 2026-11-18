package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
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
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
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
			SetB:             []scenario.CaseRef{{Pattern: "flood", Level: "builtin"}},
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
	lane  *laneClock // the worker lane's clock; stamps History and sink rows for lane posts
	s     scenario.Scenario
	srv   *httptest.Server

	mu         sync.Mutex
	notifiers  map[string]*targetops.Notifier
	policies   map[string]*fakePolicy
	revs       map[string]int
	history    []*oscar.HistoryRecord // one row per occurrence: (am_fingerprint, startsAt)
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
	withholdComments bool // resolves never get OSCAR's comment
	listErr          int  // next N campaign incident listings fail (-1: always)
	failResolves     bool // cohort resolve posts get HTTP 500
	ignoreResolves   bool // middleware resolve posts get HTTP 200 but match nothing
	resolvePosts     []oscar.AlertRequest
	onSetNotifier    func() // called before each notifier change
	corrNoTicket     bool   // ticketed set B runs never open their incident
	sinkErr          bool   // Receipts fails
	failEnable       int    // next N notifier enables fail
	draftErr         error  // CreateDraft fails with this
	db               *storage.Database
	correlates       atomic.Int32
	slow             time.Duration
	onCorr           func()
	// alert rate limits and the scripted intake breaker (v2)
	limitsRev      int
	overrides      json.RawMessage
	limitsRefused  bool // PUT refused (no manage:rate_limits)
	breakerTrip    int  // with forced thresholds, firing posts after this many are refused...
	breakerShut    int  // ...for this many posts, then the breaker reopens
	rejectResolves bool // resolves are refused while tripped (26.4.4)
	forcedPosts    int
	queryValues    map[string]float64 // instant query results by query substring (default 0)
	deadCreated    []string           // "name url" of each dead target created
	queueSeries    map[string]float64 // queue range series by queue_name (constant value); nil: one idle "alerts" series
	// mergePairs models live OSCAR for fresh re-posts: a new startsAt of an open
	// alert merges into its History row (which keeps the first startsAt) as the
	// row's open pair; only a resolve with that pair's startsAt closes the row.
	mergePairs bool
	openPair   map[*oscar.HistoryRecord]time.Time
	// fingerprint policies: while fpOn, alerts named fingerprintAlert leave
	// volatileLabel out of their identity (unless fpNoMerge) and carry fpPub.
	fpSkip                string
	fpOn, fpNoMerge       bool
	fpPub                 string
	fpEnables, fpDisables int
	fpDisableErr          error
}

func newFakeOSCAR(t *testing.T, clock *scaledClock, s scenario.Scenario) *fakeOSCAR {
	f := &fakeOSCAR{t: t, clock: clock, s: s, policies: map[string]*fakePolicy{}, revs: map[string]int{},
		incidents: map[string]*targetops.Incident{}, cohort: map[string][]delivery{}, posts: map[string]int{},
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
			f.ingest(a.Labels, time.Time{}, a.EndsAt != "", true)
		}
		w.WriteHeader(200)
		return
	}
	f.mu.Lock()
	f.lastFO = f.clock.Now()
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"records":[],"total_records":0}`)) // front office
}

// amFingerprint stands in for the native transport fingerprint OSCAR records
// as the am_fingerprint History label.
func amFingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", k, labels[k])
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// ingest applies one alert to the model. Native Alertmanager (lane) keeps one
// active occurrence per fingerprint and resolves it by labels; the middleware
// route records each startsAt as its own occurrence and resolves only the
// exact (am_fingerprint, startsAt) occurrence.
func (f *fakeOSCAR) ingest(labels map[string]string, startsAt time.Time, resolved, lane bool) {
	now := f.clock.Now().UTC()
	// stamp is when the target records the alert. A lane post is stamped on the
	// lane's clock, the clock its ledger SentAt comes from: on the scaled clock
	// the lane's sqlite write and HTTP round trip would count speed x their
	// wall time as History latency (100 ms of a loaded runner = 5 s), which
	// measures the test machine, not the engine.
	stamp := now
	if lane && f.lane != nil {
		stamp = f.lane.Now().UTC()
	}
	name := labels["alertname"]
	fp := amFingerprint(labels)
	f.mu.Lock()
	defer f.mu.Unlock()
	var annotations map[string]string
	if f.fpOn && name == fingerprintAlert && !resolved {
		annotations = map[string]string{score.RevisionAnnotation: f.fpPub}
		if !f.fpNoMerge {
			rest := map[string]string{}
			for k, v := range labels {
				if k != volatileLabel {
					rest[k] = v
				}
			}
			fp = amFingerprint(rest)
		}
	}
	if resolved {
		for _, h := range f.history {
			if h.Labels["am_fingerprint"] == fp && h.Status == "firing" && (lane || h.StartsAt.Equal(startsAt)) {
				h.Status = "resolved"
			}
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
	if f.mergePairs && !lane {
		for _, h := range f.history {
			if h.Labels["am_fingerprint"] == fp && h.Status == "firing" && !h.StartsAt.Equal(startsAt.Truncate(time.Millisecond)) {
				if f.openPair == nil {
					f.openPair = map[*oscar.HistoryRecord]time.Time{}
				}
				f.openPair[h] = startsAt.Truncate(time.Millisecond)
				return
			}
		}
	}
	seen := false
	for _, h := range f.history {
		seen = seen || h.AlertName == name
		if h.Labels["am_fingerprint"] == fp && (lane && h.Status == "firing" || !lane && h.StartsAt.Equal(startsAt)) {
			h.Status = "firing"
			return
		}
	}
	if lane || startsAt.IsZero() {
		startsAt = now
	}
	row := map[string]string{"am_fingerprint": fp, "oscar_fingerprint": fp[:12]}
	for k, v := range labels {
		row[k] = v
	}
	f.history = append(f.history, &oscar.HistoryRecord{ID: fmt.Sprintf("h%d", len(f.history)+1), AlertName: name, Fingerprint: fp[:12], Status: "firing",
		StartsAt: startsAt.Truncate(time.Millisecond), CreatedAt: stamp.Add(50 * time.Millisecond), Labels: row, Annotations: annotations})
	if seen {
		return
	}
	if f.notifiers["n5"].Status == "enabled" {
		f.receipts = append(f.receipts, targetops.Receipt{ReceivedAt: stamp.Add(100 * time.Millisecond),
			Alerts: []targetops.SinkAlert{{AlertName: name, Status: "firing", Labels: labels}}})
	}
	if labels["create_ticket"] == "snow" && f.incidents[name] == nil {
		f.incidents[name] = &targetops.Incident{Number: fmt.Sprintf("INC%07d", len(f.incidents)+1), SysID: name, State: "1",
			ShortDescription: "OSCAR " + name}
	}
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
	if p := f.policies[id]; p == nil || p.deleted { // what OSCAR's middleware answers for a missing policy
		return 0, &targetops.ConflictError{Code: "RESEND_POLICY_REQUEST_FAILED"}
	}
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
		// As OSCAR: episodes.* counts every policy on this notifier/action;
		// only recent_policy_episodes (at most 10) is the selected policy's own.
		for name := range f.cohort {
			spec, _, exp := f.expected(name)
			if spec.Key == "" {
				continue
			}
			owner := f.policyFor(spec.Key)
			if exp.Exhausted && owner != nil && owner.def.NotifierID == nid && owner.def.Action == action {
				st.Episodes.Exhausted++
			}
			if owner == p && f.episodeOpen(name) { // as OSCAR: only open episodes (closed_at IS NULL)
				if len(st.RecentPolicyEpisodes) == 10 {
					st.MorePolicyEpisodes = true
					continue
				}
				st.RecentPolicyEpisodes = append(st.RecentPolicyEpisodes, targetops.PolicyEpisode{StickyExhausted: exp.Exhausted})
			}
		}
	}
	return st, nil
}

// episodeOpen: the member still has a firing History row (f.mu held).
func (f *fakeOSCAR) episodeOpen(name string) bool {
	for _, h := range f.history {
		if h.AlertName == name && h.Status == "firing" {
			return true
		}
	}
	return false
}

// policyFor is the policy whose name ends in -<key> (f.mu held). Deleted
// policies count: OSCAR keeps an episode's selected_rule_id after deletion.
func (f *fakeOSCAR) policyFor(key string) *fakePolicy {
	for _, p := range f.policies {
		if strings.HasSuffix(p.def.Name, "-"+key) {
			return p
		}
	}
	return nil
}

// PostAlert is the middleware route for cohort firings.
// forced reports whether the breaker window's thresholds are in place (f.mu held).
func (f *fakeOSCAR) forced() bool {
	var o struct{ Queue *struct{ High int } }
	return f.s.Breaker != nil && json.Unmarshal(f.overrides, &o) == nil && o.Queue != nil && o.Queue.High == f.s.Breaker.QueueHigh
}

// tripped: the scripted breaker refuses posts (f.mu held).
func (f *fakeOSCAR) tripped() bool {
	return f.forced() && f.breakerTrip > 0 && f.forcedPosts > f.breakerTrip && f.forcedPosts <= f.breakerTrip+f.breakerShut
}

func (f *fakeOSCAR) AlertLimits(context.Context) (targetops.AlertLimits, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.overrides == nil {
		f.overrides = json.RawMessage(`{"per_source":null,"total":null,"webhook":null,"insert":null,"flood":null,"queue":null}`)
	}
	return targetops.AlertLimits{Revision: f.limitsRev, Overrides: f.overrides}, nil
}

func (f *fakeOSCAR) SetAlertLimits(ctx context.Context, rev int, overrides json.RawMessage) (targetops.AlertLimits, error) {
	f.mu.Lock()
	if f.limitsRefused {
		f.mu.Unlock()
		return targetops.AlertLimits{}, &oscar.MachineError{StatusCode: 403, Detail: "manage:rate_limits required"}
	}
	if rev != f.limitsRev {
		f.mu.Unlock()
		return targetops.AlertLimits{}, &oscar.MachineError{StatusCode: 409, Detail: "stale revision"}
	}
	f.limitsRev++
	f.overrides = overrides
	f.forcedPosts = 0
	f.mu.Unlock()
	return f.AlertLimits(ctx)
}

func (f *fakeOSCAR) PostAlert(_ context.Context, a Alert) (int, error) {
	f.mu.Lock()
	if f.forced() {
		f.forcedPosts++
		if f.tripped() {
			f.mu.Unlock()
			return 503, &oscar.MachineError{StatusCode: 503, Detail: "overloaded"}
		}
	}
	f.posts[a.Name]++
	amb := a.Name == f.ambiguous && f.posts[a.Name] == 1
	if !amb && a.Labels["campaign_lane"] != standingLane && a.Labels["campaign_lane"] != fingerprintLane { // not resend cohorts
		f.cohort[a.Name] = append(f.cohort[a.Name], delivery{at: f.clock.Now().UTC(), labels: a.Labels})
	}
	f.mu.Unlock()
	if amb {
		return 0, errors.New("context deadline exceeded")
	}
	f.ingest(a.Labels, a.StartsAt, false, false)
	return 200, nil
}

// PostAlertGroup is the middleware route for History-built resolves.
func (f *fakeOSCAR) PostAlertGroup(_ context.Context, req oscar.AlertGroupRequest) (int, error) {
	f.mu.Lock()
	if f.tripped() && f.rejectResolves {
		f.mu.Unlock()
		return 503, &oscar.MachineError{StatusCode: 503, Detail: "overloaded"}
	}
	f.resolvePosts = append(f.resolvePosts, req.Alerts...)
	fail, ignore := f.failResolves, f.ignoreResolves
	f.mu.Unlock()
	if fail {
		return 500, errors.New("HTTP 500")
	}
	for _, a := range req.Alerts {
		starts, err := time.Parse(time.RFC3339Nano, a.StartsAt)
		if a.Status != "resolved" || err != nil {
			return 422, fmt.Errorf("resolve needs status resolved and startsAt: %+v", a)
		}
		if ignore {
			continue
		}
		f.mu.Lock()
		for _, h := range f.history {
			want := h.StartsAt
			if p, ok := f.openPair[h]; ok {
				want = p
			}
			if h.Labels["am_fingerprint"] == a.Fingerprint && want.Equal(starts) && h.Status == "firing" {
				h.Status = "resolved"
				if inc := f.incidents[h.AlertName]; inc != nil && !f.withholdComments {
					inc.Comments = append(inc.Comments, targetops.Comment{Value: "[OSCAR AUTO-UPDATE 1] Alert has been resolved"})
				}
			}
		}
		f.mu.Unlock()
	}
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
		if ds := f.cohort[name]; len(ds) > 0 { // R4 resends land as incident comments, before any resolution
			_, origin, exp := f.expected(name)
			var resends []targetops.Comment
			for i, d := range exp.Resends {
				resends = append(resends, targetops.Comment{Value: fmt.Sprintf("OSCAR alert still firing: episode 1, occurrence %d.", i+2),
					CreatedOn: origin.Add(d).Format("2006-01-02 15:04:05")})
			}
			c.Comments = append(resends, c.Comments...)
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
	m := map[string]string{"queue_name": "alerts"}
	value := 0.0
	f.mu.Lock()
	for sub, v := range f.queryValues {
		if strings.Contains(q, sub) {
			value = v
		}
	}
	f.mu.Unlock()
	return targetops.Vector{{Metric: m, Time: ts, Value: value}}, nil
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
	f.mu.Lock()
	queues := f.queueSeries
	f.mu.Unlock()
	if len(queues) > 0 && strings.Contains(q, "oscar_celery_queue") {
		var m targetops.Matrix
		for name, v := range queues {
			var vals []targetops.Point
			for _, p := range pts {
				vals = append(vals, targetops.Point{Time: p.Time, Value: v})
			}
			m = append(m, targetops.Series{Metric: map[string]string{"queue_name": name}, Values: vals})
		}
		return m, nil
	}
	return targetops.Matrix{{Metric: map[string]string{"queue_name": "alerts"}, Values: pts}}, nil
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
		CleanupStatus: domain.CleanupNotRequired, HarnessVersion: "test", StartedAt: &start, CreatedAt: start, UpdatedAt: start}); err != nil {
		f.t.Errorf("create run: %v", err)
	}
	var mine []*targetops.Incident
	f.mu.Lock()
	for _, c := range doc.Cases {
		for _, e := range c.Events {
			if e.Labels["create_ticket"] == "snow" && !f.corrNoTicket {
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
	f.lane = &laneClock{scaledClock: clock}
	target := domain.Target{ID: "t1", BaseURL: f.srv.URL}
	m := worker.NewManager(laneStore{storage.NewLaneLedger(db), db}, target, f.lane, 30*time.Second)
	t.Cleanup(m.Shutdown)
	w := &fakeWorker{m: m}
	h := &harness{t: t, clock: clock, db: db, oscar: f, worker: w, s: s, id: "camp_" + strings.ReplaceAll(t.Name(), "/", "_")}
	h.deps = Deps{Store: db, Worker: w, Target: f, SNOW: f, Sink: f, Metrics: f, Probe: f.probe, Fingerprint: f,
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
	Tickets   struct{ Expected, Succeeded, Failed, Unknown int }
	Resend    struct{ Expected, Succeeded, Failed, Unknown int }
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
	for _, h2 := range f.history {
		if h2.Status != "resolved" {
			h.t.Errorf("alert %s (startsAt %s) still firing", h2.AlertName, h2.StartsAt)
		}
	}
	open, err := h.db.OpenMutations(context.Background(), h.id)
	if err != nil || len(open) > 0 {
		h.t.Errorf("open mutations: %v %+v", err, open)
	}
}

func (f *fakeOSCAR) CreateWebhookNotifier(_ context.Context, name, endpoint string) (targetops.Notifier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := &targetops.Notifier{ID: "dead-" + name, Name: name, Type: "webhook", Status: "enabled"}
	f.notifiers[n.ID] = n
	f.deadCreated = append(f.deadCreated, name+" "+endpoint)
	return *n, nil
}

func (f *fakeOSCAR) DeleteNotifier(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.notifiers, id)
	return nil
}

// --- Fingerprint policies ---

func (f *fakeOSCAR) Preflight(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fpSkip, nil
}

func (f *fakeOSCAR) Enable(_ context.Context, def targetops.FingerprintDefinition, _ string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fpEnables++
	f.fpOn, f.fpPub = true, fmt.Sprintf("pub-%d", f.fpEnables)
	return f.fpPub, nil
}

func (f *fakeOSCAR) Disable(context.Context, targetops.FingerprintDefinition, string, time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fpDisables++
	if f.fpDisableErr != nil {
		return f.fpDisableErr
	}
	f.fpOn = false
	return nil
}
