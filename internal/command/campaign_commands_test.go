package command

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/version"
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
