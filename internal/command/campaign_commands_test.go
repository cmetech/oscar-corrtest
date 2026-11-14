package command

import (
	"bytes"
	"context"
	"encoding/json"
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
	code, _, errs := campaignCLI(t, campaignServer("", ""), "campaign", "compare", "a", "b")
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
func (f *wireFake) ListCampaigns(context.Context, domain.CampaignFilter) ([]domain.Campaign, error) {
	return []domain.Campaign{{ID: "camp_1"}}, nil
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
