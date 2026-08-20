package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/applog"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/envfile"
	"github.com/cmetech/oscar-corrtest/internal/operations"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/platformpaths"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/service"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

func TestOperationsManagedKeyReachesNewOSCARClientWithoutLeaking(t *testing.T) {
	const secret = "integration-api-key-sentinel"
	var fakeMu sync.Mutex
	var receivedKeys, receivedAuthorization []string
	var probeLabels map[string]string
	oscarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		fakeMu.Lock()
		receivedKeys = append(receivedKeys, request.Header.Get("X-API-Key"))
		receivedAuthorization = append(receivedAuthorization, request.Header.Get("Authorization"))
		fakeMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/correlation_rules/validate":
			_, _ = io.WriteString(w, `{"valid":true,"errors":[]}`)
		case "/api/v1/alerts":
			var payload struct {
				CommonLabels map[string]string `json:"commonLabels"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				http.Error(w, "bad payload", http.StatusBadRequest)
				return
			}
			fakeMu.Lock()
			probeLabels = cloneMap(payload.CommonLabels)
			fakeMu.Unlock()
			_, _ = io.WriteString(w, `{"status":"accepted","task_id":"probe-task"}`)
		case "/api/v1/alerts/history":
			fakeMu.Lock()
			labels := cloneMap(probeLabels)
			fakeMu.Unlock()
			wireLabels := make([]map[string]string, 0, len(labels))
			for key, value := range labels {
				wireLabels = append(wireLabels, map[string]string{"Label": key, "Value": value})
			}
			response := map[string]any{"total_records": 1, "total_pages": 1, "page": 1, "per_page": 100, "records": []any{map[string]any{
				"id": "history-probe", "alertname": labels["alertname"], "fingerprint": "server-probe-fingerprint", "status": "firing",
				"createdAt": time.Now().UTC().Format(time.RFC3339Nano), "labels": wireLabels, "annotations": []any{},
			}}}
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.NotFound(w, request)
		}
	}))
	defer oscarServer.Close()

	root := t.TempDir()
	settings := config.Settings{
		ConfigPath: filepath.Join(root, "config.json"), EnvFile: filepath.Join(root, ".env"),
		DataDir: filepath.Join(root, "data"), LogDir: filepath.Join(root, "logs"), ListenAddress: "127.0.0.1:0",
	}
	store, err := envfile.Open(settings.EnvFile, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	logs, err := applog.Open(settings.LogDir, nil, applog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	controller := operations.New(settings, store, nil, logs)
	runtime, err := appruntime.OpenWithOptions(context.Background(), settings, version.Info{Version: "integration"}, appruntime.Options{
		Environment: store, Logger: logs.Logger("runtime"), Logs: logs, Operations: controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := web.NewHandlerWithData(version.Info{Version: "integration"}, runtime)
	appServer := httptest.NewServer(handler)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	getResponse, err := client.Get(appServer.URL + "/operations")
	if err != nil {
		t.Fatal(err)
	}
	getBody, _ := io.ReadAll(getResponse.Body)
	_ = getResponse.Body.Close()
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(getBody)
	if len(match) != 2 {
		t.Fatalf("operations response omitted CSRF token: %s", getBody)
	}
	form := url.Values{"csrf_token": {string(match[1])}, "api_key": {secret}}
	post, err := http.NewRequest(http.MethodPost, appServer.URL+"/operations/api-key", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Origin", appServer.URL)
	postResponse, err := client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	postBody, _ := io.ReadAll(postResponse.Body)
	_ = postResponse.Body.Close()
	if postResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", postResponse.StatusCode, postBody)
	}
	configuredResponse, err := client.Get(appServer.URL + "/operations")
	if err != nil {
		t.Fatal(err)
	}
	configuredBody, _ := io.ReadAll(configuredResponse.Body)
	_ = configuredResponse.Body.Close()
	if !strings.Contains(string(configuredBody), "Configured") || bytesContainAny(secret, getBody, postBody, configuredBody) {
		t.Fatalf("write-only key contract failed")
	}

	target, err := runtime.CreateTarget(context.Background(), domain.TargetInput{DisplayName: "Integration OSCAR", BaseURL: oscarServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	doctorContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	diagnostic, err := runtime.Doctor(doctorContext, target.ID, "phase_b_dispatch")
	if err != nil || !diagnostic.Compatible {
		t.Fatalf("diagnostic=%+v err=%v", diagnostic, err)
	}

	appServer.Close()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	fakeMu.Lock()
	keys := append([]string(nil), receivedKeys...)
	authorization := append([]string(nil), receivedAuthorization...)
	fakeMu.Unlock()
	if len(keys) != 3 {
		t.Fatalf("OSCAR request count=%d keys=%v", len(keys), keys)
	}
	for index := range keys {
		if keys[index] != secret || authorization[index] != "" {
			t.Fatalf("request %d key=%q authorization=%q", index, keys[index], authorization[index])
		}
	}
	assertSecretAbsentOutsideEnv(t, root, settings.EnvFile, secret)
}

func TestCanonicalFloodRunsThroughSemanticOracleAndCleansUp(t *testing.T) {
	source, err := scenario.BuiltinSource("flood")
	if err != nil {
		t.Fatal(err)
	}
	document, err := scenario.Decode(strings.NewReader(string(source)))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Cases) != 2 || document.Cases[0].Code != "P01" || document.Cases[1].Code != "N01" {
		t.Fatalf("canonical cases=%+v", document.Cases)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	run := domain.Run{ID: "crt_00000000000000000000000077", ShortToken: "00000077", Status: domain.RunQueued,
		CleanupStatus: domain.CleanupNotRequired, HarnessVersion: "integration", CreatedAt: now, UpdatedAt: now}
	plan, err := compiler.Compile(run, document, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cases) != 2 || len(plan.Cases[0].Alerts) != 5 || len(plan.Cases[0].Assertions) == 0 {
		t.Fatalf("compiled contract=%+v", plan)
	}
	identities := map[string]bool{}
	for _, alert := range plan.Cases[0].Alerts {
		for _, key := range []string{"alertname", "category", "oscar_test_run_id", "oscar_test_pattern", "oscar_test_case_code", "oscar_test_event_index"} {
			if alert.Labels[key] == "" {
				t.Fatalf("alert missing %s: %+v", key, alert)
			}
		}
		identities[alert.Labels["oscar_test_event_index"]] = true
	}
	if len(identities) != 5 {
		t.Fatalf("flood identities=%v", identities)
	}
	database, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	model := testoscar.NewModel(now)
	engine := runner.New(database, model, runner.Options{PollInterval: time.Second, Stabilization: time.Second, Now: model.Now, Sleep: model.Sleep})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.RunCompleted || stored.Verdict != domain.VerdictPass || stored.CleanupStatus != domain.CleanupClean {
		t.Fatalf("terminal run=%+v report=%s", stored, stored.CanonicalReportJSON)
	}
	for _, required := range []string{`"code":"P01"`, `"code":"N01"`, `"verdict":"PASS"`, `"resolutions":[`} {
		if !strings.Contains(string(stored.CanonicalReportJSON), required) {
			t.Fatalf("report missing %s: %s", required, stored.CanonicalReportJSON)
		}
	}
	resources, err := database.ListResources(context.Background(), run.ID)
	if err != nil || len(resources) != 2 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
	for _, resource := range resources {
		if resource.LifecycleState != domain.ResourceDeleted {
			t.Fatalf("unclean resource=%+v", resource)
		}
	}
}

func TestUserServiceDefinitionsAndLiveLogsComposeWithoutSecrets(t *testing.T) {
	const secret = "service-and-log-secret"
	root := t.TempDir()
	executable := filepath.Join(root, "Program Files", "oscar-corrtest")
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			paths := platformpaths.Paths{StateDir: filepath.Join(root, goos, "state"), BootstrapLog: filepath.Join(root, goos, "logs", "bootstrap.log"),
				ApplicationLog: filepath.Join(root, goos, "logs", "application.jsonl"), ServiceDefinition: filepath.Join(root, goos, "service-definition")}
			manager, err := service.NewManager(service.Options{GOOS: goos, Executable: executable, Paths: paths, Runner: inertServiceRunner{}})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Install(context.Background()); err != nil {
				t.Fatal(err)
			}
			definition, err := os.ReadFile(manager.DefinitionPath())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(definition), executable) || !strings.Contains(string(definition), "serve") || strings.Contains(string(definition), secret) {
				t.Fatalf("unsafe %s definition: %s", goos, definition)
			}
			if goos == "windows" && strings.Contains(string(definition), `encoding="UTF-16"`) {
				t.Fatal("Windows service definition declares UTF-16 but is emitted as UTF-8")
			}
		})
	}

	logs, err := applog.Open(filepath.Join(root, "live-logs"), nil, applog.Options{SubscriberBuffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	settings := config.Settings{EnvFile: filepath.Join(root, ".env"), DataDir: filepath.Join(root, "data"), LogDir: filepath.Join(root, "live-logs")}
	controller := operations.New(settings, nil, nil, logs)
	runtime, err := appruntime.OpenWithOptions(context.Background(), settings, version.Info{}, appruntime.Options{Logs: logs, Operations: controller})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	logs.Logger("runtime").Info("backfill transition", "api_key", secret)
	server := httptest.NewServer(web.NewHandlerWithData(version.Info{}, runtime))
	defer server.Close()
	streamContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(streamContext, http.MethodGet, server.URL+"/operations/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	logs.Logger("service").Info("live transition", "authorization", secret)
	deadline := time.After(2 * time.Second)
	var observed []string
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before live record: %v", observed)
			}
			observed = append(observed, line)
			if strings.Contains(line, "live transition") {
				joined := strings.Join(observed, "\n")
				if !strings.Contains(joined, "backfill transition") || strings.Index(joined, "backfill transition") > strings.Index(joined, "live transition") || strings.Contains(joined, secret) || !strings.Contains(joined, "[REDACTED]") {
					t.Fatalf("unsafe or unordered stream: %s", joined)
				}
				cancel()
				return
			}
		case <-deadline:
			t.Fatalf("live record timeout: %v", observed)
		}
	}
}

type inertServiceRunner struct{}

func (inertServiceRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	_, _, _ = ctx, name, args
	return nil, nil
}

func cloneMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func bytesContainAny(value string, values ...[]byte) bool {
	for _, candidate := range values {
		if strings.Contains(string(candidate), value) {
			return true
		}
	}
	return false
}

func assertSecretAbsentOutsideEnv(t *testing.T, root, envPath, secret string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == envPath {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("secret leaked to %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
