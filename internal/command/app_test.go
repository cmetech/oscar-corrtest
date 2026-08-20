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
		{[]string{"run", "builtin:flood", "--target", "tgt_lab", "--pipeline-mode", "phase_b_dispatch", "--labels-survived", "--output", "json"}, completed.ID},
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
	if err := os.WriteFile(path, []byte("apiVersion: corrtest.oscar/v1alpha1\nkind: CorrelationScenario\nname: sample\nsuite: custom\npattern: flood\nmaxDuration: 90s\ncases: []\n"), 0o600); err != nil {
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
	content := "apiVersion: corrtest.oscar/v1alpha1\nkind: CorrelationScenario\nname: sample\nsuite: custom\npattern: flood\nmaxDuration: 90s\ncases: []\n"
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

type fakeRuntime struct {
	createdTarget  domain.TargetInput
	targets        []domain.Target
	runs           []domain.Run
	lastFilter     domain.RunFilter
	backupPath     string
	closed         bool
	executedRun    domain.Run
	customScenario scenario.Scenario
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
func (f *fakeRuntime) ExecuteBuiltin(_ context.Context, targetID, pattern, mode string, labelsSurvived bool) (domain.Run, error) {
	return f.executedRun, nil
}
func (f *fakeRuntime) PreviewScenario(_ context.Context, targetID string, document scenario.Scenario, mode string) (compiler.Plan, error) {
	f.customScenario = document
	return compiler.Plan{APIVersion: outputAPIVersion, Pattern: document.Pattern, RunID: "preview"}, nil
}
func (f *fakeRuntime) ExecuteScenario(_ context.Context, targetID string, document scenario.Scenario, mode string, labelsSurvived bool) (domain.Run, error) {
	f.customScenario = document
	return f.executedRun, nil
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
			if !strings.Contains(stderr.String(), "authenticated remote serving is not implemented") {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
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
