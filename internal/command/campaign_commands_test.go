package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

func campaignCLI(t *testing.T, h http.HandlerFunc, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tok, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	campaignPollInterval = time.Millisecond
	var out, errb bytes.Buffer
	code := New(&out, &errb, version.Info{}, nil).Run(context.Background(), append(args, "--server", srv.URL, "--token-file", tok))
	if strings.Contains(out.String()+errb.String(), "secret-token") {
		t.Fatal("token leaked")
	}
	return code, out.String(), errb.String()
}

func campaignServer(status, verdict string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/campaign/runs" && status == "ACTIVE":
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"campaign_active","message":"busy"}}`))
		case r.Method == "POST":
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"apiVersion":"x","data":{"ID":"cmp_1","Status":"RUNNING"}}`))
		case r.URL.Path == "/api/v1/campaign/compare":
			w.WriteHeader(501)
			_, _ = w.Write([]byte(`{"error":{"code":"not_implemented","message":"no"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"campaign":{"ID":"cmp_1","Status":"` + status + `","Verdict":"` + verdict + `"}}}`))
		}
	}
}

func TestCampaignRunJSONWrapsAPIVersion(t *testing.T) {
	code, out, _ := campaignCLI(t, campaignServer("COMPLETED", "PASS"), "campaign", "run", "--output", "json")
	if code != 0 || !strings.Contains(out, `"apiVersion":"corrtest.oscar/v1alpha1"`) || !strings.Contains(out, "cmp_1") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestCampaignRunRefusedExit3(t *testing.T) {
	if code, _, _ := campaignCLI(t, campaignServer("ACTIVE", ""), "campaign", "run"); code != 3 {
		t.Fatalf("code=%d", code)
	}
}

func TestCampaignWaitFailExit4(t *testing.T) {
	if code, _, _ := campaignCLI(t, campaignServer("COMPLETED", "FAIL"), "campaign", "run", "--wait"); code != 4 {
		t.Fatalf("code=%d", code)
	}
	if code, _, _ := campaignCLI(t, campaignServer("COMPLETED", "PASS"), "campaign", "run", "--wait"); code != 0 {
		t.Fatalf("pass code=%d", code)
	}
}

func TestCampaignPrecheckFailureExit3(t *testing.T) {
	if code, _, _ := campaignCLI(t, campaignServer("COMPLETED", "ERROR"), "campaign", "precheck"); code != 3 {
		t.Fatalf("code=%d", code)
	}
}

func TestCampaignCompareNotImplementedExit1(t *testing.T) {
	code, _, errs := campaignCLI(t, campaignServer("", ""), "campaign", "compare", "candidate",
		"--reference-run", "baseline", "--scenario-hash", "hash", "--mode", "control_before_after")
	if code != 1 || !strings.Contains(errs, "not implemented") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

func TestCampaignRequiresServerAndToken(t *testing.T) {
	var out, errb bytes.Buffer
	if code := New(&out, &errb, version.Info{}, nil).Run(context.Background(), []string{"campaign", "run"}); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

// wireFake serves the real web handler's campaign routes.
type wireFake struct {
	web.DataSource
	active  bool
	started []appruntime.CampaignOptions
	report  string
	all     []domain.Campaign // stored campaigns, newest first; nil lists camp_1
}

func (f *wireFake) ReadyStatus() (bool, string) { return true, "" }
func (f *wireFake) StartCampaign(_ context.Context, o appruntime.CampaignOptions) (domain.Campaign, error) {
	if f.active {
		return domain.Campaign{}, appruntime.ErrCampaignActive
	}
	f.started = append(f.started, o)
	return domain.Campaign{ID: "camp_1", Status: domain.CampaignPrecheck}, nil
}
func (f *wireFake) StopCampaign(context.Context, string) error { return nil }
func (f *wireFake) CampaignStatus(_ context.Context, id string) (appruntime.CampaignView, error) {
	return appruntime.CampaignView{Campaign: domain.Campaign{ID: id, Status: domain.CampaignCompleted, Verdict: domain.VerdictInconclusive, Report: json.RawMessage(f.report)}}, nil
}
func (f *wireFake) ListCampaigns(_ context.Context, filter domain.CampaignFilter) ([]domain.Campaign, error) {
	if f.all == nil {
		return []domain.Campaign{{ID: "camp_1"}}, nil
	}
	out := f.all[min(filter.Offset, len(f.all)):]
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// TestCampaignStatusListsManyCampaigns (OSC-154 AC-1): with 150 stored
// campaigns whose full records exceed 16 MiB, campaign status without an ID
// lists the newest 20 (ID, scenario, status, verdict, start) and pages with
// --limit and --offset; bad paging values are usage errors.
func TestCampaignStatusListsManyCampaigns(t *testing.T) {
	card := json.RawMessage(`{"pad":"` + strings.Repeat("x", 256<<10) + `"}`)
	t0 := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	f := &wireFake{}
	for i := 149; i >= 0; i-- {
		f.all = append(f.all, domain.Campaign{ID: fmt.Sprintf("camp_%03d", i), ScenarioID: "alerting-day", Status: domain.CampaignCompleted,
			Verdict: domain.VerdictPass, StartedAt: t0.Add(time.Duration(i) * time.Hour), Scorecard: card, Report: card})
	}
	const token = "0123456789abcdef-token"
	h, err := web.NewHandlerWithCampaign(version.Info{}, f, web.Security{Mode: web.SecurityBearer, BearerToken: []byte(token), SecureCookies: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	run := func(args ...string) (int, []string, string) {
		var out, errb bytes.Buffer
		code := New(&out, &errb, version.Info{}, nil).Run(context.Background(), append(append([]string{"campaign", "status"}, args...), "--server", srv.URL, "--token-file", tok))
		return code, strings.Split(strings.TrimSpace(out.String()), "\n"), errb.String()
	}
	code, lines, errs := run()
	if code != 0 || len(lines) != 20 {
		t.Fatalf("code=%d lines=%d err=%s", code, len(lines), errs)
	}
	if want := "camp_149\talerting-day\tCOMPLETED\tPASS\t" + t0.Add(149*time.Hour).Format(time.RFC3339); lines[0] != want {
		t.Errorf("first line %q, want %q", lines[0], want)
	}
	if code, lines, errs := run("--limit", "5", "--offset", "5"); code != 0 || len(lines) != 5 || !strings.HasPrefix(lines[0], "camp_144\t") {
		t.Errorf("--limit 5 --offset 5: code=%d %v %s", code, lines, errs)
	}
	if code, _, _ := run("--output", "json", "--limit", "2"); code != 0 {
		t.Errorf("json code=%d", code)
	}
	for _, bad := range [][]string{{"--limit", "0"}, {"--limit", "101"}, {"--offset", "-1"}} {
		if code, _, _ := run(bad...); code != 2 {
			t.Errorf("%v: code=%d, want usage error 2", bad, code)
		}
	}
}

// TestCampaignCallRefusesOversizedResponse: a body over the 16 MiB read limit
// fails with a clear error instead of decoding a truncated body.
func TestCampaignCallRefusesOversizedResponse(t *testing.T) {
	pad := strings.Repeat("x", 16<<20)
	code, _, errs := campaignCLI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"campaign":{"ID":"` + pad + `"}}}`))
	}, "campaign", "status", "cmp_1")
	if code != 1 || !strings.Contains(errs, "exceeds 16 MiB") {
		t.Fatalf("code=%d err=%.200s", code, errs)
	}
}

func TestCampaignCLIAgainstRealWebHandler(t *testing.T) {
	f := &wireFake{report: `{"startable":false}`}
	const token = "0123456789abcdef-token"
	h, err := web.NewHandlerWithCampaign(version.Info{}, f, web.Security{Mode: web.SecurityBearer, BearerToken: []byte(token), SecureCookies: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(tok, []byte(token), 0o600)
	campaignPollInterval = time.Millisecond
	run := func(args ...string) (int, string) {
		var out, errb bytes.Buffer
		code := New(&out, &errb, version.Info{}, nil).Run(context.Background(), append(args, "--server", srv.URL, "--token-file", tok))
		return code, out.String() + errb.String()
	}
	if code, out := run("campaign", "precheck", "--ack", "A1"); code != 3 {
		t.Fatalf("precheck code=%d %s", code, out)
	}
	if code, out := run("campaign", "run", "--wait", "--run-by", "agent"); code != 3 { // startable:false
		t.Fatalf("run --wait code=%d %s", code, out)
	}
	f.report = `{"startable":true}`
	if code, out := run("campaign", "run", "--wait"); code != 4 {
		t.Fatalf("run --wait code=%d %s", code, out)
	}
	if code, out := run("campaign", "status", "camp_1"); code != 0 || !strings.Contains(out, "camp_1") {
		t.Fatalf("status code=%d %s", code, out)
	}
	if got := f.started[len(f.started)-1]; got.RunBy != "human" || got.Scenario.ID != "alerting-day" {
		t.Fatalf("body drift: %+v", got)
	}
	f.active = true
	if code, out := run("campaign", "run"); code != 3 {
		t.Fatalf("409 code=%d %s", code, out)
	}
}

// Ruling C40: campaign cleanup <id>; exit 4 while still DIRTY.
func TestCampaignCleanupCommand(t *testing.T) {
	srv := func(cleanup string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/v1/campaign/runs/cmp_1/cleanup" || r.Header.Get("Authorization") != "Bearer secret-token" {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"cmp_1","cleanup":"` + cleanup + `"}}`))
		}
	}
	if code, out, _ := campaignCLI(t, srv("CLEAN"), "campaign", "cleanup", "cmp_1"); code != 0 || !strings.Contains(out, "cmp_1\tCLEAN") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, _, _ := campaignCLI(t, srv("DIRTY"), "campaign", "cleanup", "cmp_1"); code != 4 {
		t.Fatalf("dirty code=%d", code)
	}
}

// Compare is served (web/campaign_compare.go); the help must not say pending.
func TestCampaignHelpHasNoPendingSupport(t *testing.T) {
	for name, topic := range helpTopics {
		if strings.HasPrefix(name, "campaign") && strings.Contains(topic.description, "pending") {
			t.Errorf("%s: %q", name, topic.description)
		}
	}
}

func TestCampaignCompareSendsExplicitIDsAndMode(t *testing.T) {
	code, out, errs := campaignCLI(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/campaign/compare" || q.Get("ids") != "later" ||
			q.Get("referenceIds") != "prior-1,prior-2" || q.Get("mode") != "configured_policy" || q.Get("scenarioHash") != "v3-hash" {
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"ReferenceCount":2}}`))
	}, "campaign", "compare", "later", "--reference-run", "prior-1", "--reference-run", "prior-2",
		"--mode", "configured_policy", "--scenario-hash", "v3-hash", "--output", "json")
	if code != 0 || !strings.Contains(out, "ReferenceCount") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errs)
	}
}

func TestCampaignCompareMissingBaselineDoesNotQueryServer(t *testing.T) {
	code, _, errs := campaignCLI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL)
		w.WriteHeader(http.StatusBadRequest)
	}, "campaign", "compare", "after")
	if code != 2 || !strings.Contains(errs, "missing baseline") {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

func TestCampaignRunSendsBuildProvenance(t *testing.T) {
	code, _, errs := campaignCLI(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Scenario   string                    `json:"scenario"`
			Provenance domain.CampaignProvenance `json:"provenance"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.Scenario != "alerting-day-control@v1" ||
			body.Provenance != (domain.CampaignProvenance{
				Role: domain.CampaignPostChangeControl, TargetBuild: "post-id", CorrTestBuild: "runner-id", WorkloadConfig: "pack-sha",
			}) {
			t.Errorf("run body=%+v err=%v", body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"ID":"cmp_1","Status":"PRECHECK"}}`))
	}, "campaign", "run", "--scenario", "alerting-day-control@v1", "--comparison-role", "post_change_control",
		"--target-build", "post-id", "--corrtest-build", "runner-id", "--workload-config", "pack-sha")
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errs)
	}
}

// TestSkippedLanesShowInStatus: a run with a lane skipped by option never
// prints as a bare verdict.
func TestSkippedLanesShowInStatus(t *testing.T) {
	c := domain.Campaign{Scorecard: json.RawMessage(`{"Verdict":"PASS","SkippedLanes":["report: skipped (by option)"]}`)}
	if got := skippedLanes(c); got != "\tskipped by option: report: skipped (by option)" {
		t.Fatalf("got %q", got)
	}
	if skippedLanes(domain.Campaign{Scorecard: json.RawMessage(`{"Verdict":"PASS"}`)}) != "" || skippedLanes(domain.Campaign{}) != "" {
		t.Fatal("no skipped lane prints nothing extra")
	}
}
