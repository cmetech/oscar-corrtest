package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign"
	"github.com/cmetech/oscar-corrtest/internal/campaign/frontoffice"
	"github.com/cmetech/oscar-corrtest/internal/campaign/precheck"
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
	d := Deps{ReportDir: reportDir, Store: db, Worker: wc, Target: oscarTarget{oc}, SNOW: snow, Sink: sink, Metrics: metrics, Probe: probe,
		Correlate: host.ExecuteScenarioForCampaign, ExportRun: host.ExportRun, TargetID: target.ID, IngestURL: root, CampaignNotifier: notifier,
		FrontOfficeURL: root, FrontOfficeHTTP: targetHTTP, FrontOfficeUsers: users}
	d.Precheck = func(ctx context.Context, s scenario.Scenario) []precheck.Result {
		a := &PrecheckAdapter{ID: campaignIDFrom(ctx), Clock: clock, Oscar: oc, Metrics: metrics, ProbeFn: probe, WorkerInfoFn: wc.Info,
			Store: db, Sink: sink, SinkHealthURL: cfg.SinkURL + "/healthz", SinkHTTP: sinkHTTP, SNOW: snow, Users: users,
			FrontOfficeURL: root, HTTP: targetHTTP, CampaignNotifier: notifier, IngestURL: root + ingestPath, IngestHTTP: targetHTTP,
			Queries: DefaultPrecheckQueries}
		return precheck.Run(ctx, a, s, MinVersion)
	}
	return New(d, clock), nil
}

type campaignIDKey struct{}

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

// PostAlert sends an Alertmanager webhook delivery to the middleware
// (POST /api/v1/alerts; see the cohort route note in cohorts.go). Transport
// errors and timeouts return status 0: the outcome is unknown.
func (t oscarTarget) PostAlert(ctx context.Context, a Alert) (int, error) {
	status := "firing"
	if !a.EndsAt.IsZero() {
		status = "resolved"
	}
	req, err := oscar.BuildAlertRequest(compiler.AlertPlan{Name: a.Name, Status: status, Labels: a.Labels,
		Annotations: map[string]string{"summary": a.Name}, StartsAt: a.StartsAt, EndsAt: a.EndsAt})
	if err != nil {
		return 400, err
	}
	err = t.c.DoJSON(ctx, http.MethodPost, "/api/v1/alerts", nil, req, nil)
	if err == nil {
		return 200, nil
	}
	var me *oscar.MachineError
	if errors.As(err, &me) && me.StatusCode != 0 {
		return me.StatusCode, err // the server answered
	}
	return 0, err // transport failure or timeout: outcome unknown
}
