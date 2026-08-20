package web

import (
	"context"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

const webScenarioSource = `apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: sample
suite: custom
pattern: flood
maxDuration: 90s
cases:
  - {name: positive, code: P01, polarity: positive, role: interface_down, repeat: 5, window: 30s, assertions: [{kind: synthetic-alert-count, equals: 1}]}
  - {name: negative, code: N01, polarity: negative, role: interface_down, repeat: 4, window: 30s, assertions: [{kind: synthetic-alert-count, equals: 0}]}
`

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

func TestDurableTargetsRunsAndSettingsPages(t *testing.T) {
	settings := config.Settings{
		DataDir: filepath.Join(t.TempDir(), "state"), ListenAddress: "127.0.0.1:8787",
	}
	runtime, err := appruntime.Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := runtime.CreateTarget(context.Background(), domain.TargetInput{
		DisplayName: "Lab A", BaseURL: "https://oscar.example",
		Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "OSCAR_API_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runtime.CreateRun(context.Background(), target.ID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = appruntime.Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)

	tests := []struct {
		path string
		want []string
	}{
		{"/targets", []string{"Targets", "Lab A", "OSCAR_API_TOKEN"}},
		{"/runs?status=INTERRUPTED", []string{"Runs", run.ID, "INTERRUPTED"}},
		{"/runs/" + run.ID, []string{"Run detail", run.ID, "NOT_REQUIRED", "data-run-timeline"}},
		{"/settings", []string{"Settings", "corrtest.db", "Ready"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			for _, want := range test.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("body missing %q: %s", want, response.Body.String())
				}
			}
		})
	}
}

func TestTargetCreationRequiresSameOriginAndCSRF(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{
		DataDir: filepath.Join(t.TempDir(), "state"), ListenAddress: "127.0.0.1:8787",
	}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	handler := NewHandlerWithData(version.Info{}, runtime)

	get := httptest.NewRequest(http.MethodGet, "http://example.com/targets", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || len(getResponse.Result().Cookies()) == 0 {
		t.Fatalf("GET status=%d cookies=%v", getResponse.Code, getResponse.Result().Cookies())
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatalf("CSRF token missing: %s", getResponse.Body.String())
	}

	values := url.Values{
		"csrf_token": {match[1]}, "display_name": {"Lab B"}, "base_url": {"https://lab-b.example"},
		"credential_kind": {"env"}, "credential_ref": {"OSCAR_API_TOKEN"},
	}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/targets", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusSeeOther {
		t.Fatalf("POST status=%d body=%s", postResponse.Code, postResponse.Body.String())
	}

	crossSite := httptest.NewRequest(http.MethodPost, "http://example.com/targets", strings.NewReader(values.Encode()))
	crossSite.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossSite.Header.Set("Origin", "https://evil.example")
	crossSite.AddCookie(getResponse.Result().Cookies()[0])
	crossSiteResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossSiteResponse, crossSite)
	if crossSiteResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-site status=%d", crossSiteResponse.Code)
	}
}

func TestTargetCreationLimitsBodyBeforeParsing(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{
		DataDir: filepath.Join(t.TempDir(), "state"), ListenAddress: "127.0.0.1:8787",
	}, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	handler := NewHandlerWithData(version.Info{}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/targets", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("CSRF token missing")
	}
	values := url.Values{
		"csrf_token": {match[1]}, "display_name": {strings.Repeat("x", 70<<10)}, "base_url": {"https://oscar.example"},
	}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/targets", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDatabaseReadinessFailureReturns503(t *testing.T) {
	handler := NewHandlerWithData(version.Info{}, diagnosticData{})
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "migration checksum mismatch") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunDetailKeepsMissingArtifactVisible(t *testing.T) {
	handler := NewHandlerWithData(version.Info{}, missingArtifactData{})
	request := httptest.NewRequest(http.MethodGet, "/runs/crt_00000000000000000000000000", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Integrity warning") || !strings.Contains(response.Body.String(), "missing") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunTestPageStartsBackgroundRunWithCSRF(t *testing.T) {
	data := &runUIData{}
	handler := NewHandlerWithData(version.Info{Version: "test"}, data)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/run-test", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), "Run correlation test") || !strings.Contains(getResponse.Body.String(), "parent_child") {
		t.Fatalf("GET status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("CSRF token missing")
	}
	values := url.Values{"csrf_token": {match[1]}, "target_id": {"tgt_lab"}, "pattern": {"flood"}, "pipeline_mode": {"phase_b_dispatch"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/runs", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/runs/crt_ui" {
		t.Fatalf("POST status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if data.pattern != "flood" {
		t.Fatalf("start args=%+v", data)
	}
}

func TestRunEventsSSEReplaysAfterSequence(t *testing.T) {
	handler := NewHandlerWithData(version.Info{}, &sseData{runUIData: runUIData{}})
	request := httptest.NewRequest(http.MethodGet, "/runs/crt_ui/events?after=1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	body := response.Body.String()
	if strings.Contains(body, "id: 1") || !strings.Contains(body, "id: 2") || !strings.Contains(body, `"summary":"completed"`) {
		t.Fatalf("SSE body=%q", body)
	}
}

func TestRunCancellationRequiresSameOriginAndCSRF(t *testing.T) {
	data := &runUIData{}
	handler := NewHandlerWithData(version.Info{}, data)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/runs/crt_ui", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatalf("cancel CSRF token missing: %s", getResponse.Body.String())
	}
	values := url.Values{"csrf_token": {match[1]}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/runs/crt_ui/cancel", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusSeeOther || !data.cancelled {
		t.Fatalf("cancel status=%d cancelled=%t body=%s", response.Code, data.cancelled, response.Body.String())
	}
}

func TestRunDeletionRequiresCleanupSafeTerminalStateAndCSRF(t *testing.T) {
	data := &deletionUIData{}
	handler := NewHandlerWithData(version.Info{}, data)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/runs/crt_ui", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 || !strings.Contains(getResponse.Body.String(), "Delete verified local run") {
		t.Fatalf("delete action missing: %s", getResponse.Body.String())
	}
	values := url.Values{"csrf_token": {match[1]}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/runs/crt_ui/delete", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusSeeOther || !data.deleted {
		t.Fatalf("delete status=%d deleted=%t body=%s", response.Code, data.deleted, response.Body.String())
	}
}

func TestScenarioUIPreviewsAndImportsStrictSourceWithCSRF(t *testing.T) {
	data := &scenarioUIData{}
	handler := NewHandlerWithData(version.Info{}, data)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 || !strings.Contains(getResponse.Body.String(), "Custom scenarios") {
		t.Fatalf("scenario page missing: %s", getResponse.Body.String())
	}
	source := webScenarioSource
	for _, action := range []string{"preview", "import"} {
		values := url.Values{"csrf_token": {match[1]}, "action": {action}, "source": {source}, "target_id": {"tgt_lab"}, "pipeline_mode": {"phase_b_dispatch"}}
		post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios", strings.NewReader(values.Encode()))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		post.Header.Set("Origin", "http://example.com")
		post.AddCookie(getResponse.Result().Cookies()[0])
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, post)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	if !data.previewed || !data.imported {
		t.Fatalf("previewed=%t imported=%t", data.previewed, data.imported)
	}
}

func TestBearerSecurityProtectsEveryApplicationRoute(t *testing.T) {
	t.Parallel()
	security := Security{Mode: SecurityBearer, BearerToken: []byte("correct horse battery staple"), SecureCookies: true}
	handler, err := NewHandlerWithOptions(version.Info{}, nil, security)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/healthz", "/readyz", "/targets", "/runs", "/run-test", "/settings", "/static/css/tokens.css"} {
		request := httptest.NewRequest(http.MethodGet, "https://corrtest.example"+path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s status=%d", path, response.Code)
		}
		request = httptest.NewRequest(http.MethodGet, "https://corrtest.example"+path, nil)
		request.Header.Set("Authorization", "Bearer correct horse battery staple")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusUnauthorized {
			t.Errorf("authorized %s remained unauthorized", path)
		}
	}
}

func TestBearerSecurityRefusesInsecureSessionCookies(t *testing.T) {
	t.Parallel()
	if _, err := NewHandlerWithOptions(version.Info{}, nil, Security{Mode: SecurityBearer, BearerToken: []byte("correct horse battery staple")}); err == nil {
		t.Fatal("bearer mode accepted insecure session cookies")
	}
}

func TestBearerLoginCreatesSecureSessionWithoutReflectingSecret(t *testing.T) {
	t.Parallel()
	handler, err := NewHandlerWithOptions(version.Info{}, nil, Security{Mode: SecurityBearer, BearerToken: []byte("correct horse battery staple"), SecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"token": {"correct horse battery staple"}}
	request := httptest.NewRequest(http.MethodPost, "https://corrtest.example/login", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://corrtest.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Value == "correct horse battery staple" {
		t.Fatalf("unsafe session cookie: %+v", cookies)
	}
	home := httptest.NewRequest(http.MethodGet, "https://corrtest.example/", nil)
	home.AddCookie(cookies[0])
	homeResponse := httptest.NewRecorder()
	handler.ServeHTTP(homeResponse, home)
	if homeResponse.Code != http.StatusOK || strings.Contains(homeResponse.Body.String(), "correct horse battery staple") {
		t.Fatalf("session status=%d body=%s", homeResponse.Code, homeResponse.Body.String())
	}
}

func TestBearerSessionExpiresAndLogoutRevokesReplay(t *testing.T) {
	now := time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC)
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := newAuthHandler(base, Security{Mode: SecurityBearer, BearerToken: []byte("correct horse battery staple"), SecureCookies: true}, func() time.Time { return now })
	login := func() *http.Cookie {
		values := url.Values{"token": {"correct horse battery staple"}}
		request := httptest.NewRequest(http.MethodPost, "https://corrtest.example/login", strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "https://corrtest.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Result().Cookies()[0]
	}
	cookie := login()
	now = now.Add(8*time.Hour + time.Second)
	expired := httptest.NewRequest(http.MethodGet, "https://corrtest.example/", nil)
	expired.AddCookie(cookie)
	expiredResponse := httptest.NewRecorder()
	handler.ServeHTTP(expiredResponse, expired)
	if expiredResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status=%d", expiredResponse.Code)
	}

	now = now.Add(-time.Hour)
	cookie = login()
	logout := httptest.NewRequest(http.MethodPost, "https://corrtest.example/logout", nil)
	logout.Header.Set("Origin", "https://corrtest.example")
	logout.AddCookie(cookie)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	replay := httptest.NewRequest(http.MethodGet, "https://corrtest.example/", nil)
	replay.AddCookie(cookie)
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session replay status=%d", replayResponse.Code)
	}
}

func TestLoopbackHostGuardRejectsDNSRebindingHost(t *testing.T) {
	handler := loopbackHostGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), "127.0.0.1:8787")
	for _, test := range []struct {
		host string
		want int
	}{{"127.0.0.1:8787", http.StatusOK}, {"[::1]:8787", http.StatusOK}, {"attacker.example:8787", http.StatusMisdirectedRequest}, {"127.0.0.1:9999", http.StatusMisdirectedRequest}} {
		request := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/", nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("host=%s status=%d want=%d", test.host, response.Code, test.want)
		}
	}
}

func TestHostGuardSelectionMatchesActualListener(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, test := range []struct {
		name, listener string
		want           int
	}{
		{"loopback guarded", "127.0.0.1:8787", http.StatusMisdirectedRequest},
		{"wildcard unguarded", "0.0.0.0:8787", http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := hostGuardForListener(next, test.listener, Security{})
			request := httptest.NewRequest(http.MethodGet, "http://attacker.example:8787/", nil)
			request.Host = "attacker.example:8787"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
		})
	}
}

func TestTrustedProxyRequiresPeerAndIdentity(t *testing.T) {
	t.Parallel()
	_, network, _ := net.ParseCIDR("10.20.0.0/16")
	handler, err := NewHandlerWithOptions(version.Info{}, nil, Security{
		Mode: SecurityTrustedProxy, ProxyHeader: "X-Forwarded-User", ProxyValue: "corrtest-operator", TrustedProxies: []*net.IPNet{network},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remote, identity string
		want                   int
	}{
		{"direct spoof", "192.0.2.20:1234", "corrtest-operator", http.StatusUnauthorized},
		{"missing identity", "10.20.1.2:1234", "", http.StatusUnauthorized},
		{"wrong identity", "10.20.1.2:1234", "other", http.StatusUnauthorized},
		{"trusted", "10.20.1.2:1234", "corrtest-operator", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remote
			request.Header.Set("X-Forwarded-User", test.identity)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d", response.Code, test.want)
			}
		})
	}
}

type diagnosticData struct{}

func (diagnosticData) ReadyStatus() (bool, string) { return false, "migration checksum mismatch" }
func (diagnosticData) CreateTarget(context.Context, domain.TargetInput) (domain.Target, error) {
	return domain.Target{}, errors.New("read-only")
}
func (diagnosticData) ListTargets(context.Context) ([]domain.Target, error) { return nil, nil }
func (diagnosticData) ListRuns(context.Context, domain.RunFilter) ([]domain.Run, error) {
	return nil, nil
}
func (diagnosticData) GetRun(context.Context, string) (domain.Run, error) {
	return domain.Run{}, errors.New("not found")
}
func (diagnosticData) ListRunEvents(context.Context, string) ([]domain.RunEvent, error) {
	return nil, nil
}
func (diagnosticData) ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error) {
	return nil, nil
}

type missingArtifactData struct{ diagnosticData }

func (missingArtifactData) ReadyStatus() (bool, string) { return true, "" }
func (missingArtifactData) GetRun(context.Context, string) (domain.Run, error) {
	return domain.Run{
		ID: "crt_00000000000000000000000000", ShortToken: "00000000", Status: domain.RunInterrupted,
		CleanupStatus: domain.CleanupNotRequired,
	}, nil
}

type runUIData struct {
	diagnosticData
	pattern   string
	cancelled bool
}

func (d *runUIData) ReadyStatus() (bool, string) { return true, "" }
func (d *runUIData) ListTargets(context.Context) ([]domain.Target, error) {
	return []domain.Target{{ID: "tgt_lab", DisplayName: "Lab", BaseURL: "https://oscar.example"}}, nil
}
func (d *runUIData) StartBuiltin(_ context.Context, targetID, pattern, mode string) (domain.Run, error) {
	d.pattern = pattern
	return domain.Run{ID: "crt_ui", ShortToken: "00000001", Status: domain.RunQueued, CleanupStatus: domain.CleanupNotRequired}, nil
}
func (d *runUIData) GetRun(context.Context, string) (domain.Run, error) {
	return domain.Run{ID: "crt_ui", ShortToken: "00000001", Status: domain.RunObserving, CleanupStatus: domain.CleanupUnknown}, nil
}
func (d *runUIData) CancelRun(_ context.Context, id string) error {
	if id != "crt_ui" {
		return errors.New("not found")
	}
	d.cancelled = true
	return nil
}

type sseData struct{ runUIData }

type deletionUIData struct {
	runUIData
	deleted bool
}

type scenarioUIData struct {
	runUIData
	previewed bool
	imported  bool
}

func (d *scenarioUIData) ListScenarios(context.Context) ([]domain.ScenarioRecord, error) {
	if !d.imported {
		return nil, nil
	}
	return []domain.ScenarioRecord{{ID: "scn_sample", Name: "sample", APIVersion: "corrtest.oscar/v1alpha1", SHA256: "abc"}}, nil
}
func (d *scenarioUIData) PreviewScenario(_ context.Context, targetID string, document scenario.Scenario, mode string) (compiler.Plan, error) {
	d.previewed = true
	return compiler.Plan{APIVersion: "corrtest.oscar/v1alpha1", Pattern: document.Pattern, MutationBudget: compiler.MutationBudget{Rules: 2, Alerts: 9}}, nil
}
func (d *scenarioUIData) ImportScenario(_ context.Context, source []byte, document scenario.Scenario) (domain.ScenarioRecord, error) {
	d.imported = true
	return domain.ScenarioRecord{ID: "scn_sample", Name: document.Name, APIVersion: document.APIVersion, SourceDocument: string(source), SHA256: "abc"}, nil
}

func (d *deletionUIData) GetRun(context.Context, string) (domain.Run, error) {
	return domain.Run{ID: "crt_ui", ShortToken: "00000001", Status: domain.RunCompleted, Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean}, nil
}
func (d *deletionUIData) DeleteRun(_ context.Context, id string) error {
	if id != "crt_ui" {
		return errors.New("not found")
	}
	d.deleted = true
	return nil
}

func (sseData) GetRun(context.Context, string) (domain.Run, error) {
	return domain.Run{ID: "crt_ui", ShortToken: "00000001", Status: domain.RunCompleted, Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean}, nil
}
func (sseData) ListRunEvents(context.Context, string) ([]domain.RunEvent, error) {
	return []domain.RunEvent{{RunID: "crt_ui", Sequence: 1, Type: "old", Level: "info", OccurredAt: time.Now(), Summary: "old"}, {RunID: "crt_ui", Sequence: 2, Type: "done", Level: "info", OccurredAt: time.Now(), Summary: "completed"}}, nil
}
func (missingArtifactData) ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error) {
	return []domain.ArtifactEvidence{{
		Artifact:  domain.Artifact{RelativePath: "runs/crt_00000000000000000000000000/report.json"},
		Integrity: domain.ArtifactIntegrityMissing,
	}}, nil
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
