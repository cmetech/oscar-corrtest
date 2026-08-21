package web

import (
	"context"
	"errors"
	"fmt"
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

	"github.com/cmetech/oscar-corrtest/internal/applog"
	"github.com/cmetech/oscar-corrtest/internal/authoring"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/envfile"
	"github.com/cmetech/oscar-corrtest/internal/operations"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/service"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestRequestLogExcludesSecretsAndQuery(t *testing.T) {
	logs, err := applog.Open(t.TempDir(), nil, applog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	handler := requestLogger(NewHandler(version.Info{}), logs.Logger("web"))
	request := httptest.NewRequest(http.MethodGet, "/?api_key=query-secret", nil)
	request.Header.Set("Cookie", "corrtest_session=cookie-secret")
	request.Header.Set("X-API-Key", "header-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	records := logs.Recent(10)
	if len(records) != 1 || records[0].Attributes["method"] != "GET" || records[0].Attributes["route"] != "/{$}" || records[0].Attributes["status"] != "200" {
		t.Fatalf("records=%+v", records)
	}
	encoded := records[0].Message
	for key, value := range records[0].Attributes {
		encoded += key + value
	}
	for _, secret := range []string{"query-secret", "cookie-secret", "header-secret"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("request log leaked %q: %+v", secret, records[0])
		}
	}
}

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

func TestAuthoringRouteRendersCompleteDefaultWithoutDataSource(t *testing.T) {
	h := NewHandler(version.Info{Version: "test"})
	r := httptest.NewRequest(http.MethodGet, "/authoring", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	for _, text := range []string{"Scenario Authoring", "Document identity", "Assembled YAML", "apiVersion", "Open in Scenarios editor"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Errorf("missing %q", text)
		}
	}
}

func TestAuthoringRouteAcceptsEveryLegalSelectionDimension(t *testing.T) {
	h := NewHandler(version.Info{Version: "test"})
	tests := []struct {
		name, query, want string
	}{
		{"section", "section=schema", "Schema reference"},
		{"step", "step=cases", "P01 and N01"},
		{"pattern", "pattern=sequence", "sequence"},
		{"level", "level=advanced", "advanced"},
		{"view", "view=api", "OSCAR API"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/authoring?"+test.query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), test.want) {
				t.Fatalf("body missing %q", test.want)
			}
		})
	}
}

func TestAuthoringRouteRejectsInvalidSelectionValues(t *testing.T) {
	h := NewHandler(version.Info{Version: "test"})
	for _, query := range []string{"pattern=typo", "level=expert", "view=secret"} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/authoring?"+query, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthoringNavigationIsSelected(t *testing.T) {
	w := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/authoring", nil))
	if !strings.Contains(w.Body.String(), `<a href="/authoring" aria-current="page">Authoring</a>`) {
		t.Fatal("authoring navigation is not selected")
	}
}

func TestAuthoringSchemaFilterWorksWithoutJavaScript(t *testing.T) {
	w := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/authoring?filter=assertion", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="schema-assertion.kind"`) {
		t.Fatal("filtered schema is missing assertion fields")
	}
	if strings.Contains(body, `id="schema-scenario.apiVersion"`) {
		t.Fatal("unmatched schema field rendered despite native filter")
	}
}

func TestAuthoringPatternCookbookRendersEveryExample(t *testing.T) {
	handler := NewHandler(version.Info{Version: "test"})
	fixed := map[string]string{
		"flood": "min_count=5", "co_occurrence": "all compiled required alert names", "sequence": "login_failure then privileged_command",
		"persistence": "unresolved for 30 seconds", "absence": "55-second observation", "parent_child": "no synthetic emit rule",
		"cross_source": "required sources snmp and api", "threshold": "minimum distinct count 3",
	}
	for _, pattern := range scenario.SupportedPatterns() {
		for _, level := range []string{"basic", "advanced"} {
			for _, view := range []string{"yaml", "contract", "api", "lifecycle"} {
				path := fmt.Sprintf("/authoring?section=patterns&pattern=%s&level=%s&view=%s", pattern, level, view)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
				}
				body := response.Body.String()
				for _, want := range []string{
					`class="authoring-patterns"`, "Pattern cookbook", "P01", "N01", "positive", "negative",
					"Expected evidence", "Fixed compiler semantics", "Configurable fields", "Common mistakes", fixed[pattern],
					"Open in Scenarios editor", "view=yaml", "view=contract", "view=api", "view=lifecycle",
					"Exact zero needs the final window", "Phase A and Phase B", "Strict YAML", "Protected labels", "P01 / N01 matrix",
				} {
					if !strings.Contains(body, want) {
						t.Errorf("%s missing %q", path, want)
					}
				}
				selected := regexp.MustCompile(`id="authoring-view-link-` + regexp.QuoteMeta(view) + `"[^>]+aria-current="page"`)
				if !selected.MatchString(body) {
					t.Errorf("%s did not select the requested server-rendered panel", path)
				}
				for _, forbidden := range []string{`role="tablist"`, `role="tab"`, `role="tabpanel"`, `aria-selected=`} {
					if strings.Contains(body, forbidden) {
						t.Errorf("%s retained incomplete tab semantics %q", path, forbidden)
					}
				}
				for _, supported := range scenario.SupportedPatterns() {
					if !strings.Contains(body, "pattern="+supported) {
						t.Errorf("%s missing cookbook link for %q", path, supported)
					}
				}
				for _, depth := range []string{"level=basic", "level=advanced"} {
					if !strings.Contains(body, depth) {
						t.Errorf("%s missing level link %q", path, depth)
					}
				}
			}
		}
	}
}

func TestAuthoringAPIPreviewRendersExactCredentialFreeRequests(t *testing.T) {
	handler := NewHandler(version.Info{Version: "test"})
	for _, pattern := range scenario.SupportedPatterns() {
		for _, level := range []string{"basic", "advanced"} {
			path := fmt.Sprintf("/authoring?section=patterns&pattern=%s&level=%s&view=api", pattern, level)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, want := range []string{
				"OSCAR API", "POST", "/api/v1/correlation_rules/validate", "/api/v1/correlation_rules",
				"/api/v1/alerts", "window_seconds", "group_by_labels", "match_criteria", "created_by",
				"P01", "N01", "Attempt", "Scheduled delay", "Runtime-dependent", "{\n  &#34;receiver&#34;",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s missing %q", path, want)
				}
			}
			for _, forbidden := range []string{
				"X-API-Key", "Authorization: Bearer", "api_key", "https://oscar", "http://oscar",
				`returned-rule-id&quot;:`, `server-fingerprint&quot;:`,
			} {
				if strings.Contains(body, forbidden) {
					t.Errorf("%s leaked or fabricated %q", path, forbidden)
				}
			}
			if pattern == "parent_child" && !strings.Contains(body, "no synthetic emit_spec") {
				t.Errorf("%s does not explain the omitted parent-child emit_spec", path)
			}
		}
	}
}

func TestAuthoringLifecycleRendersOrderedRuntimeHonestStages(t *testing.T) {
	handler := NewHandler(version.Info{Version: "test"})
	for _, pattern := range scenario.SupportedPatterns() {
		for _, level := range []string{"basic", "advanced"} {
			path := fmt.Sprintf("/authoring?section=patterns&pattern=%s&level=%s&view=lifecycle", pattern, level)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, want := range []string{
				"Compatibility preflight", "Run mutation", "Observation", "Assertion evaluation", "Evidence persistence", "Cleanup",
				"preflight.validate_rule", "setup.create_rule", "stimulus.inject_alert", "evidence.read_history",
				"evidence.evaluate_assertions", "evidence.persist_final_transaction", "cleanup.delete_rule", "cleanup.resolve_alert",
				"{returned-rule-id}", "{server-fingerprint}", "Runtime-dependent",
				"CorrTest creates two temporary correlation rules (P01 and N01), injects source alerts directly through the public alert API, observes OSCAR evidence, deletes only the returned rule IDs, and resolves its injected alerts. It does not create ordinary OSCAR alert rules.",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s missing %q", path, want)
				}
			}
			if strings.Index(body, "preflight.validate_rule") > strings.Index(body, "evidence.evaluate_assertions") ||
				strings.Index(body, "evidence.evaluate_assertions") > strings.Index(body, "evidence.persist_final_transaction") ||
				strings.Index(body, "evidence.persist_final_transaction") > strings.Index(body, "cleanup.delete_rule") {
				t.Errorf("%s rendered lifecycle out of order", path)
			}
		}
	}
}

func TestAuthoringViewEnhancementPreservesModifiedAndNonPrimaryClicks(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/js/authoring.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	source := response.Body.String()
	for _, want := range []string{
		"event.defaultPrevented", "event.button !== 0", "event.metaKey", "event.ctrlKey", "event.shiftKey", "event.altKey",
		"if (!shouldEnhanceViewClick(event)) return;",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("authoring enhancement missing %q", want)
		}
	}
	guard := strings.Index(source, "if (!shouldEnhanceViewClick(event)) return;")
	intercept := strings.Index(source, "event.preventDefault()")
	if guard < 0 || intercept < 0 || guard > intercept {
		t.Error("authoring view navigation intercepts before checking ordinary-link semantics")
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

func TestSettingsRedirectsToOperations(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/operations" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
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
	for _, required := range []string{
		`action="/runs/crt_ui/cancel" data-confirm-form`,
		`data-confirm-title="Cancel active run?"`,
		`data-confirm-label="Cancel and clean up"`,
	} {
		if !strings.Contains(getResponse.Body.String(), required) {
			t.Errorf("run cancellation confirmation missing %q", required)
		}
	}
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
	for _, required := range []string{
		`action="/runs/crt_ui/delete" data-confirm-form`,
		`data-confirm-title="Delete historical run?"`,
		`data-confirm-label="Delete run"`,
	} {
		if !strings.Contains(getResponse.Body.String(), required) {
			t.Errorf("run deletion confirmation missing %q", required)
		}
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

func TestScenarioRoutesRequireCompleteInspector(t *testing.T) {
	data := &managerWithoutInspectorData{}
	handler := NewHandlerWithData(version.Info{Version: "test"}, data)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios", nil))
	if getResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	if strings.Contains(getResponse.Body.String(), "Structurally valid") {
		t.Fatal("manager-only GET rendered a plan-only inspection")
	}

	values := url.Values{"action": {"import"}, "source": {webScenarioSource}, "pipeline_mode": {"phase_b_dispatch"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST status=%d body=%s", response.Code, response.Body.String())
	}
	if data.imported {
		t.Fatal("manager-only POST imported before establishing inspector availability")
	}
	if data.listCalls != 0 {
		t.Fatalf("manager-only routes listed scenarios before establishing inspector availability: %d", data.listCalls)
	}
}

func TestScenarioGETFailsWithoutPartialInspectionWhenInspectorErrors(t *testing.T) {
	handler := NewHandlerWithData(version.Info{Version: "test"}, &alwaysFailingInspectionData{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=builtin:flood", nil))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, stale := range []string{"Structurally valid", "preflight.validate_rule", "corrtest-flood-p01-prev1ew1"} {
		if strings.Contains(response.Body.String(), stale) {
			t.Errorf("failed GET retained partial inspection content %q", stale)
		}
	}
}

func TestScenarioWorkbenchPreviewsBuiltinSourceAndOpensUnsavedDraft(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=builtin:flood", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, required := range []string{
		"Scenario catalog", "pattern: flood", "P01", "N01", "CORRTEST_FLOOD_P01", "oscar_test_run_id",
		`type="search"`, `data-scenario-search`, `data-copy-source`, `data-case-tab="P01"`, `data-case-tab="N01"`,
		`data-case-code="P01"`, `data-case-code="N01"`, "Positive trigger", "Negative control",
		"Rule contract", "Stimulus alerts", "Expected evidence", "Inspection filters", "Edit a copy",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("workbench missing %q", required)
		}
	}
	for _, forbidden := range []string{`<pre class="contract-code">`, `id="scenario-authoring"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("workbench retained obsolete raw/separate authoring surface %q", forbidden)
		}
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("clone CSRF token missing")
	}
	values := url.Values{"csrf_token": {match[1]}, "scenario_ref": {"builtin:flood"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios/clone", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(response.Result().Cookies()[0])
	cloned := httptest.NewRecorder()
	handler.ServeHTTP(cloned, post)
	if cloned.Code != http.StatusSeeOther || cloned.Header().Get("Location") != "/scenarios?selected=draft%3Aflood" {
		t.Fatalf("clone status=%d location=%q body=%s", cloned.Code, cloned.Header().Get("Location"), cloned.Body.String())
	}
	items, err := runtime.ListScenarios(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("opening a draft persisted scenarios=%+v err=%v", items, err)
	}
	draftGet := httptest.NewRequest(http.MethodGet, "http://example.com"+cloned.Header().Get("Location"), nil)
	draftResponse := httptest.NewRecorder()
	handler.ServeHTTP(draftResponse, draftGet)
	if draftResponse.Code != http.StatusOK {
		t.Fatalf("draft status=%d body=%s", draftResponse.Code, draftResponse.Body.String())
	}
	draftBody := draftResponse.Body.String()
	for _, required := range []string{"flood-basic-custom", "Unsaved draft", "Save custom scenario"} {
		if !strings.Contains(draftBody, required) {
			t.Errorf("draft workbench missing %q", required)
		}
	}
	if strings.Contains(draftBody, "Delete custom scenario") {
		t.Fatal("unsaved draft exposed a delete action")
	}
}

func TestScenarioExampleGETInspectsWithoutPersisting(t *testing.T) {
	data := &authoringExampleSpy{records: []domain.ScenarioRecord{{ID: "scn_existing", Name: "existing", SourceDocument: webScenarioSource}}}
	handler := NewHandlerWithData(version.Info{Version: "test"}, data)
	for requestNumber := 0; requestNumber < 2; requestNumber++ {
		t.Run(fmt.Sprintf("request-%d", requestNumber+1), func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected="+url.QueryEscape("example:flood:advanced"), nil)
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, want := range []string{
				"flood-advanced", "Unsaved example", "corrtest-flood-p01-prev1ew1",
				"OSCAR API JSON", "Lifecycle", "/authoring?section=patterns&amp;pattern=flood&amp;level=advanced",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, `data-scenario-source readonly`) {
				t.Fatal("server-known example source is not editable")
			}
			if strings.Contains(body, "Delete custom scenario") {
				t.Fatal("unsaved example exposed a delete action")
			}
		})
	}
	if data.listCalls != 2 || data.inspectCalls != 2 || len(data.records) != 1 {
		t.Fatalf("example GETs must be read-only: list=%d inspect=%d records=%d", data.listCalls, data.inspectCalls, len(data.records))
	}
}

func TestScenarioExamplesOpenEveryServerKnownDocument(t *testing.T) {
	data := &authoringExampleSpy{}
	handler := NewHandlerWithData(version.Info{Version: "test"}, data)
	for _, example := range scenario.AllExamples() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected="+url.QueryEscape("example:"+example.ID), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", example.ID, response.Code, response.Body.String())
		}
		for _, want := range []string{`name="source"`, "Unsaved example", example.Scenario.Name} {
			if !strings.Contains(response.Body.String(), want) {
				t.Errorf("%s missing %q", example.ID, want)
			}
		}
	}
	if data.listCalls != len(scenario.AllExamples()) || data.inspectCalls != len(scenario.AllExamples()) || len(data.records) != 0 {
		t.Fatalf("all example GETs must be inspect-only: list=%d inspect=%d records=%d", data.listCalls, data.inspectCalls, len(data.records))
	}
}

func TestScenarioExampleRejectsInvalidIDs(t *testing.T) {
	data := &authoringExampleSpy{}
	handler := NewHandlerWithData(version.Info{Version: "test"}, data)
	for _, selected := range []string{"example", "example:flood", "example:flood:basic:extra", "example:unknown:basic", "example:flood:expert"} {
		t.Run("reject-"+selected, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected="+url.QueryEscape(selected), nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if data.listCalls != 5 || data.inspectCalls != 0 || len(data.records) != 0 {
		t.Fatalf("example GETs must be read-only: list=%d inspect=%d records=%d", data.listCalls, data.inspectCalls, len(data.records))
	}
}

func TestScenarioInspectionViewsRenderAfterPreviewAndImport(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=draft:flood", nil))
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("scenario CSRF token missing")
	}

	for _, action := range []string{"preview", "import"} {
		values := url.Values{
			"csrf_token": {match[1]}, "action": {action}, "source": {webScenarioSource},
			"pipeline_mode": {"phase_b_dispatch"},
		}
		request := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios?selected=draft:flood", strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "http://example.com")
		request.AddCookie(getResponse.Result().Cookies()[0])
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", action, response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, want := range []string{
			`role="tablist" aria-label="Scenario inspection views"`, `role="tab"`, `role="tabpanel"`,
			"Compiled contract", "OSCAR API JSON", "Lifecycle", "preflight.validate_rule",
			"evidence.evaluate_assertions", "cleanup.delete_rule",
			"/authoring?section=patterns&amp;pattern=flood",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", action, want)
			}
		}
		if strings.Contains(body, `onclick=`) {
			t.Errorf("%s rendered an inline event handler", action)
		}
	}
}

func TestScenarioInspectionViewLinksWorkWithoutJavaScript(t *testing.T) {
	data := &authoringExampleSpy{}
	handler := NewHandlerWithData(version.Info{Version: "test"}, data)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=example%3Aflood%3Abasic", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, view := range []string{"contract", "api", "lifecycle"} {
		want := `href="#scenario-inspection-` + view + `"`
		if !strings.Contains(body, want) {
			t.Errorf("missing ordinary fragment link %q", want)
		}
		panel := regexp.MustCompile(`<section id="scenario-inspection-` + view + `"[^>]*>`).FindString(body)
		if panel == "" || strings.Contains(panel, " hidden") {
			t.Errorf("%s server-rendered panel is not visible: %q", view, panel)
		}
	}
}

func TestScenarioPOSTPreviewKeepsChangedSourceAcrossFragmentViews(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=draft:flood", nil))
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("scenario CSRF token missing")
	}
	changedSource := strings.Replace(webScenarioSource, "name: sample", "name: changed-post-preview", 1)
	values := url.Values{
		"csrf_token": {match[1]}, "action": {"preview"}, "source": {changedSource},
		"pipeline_mode": {"phase_b_dispatch"},
	}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios?selected=draft:flood", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "changed-post-preview") {
		t.Fatal("POST response lost the changed submitted source marker")
	}
	viewHeadings := map[string]string{"contract": "Compiled contract", "api": "OSCAR API JSON", "lifecycle": "Lifecycle"}
	for _, view := range []string{"contract", "api", "lifecycle"} {
		if !strings.Contains(body, `href="#scenario-inspection-`+view+`"`) {
			t.Errorf("POST response missing fragment-only %s link", view)
		}
		panel := regexp.MustCompile(`<section id="scenario-inspection-` + view + `"[^>]*>`).FindString(body)
		if panel == "" || strings.Contains(panel, " hidden") {
			t.Errorf("POST response %s panel is not available without JavaScript: %q", view, panel)
		}
		heading := regexp.MustCompile(`(?s)<section id="scenario-inspection-` + view + `"[^>]*>.*?<h3[^>]*>` + regexp.QuoteMeta(viewHeadings[view]) + `</h3>`)
		if !heading.MatchString(body) {
			t.Errorf("POST response %s panel lacks its unique heading", view)
		}
	}
	if strings.Contains(body, `&amp;view=`) {
		t.Fatal("POST inspection navigation contains a lossy GET link")
	}
}

func TestScenarioInspectionEnhancementNeverReplacesPOSTURLWithGET(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(version.Info{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/js/scenarios.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	source := response.Body.String()
	for _, want := range []string{
		"shouldEnhanceInspectionClick", "if (!shouldEnhanceInspectionClick(event)) return;",
		"event.defaultPrevented", "event.button === 0", "event.metaKey", "event.ctrlKey", "event.shiftKey", "event.altKey",
		"window.location.hash = tab.hash",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("inspection enhancement missing %q", want)
		}
	}
	for _, forbidden := range []string{"history.replaceState", "tab.href"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("inspection enhancement retains lossy URL replacement %q", forbidden)
		}
	}
	inspectionStart := strings.Index(source, "function shouldEnhanceInspectionClick")
	if inspectionStart < 0 {
		t.Fatal("inspection enhancement guard is absent")
	}
	inspectionEnhancement := source[inspectionStart:]
	guard := strings.Index(inspectionEnhancement, "if (!shouldEnhanceInspectionClick(event)) return;")
	prevent := strings.Index(inspectionEnhancement, "event.preventDefault()")
	if guard < 0 || prevent < 0 || guard > prevent {
		t.Fatal("inspection enhancement intercepts before checking ordinary-link semantics")
	}
}

func TestScenarioInspectionErrorDiscardsPriorPlanAndOperations(t *testing.T) {
	for _, action := range []string{"preview", "import"} {
		t.Run(action, func(t *testing.T) {
			data := &failingSubmittedInspectionData{}
			handler := NewHandlerWithData(version.Info{Version: "test"}, data)
			getResponse := httptest.NewRecorder()
			handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=draft:flood", nil))
			match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
			if len(match) != 2 {
				t.Fatal("scenario CSRF token missing")
			}
			values := url.Values{
				"csrf_token": {match[1]}, "action": {action}, "source": {webScenarioSource},
				"pipeline_mode": {"phase_b_dispatch"},
			}
			request := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios?selected=draft:flood", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "http://example.com")
			request.AddCookie(getResponse.Result().Cookies()[0])
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			body := response.Body.String()
			if !strings.Contains(body, "submitted inspection failed") {
				t.Fatalf("%s did not render inspection failure: status=%d body=%s", action, response.Code, body)
			}
			for _, stale := range []string{"Structurally valid", "preflight.validate_rule", "corrtest-flood-p01-prev1ew1"} {
				if strings.Contains(body, stale) {
					t.Errorf("%s retained stale inspection content %q", action, stale)
				}
			}
		})
	}
}

func TestSavingScenarioDraftTransitionsToSingleSavedVersion(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=draft:flood", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("scenario draft CSRF token missing")
	}
	document := scenario.Builtin("flood")
	document.Name = "flood-basic-custom"
	document.Suite = "custom"
	source, err := scenario.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"csrf_token": {match[1]}, "action": {"import"}, "source": {string(source)}, "pipeline_mode": {"phase_b_dispatch"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios?selected=draft:flood", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Saved custom version") || !strings.Contains(body, "Delete custom scenario") {
		t.Fatalf("saved draft did not transition to saved controls: %s", body)
	}
	items, err := runtime.ListScenarios(context.Background())
	if err != nil || len(items) != 1 || items[0].Name != "flood-basic-custom" {
		t.Fatalf("saved scenarios=%+v err=%v", items, err)
	}
}

func TestScenarioWorkbenchDeletesUnusedCustomScenario(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	document, err := scenario.Decode(strings.NewReader(webScenarioSource))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := runtime.ImportScenario(context.Background(), []byte(webScenarioSource), document)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=imported:"+imported.ID, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	body := getResponse.Body.String()
	if !strings.Contains(body, "Delete custom scenario") || !strings.Contains(body, `data-confirm-form`) || !strings.Contains(body, `data-confirm-title="Delete custom scenario?"`) || !strings.Contains(body, `data-confirm-value="delete"`) || !strings.Contains(body, `data-confirmation-field`) {
		t.Fatalf("saved custom scenario missing guarded delete action: %s", body)
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("scenario delete CSRF token missing")
	}
	values := url.Values{"csrf_token": {match[1]}, "confirm": {"delete"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios/"+imported.ID+"/delete", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, post)
	if deleted.Code != http.StatusSeeOther || deleted.Header().Get("Location") != "/scenarios?message=scenario-deleted" {
		t.Fatalf("delete status=%d location=%q body=%s", deleted.Code, deleted.Header().Get("Location"), deleted.Body.String())
	}
	items, err := runtime.ListScenarios(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted scenarios=%+v err=%v", items, err)
	}
}

func TestScenarioDeleteRequiresExplicitConfirmation(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	document, err := scenario.Decode(strings.NewReader(webScenarioSource))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := runtime.ImportScenario(context.Background(), []byte(webScenarioSource), document)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=imported:"+imported.ID, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("scenario delete CSRF token missing")
	}
	values := url.Values{"csrf_token": {match[1]}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios/"+imported.ID+"/delete", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete without confirmation status=%d body=%s", response.Code, response.Body.String())
	}
	items, err := runtime.ListScenarios(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != imported.ID {
		t.Fatalf("unconfirmed deletion changed scenarios=%+v err=%v", items, err)
	}
}

func TestScenarioWorkbenchRejectsDeletingScenarioUsedByHistoricalRun(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	document, err := scenario.Decode(strings.NewReader(webScenarioSource))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := runtime.ImportScenario(context.Background(), []byte(webScenarioSource), document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CreateRun(context.Background(), "", imported.ID, "test"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=imported:"+imported.ID, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("scenario delete CSRF token missing")
	}
	values := url.Values{"csrf_token": {match[1]}, "confirm": {"delete"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios/"+imported.ID+"/delete", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "historical runs") {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	items, err := runtime.ListScenarios(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != imported.ID {
		t.Fatalf("referenced scenario was not preserved: scenarios=%+v err=%v", items, err)
	}
}

func TestScenarioImportRendersContractForSubmittedSource(t *testing.T) {
	runtime, err := appruntime.Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/scenarios?selected=builtin:flood", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(getResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("scenario CSRF token missing")
	}
	source := strings.Replace(webScenarioSource, "repeat: 5", "repeat: 6", 1)
	values := url.Values{"csrf_token": {match[1]}, "action": {"import"}, "source": {source}, "pipeline_mode": {"phase_b_dispatch"}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/scenarios?selected=builtin:flood", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(getResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, post)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "2 rules · 10 alerts") {
		t.Fatalf("import response retained a stale compiled contract: %s", body)
	}
}

func TestOperationsPageManagesWriteOnlyAPIKeyAndShowsServiceLogs(t *testing.T) {
	root := t.TempDir()
	store, err := envfile.Open(filepath.Join(root, ".env"), nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := applog.Open(filepath.Join(root, "logs"), nil, applog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	logs.Logger("runtime").Info("runtime ready")
	settings := config.Settings{ConfigPath: filepath.Join(root, "config.json"), EnvFile: filepath.Join(root, ".env"), DataDir: filepath.Join(root, "data"), LogDir: filepath.Join(root, "logs"), ListenAddress: "127.0.0.1:8787"}
	controller := operations.New(settings, store, &webOperationsManager{status: service.Status{State: service.StateRunning, Mechanism: "test", PID: 42}}, logs)
	runtime, err := appruntime.OpenWithOptions(context.Background(), settings, version.Info{Version: "test"}, appruntime.Options{Environment: store, Logs: logs, Operations: controller})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandlerWithData(version.Info{Version: "test"}, runtime)
	get := httptest.NewRequest(http.MethodGet, "http://example.com/operations", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, required := range []string{
		"Operations", "Not configured", "running", "application.jsonl", "runtime ready", settings.EnvFile,
		`class="operations-workspace"`, `class="operations-rail"`, `data-log-source-filter`,
		`data-log-level-filter`, `data-log-text-filter`, `data-log-source="runtime"`, `data-log-level="info"`,
		`action="/operations/api-key/clear" data-confirm-form`, `data-confirm-title="Clear managed OSCAR API key?"`,
		`action="/operations/service/stop" data-confirm-form`, `data-confirm-title="Stop CorrTest service?"`,
		`action="/operations/service/uninstall" data-confirm-form`, `data-confirm-title="Uninstall CorrTest user service?"`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("operations page missing %q", required)
		}
	}
	workspace := regexp.MustCompile(`(?s)<section class="operations-workspace">\s*<aside class="operations-rail".*?</aside>\s*<section class="panel log-console"`).FindString(body)
	if workspace == "" {
		t.Fatal("operations controls and live log are not adjacent in the approved workspace")
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("operations CSRF missing")
	}
	const secret = "operations-secret-sentinel"
	values := url.Values{"csrf_token": {match[1]}, "api_key": {secret}}
	post := httptest.NewRequest(http.MethodPost, "http://example.com/operations/api-key", strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", "http://example.com")
	post.AddCookie(response.Result().Cookies()[0])
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, post)
	if saved.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
	}
	configured := httptest.NewRecorder()
	handler.ServeHTTP(configured, httptest.NewRequest(http.MethodGet, "/operations", nil))
	if !strings.Contains(configured.Body.String(), "Configured") || strings.Contains(configured.Body.String(), secret) {
		t.Fatalf("unsafe configured page: %s", configured.Body.String())
	}
}

type webOperationsManager struct{ status service.Status }

func (*webOperationsManager) Install(context.Context) error                    { return nil }
func (*webOperationsManager) Start(context.Context) error                      { return nil }
func (*webOperationsManager) Stop(context.Context) error                       { return nil }
func (*webOperationsManager) Restart(context.Context) error                    { return nil }
func (m *webOperationsManager) Status(context.Context) (service.Status, error) { return m.status, nil }
func (*webOperationsManager) Logs(context.Context, int, bool) error            { return nil }
func (*webOperationsManager) Uninstall(context.Context) error                  { return nil }
func (*webOperationsManager) DefinitionPath() string                           { return "/test/service" }

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

type managerWithoutInspectorData struct {
	diagnosticData
	listCalls int
	imported  bool
}

func (d *managerWithoutInspectorData) ListScenarios(context.Context) ([]domain.ScenarioRecord, error) {
	d.listCalls++
	return nil, nil
}
func (d *managerWithoutInspectorData) PreviewScenario(context.Context, string, scenario.Scenario, string) (compiler.Plan, error) {
	return compiler.Plan{Pattern: "flood"}, nil
}
func (d *managerWithoutInspectorData) ImportScenario(context.Context, []byte, scenario.Scenario) (domain.ScenarioRecord, error) {
	d.imported = true
	return domain.ScenarioRecord{}, nil
}

type alwaysFailingInspectionData struct{ scenarioUIData }

func (*alwaysFailingInspectionData) InspectScenario(context.Context, []byte, string) (appruntime.ScenarioInspection, error) {
	return appruntime.ScenarioInspection{}, errors.New("scenario inspection failed")
}

type authoringExampleSpy struct {
	records      []domain.ScenarioRecord
	listCalls    int
	inspectCalls int
}

type failingSubmittedInspectionData struct {
	scenarioUIData
	inspectCalls int
}

func (d *failingSubmittedInspectionData) InspectScenario(ctx context.Context, source []byte, mode string) (appruntime.ScenarioInspection, error) {
	d.inspectCalls++
	if d.inspectCalls == 3 {
		return appruntime.ScenarioInspection{}, errors.New("submitted inspection failed")
	}
	return authoring.New("test").Inspect(ctx, source, mode)
}

func (d *authoringExampleSpy) ReadyStatus() (bool, string) { return true, "" }
func (d *authoringExampleSpy) CreateTarget(context.Context, domain.TargetInput) (domain.Target, error) {
	panic("example GET must not create a target")
}
func (d *authoringExampleSpy) ListTargets(context.Context) ([]domain.Target, error) {
	panic("example GET must not resolve a target")
}
func (d *authoringExampleSpy) ListRuns(context.Context, domain.RunFilter) ([]domain.Run, error) {
	panic("example GET must not list runs")
}
func (d *authoringExampleSpy) GetRun(context.Context, string) (domain.Run, error) {
	panic("example GET must not resolve a run")
}
func (d *authoringExampleSpy) ListRunEvents(context.Context, string) ([]domain.RunEvent, error) {
	panic("example GET must not resolve run events")
}
func (d *authoringExampleSpy) ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error) {
	panic("example GET must not resolve evidence")
}
func (d *authoringExampleSpy) ListScenarios(context.Context) ([]domain.ScenarioRecord, error) {
	d.listCalls++
	return append([]domain.ScenarioRecord(nil), d.records...), nil
}
func (d *authoringExampleSpy) PreviewScenario(context.Context, string, scenario.Scenario, string) (compiler.Plan, error) {
	panic("example GET must not preview through a target")
}
func (d *authoringExampleSpy) ImportScenario(context.Context, []byte, scenario.Scenario) (domain.ScenarioRecord, error) {
	panic("example GET must not import")
}
func (d *authoringExampleSpy) InspectScenario(ctx context.Context, source []byte, mode string) (appruntime.ScenarioInspection, error) {
	d.inspectCalls++
	return authoring.New("test").Inspect(ctx, source, mode)
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
func (d *scenarioUIData) InspectScenario(ctx context.Context, source []byte, mode string) (appruntime.ScenarioInspection, error) {
	d.previewed = true
	return authoring.New("test").Inspect(ctx, source, mode)
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
	if got := res.Header().Get("Referrer-Policy"); got != "same-origin" {
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

func TestStreamingResponsesDisableServerWriteDeadline(t *testing.T) {
	writer := &deadlineResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	disableStreamingDeadline(writer)
	if len(writer.deadlines) != 1 || !writer.deadlines[0].IsZero() {
		t.Fatalf("deadlines=%v", writer.deadlines)
	}
}

func TestDetachedServiceActionContextIsBoundedAndSurvivesRequestCancellation(t *testing.T) {
	requestContext, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/operations/service/stop", nil).WithContext(requestContext)
	ctx, cancel := detachedServiceActionContext(request)
	defer cancel()
	cancelRequest()
	if err := ctx.Err(); err != nil {
		t.Fatalf("detached context inherited request cancellation: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 15*time.Second {
		t.Fatalf("deadline=%v ok=%t", deadline, ok)
	}
}

type deadlineResponseWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}
