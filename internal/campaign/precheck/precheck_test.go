package precheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerapi"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func sp(s string) *string { return &s }

type fake struct {
	probe         targetops.ProbeResult
	probeT        Timing
	winfo         workerapi.Info
	winfoT        Timing
	ctlVersion    string
	queues        []Queue
	camps         []domain.Campaign
	openMut       map[string][]domain.CampaignMutation
	rules         []string
	sinkEnabled   bool
	sinkHealthErr error
	receipts      []targetops.Receipt
	external      []targetops.Notifier
	recorded      []domain.CampaignMutation
	restored      []int64
	sinkSets      []bool
	down          []string
	fresh         map[string]time.Duration
	rate          SNOWRate
	wakeErr       error
	credErr       error
	incidents     map[string][]targetops.Incident
	taskRuns      []targetops.TaskRun
	foStatus      map[string]int
	foUsers       []FOUser
	disk          uint64
	diskPct, mem  float64
	historyNotif  int
	resendErr     error
	openAlerts    int
	leftovers     []string
	hostsDown     []string
	backlog       int
	foreignRate   float64
	resolved      []map[string]string
	posted        []map[string]string
	postErr       error
	incErr        map[string]error
	incQueries    []string
}

func good() *fake {
	f := &fake{
		probe: targetops.ProbeResult{
			UTC: t0.Format(time.RFC3339Nano), OscarVersion: sp("26.4.6+main.89dc4a9"), MetricsRetention: sp("6"),
			Flags:        map[string]*string{"RESEND_POLICY_ENABLED": sp("true"), "ALERT_HOOKS_LEGACY_LABELS_ENABLED": sp("false")},
			Containers:   []targetops.ProbeContainer{{Name: "api", Health: "healthy", StartedAt: t0.Add(-48 * time.Hour).Format(time.RFC3339)}},
			AlertHistory: &targetops.ProbeHistory{Rows: 1000, DataBytes: 1 << 20},
		},
		probeT:     Timing{Sent: t0, Recv: t0},
		winfo:      workerapi.Info{Version: "v1", Now: t0, DiskFreeBytes: 50 << 30},
		winfoT:     Timing{Sent: t0, Recv: t0},
		ctlVersion: "v1",
		queues:     []Queue{{Name: "alerts", Depth: 50, OldestAge: 30 * time.Second}},
		camps:      []domain.Campaign{{ID: "prior-001", Status: domain.CampaignCompleted, Cleanup: domain.CleanupClean, Verdict: domain.VerdictPass}},
		openMut:    map[string][]domain.CampaignMutation{},
		fresh:      map[string]time.Duration{"alerts": 5 * time.Second},
		rate:       SNOWRate{Healthy: true, Configured: true, OpsPerSecond: 0.2},
		incidents:  map[string][]targetops.Incident{},
		foStatus:   map[string]int{"k1": 200, "k2": 200},
		foUsers:    []FOUser{{Name: "fo-01", Key: "k1"}, {Name: "fo-02", Key: "k2"}},
		disk:       50 << 30, diskPct: 40, mem: 60, historyNotif: 1,
		external: []targetops.Notifier{{ID: "n1", Name: "email", Status: "enabled"}},
	}
	for _, j := range b9Jobs {
		f.taskRuns = append(f.taskRuns, targetops.TaskRun{Job: j, Status: "SUCCESS", StartedAt: t0.Add(-10 * time.Minute)})
	}
	f.receipts = []targetops.Receipt{{ReceivedAt: t0, Alerts: []targetops.SinkAlert{{Labels: map[string]string{"campaign_run": "camp-001-precheck"}}}}}
	return f
}

func (f *fake) Now() time.Time                             { return t0 }
func (f *fake) CampaignID() string                         { return "camp-001" }
func (f *fake) ControllerVersion() string                  { return f.ctlVersion }
func (f *fake) Sleep(context.Context, time.Duration) error { return nil }
func (f *fake) Probe(context.Context) (targetops.ProbeResult, Timing, error) {
	return f.probe, f.probeT, nil
}
func (f *fake) WorkerInfo(context.Context) (workerapi.Info, Timing, error) {
	return f.winfo, f.winfoT, nil
}
func (f *fake) ResendSchema(context.Context) error                { return f.resendErr }
func (f *fake) HistoryNotifierCount(context.Context) (int, error) { return f.historyNotif, nil }
func (f *fake) Queues(context.Context) ([]Queue, error)           { return f.queues, nil }
func (f *fake) ListCampaigns(context.Context, domain.CampaignFilter) ([]domain.Campaign, error) {
	return f.camps, nil
}
func (f *fake) OpenMutations(_ context.Context, id string) ([]domain.CampaignMutation, error) {
	return f.openMut[id], nil
}
func (f *fake) RecordMutation(_ context.Context, m domain.CampaignMutation) (int64, error) {
	f.recorded = append(f.recorded, m)
	return int64(len(f.recorded)), nil
}
func (f *fake) MarkMutationRestored(_ context.Context, id int64, _ time.Time) error {
	f.restored = append(f.restored, id)
	return nil
}
func (f *fake) CampaignRules(context.Context) ([]string, error) { return f.rules, nil }
func (f *fake) SinkHealth(context.Context) error                { return f.sinkHealthErr }
func (f *fake) SinkEnabled(context.Context) (bool, error)       { return f.sinkEnabled, nil }
func (f *fake) SetSinkEnabled(_ context.Context, on bool) error {
	f.sinkSets = append(f.sinkSets, on)
	return nil
}
func (f *fake) PostAlert(_ context.Context, l map[string]string) error {
	f.posted = append(f.posted, l)
	return f.postErr
}
func (f *fake) ResolveAlert(_ context.Context, l map[string]string) error {
	f.resolved = append(f.resolved, l)
	return nil
}
func (f *fake) SinkReceipts(context.Context, string, time.Time) ([]targetops.Receipt, error) {
	return f.receipts, nil
}
func (f *fake) ExternalNotifiers(context.Context) ([]targetops.Notifier, error) {
	return f.external, nil
}
func (f *fake) OpenCampaignAlerts(context.Context) (int, error)             { return f.openAlerts, nil }
func (f *fake) LeftoverObjects(context.Context) ([]string, error)           { return f.leftovers, nil }
func (f *fake) ScrapeDown(context.Context) ([]string, error)                { return f.down, nil }
func (f *fake) Freshness(context.Context) (map[string]time.Duration, error) { return f.fresh, nil }
func (f *fake) SNOWRate(context.Context) (SNOWRate, error)                  { return f.rate, nil }
func (f *fake) SNOWWake(context.Context) error                              { return f.wakeErr }
func (f *fake) SNOWCredential(context.Context) error                        { return f.credErr }
func (f *fake) OpenIncidents(_ context.Context, run string) ([]targetops.Incident, error) {
	f.incQueries = append(f.incQueries, run)
	return f.incidents[run], f.incErr[run]
}
func (f *fake) TaskRuns(context.Context, []string, time.Time, time.Time) ([]targetops.TaskRun, error) {
	return f.taskRuns, nil
}
func (f *fake) FrontOfficeUsers() ([]FOUser, error)                  { return f.foUsers, nil }
func (f *fake) FOHistory(_ context.Context, key string) (int, error) { return f.foStatus[key], nil }
func (f *fake) LocalDiskFree() (uint64, error)                       { return f.disk, nil }
func (f *fake) TargetHeadroom(context.Context) (float64, float64, error) {
	return f.diskPct, f.mem, nil
}
func (f *fake) HostsDown(context.Context) ([]string, error)     { return f.hostsDown, nil }
func (f *fake) FiringBacklog(context.Context) (int, int, error) { return f.backlog, 5, nil }
func (f *fake) ForeignArrivalRate(context.Context, time.Duration) (float64, error) {
	return f.foreignRate, nil
}

func find(rs []Result, id string) Result {
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	return Result{ID: id, Status: "missing"}
}

func run(f *fake) []Result { return Run(context.Background(), f, scenario.AlertingDayV1(), "26.4.6") }

func TestAllGoodPasses(t *testing.T) {
	rs := run(good())
	if len(rs) != 16 {
		t.Fatalf("want 16 results, got %d", len(rs))
	}
	for _, r := range rs {
		if r.Status != "pass" {
			t.Errorf("%s %s %q %q", r.ID, r.Status, r.Measured, r.Detail)
		}
	}
	if ok, cmp, _ := Startable(rs, nil); !ok || !cmp {
		t.Fatal("all-good must be startable and comparable")
	}
}

func TestBlockingBoundaries(t *testing.T) {
	ms := func(n int) Timing {
		return Timing{Sent: t0.Add(time.Duration(n) * time.Millisecond), Recv: t0.Add(time.Duration(n) * time.Millisecond)}
	}
	_ = ms
	cases := []struct {
		name, id string
		mut      func(*fake)
		want     string
	}{
		{"B1 flag absent", "B1", func(f *fake) { delete(f.probe.Flags, "RESEND_POLICY_ENABLED") }, "fail"},
		{"B1 flag false", "B1", func(f *fake) { f.probe.Flags["RESEND_POLICY_ENABLED"] = sp("false") }, "fail"},
		{"B1 version below", "B1", func(f *fake) { f.probe.OscarVersion = sp("26.4.5+main.abc") }, "fail"},
		{"B1 version equal plain", "B1", func(f *fake) { f.probe.OscarVersion = sp("26.4.6") }, "pass"},
		{"B1 version newer", "B1", func(f *fake) { f.probe.OscarVersion = sp("26.10.0+x.1") }, "pass"},
		{"B1 unparsable", "B1", func(f *fake) { f.probe.OscarVersion = sp("main-89dc4a9") }, "fail"},
		{"B1 two history notifiers", "B1", func(f *fake) { f.historyNotif = 2 }, "fail"},
		{"B1 resend schema", "B1", func(f *fake) { f.resendErr = errors.New("404") }, "fail"},
		{"B2 restart 10m ago", "B2", func(f *fake) {
			f.probe.Containers[0].RestartCount = 1
			f.probe.Containers[0].StartedAt = t0.Add(-10 * time.Minute).Format(time.RFC3339)
		}, "fail"},
		{"B2 restart 40m ago", "B2", func(f *fake) {
			f.probe.Containers[0].RestartCount = 1
			f.probe.Containers[0].StartedAt = t0.Add(-40 * time.Minute).Format(time.RFC3339)
		}, "pass"},
		{"B2 unhealthy", "B2", func(f *fake) { f.probe.Containers[0].Health = "unhealthy" }, "fail"},
		{"B2 no healthcheck none", "B2", func(f *fake) {
			f.probe.Containers = append(f.probe.Containers, targetops.ProbeContainer{Name: "x", Health: "none"}, targetops.ProbeContainer{Name: "y", Health: ""})
		}, "pass"},
		{"B2 starting", "B2", func(f *fake) { f.probe.Containers[0].Health = "starting" }, "fail"},
		{"B2 oom", "B2", func(f *fake) { f.probe.Containers[0].OOMKilled = true }, "fail"},
		{"B3 depth 51", "B3", func(f *fake) { f.queues[0].Depth = 51 }, "fail"},
		{"B3 age 31s", "B3", func(f *fake) { f.queues[0].OldestAge = 31 * time.Second }, "fail"},
		{"B3 other campaign running", "B3", func(f *fake) {
			f.camps = append(f.camps, domain.Campaign{ID: "other-1", Status: domain.CampaignRunning})
		}, "fail"},
		{"B3 matching rule", "B3", func(f *fake) { f.rules = []string{"stray"} }, "fail"},
		{"B4 skew 251ms", "B4", func(f *fake) { f.probe.UTC = t0.Add(251 * time.Millisecond).Format(time.RFC3339Nano) }, "fail"},
		{"B4 skew 249ms", "B4", func(f *fake) { f.probe.UTC = t0.Add(249 * time.Millisecond).Format(time.RFC3339Nano) }, "pass"},
		{"B4 midpoint compensates", "B4", func(f *fake) {
			f.probe.UTC = t0.Add(400 * time.Millisecond).Format(time.RFC3339Nano)
			f.probeT = Timing{Sent: t0.Add(300 * time.Millisecond), Recv: t0.Add(500 * time.Millisecond)}
		}, "pass"},
		{"B4 worker skew", "B4", func(f *fake) { f.winfo.Now = t0.Add(-300 * time.Millisecond) }, "fail"},
		{"B4 worker version", "B4", func(f *fake) { f.winfo.Version = "v2" }, "fail"},
		{"B5 not received", "B5", func(f *fake) { f.receipts = nil }, "fail"},
		{"B5 sink unhealthy", "B5", func(f *fake) { f.sinkHealthErr = errors.New("down") }, "fail"},
		{"B6 stale 61s", "B6", func(f *fake) { f.fresh["alerts"] = 61 * time.Second }, "fail"},
		{"B6 fresh 60s", "B6", func(f *fake) { f.fresh["alerts"] = 60 * time.Second }, "pass"},
		{"B6 job down", "B6", func(f *fake) { f.down = []string{"node"} }, "fail"},
		{"B7 open incident", "B7", func(f *fake) {
			f.incidents["prior-001"] = []targetops.Incident{{Number: "INC1"}}
		}, "fail"},
		{"B7 wake timeout", "B7", func(f *fake) { f.wakeErr = errors.New("timeout") }, "fail"},
		{"B7 credential", "B7", func(f *fake) { f.credErr = errors.New("401") }, "fail"},
		{"B7 paused", "B7", func(f *fake) { f.rate.Paused = true }, "fail"},
		{"B7 rate 599/h", "B7", func(f *fake) { f.rate.OpsPerSecond = 599.0 / 3600 }, "fail"},
		{"B7 rate 600/h", "B7", func(f *fake) { f.rate.OpsPerSecond = 600.0 / 3600 }, "pass"},
		{"B8 open mutation prior", "B8", func(f *fake) {
			f.openMut["prior-001"] = []domain.CampaignMutation{{ID: 9, Kind: "notifier_disable"}}
		}, "fail"},
		{"B8 sink enabled", "B8", func(f *fake) { f.sinkEnabled = true }, "fail"},
		{"B8 open alerts", "B8", func(f *fake) { f.openAlerts = 1 }, "fail"},
		{"B8 prior cleanup dirty", "B8", func(f *fake) { f.camps[0].Cleanup = domain.CleanupDirty }, "fail"},
		{"B9 job no success", "B9", func(f *fake) { f.taskRuns = f.taskRuns[1:] }, "fail"},
		{"B9 job only failed", "B9", func(f *fake) { f.taskRuns[0].Status = "FAILURE" }, "fail"},
		{"B9 job too old", "B9", func(f *fake) { f.taskRuns[0].StartedAt = t0.Add(-61 * time.Minute) }, "fail"},
		{"B10 401", "B10", func(f *fake) { f.foStatus["k2"] = 401 }, "fail"},
		{"B11 disk 19%", "B11", func(f *fake) { f.diskPct = 19 }, "fail"},
		{"B11 disk 20%", "B11", func(f *fake) { f.diskPct = 20 }, "pass"},
		{"B11 mem 24%", "B11", func(f *fake) { f.mem = 24 }, "fail"},
		{"B11 controller 9GiB", "B11", func(f *fake) { f.disk = 9 << 30 }, "fail"},
		{"B11 worker 10GiB", "B11", func(f *fake) { f.winfo.DiskFreeBytes = 10 << 30 }, "pass"},
		{"B11 worker unknown", "B11", func(f *fake) { f.winfo.DiskFreeBytes = 0 }, "fail"},
	}
	for _, c := range cases {
		f := good()
		c.mut(f)
		if got := find(run(f), c.id); got.Status != c.want {
			t.Errorf("%s: %s = %s (%s %s), want %s", c.name, c.id, got.Status, got.Measured, got.Detail, c.want)
		}
	}
}

func TestB5SmokeRecordsAndRestoresMutation(t *testing.T) {
	f := good()
	run(f)
	if len(f.recorded) != 1 || f.recorded[0].Kind != "notifier_enable" || f.recorded[0].CampaignID != "camp-001" {
		t.Fatalf("mutation not recorded: %+v", f.recorded)
	}
	if len(f.sinkSets) != 2 || !f.sinkSets[0] || f.sinkSets[1] || len(f.restored) != 1 {
		t.Fatalf("sink must be enabled then disabled and mutation restored: %v %v", f.sinkSets, f.restored)
	}
	f = good()
	f.receipts = nil
	run(f)
	if len(f.sinkSets) != 2 || len(f.restored) != 1 {
		t.Fatal("failed smoke must still disable and restore")
	}
}

func TestB5ResolvesSmokeAlertOnAllPaths(t *testing.T) {
	for name, mut := range map[string]func(*fake){
		"success":  func(*fake) {},
		"timeout":  func(f *fake) { f.receipts = nil },
		"postfail": func(f *fake) { f.postErr = errors.New("boom") },
	} {
		f := good()
		mut(f)
		run(f)
		if len(f.resolved) != 1 || len(f.posted) != 1 || f.resolved[0]["campaign_run"] != "camp-001-precheck" || f.resolved[0]["alertname"] != f.posted[0]["alertname"] {
			t.Errorf("%s: posted %v resolved %v", name, f.posted, f.resolved)
		}
	}
}

func manyCampaigns(f *fake, n int, cleanup domain.CleanupStatus) {
	f.camps = nil
	for i := 0; i < n; i++ {
		f.camps = append(f.camps, domain.Campaign{ID: fmt.Sprintf("old-%03d", i), Status: domain.CampaignCompleted, Cleanup: cleanup,
			Verdict: domain.VerdictPass, StartedAt: t0.Add(-time.Duration(i+1) * time.Hour)})
	}
}

func TestB7BoundedQueries(t *testing.T) {
	f := good()
	manyCampaigns(f, 20, domain.CleanupClean)
	run(f)
	if len(f.incQueries) != 5 {
		t.Fatalf("want 5 queries, got %v", f.incQueries)
	}
	f = good()
	manyCampaigns(f, 20, domain.CleanupClean)
	f.camps[15].Cleanup = domain.CleanupDirty
	run(f)
	if len(f.incQueries) != 6 {
		t.Fatalf("dirty old campaign must be included: %v", f.incQueries)
	}
}

func TestB7ErrorsAggregate(t *testing.T) {
	f := good()
	manyCampaigns(f, 3, domain.CleanupClean)
	f.incErr = map[string]error{"old-000": errors.New("e0"), "old-002": errors.New("e2")}
	f.incidents["old-001"] = []targetops.Incident{{Number: "INC9"}}
	r := find(run(f), "B7")
	if r.Status != Error || len(f.incQueries) != 3 || !strings.Contains(r.Detail, "old-000") || !strings.Contains(r.Detail, "old-002") || !strings.Contains(r.Detail, "INC9") {
		t.Fatalf("%+v %v", r, f.incQueries)
	}
}

func TestAdvisories(t *testing.T) {
	cases := []struct {
		name, id string
		mut      func(*fake)
	}{
		{"A1", "A1", func(f *fake) { f.hostsDown = []string{"hl01"} }},
		{"A2 29d", "A2", func(f *fake) { f.probe.MetricsRetention = sp("29d") }},
		{"A2 4w", "A2", func(f *fake) { f.probe.MetricsRetention = sp("4w") }},
		{"A2 700h", "A2", func(f *fake) { f.probe.MetricsRetention = sp("700h") }},
		{"A3", "A3", func(f *fake) { f.backlog = 6 }},
		{"A4", "A4", func(f *fake) { f.foreignRate = 1.01 }},
		{"A5", "A5", func(f *fake) { f.probe.AlertHistory.Rows = 10_000_000 }},
	}
	for _, c := range cases {
		f := good()
		c.mut(f)
		if got := find(run(f), c.id); got.Status != "warn" || got.Tier != Advisory {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	for _, ok := range []string{"30d", "1y", "6", "720h", "5w"} {
		f := good()
		f.probe.MetricsRetention = sp(ok)
		if got := find(run(f), "A2"); got.Status != "pass" {
			t.Errorf("retention %s: %+v", ok, got)
		}
	}
	f := good()
	f.foreignRate = 1.0
	if find(run(f), "A4").Status != "pass" {
		t.Error("1/s is not above 1/s")
	}
}

func TestParseVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		cmp  int
		bad  bool
	}{
		{"26.4.6+main.89dc4a9", "26.4.6", 0, false},
		{"26.4.6", "26.4.10", -1, false},
		{"v26.5", "26.4.6", 1, false},
		{"26.4.6.0", "26.4.6", 0, false},
		{"main-89dc4a9", "26.4.6", 0, true},
		{"", "26.4.6", 0, true},
	} {
		got, err := compareVersion(c.a, c.b)
		if (err != nil) != c.bad || (!c.bad && got != c.cmp) {
			t.Errorf("%q vs %q = %d, %v", c.a, c.b, got, err)
		}
	}
}

func TestStartableAdvisoryNeedsAck(t *testing.T) {
	f := good()
	f.hostsDown = []string{"hl01"}
	rs := run(f)
	if ok, _, reason := Startable(rs, nil); ok || !strings.Contains(reason, "A1") {
		t.Fatalf("unacked advisory must block: %v %q", ok, reason)
	}
	ok, cmp, _ := Startable(rs, []string{"A1"})
	if !ok || cmp {
		t.Fatalf("acked advisory: startable but not comparable, got ok=%v cmp=%v", ok, cmp)
	}
}

func TestAnyBlockingFailNotStartableEvenWithAck(t *testing.T) {
	f := good()
	f.hostsDown = []string{"hl01"}
	f.queues[0].Depth = 99
	ok, _, reason := Startable(run(f), []string{"A1", "B3"})
	if ok || !strings.Contains(reason, "B3") {
		t.Fatalf("%v %q", ok, reason)
	}
}

func TestLoadFrontOfficeUsers(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fo-01.json"), []byte(`{"username":"x","password":"secret","api_key":"k1"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "fo-02.json"), []byte(`{"api_key":"k2"}`), 0o600)
	u, err := LoadFrontOfficeUsers(dir, 2)
	if err != nil || len(u) != 2 || u[0].Key != "k1" || u[1].Name != "fo-02" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := LoadFrontOfficeUsers(dir, 3); err == nil {
		t.Fatal("missing user file must error")
	}
}
