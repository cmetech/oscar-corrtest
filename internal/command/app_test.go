package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

const commandScenarioSource = `apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: sample
suite: custom
pattern: flood
maxDuration: 90s
cases:
  - {name: positive, code: P01, polarity: positive, role: interface_down, repeat: 5, window: 30s, assertions: [{kind: synthetic-alert-count, equals: 1}]}
  - {name: negative, code: N01, polarity: negative, role: interface_down, repeat: 4, window: 30s, assertions: [{kind: synthetic-alert-count, equals: 0}]}
`

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{Version: "v1.2.3", Commit: "abc", BuildDate: "now"}, nil)
	if code := app.Run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != "oscar-corrtest v1.2.3 commit=abc built=now\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestScenarioListPlanAndRunCommandsShareRuntimeContracts(t *testing.T) {
	now := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	completed := domain.Run{ID: "crt_00000000000000000000000099", ShortToken: "00000099", Status: domain.RunCompleted,
		Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean, HarnessVersion: "test", CreatedAt: now, UpdatedAt: now}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"scenario", "list", "--output", "json"}, `"pattern":"parent_child"`},
		{[]string{"plan", "builtin:flood", "--target", "tgt_lab", "--pipeline-mode", "phase_b_dispatch", "--output", "json"}, `"mutationBudget"`},
		{[]string{"run", "builtin:flood", "--target", "tgt_lab", "--pipeline-mode", "phase_b_dispatch", "--output", "json"}, completed.ID},
	}
	for _, test := range tests {
		var stdout, stderr bytes.Buffer
		fake := &fakeRuntime{executedRun: completed}
		open := func(context.Context, config.Settings) (ApplicationRuntime, error) { return fake, nil }
		app := NewConfigured(&stdout, &stderr, version.Info{Version: "test"}, nil, open, testGetenv)
		if code := app.Run(context.Background(), test.args); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", test.args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), test.want) {
			t.Fatalf("args=%v stdout=%q missing %q", test.args, stdout.String(), test.want)
		}
	}
}

func TestScenarioValidateAcceptsStrictCustomDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(path, []byte(commandScenarioSource), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{}, nil)
	if code := app.Run(context.Background(), []string{"scenario", "validate", path, "--output", "json"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"valid":true`) || !strings.Contains(stdout.String(), `"name":"sample"`) {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestPlanAcceptsCustomScenarioFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	content := commandScenarioSource
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	fake := &fakeRuntime{}
	open := func(context.Context, config.Settings) (ApplicationRuntime, error) { return fake, nil }
	app := NewConfigured(&stdout, &stderr, version.Info{}, nil, open, testGetenv)
	if code := app.Run(context.Background(), []string{"plan", path, "--target", "tgt_lab", "--pipeline-mode", "phase_b_dispatch", "--output", "json"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if fake.customScenario.Name != "sample" || !strings.Contains(stdout.String(), `"pattern":"flood"`) {
		t.Fatalf("scenario=%+v stdout=%q", fake.customScenario, stdout.String())
	}
}

func TestTargetAddUsesConfiguredRuntimeAndPrintsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fake := &fakeRuntime{}
	var settings config.Settings
	open := func(_ context.Context, got config.Settings) (ApplicationRuntime, error) {
		settings = got
		return fake, nil
	}
	app := NewConfigured(&stdout, &stderr, version.Info{Version: "test"}, nil, open, func(key string) string {
		if key == "HOME" {
			return "/tmp/corrtest-home"
		}
		return ""
	})
	code := app.Run(context.Background(), []string{
		"target", "add", "--data-dir", "/tmp/corrtest-state", "--name", "Lab A", "--url", "https://oscar.example",
		"--credential-env", "OSCAR_API_TOKEN", "--output", "json",
	})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if settings.DataDir != "/tmp/corrtest-state" || fake.createdTarget.DisplayName != "Lab A" || fake.createdTarget.Credential.Reference != "OSCAR_API_TOKEN" {
		t.Fatalf("settings=%+v input=%+v", settings, fake.createdTarget)
	}
	if !strings.Contains(stdout.String(), `"apiVersion":"corrtest.oscar/v1alpha1"`) || !strings.Contains(stdout.String(), `"displayName":"Lab A"`) {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if !fake.closed {
		t.Fatal("runtime not closed")
	}
}

func TestRunsListShowAndBackupCommands(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	run := domain.Run{
		ID: "crt_00000000000000000000000000", ShortToken: "00000000", Status: domain.RunCompleted,
		Verdict: domain.VerdictFail, CleanupStatus: domain.CleanupClean, HarnessVersion: "test", CreatedAt: now, UpdatedAt: now,
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"runs", "list", "--verdict", "FAIL", "--output", "json"}, run.ID},
		{"show", []string{"runs", "show", "--output", "json", run.ID}, `"status":"COMPLETED"`},
		{"backup", []string{"backup", "--output", "/tmp/corrtest-backup.db"}, "Backup written"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			fake := &fakeRuntime{runs: []domain.Run{run}}
			open := func(context.Context, config.Settings) (ApplicationRuntime, error) { return fake, nil }
			app := NewConfigured(&stdout, &stderr, version.Info{}, nil, open, testGetenv)
			if code := app.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout=%q missing %q", stdout.String(), test.want)
			}
			if test.name == "list" && fake.lastFilter.Verdict != domain.VerdictFail {
				t.Fatalf("filter=%+v", fake.lastFilter)
			}
			if test.name == "backup" && fake.backupPath != "/tmp/corrtest-backup.db" {
				t.Fatalf("backup path=%q", fake.backupPath)
			}
		})
	}
}

func TestRetentionRequiresExplicitApplyConfirmation(t *testing.T) {
	cutoff := "2026-08-01T00:00:00Z"
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"preview", []string{"retention", "preview", "--before", cutoff, "--output", "json"}, `"id":"crt_old"`},
		{"apply", []string{"retention", "apply", "--before", cutoff, "--yes", "--output", "json"}, `"crt_old"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			fake := &fakeRuntime{retentionRuns: []domain.Run{{ID: "crt_old", Status: domain.RunCompleted, CleanupStatus: domain.CleanupClean}}}
			open := func(context.Context, config.Settings) (ApplicationRuntime, error) { return fake, nil }
			app := NewConfigured(&stdout, &stderr, version.Info{}, nil, open, testGetenv)
			if code := app.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout=%q missing %q", stdout.String(), test.want)
			}
			if test.name == "apply" && !fake.retentionApplied {
				t.Fatal("retention apply was not invoked")
			}
		})
	}
}

type fakeRuntime struct {
	createdTarget    domain.TargetInput
	targets          []domain.Target
	runs             []domain.Run
	lastFilter       domain.RunFilter
	backupPath       string
	closed           bool
	executedRun      domain.Run
	customScenario   scenario.Scenario
	retentionRuns    []domain.Run
	retentionApplied bool
}

func (f *fakeRuntime) CreateTarget(_ context.Context, input domain.TargetInput) (domain.Target, error) {
	f.createdTarget = input
	return domain.Target{ID: "tgt_test", DisplayName: input.DisplayName, BaseURL: input.BaseURL, APIProfile: "public-v1", TLS: input.TLS, Credential: input.Credential}, nil
}
func (f *fakeRuntime) ListTargets(context.Context) ([]domain.Target, error) { return f.targets, nil }
func (f *fakeRuntime) ListRuns(_ context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	f.lastFilter = filter
	return f.runs, nil
}
func (f *fakeRuntime) GetRun(_ context.Context, id string) (domain.Run, error) {
	for _, run := range f.runs {
		if run.ID == id {
			return run, nil
		}
	}
	return domain.Run{}, errors.New("not found")
}
func (f *fakeRuntime) ListRunEvents(context.Context, string) ([]domain.RunEvent, error) {
	return nil, nil
}
func (f *fakeRuntime) ListArtifactEvidence(context.Context, string) ([]domain.ArtifactEvidence, error) {
	return nil, nil
}
func (f *fakeRuntime) ReadyStatus() (bool, string)                 { return true, "" }
func (f *fakeRuntime) Backup(_ context.Context, path string) error { f.backupPath = path; return nil }
func (f *fakeRuntime) Close() error                                { f.closed = true; return nil }
func (f *fakeRuntime) PreviewBuiltin(_ context.Context, targetID, pattern, mode string) (compiler.Plan, error) {
	return compiler.Plan{APIVersion: outputAPIVersion, Pattern: pattern, RunID: "preview", MutationBudget: compiler.MutationBudget{Rules: 2, Alerts: 9}}, nil
}
func (f *fakeRuntime) ExecuteBuiltin(_ context.Context, targetID, pattern, mode string) (domain.Run, error) {
	return f.executedRun, nil
}
func (f *fakeRuntime) PreviewScenario(_ context.Context, targetID string, document scenario.Scenario, mode string) (compiler.Plan, error) {
	f.customScenario = document
	return compiler.Plan{APIVersion: outputAPIVersion, Pattern: document.Pattern, RunID: "preview"}, nil
}
func (f *fakeRuntime) ExecuteScenario(_ context.Context, targetID string, document scenario.Scenario, mode string) (domain.Run, error) {
	f.customScenario = document
	return f.executedRun, nil
}
func (f *fakeRuntime) PreviewRetention(context.Context, time.Time) ([]domain.Run, error) {
	return f.retentionRuns, nil
}
func (f *fakeRuntime) ApplyRetention(context.Context, time.Time) ([]string, error) {
	f.retentionApplied = true
	ids := make([]string, 0, len(f.retentionRuns))
	for _, run := range f.retentionRuns {
		ids = append(ids, run.ID)
	}
	return ids, nil
}

func testGetenv(key string) string {
	if key == "HOME" {
		return "/tmp/corrtest-home"
	}
	return ""
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{}, nil)
	if code := app.Run(context.Background(), []string{"bogus"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestServeCommandPassesListenAddress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var got string
	serve := func(_ context.Context, opts web.Options) error {
		got = opts.ListenAddress
		return nil
	}
	app := New(&stdout, &stderr, version.Info{}, serve)
	if code := app.Run(context.Background(), []string{"serve", "--listen", "127.0.0.1:9999"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got != "127.0.0.1:9999" {
		t.Fatalf("listen=%q", got)
	}
	if !strings.Contains(stdout.String(), "listening on http://127.0.0.1:9999") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestServeAcceptsOnlyLiteralLoopbackAddresses(t *testing.T) {
	tests := []struct {
		address string
		wantOK  bool
	}{
		{"127.0.0.1:8787", true},
		{"127.0.0.2:8787", true},
		{"[::1]:8787", true},
		{"localhost:8787", false},
		{"0.0.0.0:8787", false},
		{"[::]:8787", false},
		{":8787", false},
		{"192.0.2.10:8787", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			called := false
			serve := func(_ context.Context, _ web.Options) error {
				called = true
				return nil
			}
			code := New(&stdout, &stderr, version.Info{}, serve).Run(
				context.Background(), []string{"serve", "--listen", tt.address},
			)
			if tt.wantOK {
				if code != 0 || !called {
					t.Fatalf("exit=%d called=%v stderr=%q", code, called, stderr.String())
				}
				return
			}
			if code != 2 || called {
				t.Fatalf("exit=%d called=%v stderr=%q", code, called, stderr.String())
			}
			if !strings.Contains(stderr.String(), "non-loopback serving requires --remote-mode") {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
	}
}

func TestServeAllowsExplicitAuthenticatedRemoteModes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		mode web.SecurityMode
	}{
		{"bearer", []string{"serve", "--listen", "0.0.0.0:8787", "--remote-mode", "bearer", "--auth-token-env", "CORRTEST_AUTH", "--tls-cert", "/cert.pem", "--tls-key", "/key.pem"}, web.SecurityBearer},
		{"proxy", []string{"serve", "--listen", "0.0.0.0:8787", "--remote-mode", "trusted-proxy", "--proxy-header", "X-Forwarded-User", "--proxy-value", "corrtest", "--trusted-proxy", "10.0.0.0/8"}, web.SecurityTrustedProxy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var got web.Options
			serve := func(_ context.Context, options web.Options) error { got = options; return nil }
			app := NewConfigured(&stdout, &stderr, version.Info{}, serve, nil, func(key string) string {
				switch key {
				case "HOME":
					return "/tmp/corrtest-home"
				case "CORRTEST_AUTH":
					return "correct horse battery staple"
				default:
					return ""
				}
			})
			if code := app.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if got.Security.Mode != test.mode {
				t.Fatalf("security=%+v", got.Security)
			}
			if strings.Contains(stdout.String()+stderr.String(), "correct horse battery staple") {
				t.Fatal("credential leaked to output")
			}
		})
	}
}

func TestServeRejectsBearerRemoteModeWithoutTLS(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := NewConfigured(&stdout, &stderr, version.Info{}, func(context.Context, web.Options) error { return nil }, nil, func(key string) string {
		if key == "CORRTEST_AUTH" {
			return "correct horse battery staple"
		}
		return "/tmp"
	})
	code := app.Run(context.Background(), []string{"serve", "--listen", "0.0.0.0:8787", "--remote-mode", "bearer", "--auth-token-env", "CORRTEST_AUTH"})
	if code != 2 || !strings.Contains(stderr.String(), "requires --tls-cert") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestServeRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	serve := func(_ context.Context, _ web.Options) error { called = true; return nil }
	code := New(&stdout, &stderr, version.Info{}, serve).Run(
		context.Background(), []string{"serve", "extra"},
	)
	if code != 2 || called {
		t.Fatalf("exit=%d called=%v stderr=%q", code, called, stderr.String())
	}
}

func TestServeReportsServerFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	serve := func(_ context.Context, _ web.Options) error { return errors.New("boom") }
	code := New(&stdout, &stderr, version.Info{}, serve).Run(context.Background(), []string{"serve"})
	if code != 1 || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}
