package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// promFake answers Query from a table keyed by PromQL text.
type promFake struct {
	at  time.Time
	vec map[string]targetops.Vector
}

func (p promFake) Query(_ context.Context, q string, _ time.Time) (targetops.Vector, error) {
	return p.vec[q], nil
}
func (promFake) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) (targetops.Matrix, error) {
	return nil, nil
}
func (promFake) Snapshot(context.Context, map[string]string, time.Time, time.Time, time.Duration) (json.RawMessage, error) {
	return nil, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                                   { return c.t }
func (c fixedClock) SleepUntil(ctx context.Context, _ time.Time) bool { return ctx.Err() == nil }

var adapterNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func sample(labels map[string]string, v float64, age time.Duration) targetops.Sample {
	return targetops.Sample{Metric: labels, Value: v, Time: float64(adapterNow.Add(-age).UnixMilli()) / 1000}
}

func adapter(vec map[string]targetops.Vector) *PrecheckAdapter {
	return &PrecheckAdapter{ID: "camp_mine", Clock: fixedClock{adapterNow}, Metrics: promFake{vec: vec}, Queries: DefaultPrecheckQueries}
}

func TestAdapterQueues(t *testing.T) {
	a := adapter(map[string]targetops.Vector{
		queueDepthQuery: {sample(map[string]string{"queue": "alerts"}, 7, 0), sample(map[string]string{"queue": "notify"}, 0, 0)},
		queueAgeQuery:   {sample(map[string]string{"queue": "alerts"}, 2.5, 0)},
	})
	qs, err := a.Queues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 || qs[0].Name != "alerts" || qs[0].Depth != 7 || qs[0].OldestAge != 2500*time.Millisecond || qs[1].Depth != 0 {
		t.Fatalf("queues %+v", qs)
	}
}

func TestAdapterFreshness(t *testing.T) {
	a := adapter(map[string]targetops.Vector{queueDepthQuery: {sample(nil, 1, 20*time.Second)}})
	f, err := a.Freshness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f["queue_depth"] != 20*time.Second {
		t.Fatalf("queue_depth age %s", f["queue_depth"])
	}
	if f["mysql_threads"] != 24*time.Hour {
		t.Fatalf("a family with no samples must read as stale, got %s", f["mysql_threads"])
	}
}

func TestAdapterHostsDownAndHeadroom(t *testing.T) {
	q := DefaultPrecheckQueries
	a := adapter(map[string]targetops.Vector{
		q.HostsDown:   {sample(map[string]string{"instance": "hl02:9100"}, 0, 0), sample(map[string]string{"instance": "hl01:9100"}, 1, 0)},
		q.DiskFreePct: {sample(nil, 42, 0)},
		q.MemAvailPct: {sample(nil, 61, 0)},
	})
	down, err := a.HostsDown(context.Background())
	if err != nil || !reflect.DeepEqual(down, []string{"hl02:9100"}) {
		t.Fatalf("hosts down %v %v", down, err)
	}
	if _, err := adapter(nil).HostsDown(context.Background()); err == nil {
		t.Fatal("an empty up{job=node} result must be an error (fail closed)")
	}
	if _, err := adapter(nil).ScrapeDown(context.Background()); err == nil {
		t.Fatal("an empty up result must be an error (fail closed)")
	}
	disk, mem, err := a.TargetHeadroom(context.Background())
	if err != nil || disk != 42 || mem != 61 {
		t.Fatalf("headroom %v %v %v", disk, mem, err)
	}
	if _, _, err := adapter(nil).TargetHeadroom(context.Background()); err == nil {
		t.Fatal("missing headroom samples must be an error, not 0%")
	}
}

func TestAdapterFiringBacklogAndForeignRate(t *testing.T) {
	q := DefaultPrecheckQueries
	a := adapter(map[string]targetops.Vector{
		q.FiringAlerts: {sample(nil, 80, 0)},
		strings.Replace(q.ArrivalRate, "%s", "300s", 1): {sample(nil, 0.4, 0)},
	})
	n, base, err := a.FiringBacklog(context.Background())
	if err != nil || n != 80 || base != q.FiringBaseline {
		t.Fatalf("backlog %d/%d %v", n, base, err)
	}
	r, err := a.ForeignArrivalRate(context.Background(), 5*time.Minute)
	if err != nil || r != 0.4 {
		t.Fatalf("rate %v %v", r, err)
	}
}

func testOSCAR(t *testing.T, h http.HandlerFunc) *oscar.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := oscar.New(domain.Target{ID: "t", APIProfile: "public-v1", BaseURL: srv.URL + "/ext/mw"},
		oscar.Options{HTTPClient: srv.Client(), Getenv: func(string) string { return "k" }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testDB(t *testing.T) *storage.Database {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAdapterCampaignRulesAndLeftovers(t *testing.T) {
	a := adapter(nil)
	a.Oscar = testOSCAR(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"rows":[{"id":1,"name":"campaign_old_flood"},{"id":2,"name":"campaign_camp_mine_flood"},{"id":3,"name":"unrelated"}],"total":3,"page":1,"perPage":100}`))
	})
	db := testDB(t)
	a.Store = db
	ctx := context.Background()
	if err := db.CreateCampaign(ctx, domain.Campaign{ID: "camp_old", ScenarioID: "s", ScenarioHash: "h", Trigger: "cli", RunBy: "agent",
		Status: domain.CampaignPrecheck, StartedAt: adapterNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordMutation(ctx, domain.CampaignMutation{CampaignID: "camp_old", Kind: kindPolicyCreate, ObjectID: "pol-9", Prior: json.RawMessage(`{}`), RecordedAt: adapterNow}); err != nil {
		t.Fatal(err)
	}
	rules, err := a.CampaignRules(ctx)
	if err != nil || !reflect.DeepEqual(rules, []string{"campaign_old_flood"}) {
		t.Fatalf("campaign rules %v %v", rules, err)
	}
	left, err := a.LeftoverObjects(ctx)
	if err != nil || !reflect.DeepEqual(left, []string{"campaign_old_flood", "policy:pol-9"}) {
		t.Fatalf("leftovers %v %v", left, err)
	}
}

func TestAdapterOpenCampaignAlerts(t *testing.T) {
	a := adapter(nil)
	var filters []string
	a.Oscar = testOSCAR(t, func(w http.ResponseWriter, r *http.Request) {
		filters = append(filters, r.URL.Query().Get("filter"))
		_, _ = w.Write([]byte(`{"total_records":2,"total_pages":1,"page":1,"per_page":100,"records":[
			{"id":1,"alertname":"Campaign-camp_old-1","status":"firing","createdAt":"2026-11-14T11:00:00Z","startsAt":"2026-11-14T11:00:00Z"},
			{"id":2,"alertname":"Campaign-camp_old-2","status":"resolved","createdAt":"2026-11-14T11:00:00Z","startsAt":"2026-11-14T11:00:00Z"}]}`))
	})
	db := testDB(t)
	a.Store = db
	ctx := context.Background()
	for _, id := range []string{"camp_old", "camp_mine"} {
		if err := db.CreateCampaign(ctx, domain.Campaign{ID: id, ScenarioID: "s", ScenarioHash: "h", Trigger: "cli", RunBy: "agent",
			Status: domain.CampaignPrecheck, StartedAt: adapterNow.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := a.OpenCampaignAlerts(ctx)
	if err != nil || n != 1 {
		t.Fatalf("open campaign alerts %d %v", n, err)
	}
	if len(filters) != 1 || !strings.Contains(filters[0], "camp_old") {
		t.Fatalf("history must be read for earlier campaigns only: %v", filters)
	}
}

func TestSampleGuard(t *testing.T) {
	m := promFake{vec: map[string]targetops.Vector{
		queueDepthQuery: {sample(map[string]string{"queue": "alerts"}, 3, 5*time.Second)},
		queueAgeQuery:   {sample(map[string]string{"queue": "alerts"}, 1, 0)},
	}}
	probe := func(context.Context) (targetops.ProbeResult, error) {
		return targetops.ProbeResult{Containers: []targetops.ProbeContainer{{RestartCount: 2}, {RestartCount: 1, OOMKilled: true}}}, nil
	}
	s, err := sampleGuard(context.Background(), m, probe, adapterNow)
	if err != nil {
		t.Fatal(err)
	}
	if s.QueueDepth["alerts"] != 3 || s.Restarts != 3 || s.OOMs != 1 || !s.Newest.Equal(adapterNow) {
		t.Fatalf("sample %+v", s)
	}
	if _, err := sampleGuard(context.Background(), promFake{}, probe, adapterNow); err == nil {
		t.Fatal("no samples must not produce a guard sample")
	}
}

func TestPostAlertUnknownOutcomeIsStatusZero(t *testing.T) {
	c := testOSCAR(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ext/mw/api/v1/alerts" {
			t.Errorf("cohort route %s", r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	})
	tgt := oscarTarget{c}
	a := Alert{Name: "Campaign-x-resend-time-1", Labels: map[string]string{"campaign_run": "x"}, StartsAt: adapterNow}
	if st, err := tgt.PostAlert(context.Background(), a); err != nil || st != 200 {
		t.Fatalf("post %d %v", st, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if st, err := tgt.PostAlert(ctx, a); err == nil || st != 0 {
		t.Fatalf("timed-out post must be status 0 (unknown), got %d %v", st, err)
	}
}

type stubHost struct {
	db      *storage.Database
	url     string
	err     error
	dataDir string
	apiKey  string
}

func (h stubHost) CampaignDataDir() string { return h.dataDir }

func (h stubHost) CampaignDatabase() *storage.Database { return h.db }
func (h stubHost) CampaignTarget(context.Context, string) (domain.Target, *oscar.Client, error) {
	if h.err != nil {
		return domain.Target{}, nil, h.err
	}
	t := domain.Target{ID: "t1", APIProfile: "public-v1", BaseURL: h.url + "/ext/mw"}
	c, err := oscar.New(t, oscar.Options{Getenv: func(string) string {
		if h.apiKey != "" {
			return h.apiKey
		}
		return "k"
	}})
	return t, c, err
}
func (stubHost) ExportRun(context.Context, string, string) (evidence.Result, error) {
	return evidence.Result{}, nil
}
func (stubHost) ExecuteScenarioForCampaign(context.Context, string, corr.Scenario, string) (domain.Run, error) {
	return domain.Run{}, nil
}

func TestNewFromConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	token := write("token", "secret")
	cred := write("snow.json", `{"username":"u","password":"p"}`)
	fo := filepath.Join(dir, "fo")
	_ = os.Mkdir(fo, 0o700)
	for i := 1; i <= 4; i++ {
		_ = os.WriteFile(filepath.Join(fo, fmt.Sprintf("fo-%02d.json", i)), []byte(`{"api_key":"k"}`), 0o600)
	}
	good := campaign.Config{TargetID: "t1", WorkerURL: "https://mini:8443", WorkerTokenFile: token, SinkURL: "https://mini:9443",
		SinkAdminTokenFile: token, MetricsURL: "https://vm:8428", MetricsCredentialFile: token, SNOWInstanceURL: "https://dev.service-now.com",
		SNOWCredentialFile: cred, FrontOfficeDir: fo, ProbeHost: "oscar", ProbeKeyFile: token}
	db := testDB(t)
	data := t.TempDir()
	host := stubHost{db: db, url: "https://oscar.home.arpa", dataDir: data}
	for _, tc := range []struct {
		name string
		edit func(*campaign.Config)
		host stubHost
		ok   bool
	}{
		{"valid", func(*campaign.Config) {}, host, true},
		{"unknown target", func(*campaign.Config) {}, stubHost{db: db, dataDir: data, err: errors.New("not found")}, false},
		{"missing worker token", func(c *campaign.Config) { c.WorkerTokenFile = filepath.Join(dir, "nope") }, host, false},
		{"missing snow credential", func(c *campaign.Config) { c.SNOWCredentialFile = filepath.Join(dir, "nope") }, host, false},
		{"short front-office dir", func(c *campaign.Config) { c.FrontOfficeDir = dir }, host, false},
		{"bad sink CA", func(c *campaign.Config) { c.SinkCAFile = token }, host, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.edit(&cfg)
			e, err := NewFromConfig(context.Background(), cfg, tc.host)
			if tc.ok != (err == nil) {
				t.Fatalf("err %v", err)
			}
			if tc.ok && (e.d.CampaignNotifier != "CAMPAIGN_SINK" || e.d.IngestURL != "https://oscar.home.arpa" || len(e.d.FrontOfficeUsers) != 4 || e.d.Precheck == nil) {
				t.Fatalf("deps %+v", e.d)
			}
			if tc.ok {
				want := filepath.Join(data, "campaigns")
				if fi, err := os.Stat(want); e.d.ReportDir != want || err != nil || fi.Mode().Perm() != 0o700 {
					t.Fatalf("ReportDir %q (%v), want %q mode 0700", e.d.ReportDir, err, want)
				}
			}
		})
	}
}
