package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign"
	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/campaign/managed"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
	"github.com/cmetech/oscar-corrtest/internal/campaign/receivers"
	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/campaign/worker"
	"github.com/cmetech/oscar-corrtest/internal/campaign/workerclient"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	corr "github.com/cmetech/oscar-corrtest/internal/scenario"
)

// Host is what the real engine needs from the runtime.
type Host interface {
	CampaignDatabase() *storage.Database
	CampaignTarget(ctx context.Context, targetID string) (domain.Target, *oscar.Client, error)
	CampaignDataDir() string
	ExportRun(ctx context.Context, runID, destination string) (evidence.Result, error)
	ExecuteScenarioForCampaign(ctx context.Context, targetID string, s corr.Scenario, mode string) (domain.Run, error)
}

const ingestPath = "/ext/alertmanager/api/v2/alerts"

// NewFromConfig wires the engine to the real clients named by cfg.
func NewFromConfig(ctx context.Context, cfg campaign.Config, host Host) (*Engine, error) {
	for name, path := range map[string]string{"worker token": cfg.WorkerTokenFile, "sink admin token": cfg.SinkAdminTokenFile,
		"metrics credential": cfg.MetricsCredentialFile, "SNOW credential": cfg.SNOWCredentialFile, "probe key": cfg.ProbeKeyFile} {
		if path == "" && name == "metrics credential" { // optional (campaign.Config)
			continue
		}
		if _, err := os.Stat(path); err != nil { // fail at startup, not mid-campaign
			return nil, fmt.Errorf("%s file is not readable", name)
		}
	}
	target, oc, err := host.CampaignTarget(ctx, cfg.TargetID)
	if err != nil {
		return nil, fmt.Errorf("campaign target: %w", err)
	}
	u, err := url.Parse(target.BaseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("campaign target base URL %q is invalid", target.BaseURL)
	}
	root := u.Scheme + "://" + u.Host
	wc, err := workerclient.New(cfg.WorkerURL, cfg.WorkerTokenFile, cfg.WorkerCAFile)
	if err != nil {
		return nil, fmt.Errorf("worker client: %w", err)
	}
	sink, err := targetops.NewSink(cfg.SinkURL, cfg.SinkAdminTokenFile, cfg.SinkCAFile)
	if err != nil {
		return nil, fmt.Errorf("sink client: %w", err)
	}
	metrics, err := targetops.NewMetrics(cfg.MetricsURL, cfg.MetricsCAFile, cfg.MetricsCredentialFile)
	if err != nil {
		return nil, fmt.Errorf("metrics client: %w", err)
	}
	snow, err := targetops.NewSNOW(cfg.SNOWInstanceURL, cfg.SNOWCredentialFile, "")
	if err != nil {
		return nil, fmt.Errorf("snow client: %w", err)
	}
	targetHTTP, err := httpClient(target.TLS.CAPath)
	if err != nil {
		return nil, err
	}
	sinkHTTP, err := httpClient(cfg.SinkCAFile)
	if err != nil {
		return nil, err
	}
	var users []frontoffice.User
	if cfg.FrontOfficeDir != "" {
		if users, err = frontoffice.LoadUsers(cfg.FrontOfficeDir, scenario.AlertingDayV1().FrontOffice.Users); err != nil {
			return nil, fmt.Errorf("front-office users: %w", err)
		}
	}
	db := host.CampaignDatabase()
	clock := worker.RealClock{}
	probe := func(ctx context.Context) (targetops.ProbeResult, error) {
		return targetops.Probe(ctx, "ssh", cfg.ProbeKeyFile, cfg.ProbeHost)
	}
	notifier := cfg.CampaignNotifier
	if notifier == "" {
		notifier = "CAMPAIGN_SINK"
	}
	if host.CampaignDataDir() == "" {
		return nil, errors.New("campaign data directory is not set")
	}
	reportDir := filepath.Join(host.CampaignDataDir(), "campaigns")
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		return nil, fmt.Errorf("campaign report directory: %w", err)
	}
	d := Deps{ObserverDir: cfg.NativeObserverDir, ReportDir: reportDir, Store: db, Worker: wc, Target: oscarTarget{oc}, SNOW: snow, Sink: sink, Metrics: metrics, Probe: probe,
		Correlate: host.ExecuteScenarioForCampaign, ExportRun: host.ExportRun, TargetID: target.ID, IngestURL: root, CampaignNotifier: notifier,
		FrontOfficeURL: root, FrontOfficeHTTP: targetHTTP, FrontOfficeUsers: users, Fingerprint: targetops.FingerprintPolicies{C: oc}}
	d.nativeSender, err = lane.NewAlertmanagerSender(root+ingestPath, target.TLS.CAPath, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if cfg.ManagedTL1 != nil {
		actor, err := managed.NewActor(*cfg.ManagedTL1, root, targetHTTP)
		d.managed = &managedDeps{setupErr: err, originals: *cfg.ManagedTL1, config: *cfg.ManagedTL1, actor: actor, oscar: oc, sink: sink, snow: snow}
	}
	if len(users) > 0 { // v4 tasks and reports refuse system-owned keys: use the first front-office user
		d.UserOps = targetops.UserAPI{Root: root, Key: users[0].APIKey, HTTP: targetHTTP}
		d.Maintenance = targetops.UserAPI{Root: root, Key: users[0].APIKey, HTTP: targetHTTP}
	}
	getenv := os.Getenv // the runtime's environment file when it offers one
	if h, ok := host.(interface{ CampaignGetenv(string) string }); ok {
		getenv = h.CampaignGetenv
	}
	d.Receivers = targetops.Receivers{C: oc, Root: root, HTTP: targetHTTP, Community: targetops.CommunityFromEnv(getenv)}
	d.TaskRuns = func(ctx context.Context, start, end time.Time) ([]targetops.TaskRun, error) {
		return targetops.TaskRuns(ctx, oc, precheck.HomelabJobs, start, end)
	}
	d.Precheck = func(ctx context.Context, s scenario.Scenario) []precheck.Result {
		a := &PrecheckAdapter{ID: campaignIDFrom(ctx), Clock: clock, Oscar: oc, Metrics: metrics, ProbeFn: probe, WorkerInfoFn: wc.Info,
			Store: db, Sink: sink, SinkHealthURL: cfg.SinkURL + "/healthz", SinkHTTP: sinkHTTP, SNOW: snow, Users: users,
			FrontOfficeURL: root, HTTP: targetHTTP, CampaignNotifier: notifier, IngestURL: root + ingestPath, IngestHTTP: targetHTTP,
			Queries: DefaultPrecheckQueries}
		if s.ID == "managed-alerts-smoke" && s.Version == "v1" {
			results := []precheck.Result{d.managed.precheck(ctx)}
			_, err := controlledNotifiers(ctx, d.Target, d.CampaignNotifier)
			check := precheck.Result{ID: "managed-destinations", Tier: precheck.Blocking, Status: precheck.Pass, Detail: "controlled notifiers enabled"}
			if err != nil {
				check.Status = precheck.Fail
				check.Detail = err.Error()
			}
			return append(results, check)
		}
		results := precheck.Run(ctx, a, s, MinVersion)
		results = append(results, v4Prechecks(ctx, d, s)...)
		if s.NativeWorkload != nil && len(s.NativeWorkload.Cohorts) > 0 {
			detail := "Scoring requires observed ordinary group decisions, request attempts and receipts."
			if !s.Custom {
				detail += " Full cost scoring also requires matched SQL/HTTP traces, overhead and build provenance."
			}
			check := precheck.Result{ID: "ordinary-capture-directory", Tier: precheck.Advisory, Detail: detail, Status: precheck.Pass, Measured: "observer directory readable"}
			root, err := os.OpenRoot(d.ObserverDir)
			if err != nil {
				check.Status = precheck.Warn
				check.Measured = "observer directory unavailable"
			} else {
				_ = root.Close()
			}
			results = append(results, check)
		}
		if s.NativeWorkload != nil && s.NativeWorkload.ManagedTL1 {
			results = append(results, d.managed.precheck(ctx))
		}
		return results
	}
	return New(d, clock), nil
}

// v4Prechecks are the blocking v4 checks: B12 escalation scheduling, B14
// fingerprint policies commissioned, B15 one task run succeeds, B16
// maintenance API usable and clean, B13 alert report approved; each only for
// a scenario with that lane.
func v4Prechecks(ctx context.Context, d Deps, s scenario.Scenario) []precheck.Result {
	var out []precheck.Result
	if precheck.NeedsEscalation(s) {
		st, err := func() (targetops.PolicyStatus, error) {
			all, err := d.Target.Notifiers(ctx)
			if err != nil {
				return targetops.PolicyStatus{}, err
			}
			for _, n := range all {
				if strings.EqualFold(n.Name, d.CampaignNotifier) {
					return d.Target.PolicyStatus(ctx, n.ID, "notification", "")
				}
			}
			return targetops.PolicyStatus{}, fmt.Errorf("notifier %s not found", d.CampaignNotifier)
		}()
		out = append(out, precheck.EscalationSchedule(st, err))
	}
	if f := s.Fingerprint; f != nil && f.Every > 0 {
		why, err := "no fingerprint policy client", error(nil)
		if d.Fingerprint != nil {
			why, err = d.Fingerprint.Preflight(ctx)
		}
		out = append(out, precheck.FingerprintReady(why, err))
	}
	if t := s.Tasks; t != nil {
		status, err := "", errors.New("no front-office user credential (campaign-fo)")
		if d.UserOps != nil {
			status, err = precheckTask(ctx, d.UserOps, t)
		}
		out = append(out, precheck.TaskRunnable(t.Job, status, err))
	}
	if s.Maintenance != nil {
		switch {
		case skipped(ctx, maintLane):
			out = append(out, precheck.SkippedLane("B16", maintLane))
		case d.Maintenance == nil:
			out = append(out, precheck.MaintenanceReady("", nil, errors.New("no front-office user credential (campaign-fo)")))
		default:
			out = append(out, precheck.MaintenanceReady(precheckMaint(ctx, d.Maintenance)))
		}
	}
	if s.Receivers != nil {
		switch {
		case skipped(ctx, receivers.Lane):
			out = append(out, precheck.SkippedLane("B17", receivers.Lane))
		case d.Receivers == nil:
			out = append(out, precheck.ReceiversReady("", nil, errors.New("no receivers client")))
		default:
			out = append(out, precheckReceivers(ctx, d.Receivers))
		}
	}
	if r := s.Report; r != nil {
		if skipped(ctx, "report") {
			out = append(out, precheck.SkippedLane("B13", "report"))
		} else if d.UserOps == nil {
			out = append(out, precheck.ReportSetup(r.Key, targetops.ReportInfo{}, errors.New("no front-office user credential (campaign-fo)")))
		} else {
			info, err := d.UserOps.Report(ctx, r.Key)
			out = append(out, precheck.ReportSetup(r.Key, info, err))
		}
	}
	return out
}

// precheckTask runs the v4 task once (B15) on wall time and returns its
// terminal status, or "timeout" after t.Timeout.
func precheckTask(ctx context.Context, u UserOps, t *scenario.TaskExecSpec) (string, error) {
	id, err := u.RunTask(ctx, t.Job, newUUID())
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(t.Timeout)
	for {
		status, done, err := u.TaskResult(ctx, id)
		if done {
			return status, nil
		}
		if !time.Now().Before(deadline) {
			if err != nil {
				return "", err
			}
			return "timeout", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(taskPoll):
		}
	}
}

type campaignIDKey struct{}

// skipLanesKey carries the run's --skip-lane option to the pre-checks.
type skipLanesKey struct{}

// skipped reports whether the run on ctx skips lane.
func skipped(ctx context.Context, lane string) bool {
	lanes, _ := ctx.Value(skipLanesKey{}).([]string)
	return slices.Contains(lanes, lane)
}

// campaignIDFrom returns the campaign id Run put on ctx for the pre-checks.
func campaignIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(campaignIDKey{}).(string)
	return id
}

func httpClient(caFile string) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile) // #nosec G304 -- operator-configured CA path from the campaign config, not request input.
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file %s has no certificates", caFile)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}, nil
}

// oscarTarget implements Target with the targetops functions.
type oscarTarget struct{ c *oscar.Client }

func (t oscarTarget) Notifiers(ctx context.Context) ([]targetops.Notifier, error) {
	return targetops.ListNotifiers(ctx, t.c)
}
func (t oscarTarget) SetNotifier(ctx context.Context, id string, on bool) (targetops.Notifier, error) {
	return targetops.SetNotifierStatus(ctx, t.c, id, on)
}
func (t oscarTarget) CreateWebhookNotifier(ctx context.Context, name, endpoint string) (targetops.Notifier, error) {
	return targetops.CreateWebhookNotifier(ctx, t.c, name, endpoint)
}
func (t oscarTarget) DeleteNotifier(ctx context.Context, id string) error {
	return targetops.DeleteNotifier(ctx, t.c, id)
}
func (t oscarTarget) CreateDraft(ctx context.Context, def targetops.PolicyDefinition, rev int) (targetops.Policy, error) {
	return targetops.CreateDraft(ctx, t.c, def, rev)
}
func (t oscarTarget) Activate(ctx context.Context, id, nid, action string, rev int, wait time.Duration) (targetops.Policy, error) {
	return targetops.Activate(ctx, t.c, id, nid, action, rev, wait)
}
func (t oscarTarget) Deactivate(ctx context.Context, id, nid, action string, rev int) (int, error) {
	return targetops.Deactivate(ctx, t.c, id, nid, action, rev)
}
func (t oscarTarget) Delete(ctx context.Context, id, nid, action string, rev int) (int, error) {
	return targetops.Delete(ctx, t.c, id, nid, action, rev)
}
func (t oscarTarget) PolicyStatus(ctx context.Context, nid, action, policyID string) (targetops.PolicyStatus, error) {
	return targetops.Status(ctx, t.c, nid, action, policyID)
}
func (t oscarTarget) ListPolicies(ctx context.Context, nid, action, name string) ([]targetops.PolicyItem, error) {
	return targetops.ListPolicies(ctx, t.c, nid, action, name)
}
func (t oscarTarget) History(ctx context.Context, run string, start, end time.Time) ([]oscar.HistoryRecord, error) {
	return t.c.HistoryByLabel(ctx, "campaign_run", run, start, end)
}

// PostAlert sends a firing Alertmanager webhook delivery to the middleware
// (POST /api/v1/alerts; see the cohort route note in cohorts.go).
func (t oscarTarget) AlertLimits(ctx context.Context) (targetops.AlertLimits, error) {
	return targetops.GetAlertLimits(ctx, t.c)
}
func (t oscarTarget) SetAlertLimits(ctx context.Context, rev int, overrides json.RawMessage) (targetops.AlertLimits, error) {
	return targetops.PutAlertLimits(ctx, t.c, rev, overrides)
}

func (t oscarTarget) PostAlert(ctx context.Context, a Alert) (int, error) {
	req, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: a.Name, Status: "firing", Labels: a.Labels,
		Annotations: map[string]string{"summary": a.Name}, StartsAt: a.StartsAt})
	if err != nil {
		return 400, err
	}
	return t.PostAlertGroup(ctx, req)
}

// PostAlertGroup posts a prebuilt envelope. Transport errors and timeouts
// return status 0: the outcome is unknown.
func (t oscarTarget) PostAlertGroup(ctx context.Context, req oscar.AlertGroupRequest) (int, error) {
	err := t.c.DoJSON(ctx, http.MethodPost, "/api/v1/alerts", nil, req, nil)
	if err == nil {
		return 200, nil
	}
	var me *oscar.MachineError
	if errors.As(err, &me) && me.StatusCode != 0 {
		return me.StatusCode, err // the server answered
	}
	return 0, err // transport failure or timeout: outcome unknown
}
