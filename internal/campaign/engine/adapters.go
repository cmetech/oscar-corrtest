package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/diskfree"
	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/campaign/guard"
	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/score"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

// sampleGuard builds one guard sample: queues and refusal counters from
// PromQL, restarts and OOMs from the probe. Newest is the newest sample time.
func sampleGuard(ctx context.Context, m Metrics, probe func(context.Context) (targetops.ProbeResult, error), now time.Time) (guard.Sample, error) {
	s := guard.Sample{At: now, QueueDepth: map[string]float64{}, QueueAge: map[string]float64{}}
	newest := 0.0
	vec := func(q string) (targetops.Vector, error) {
		v, err := m.Query(ctx, q, now)
		for _, x := range v {
			newest = math.Max(newest, x.Time)
		}
		return v, err
	}
	for q, into := range map[string]map[string]float64{queueDepthQuery: s.QueueDepth, queueAgeQuery: s.QueueAge} {
		v, err := vec(q)
		if err != nil {
			return s, err
		}
		for _, x := range v {
			into[x.Metric["queue"]] = x.Value
		}
	}
	for q, into := range map[string]*float64{score.Queries["mysql_max_conn_errors"]: &s.MySQLRefusals, score.Queries["redis_rejected_cmds"]: &s.RedisDenied} {
		v, err := vec(q)
		if err != nil {
			return s, err
		}
		for _, x := range v {
			*into += x.Value
		}
	}
	if newest == 0 {
		return s, errors.New("no metric samples")
	}
	s.Newest = time.UnixMilli(int64(newest * 1000)).UTC()
	if probe != nil {
		p, err := probe(ctx)
		if err != nil {
			return s, err
		}
		for _, c := range p.Containers {
			s.Restarts += c.RestartCount
			if c.OOMKilled {
				s.OOMs++
			}
		}
	}
	return s, nil
}

// PromQL used by the pre-check adapters. Fields so a target can recalibrate.
type PrecheckQueries struct {
	HostsDown, ScrapeDown, DiskFreePct, MemAvailPct, FiringAlerts, ArrivalRate string
	FiringBaseline                                                             int
}

// DefaultPrecheckQueries are the homelab defaults.
var DefaultPrecheckQueries = PrecheckQueries{
	// HostsDown and ScrapeDown return every series; zero-valued ones are down.
	// An empty result is an error (fail closed).
	HostsDown:    `up{job="node-exporter"}`, // OSCAR vmagent job name
	ScrapeDown:   `up`,
	DiskFreePct:  `min(100 * node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"})`,
	MemAvailPct:  `min(100 * node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`,
	FiringAlerts: `sum(alertmanager_alerts{state="active"})`,
	// %s is the rate window, e.g. 5m.
	ArrivalRate:    `sum(rate(alertmanager_alerts_received_total[%s]))`,
	FiringBaseline: 50,
}

// PrecheckAdapter implements precheck.Deps against the real clients.
type PrecheckAdapter struct {
	ID               string
	Clock            lane.Clock
	Oscar            *oscar.Client
	Metrics          Metrics
	ProbeFn          func(context.Context) (targetops.ProbeResult, error)
	WorkerInfoFn     func(context.Context) (workerapi.Info, error)
	Store            PrecheckStore
	Sink             *targetops.Sink
	SinkHealthURL    string
	SinkHTTP         *http.Client
	SNOW             *targetops.SNOW
	Users            []frontoffice.User
	FrontOfficeURL   string
	HTTP             *http.Client
	CampaignNotifier string
	IngestURL        string // native Alertmanager ingest (host root + /ext/alertmanager/api/v2/alerts)
	IngestHTTP       *http.Client
	Queries          PrecheckQueries

	smokeStart time.Time // startsAt PostAlert sent; the resolve must echo it exactly
}

// PrecheckStore is the persistence the pre-checks read and write.
type PrecheckStore interface {
	ListCampaigns(ctx context.Context, f domain.CampaignFilter) ([]domain.Campaign, error)
	OpenMutations(ctx context.Context, campaignID string) ([]domain.CampaignMutation, error)
	RecordMutation(ctx context.Context, m domain.CampaignMutation) (int64, error)
	MarkMutationRestored(ctx context.Context, id int64, at time.Time) error
}

var _ precheck.Deps = (*PrecheckAdapter)(nil)

func (a *PrecheckAdapter) Now() time.Time                 { return a.Clock.Now() }
func (a *PrecheckAdapter) CampaignID() string             { return a.ID }
func (a *PrecheckAdapter) ControllerVersion() string      { return version.Current().Version }
func (a *PrecheckAdapter) LocalDiskFree() (uint64, error) { return diskfree.Bytes(".") }
func (a *PrecheckAdapter) Sleep(ctx context.Context, d time.Duration) error {
	if !a.Clock.SleepUntil(ctx, a.Clock.Now().Add(d)) {
		return ctx.Err()
	}
	return nil
}

func (a *PrecheckAdapter) Probe(ctx context.Context) (targetops.ProbeResult, precheck.Timing, error) {
	t := precheck.Timing{Sent: a.Clock.Now()}
	r, err := a.ProbeFn(ctx)
	t.Recv = a.Clock.Now()
	return r, t, err
}

func (a *PrecheckAdapter) WorkerInfo(ctx context.Context) (workerapi.Info, precheck.Timing, error) {
	t := precheck.Timing{Sent: a.Clock.Now()}
	r, err := a.WorkerInfoFn(ctx)
	t.Recv = a.Clock.Now()
	return r, t, err
}

func (a *PrecheckAdapter) notifier(ctx context.Context, name string) (targetops.Notifier, error) {
	all, err := targetops.ListNotifiers(ctx, a.Oscar)
	if err != nil {
		return targetops.Notifier{}, err
	}
	for _, n := range all {
		if strings.EqualFold(n.Name, name) {
			return n, nil
		}
	}
	return targetops.Notifier{}, fmt.Errorf("notifier %s not found", name)
}

func (a *PrecheckAdapter) ResendSchema(ctx context.Context) error {
	n, err := a.notifier(ctx, a.CampaignNotifier)
	if err != nil {
		return err
	}
	_, err = targetops.Status(ctx, a.Oscar, n.ID, "notification", "")
	return err
}

func (a *PrecheckAdapter) HistoryNotifierCount(ctx context.Context) (int, error) {
	all, err := targetops.ListNotifiers(ctx, a.Oscar)
	n := 0
	for _, x := range all {
		if strings.EqualFold(x.Name, "ALERTHISTORY") {
			n++
		}
	}
	return n, err
}

// Queues reads queue depth and oldest age from PromQL.
func (a *PrecheckAdapter) Queues(ctx context.Context) ([]precheck.Queue, error) {
	now := a.Clock.Now()
	depth, err := a.Metrics.Query(ctx, queueDepthQuery, now)
	if err != nil {
		return nil, err
	}
	age, err := a.Metrics.Query(ctx, queueAgeQuery, now)
	if err != nil {
		return nil, err
	}
	byName := map[string]*precheck.Queue{}
	get := func(n string) *precheck.Queue {
		if byName[n] == nil {
			byName[n] = &precheck.Queue{Name: n}
		}
		return byName[n]
	}
	for _, s := range depth {
		get(s.Metric["queue"]).Depth = int(s.Value)
	}
	for _, s := range age {
		get(s.Metric["queue"]).OldestAge = time.Duration(s.Value * float64(time.Second))
	}
	out := make([]precheck.Queue, 0, len(byName))
	for _, q := range byName {
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Freshness is the newest-sample age per scorecard family.
func (a *PrecheckAdapter) Freshness(ctx context.Context) (map[string]time.Duration, error) {
	now := a.Clock.Now()
	out := map[string]time.Duration{}
	for fam, q := range score.Queries {
		v, err := a.Metrics.Query(ctx, q, now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fam, err)
		}
		newest := 0.0
		for _, s := range v {
			newest = math.Max(newest, s.Time)
		}
		out[fam] = 24 * time.Hour // no sample counts as stale
		if newest > 0 {
			out[fam] = now.Sub(time.UnixMilli(int64(newest * 1000)))
		}
	}
	return out, nil
}

// CampaignRules lists correlation rules named like campaign_* that are not
// this run's own.
func (a *PrecheckAdapter) CampaignRules(ctx context.Context) ([]string, error) {
	// ponytail: first 100 matches only; paginate if a target ever holds more campaign rules.
	var resp struct {
		Rows []struct {
			Name string `json:"name"`
		} `json:"rows"`
	}
	q := url.Values{"perPage": {"100"}, "page": {"1"}, "column": {"id"}, "order": {"asc"}, "search": {"campaign"}}
	if err := a.Oscar.DoJSON(ctx, http.MethodGet, "/api/v1/correlation_rules", q, nil, &resp); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range resp.Rows {
		if strings.Contains(strings.ToLower(r.Name), "campaign") && !strings.Contains(r.Name, a.ID) {
			out = append(out, r.Name)
		}
	}
	return out, nil
}

func (a *PrecheckAdapter) labelsFrom(ctx context.Context, q, label string) ([]string, error) {
	v, err := a.Metrics.Query(ctx, q, a.Clock.Now())
	if err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, fmt.Errorf("no series for %s", q)
	}
	var out []string
	for _, s := range v {
		if s.Value == 0 {
			out = append(out, s.Metric[label])
		}
	}
	sort.Strings(out)
	return out, nil
}

func (a *PrecheckAdapter) HostsDown(ctx context.Context) ([]string, error) {
	return a.labelsFrom(ctx, a.Queries.HostsDown, "instance")
}

func (a *PrecheckAdapter) ScrapeDown(ctx context.Context) ([]string, error) {
	return a.labelsFrom(ctx, a.Queries.ScrapeDown, "job")
}

func (a *PrecheckAdapter) scalar(ctx context.Context, q string) (float64, error) {
	v, err := a.Metrics.Query(ctx, q, a.Clock.Now())
	if err != nil {
		return 0, err
	}
	if len(v) == 0 {
		return 0, fmt.Errorf("no samples for %s", q)
	}
	return v[0].Value, nil
}

func (a *PrecheckAdapter) TargetHeadroom(ctx context.Context) (float64, float64, error) {
	disk, err := a.scalar(ctx, a.Queries.DiskFreePct)
	if err != nil {
		return 0, 0, err
	}
	mem, err := a.scalar(ctx, a.Queries.MemAvailPct)
	return disk, mem, err
}

func (a *PrecheckAdapter) FiringBacklog(ctx context.Context) (int, int, error) {
	n, err := a.scalar(ctx, a.Queries.FiringAlerts)
	return int(n), a.Queries.FiringBaseline, err
}

func (a *PrecheckAdapter) ForeignArrivalRate(ctx context.Context, window time.Duration) (float64, error) {
	return a.scalar(ctx, fmt.Sprintf(a.Queries.ArrivalRate, fmt.Sprintf("%ds", int(window.Seconds()))))
}

// OpenCampaignAlerts counts firing History rows of earlier campaigns (the
// ten most recent).
func (a *PrecheckAdapter) OpenCampaignAlerts(ctx context.Context) (int, error) {
	camps, err := a.Store.ListCampaigns(ctx, domain.CampaignFilter{Limit: 10})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range camps {
		if c.ID == a.ID {
			continue
		}
		rows, err := a.Oscar.HistoryByLabel(ctx, "campaign_run", c.ID, c.StartedAt, a.Clock.Now())
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if r.Status == "firing" {
				n++
			}
		}
	}
	return n, nil
}

// LeftoverObjects lists campaign rules and earlier campaigns' unrestored
// policies.
func (a *PrecheckAdapter) LeftoverObjects(ctx context.Context) ([]string, error) {
	out, err := a.CampaignRules(ctx)
	if err != nil {
		return nil, err
	}
	camps, err := a.Store.ListCampaigns(ctx, domain.CampaignFilter{})
	if err != nil {
		return nil, err
	}
	for _, c := range camps {
		if c.ID == a.ID {
			continue
		}
		open, err := a.Store.OpenMutations(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range open {
			if m.Kind == kindPolicyCreate || m.Kind == kindPolicyActivate {
				out = append(out, "policy:"+m.ObjectID)
			}
		}
	}
	return out, nil
}

func (a *PrecheckAdapter) ListCampaigns(ctx context.Context, f domain.CampaignFilter) ([]domain.Campaign, error) {
	return a.Store.ListCampaigns(ctx, f)
}
func (a *PrecheckAdapter) OpenMutations(ctx context.Context, id string) ([]domain.CampaignMutation, error) {
	return a.Store.OpenMutations(ctx, id)
}
func (a *PrecheckAdapter) RecordMutation(ctx context.Context, m domain.CampaignMutation) (int64, error) {
	return a.Store.RecordMutation(ctx, m)
}
func (a *PrecheckAdapter) MarkMutationRestored(ctx context.Context, id int64, at time.Time) error {
	return a.Store.MarkMutationRestored(ctx, id, at)
}

func (a *PrecheckAdapter) SinkHealth(ctx context.Context) error {
	st, err := a.get(ctx, a.SinkHTTP, a.SinkHealthURL, nil)
	if err == nil && st != http.StatusOK {
		err = fmt.Errorf("sink health HTTP %d", st)
	}
	return err
}

func (a *PrecheckAdapter) SinkEnabled(ctx context.Context) (bool, error) {
	n, err := a.notifier(ctx, a.CampaignNotifier)
	return n.Status == "enabled", err
}

func (a *PrecheckAdapter) SetSinkEnabled(ctx context.Context, on bool) error {
	n, err := a.notifier(ctx, a.CampaignNotifier)
	if err != nil {
		return err
	}
	got, err := targetops.SetNotifierStatus(ctx, a.Oscar, n.ID, on)
	if err == nil && got.Status != statusWord(on) {
		err = fmt.Errorf("%s read back %q", a.CampaignNotifier, got.Status)
	}
	return err
}

// PostAlert and ResolveAlert use the native Alertmanager ingest. The resolve
// echoes the posted startsAt: OSCAR matches a resolve on the exact original
// startsAt, and Alertmanager rejects a startsAt after endsAt.
// ponytail: one remembered startsAt, B5 posts a single smoke alert.
func (a *PrecheckAdapter) PostAlert(ctx context.Context, labels map[string]string) error {
	a.smokeStart = a.Clock.Now()
	return a.ingest(ctx, labels, a.smokeStart, time.Time{})
}

func (a *PrecheckAdapter) ResolveAlert(ctx context.Context, labels map[string]string) error {
	start, end := a.smokeStart, a.Clock.Now()
	if start.IsZero() || start.After(end) {
		start = end
	}
	return a.ingest(ctx, labels, start, end)
}

func (a *PrecheckAdapter) ingest(ctx context.Context, labels map[string]string, startsAt, endsAt time.Time) error {
	alert := map[string]any{"labels": labels, "annotations": map[string]string{"summary": labels["alertname"]}, "startsAt": startsAt.UTC().Format(time.RFC3339Nano)}
	if !endsAt.IsZero() {
		alert["endsAt"] = endsAt.UTC().Format(time.RFC3339Nano)
	}
	body, _ := json.Marshal([]any{alert})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.IngestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := a.IngestHTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("ingest request failed")
	}
	_ = resp.Body.Close() // status already read; close error is not actionable
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ingest HTTP %d", resp.StatusCode)
	}
	return nil
}

func (a *PrecheckAdapter) SinkReceipts(ctx context.Context, run string, since time.Time) ([]targetops.Receipt, error) {
	return a.Sink.Receipts(ctx, "campaign", run, since)
}

func (a *PrecheckAdapter) ExternalNotifiers(ctx context.Context) ([]targetops.Notifier, error) {
	return targetops.ExternalEnabled(ctx, a.Oscar, append(append([]string{}, keepNotifiers...), a.CampaignNotifier))
}

// SNOWRate reads the ticketing rate limits (GET /api/v1/alerts/ratelimits/ticketing).
// ponytail: tolerant decode of {snow:{paused,operations_per_second}} at the top level or under "effective"; pin the shape once the middleware schema is frozen.
func (a *PrecheckAdapter) SNOWRate(ctx context.Context) (precheck.SNOWRate, error) {
	type snow struct {
		Paused bool    `json:"paused"`
		OPS    float64 `json:"operations_per_second"`
	}
	var body struct {
		Snow      *snow `json:"snow"`
		Effective struct {
			Snow *snow `json:"snow"`
		} `json:"effective"`
	}
	if err := a.Oscar.DoJSON(ctx, http.MethodGet, "/api/v1/alerts/ratelimits/ticketing", nil, nil, &body); err != nil {
		return precheck.SNOWRate{}, err
	}
	s := body.Snow
	if s == nil {
		s = body.Effective.Snow
	}
	if s == nil {
		return precheck.SNOWRate{Healthy: true}, nil
	}
	return precheck.SNOWRate{Healthy: true, Configured: s.OPS > 0, Paused: s.Paused, OpsPerSecond: s.OPS}, nil
}

func (a *PrecheckAdapter) SNOWWake(ctx context.Context) error { return a.SNOW.Wake(ctx, 5*time.Minute) }

// SNOWCredential proves the close credential with a read-only listing.
func (a *PrecheckAdapter) SNOWCredential(ctx context.Context) error {
	_, err := a.SNOW.OpenCampaignIncidents(ctx, "credential-check")
	return err
}

func (a *PrecheckAdapter) OpenIncidents(ctx context.Context, run string) ([]targetops.Incident, error) {
	return a.SNOW.OpenCampaignIncidents(ctx, run)
}

func (a *PrecheckAdapter) TaskRuns(ctx context.Context, jobs []string, start, end time.Time) ([]targetops.TaskRun, error) {
	return targetops.TaskRuns(ctx, a.Oscar, jobs, start, end)
}

func (a *PrecheckAdapter) FrontOfficeUsers() ([]precheck.FOUser, error) {
	out := make([]precheck.FOUser, 0, len(a.Users))
	for _, u := range a.Users {
		out = append(out, precheck.FOUser{Name: u.Name, Key: u.APIKey})
	}
	return out, nil
}

func (a *PrecheckAdapter) FOHistory(ctx context.Context, key string) (int, error) {
	return a.get(ctx, a.HTTP, a.FrontOfficeURL+"/ext/mw/api/v1/alerts/history?perPage=1", map[string]string{"X-API-Key": key})
}

func (a *PrecheckAdapter) get(ctx context.Context, hc *http.Client, url string, hdr map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, errors.New("request failed") // no wrapped err: the URL may carry secrets
	}
	_ = resp.Body.Close() // status already read; close error is not actionable
	return resp.StatusCode, nil
}
