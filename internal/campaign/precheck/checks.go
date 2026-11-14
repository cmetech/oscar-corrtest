package precheck

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/scenario"
	"github.com/cmetech/oscar-corrtest/internal/campaign/targetops"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

const (
	retentionMinDays = 30
	recentCampaigns  = 5   // B7 always checks this many most recent campaigns
	foreignRateMax   = 1.0 // A4: non-campaign History arrivals per second
	foreignWindow    = 2 * time.Minute
	snowMinOpsPerHr  = 600
	b9Window         = 60 * time.Minute
	smokeAttempts    = 10
	smokeWait        = 2 * time.Second
	gib              = 1 << 30
)

// b9Jobs are the homelab pack's jobs; each needs a success in the last hour.
var b9Jobs = []string{
	"monitor:check_connection:homelab",
	"monitor:check_processes:homelab_lan",
	"monitor:check_processes:homelab_k8s",
	"monitor:check_host_performance:homelab",
	"homelab_memory:check_memory:homelab",
	"monitor:check_filesystem_space:homelab",
}

type probeData struct {
	r   targetops.ProbeResult
	t   Timing
	err error
}

// compareVersion compares PEP 440 release segments numerically; a local label (+main.<sha>) is ignored.
func compareVersion(a, b string) (int, error) {
	pa, err := parseRelease(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseRelease(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func parseRelease(v string) ([]int, error) {
	rel, _, _ := strings.Cut(strings.TrimSpace(v), "+")
	rel = strings.TrimPrefix(rel, "v")
	var out []int
	for _, p := range strings.Split(rel, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("version %q is not a PEP 440 release (expected N.N.N[+local])", v)
		}
		out = append(out, n)
	}
	return out, nil
}

func flag(m map[string]*string, k string) (string, bool) {
	if v, ok := m[k]; ok && v != nil {
		return strings.ToLower(strings.TrimSpace(*v)), true
	}
	return "", false
}

func b1(ctx context.Context, d Deps, p *probeData, minV string) Result {
	if p.err != nil {
		return errRes("B1", Blocking, "target probe", p.err)
	}
	var probs []string
	ver := "absent"
	if p.r.OscarVersion != nil {
		ver = *p.r.OscarVersion
	}
	if c, err := compareVersion(ver, minV); err != nil {
		probs = append(probs, err.Error())
	} else if c < 0 {
		probs = append(probs, fmt.Sprintf("version %s is below minimum %s", ver, minV))
	}
	if v, ok := flag(p.r.Flags, "RESEND_POLICY_ENABLED"); !ok || v != "true" {
		probs = append(probs, "RESEND_POLICY_ENABLED is not true")
	}
	if v, ok := flag(p.r.Flags, "ALERT_HOOKS_LEGACY_LABELS_ENABLED"); !ok || v != "false" {
		probs = append(probs, "ALERT_HOOKS_LEGACY_LABELS_ENABLED is not false")
	}
	if err := d.ResendSchema(ctx); err != nil {
		probs = append(probs, "resend schema not present: "+err.Error())
	}
	n, err := d.HistoryNotifierCount(ctx)
	if err != nil {
		return errRes("B1", Blocking, "alert history notifier count", err)
	}
	if n != 1 {
		probs = append(probs, fmt.Sprintf("%d Alert History gateway notifiers, want exactly 1", n))
	}
	return res("B1", Blocking, true, "version "+ver, probs)
}

func b2(d Deps, p *probeData, s scenario.Scenario) Result {
	if p.err != nil {
		return errRes("B2", Blocking, "target probe", p.err)
	}
	var probs []string
	now := d.Now()
	for _, c := range p.r.Containers {
		if c.Health == "unhealthy" || c.Health == "starting" { // "none" or "" means no healthcheck: fine
			probs = append(probs, fmt.Sprintf("%s health %q", c.Name, c.Health))
		}
		if c.OOMKilled {
			probs = append(probs, c.Name+" was OOM killed")
		}
		if c.RestartCount > 0 {
			st, err := time.Parse(time.RFC3339Nano, c.StartedAt)
			if err != nil || now.Sub(st) < s.Precheck.RestartWindow {
				probs = append(probs, fmt.Sprintf("%s restarted %d time(s), started_at %q within %s", c.Name, c.RestartCount, c.StartedAt, s.Precheck.RestartWindow))
			}
		}
	}
	if len(p.r.Containers) == 0 {
		probs = append(probs, "probe returned no containers")
	}
	return res("B2", Blocking, true, fmt.Sprintf("%d containers", len(p.r.Containers)), probs)
}

func activeStatus(s domain.CampaignStatus) bool {
	return s != domain.CampaignCompleted && s != domain.CampaignInterrupted
}

func b3(ctx context.Context, d Deps, s scenario.Scenario) Result {
	qs, err := d.Queues(ctx)
	if err != nil {
		return errRes("B3", Blocking, "queues", err)
	}
	camps, err := d.ListCampaigns(ctx, domain.CampaignFilter{})
	if err != nil {
		return errRes("B3", Blocking, "campaign list", err)
	}
	rules, err := d.CampaignRules(ctx)
	if err != nil {
		return errRes("B3", Blocking, "correlation rules", err)
	}
	var probs []string
	for _, q := range qs {
		if !s.Precheck.QueueIdle(float64(q.Depth)) || q.OldestAge > s.Precheck.IdleQueueAge {
			probs = append(probs, fmt.Sprintf("queue %s depth %d age %s", q.Name, q.Depth, q.OldestAge))
		}
	}
	for _, c := range camps {
		if c.ID != d.CampaignID() && activeStatus(c.Status) {
			probs = append(probs, fmt.Sprintf("campaign %s is %s", c.ID, c.Status))
		}
	}
	if len(rules) > 0 {
		probs = append(probs, "enabled correlation rules match campaign_*: "+strings.Join(rules, ","))
	}
	return res("B3", Blocking, true, fmt.Sprintf("%d queues", len(qs)), probs)
}

func skew(server time.Time, t Timing) time.Duration {
	s := server.Sub(t.Mid())
	if s < 0 {
		s = -s
	}
	return s
}

func b4(ctx context.Context, d Deps, p *probeData, s scenario.Scenario) Result {
	wi, wt, err := d.WorkerInfo(ctx)
	if err != nil {
		return errRes("B4", Blocking, "worker info", err)
	}
	var probs []string
	if wi.Version != d.ControllerVersion() {
		probs = append(probs, fmt.Sprintf("worker version %q differs from controller %q", wi.Version, d.ControllerVersion()))
	}
	ws := skew(wi.Now, wt)
	measured := "worker " + ws.String()
	if ws > s.Precheck.ClockSkew {
		probs = append(probs, fmt.Sprintf("worker clock skew %s > %s", ws, s.Precheck.ClockSkew))
	}
	if p.err != nil {
		return errRes("B4", Blocking, "target probe", p.err)
	}
	tu, err := time.Parse(time.RFC3339Nano, p.r.UTC)
	if err != nil {
		probs = append(probs, fmt.Sprintf("probe utc %q unparsable", p.r.UTC))
	} else {
		ts := skew(tu, p.t)
		measured += ", target " + ts.String()
		if ts > s.Precheck.ClockSkew {
			probs = append(probs, fmt.Sprintf("target clock skew %s > %s", ts, s.Precheck.ClockSkew))
		}
	}
	return res("B4", Blocking, true, measured, probs)
}

// b5: ruling C5, no ops-webhook receipt. The smoke enables CAMPAIGN_SINK as a recorded mutation and always disables it.
func b5(ctx context.Context, d Deps) Result {
	if err := d.SinkHealth(ctx); err != nil {
		return Result{ID: "B5", Tier: Blocking, Status: Fail, Detail: "sink unhealthy: " + err.Error()}
	}
	ext, err := d.ExternalNotifiers(ctx)
	if err != nil {
		return errRes("B5", Blocking, "external notifiers", err)
	}
	names := make([]string, 0, len(ext))
	for _, n := range ext {
		names = append(names, n.Name+"("+n.ID+"):"+n.Status)
	}
	measured := "external enabled: " + strings.Join(names, ",")
	on, err := d.SinkEnabled(ctx)
	if err != nil {
		return errRes("B5", Blocking, "sink notifier state", err)
	}
	if on {
		return Result{ID: "B5", Tier: Blocking, Status: Fail, Measured: measured, Detail: "CAMPAIGN_SINK already enabled; smoke not run (see B8)"}
	}
	received, smokeErr := smoke(ctx, d)
	var probs []string
	if smokeErr != nil {
		probs = append(probs, smokeErr.Error())
	} else if !received {
		probs = append(probs, "target to CAMPAIGN_SINK smoke alert not received")
	}
	return res("B5", Blocking, true, measured, probs)
}

func smoke(ctx context.Context, d Deps) (received bool, err error) {
	id, err := d.RecordMutation(ctx, domain.CampaignMutation{CampaignID: d.CampaignID(), Kind: "notifier_enable", ObjectID: "CAMPAIGN_SINK",
		Prior: json.RawMessage(`{"status":"disabled"}`), RecordedAt: d.Now()})
	if err != nil {
		return false, fmt.Errorf("record mutation: %w", err)
	}
	run := d.CampaignID() + "-precheck"
	labels := map[string]string{"alertname": "CampaignPrecheckSmoke", "campaign_run": run}
	posted := false
	defer func() {
		// Restore even if the caller's context was cancelled.
		rctx := context.WithoutCancel(ctx)
		if posted { // never leave the smoke alert firing; best effort, it expires with the sink off
			if rerr := d.ResolveAlert(rctx, labels); rerr != nil && err == nil {
				err = fmt.Errorf("resolve smoke alert: %w", rerr)
			}
		}
		if derr := d.SetSinkEnabled(rctx, false); derr != nil {
			err = fmt.Errorf("CAMPAIGN_SINK left enabled, mutation %d stays open: %v", id, derr)
			return
		}
		if merr := d.MarkMutationRestored(rctx, id, d.Now()); merr != nil && err == nil {
			err = fmt.Errorf("mark mutation %d restored: %w", id, merr)
		}
	}()
	if err := d.SetSinkEnabled(ctx, true); err != nil {
		return false, fmt.Errorf("enable CAMPAIGN_SINK: %w", err)
	}
	since := d.Now().Add(-2 * time.Second) // receipts filter at one-second resolution
	posted = true                          // resolve even if the post errored: it may have landed
	if err := d.PostAlert(ctx, labels); err != nil {
		return false, fmt.Errorf("post smoke alert: %w", err)
	}
	for i := 0; i < smokeAttempts; i++ {
		rs, rerr := d.SinkReceipts(ctx, run, since)
		if rerr != nil {
			return false, fmt.Errorf("read sink receipts: %w", rerr)
		}
		if len(rs) > 0 {
			return true, nil
		}
		if serr := d.Sleep(ctx, smokeWait); serr != nil {
			return false, serr
		}
	}
	return false, nil
}

func b6(ctx context.Context, d Deps, s scenario.Scenario) Result {
	down, err := d.ScrapeDown(ctx)
	if err != nil {
		return errRes("B6", Blocking, "metrics scrape jobs", err)
	}
	fresh, err := d.Freshness(ctx)
	if err != nil {
		return errRes("B6", Blocking, "metrics freshness", err)
	}
	var probs []string
	if len(down) > 0 {
		probs = append(probs, "scrape jobs down: "+strings.Join(down, ","))
	}
	var worst time.Duration
	for fam, age := range fresh {
		if age > worst {
			worst = age
		}
		if age > s.Precheck.MetricsFreshness {
			probs = append(probs, fmt.Sprintf("family %s newest sample %s old", fam, age))
		}
	}
	if len(fresh) == 0 {
		probs = append(probs, "no scorecard family freshness reported")
	}
	return res("B6", Blocking, true, "worst age "+worst.String(), probs)
}

// priorCampaignIDs lists earlier campaign ids. The SNOW marker is "Campaign-<run>-", so checking the campaign id also
// matches runs named "<campaign id>-<suffix>". Cost: one SNOW query per earlier campaign.
func priorCampaignIDs(ctx context.Context, d Deps) ([]domain.Campaign, error) {
	all, err := d.ListCampaigns(ctx, domain.CampaignFilter{})
	if err != nil {
		return nil, err
	}
	var out []domain.Campaign
	for _, c := range all {
		if c.ID != d.CampaignID() {
			out = append(out, c)
		}
	}
	return out, nil
}

// b7Scope bounds SNOW queries (ruling C19): the most recent recentCampaigns, plus any older campaign whose cleanup is
// not CLEAN, that was interrupted, or that has no verdict.
func b7Scope(prior []domain.Campaign) []domain.Campaign {
	sorted := append([]domain.Campaign(nil), prior...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartedAt.After(sorted[j].StartedAt) })
	var out []domain.Campaign
	for i, c := range sorted {
		if i < recentCampaigns || c.Cleanup != domain.CleanupClean || c.Status == domain.CampaignInterrupted || c.Verdict == "" {
			out = append(out, c)
		}
	}
	return out
}

func b7(ctx context.Context, d Deps) Result {
	var probs []string
	rate, err := d.SNOWRate(ctx)
	if err != nil {
		return errRes("B7", Blocking, "ticketing rate limits", err)
	}
	perHour := rate.OpsPerSecond * 3600
	if !rate.Healthy || !rate.Configured || rate.Paused {
		probs = append(probs, fmt.Sprintf("snow provider healthy=%v configured=%v paused=%v", rate.Healthy, rate.Configured, rate.Paused))
	}
	if math.Round(perHour*1e6)/1e6 < snowMinOpsPerHr {
		probs = append(probs, fmt.Sprintf("snow rate %.0f ops/hour < %d", perHour, snowMinOpsPerHr))
	}
	if err := d.SNOWWake(ctx); err != nil {
		probs = append(probs, "PDI not awake: "+err.Error())
	} else if err := d.SNOWCredential(ctx); err != nil {
		probs = append(probs, "SNOW credential check failed: "+err.Error())
	}
	prior, err := priorCampaignIDs(ctx, d)
	if err != nil {
		return errRes("B7", Blocking, "campaign list", err)
	}
	open := 0
	var failed []string
	for _, c := range b7Scope(prior) {
		inc, err := d.OpenIncidents(ctx, c.ID)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", c.ID, err))
			continue
		}
		for _, i := range inc {
			open++
			probs = append(probs, fmt.Sprintf("open incident %s from campaign %s", i.Number, c.ID))
		}
	}
	if len(failed) > 0 {
		r := res("B7", Blocking, true, fmt.Sprintf("%.0f ops/hour, %d open prior incidents", perHour, open), probs)
		r.Status = Error
		r.Detail = strings.Trim(r.Detail+"; incident query failed for "+strings.Join(failed, " | "), "; ")
		return r
	}
	return res("B7", Blocking, true, fmt.Sprintf("%.0f ops/hour, %d open prior incidents", perHour, open), probs)
}

func b8(ctx context.Context, d Deps) Result {
	prior, err := priorCampaignIDs(ctx, d)
	if err != nil {
		return errRes("B8", Blocking, "campaign list", err)
	}
	var probs []string
	for _, c := range prior {
		ms, err := d.OpenMutations(ctx, c.ID)
		if err != nil {
			return errRes("B8", Blocking, "open mutations for "+c.ID, err)
		}
		for _, m := range ms {
			probs = append(probs, fmt.Sprintf("campaign %s has unrestored %s mutation %d (%s)", c.ID, m.Kind, m.ID, m.ObjectID))
		}
	}
	var last *domain.Campaign
	for i := range prior {
		if prior[i].Status == domain.CampaignCompleted && (last == nil || prior[i].StartedAt.After(last.StartedAt)) {
			last = &prior[i]
		}
	}
	if last != nil && last.Cleanup != domain.CleanupClean && last.Cleanup != domain.CleanupNotRequired {
		probs = append(probs, fmt.Sprintf("previous campaign %s cleanup is %q, restore not confirmed", last.ID, last.Cleanup))
	}
	on, err := d.SinkEnabled(ctx)
	if err != nil {
		return errRes("B8", Blocking, "sink notifier state", err)
	}
	if on {
		probs = append(probs, "CAMPAIGN_SINK is enabled")
	}
	alerts, err := d.OpenCampaignAlerts(ctx)
	if err != nil {
		return errRes("B8", Blocking, "open campaign alerts", err)
	}
	if alerts > 0 {
		probs = append(probs, fmt.Sprintf("%d open campaign alerts", alerts))
	}
	left, err := d.LeftoverObjects(ctx)
	if err != nil {
		return errRes("B8", Blocking, "leftover rules and policies", err)
	}
	if len(left) > 0 {
		probs = append(probs, "leftover rules/policies: "+strings.Join(left, ","))
	}
	return res("B8", Blocking, true, fmt.Sprintf("%d prior campaigns", len(prior)), probs)
}

func b9(ctx context.Context, d Deps) Result {
	end := d.Now()
	runs, err := d.TaskRuns(ctx, b9Jobs, end.Add(-b9Window), end)
	if err != nil {
		return errRes("B9", Blocking, "task history", err)
	}
	ok := map[string]bool{}
	for _, r := range runs {
		if strings.EqualFold(r.Status, "SUCCESS") && !r.StartedAt.Before(end.Add(-b9Window)) {
			ok[r.Job] = true
		}
	}
	var probs []string
	for _, j := range b9Jobs {
		if !ok[j] {
			probs = append(probs, "no successful run in 60m: "+j)
		}
	}
	return res("B9", Blocking, true, fmt.Sprintf("%d/%d jobs ok", len(b9Jobs)-len(probs), len(b9Jobs)), probs)
}

func b10(ctx context.Context, d Deps) Result {
	users, err := d.FrontOfficeUsers()
	if err != nil {
		return errRes("B10", Blocking, "front office users", err)
	}
	var probs []string
	for _, u := range users {
		st, err := d.FOHistory(ctx, u.Key)
		if err != nil {
			probs = append(probs, fmt.Sprintf("%s: %v", u.Name, err))
		} else if st != 200 {
			probs = append(probs, fmt.Sprintf("%s: HTTP %d", u.Name, st))
		}
	}
	if len(users) == 0 {
		probs = append(probs, "no front office users")
	}
	return res("B10", Blocking, true, fmt.Sprintf("%d/%d keys ok", len(users)-len(probs), len(users)), probs)
}

func b11(ctx context.Context, d Deps, p *probeData, s scenario.Scenario) Result {
	diskPct, memPct, err := d.TargetHeadroom(ctx)
	if err != nil {
		return errRes("B11", Blocking, "target headroom", err)
	}
	local, err := d.LocalDiskFree()
	if err != nil {
		return errRes("B11", Blocking, "controller disk", err)
	}
	wi, _, err := d.WorkerInfo(ctx)
	if err != nil {
		return errRes("B11", Blocking, "worker info", err)
	}
	var probs []string
	if diskPct < s.Precheck.TargetDiskFreePct {
		probs = append(probs, fmt.Sprintf("target disk free %.1f%% < %.0f%%", diskPct, s.Precheck.TargetDiskFreePct))
	}
	if memPct < s.Precheck.TargetMemAvailPct {
		probs = append(probs, fmt.Sprintf("target memory available %.1f%% < %.0f%%", memPct, s.Precheck.TargetMemAvailPct))
	}
	min := uint64(s.Precheck.RunnerDiskFreeGiB * gib)
	if local < min {
		probs = append(probs, fmt.Sprintf("controller disk free %.1f GiB < %.0f", float64(local)/gib, s.Precheck.RunnerDiskFreeGiB))
	}
	if wi.DiskFreeBytes < min { // 0 = worker did not report; fail rather than assume
		probs = append(probs, fmt.Sprintf("worker disk free %.1f GiB < %.0f", float64(wi.DiskFreeBytes)/gib, s.Precheck.RunnerDiskFreeGiB))
	}
	m := fmt.Sprintf("target disk %.0f%% mem %.0f%%, controller %.0f GiB, worker %.0f GiB", diskPct, memPct, float64(local)/gib, float64(wi.DiskFreeBytes)/gib)
	if p.err == nil && p.r.AlertHistory != nil {
		m += fmt.Sprintf(", history %d rows %d bytes", p.r.AlertHistory.Rows, p.r.AlertHistory.DataBytes)
	}
	return res("B11", Blocking, true, m, probs)
}

func a1(ctx context.Context, d Deps) Result {
	h, err := d.HostsDown(ctx)
	if err != nil {
		return errRes("A1", Advisory, "host status", err)
	}
	var probs []string
	if len(h) > 0 {
		probs = append(probs, "homelab hosts down or in maintenance: "+strings.Join(h, ","))
	}
	return res("A1", Advisory, true, fmt.Sprintf("%d hosts down", len(h)), probs)
}

var retentionRE = regexp.MustCompile(`^(\d+(?:\.\d+)?)([hdwmy]?)$`)

// parseRetention reads the VictoriaMetrics flag. Unitless means months (ruling C6); m is months as well.
func parseRetention(raw string) (time.Duration, error) {
	m := retentionRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(raw)))
	if m == nil {
		return 0, fmt.Errorf("unrecognised retention %q", raw)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	day := 24 * float64(time.Hour)
	mult := map[string]float64{"h": float64(time.Hour), "d": day, "w": 7 * day, "": 30 * day, "m": 30 * day, "y": 365 * day}[m[2]]
	return time.Duration(n * mult), nil
}

func a2(p *probeData) Result {
	if p.err != nil {
		return errRes("A2", Advisory, "target probe", p.err)
	}
	if p.r.MetricsRetention == nil {
		return errRes("A2", Advisory, "metrics retention", fmt.Errorf("probe did not report it"))
	}
	dur, err := parseRetention(*p.r.MetricsRetention)
	if err != nil {
		return errRes("A2", Advisory, "metrics retention", err)
	}
	days := dur.Hours() / 24
	var probs []string
	if days < retentionMinDays {
		probs = append(probs, fmt.Sprintf("retention %.1f days < %d", days, retentionMinDays))
	}
	return res("A2", Advisory, true, fmt.Sprintf("raw %q = %.0f days", *p.r.MetricsRetention, days), probs)
}

func a3(ctx context.Context, d Deps) Result {
	n, base, err := d.FiringBacklog(ctx)
	if err != nil {
		return errRes("A3", Advisory, "firing backlog", err)
	}
	var probs []string
	if n > base {
		probs = append(probs, fmt.Sprintf("%d firing alerts above idle baseline %d", n, base))
	}
	return res("A3", Advisory, true, fmt.Sprintf("%d firing (baseline %d)", n, base), probs)
}

func a4(ctx context.Context, d Deps) Result {
	r, err := d.ForeignArrivalRate(ctx, foreignWindow)
	if err != nil {
		return errRes("A4", Advisory, "foreign arrival rate", err)
	}
	var probs []string
	if r > foreignRateMax {
		probs = append(probs, fmt.Sprintf("non-campaign History arrivals %.2f/s over the last %s", r, foreignWindow))
	}
	return res("A4", Advisory, true, fmt.Sprintf("%.2f/s", r), probs)
}

func a5(p *probeData, s scenario.Scenario) Result {
	if p.err != nil || p.r.AlertHistory == nil {
		return errRes("A5", Advisory, "alert history size", fmt.Errorf("probe did not report it"))
	}
	var probs []string
	if p.r.AlertHistory.Rows > s.Precheck.HistoryRowsAdvisory {
		probs = append(probs, fmt.Sprintf("%d rows above %d (recorded, never purged)", p.r.AlertHistory.Rows, s.Precheck.HistoryRowsAdvisory))
	}
	return res("A5", Advisory, true, fmt.Sprintf("%d rows", p.r.AlertHistory.Rows), probs)
}

// LoadFrontOfficeUsers reads <dir>/fo-0N.json for N in 1..n and keeps api_key only.
func LoadFrontOfficeUsers(dir string, n int) ([]FOUser, error) {
	out := make([]FOUser, 0, n)
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("fo-%02d", i)
		raw, err := os.ReadFile(filepath.Join(dir, name+".json")) // #nosec G304 -- operator-configured directory
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var v struct {
			APIKey string `json:"api_key"`
		}
		if json.Unmarshal(raw, &v) != nil || v.APIKey == "" {
			return nil, fmt.Errorf("%s has no api_key", name)
		}
		out = append(out, FOUser{Name: name, Key: v.APIKey})
	}
	return out, nil
}
