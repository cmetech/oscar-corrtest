package web

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestHandlerRoutes(t *testing.T) {
	handler := NewHandler(version.Info{Version: "v1.2.3", Commit: "abc", BuildDate: "now"})
	tests := []struct{ path, contentType, body string }{
		{"/healthz", "application/json", `"status":"ok"`},
		{"/readyz", "application/json", `"status":"ready"`},
		{"/", "text/html", "OSCAR Correlation Test Harness"},
		{"/static/css/tokens.css", "text/css", "--ct-bg"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
			}
			if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.contentType) {
				t.Fatalf("content-type=%q want prefix %q", got, tt.contentType)
			}
			if !strings.Contains(res.Body.String(), tt.body) {
				t.Fatalf("body=%q missing %q", res.Body.String(), tt.body)
			}
		})
	}
}

func TestHealthRejectsPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", res.Code)
	}
}

func TestDashboardSecurityHeadersAndNonce(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)

	if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}
	if got := res.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy=%q", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	csp := res.Header().Get("Content-Security-Policy")
	match := regexp.MustCompile(`script-src 'self' 'nonce-([^']+)'`).FindStringSubmatch(csp)
	if len(match) != 2 {
		t.Fatalf("CSP missing nonce: %q", csp)
	}
	if !strings.Contains(res.Body.String(), `nonce="`+match[1]+`"`) {
		t.Fatalf("HTML does not contain CSP nonce %q", match[1])
	}
}

func TestTemplateFailureReturns500BeforeHeadersCommit(t *testing.T) {
	tmpl := template.Must(template.New("base").Parse(`{{template "missing" .}}`))
	handler := newHandler(version.Info{}, tmpl, http.NotFoundHandler(), func() (string, error) { return "nonce", nil })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestNonceGenerationFailureReturns500(t *testing.T) {
	handler := newHandler(version.Info{}, parsedTemplates, http.NotFoundHandler(), func() (string, error) {
		return "", errors.New("entropy unavailable")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestStaticAssetsDisableCaching(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/css/tokens.css", nil)
	res := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(res, req)
	if got := res.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control=%q", got)
	}
}

func TestRunRejectsMissingListenAddress(t *testing.T) {
	err := Run(t.Context(), Options{})
	if !errors.Is(err, errListenAddressRequired) {
		t.Fatalf("error=%v", err)
	}
}
