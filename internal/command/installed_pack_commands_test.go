//go:build linux || darwin

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
	"github.com/cmetech/oscar-corrtest/internal/version"
	"golang.org/x/sys/unix"
)

func installedCommandFiles(t *testing.T) ([]string, string, string) {
	t.Helper()
	root := t.TempDir()
	kit := filepath.Join(root, "kit")
	original := filepath.Join(root, "original")
	for _, dir := range []string{kit, original, filepath.Join(original, "kit")} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	copyDir := func(from, to string) {
		entries, e := os.ReadDir(from)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			raw, e := os.ReadFile(filepath.Join(from, entry.Name()))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(to, entry.Name()), raw, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	copyDir("../testpack/testdata/kit", kit)
	copyDir("../testpack/testdata/kit", filepath.Join(original, "kit"))
	copyDir("../testpack/testdata/installation/records", original)
	completed, e := os.ReadFile(filepath.Join(original, "completed.json"))
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(completed, &fields); e != nil {
		t.Fatal(e)
	}
	owner := filepath.Join(root, "ownership.json")
	if e = os.WriteFile(owner, fields["ownership_declaration"], 0600); e != nil {
		t.Fatal(e)
	}
	pin, e := os.ReadFile(filepath.Join(kit, "manifest.sha256"))
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"--manifest", filepath.Join(kit, "manifest.json"), "--manifest-sha256", strings.TrimSpace(string(pin)), "--installation", original, "--ownership", owner, "--api-base", "https://owned-target.test", "--target", "tgt_api", "--receiver-target", "tgt_receiver"}
	return args, original, owner
}
func installedCommandArg(args []string, key, value string) []string {
	out := append([]string(nil), args...)
	for i := range out {
		if out[i] == key {
			out[i+1] = value
			return out
		}
	}
	return append(out, key, value)
}

type installedCommandRuntime struct {
	fakeRuntime
	calls         int
	kit           *testpack.Kit
	installation  *testpack.Installation
	api, receiver string
	result        domain.Run
	err           error
}

func (r *installedCommandRuntime) ExecuteInstalledPack(_ context.Context, k *testpack.Kit, i *testpack.Installation, api, receiver string) (domain.Run, error) {
	r.calls++
	r.kit = k
	r.installation = i
	r.api = api
	r.receiver = receiver
	return r.result, r.err
}

func TestInstalledPackPlanIsOfflineAndUsesActualOriginalArtifacts(t *testing.T) {
	for _, output := range []string{"human", "json"} {
		t.Run(output, func(t *testing.T) {
			args, _, _ := installedCommandFiles(t)
			args = append(args, "--output", output, "--config", "/does/not/exist", "--data-dir", "/not/created")
			var stdout, stderr bytes.Buffer
			app := NewConfigured(&stdout, &stderr, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) {
				t.Fatal("plan opened runtime")
				return nil, nil
			}, func(string) string { t.Fatal("plan resolved environment/configuration"); return "" })
			if code := app.Run(context.Background(), append([]string{"test-pack", "plan"}, args...)); code != 0 {
				t.Fatalf("%d: %s", code, stderr.String())
			}
			for _, want := range []string{"15", "audit", "tgt_api", "tgt_receiver", "https://owned-target.test", "operator-declared", "DIRTY", "not scenario PASS"} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("missing %q: %s", want, stdout.String())
				}
			}
			if output == "json" {
				var v any
				if json.Unmarshal(stdout.Bytes(), &v) != nil {
					t.Fatal("non-JSON output")
				}
			}
		})
	}
}
func TestInstalledPackValidationFailsBeforeRuntime(t *testing.T) {
	cases := []string{"missing-flag", "wrong-basename", "bad-pin", "foreign-origin", "prefix-changed", "malformed-owner", "owner-symlink", "owner-directory", "owner-fifo", "owner-too-large", "changed-journal", "bad-output", "positional", "arbitrary-scenario", "credential-flag", "blank-target", "blank-receiver"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			args, original, owner := installedCommandFiles(t)
			switch name {
			case "missing-flag":
				args = args[2:]
			case "wrong-basename":
				args = installedCommandArg(args, "--manifest", filepath.Join(filepath.Dir(args[1]), "other.json"))
			case "bad-pin":
				args = installedCommandArg(args, "--manifest-sha256", strings.Repeat("0", 64))
			case "foreign-origin":
				args = installedCommandArg(args, "--api-base", "https://foreign.test")
			case "prefix-changed":
				args = installedCommandArg(args, "--api-base", "https://owned-target.test/ext/mw")
			case "malformed-owner":
				_ = os.WriteFile(owner, []byte(`{"format":"wrong"}`), 0600)
			case "owner-symlink":
				link := owner + "-link"
				if e := os.Symlink(owner, link); e != nil {
					t.Fatal(e)
				}
				args = installedCommandArg(args, "--ownership", link)
			case "owner-directory":
				args = installedCommandArg(args, "--ownership", filepath.Dir(owner))
			case "owner-fifo":
				fifo := owner + "-fifo"
				if e := unix.Mkfifo(fifo, 0600); e != nil {
					t.Fatal(e)
				}
				args = installedCommandArg(args, "--ownership", fifo)
			case "owner-too-large":
				_ = os.WriteFile(owner, bytes.Repeat([]byte(" "), 65537), 0600)
			case "changed-journal":
				_ = os.WriteFile(filepath.Join(original, "0001-before.json"), []byte("{}"), 0600)
			case "bad-output":
				args = append(args, "--output", "yaml")
			case "positional":
				args = append(args, "other")
			case "arbitrary-scenario":
				args = append(args, "--scenario", "flood")
			case "credential-flag":
				args = append(args, "--key", "secret")
			case "blank-target":
				args = installedCommandArg(args, "--target", " ")
			case "blank-receiver":
				args = installedCommandArg(args, "--receiver-target", "")
			}
			for _, action := range []string{"plan", "run"} {
				var stdout, stderr bytes.Buffer
				app := NewConfigured(&stdout, &stderr, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) {
					t.Fatal("invalid input opened runtime")
					return nil, nil
				}, func(string) string { t.Fatal("invalid input resolved environment"); return "" })
				if code := app.Run(context.Background(), append([]string{"test-pack", action}, args...)); code == 0 {
					t.Fatalf("invalid accepted: %s", stdout.String())
				}
			}
		})
	}
}
func TestInstalledPackRunHandsVerifiedObjectsAndPreservesExitPolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		cleanup domain.CleanupStatus
		verdict domain.Verdict
		err     error
		want    int
	}{{"dirty-pass", domain.CleanupDirty, domain.VerdictPass, nil, 4}, {"unknown-error", domain.CleanupUnknown, domain.VerdictError, errors.New("interrupted"), 4}, {"clean-fail", domain.CleanupClean, domain.VerdictFail, nil, 1}, {"clean-inconclusive", domain.CleanupClean, domain.VerdictInconclusive, nil, 2}, {"clean-error", domain.CleanupClean, domain.VerdictError, errors.New("refused"), 3}} {
		t.Run(test.name, func(t *testing.T) {
			args, _, _ := installedCommandFiles(t)
			runtime := &installedCommandRuntime{result: domain.Run{ID: "crt_original", Verdict: test.verdict, CleanupStatus: test.cleanup}, err: test.err}
			var stdout, stderr bytes.Buffer
			opened := 0
			app := NewConfigured(&stdout, &stderr, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) { opened++; return runtime, nil }, testGetenv)
			if got := app.Run(context.Background(), append([]string{"test-pack", "run"}, args...)); got != test.want {
				t.Fatalf("%d want%d stderr%s", got, test.want, stderr.String())
			}
			if opened != 1 || runtime.calls != 1 || !runtime.closed || runtime.kit == nil || runtime.installation == nil || runtime.kit.Bindings().ManifestSHA256 != runtime.installation.Binding().ManifestSHA256 || runtime.api != "tgt_api" || runtime.receiver != "tgt_receiver" {
				t.Fatal("validated handoff or close lost")
			}
			for _, want := range []string{"audit-only", "not full UAT", "No source clear", "Cleanup:"} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("missing %s in %s", want, stdout.String())
				}
			}
			if strings.Contains(stdout.String(), "Native Alertmanager bypassed") {
				t.Fatal("false route claim")
			}
		})
	}
}
func TestInstalledPackRunUnsupportedRuntimeCloses(t *testing.T) {
	args, _, _ := installedCommandFiles(t)
	runtime := &fakeRuntime{}
	var stdout, stderr bytes.Buffer
	app := NewConfigured(&stdout, &stderr, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) { return runtime, nil }, testGetenv)
	if code := app.Run(context.Background(), append([]string{"test-pack", "run"}, args...)); code != 3 || !runtime.closed {
		t.Fatalf("%d closed%v %s", code, runtime.closed, stderr.String())
	}
}
func TestInstalledPackHelpExhaustiveOffline(t *testing.T) {
	for _, action := range []string{"plan", "run"} {
		var stdout, stderr bytes.Buffer
		app := NewConfigured(&stdout, &stderr, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) {
			t.Fatal("help opens runtime")
			return nil, nil
		}, func(string) string { t.Fatal("help resolves config"); return "" })
		if code := app.Run(context.Background(), []string{"test-pack", action, "--help"}); code != 0 {
			t.Fatalf("%d %s", code, stderr.String())
		}
		for _, flag := range []string{"--manifest", "--manifest-sha256", "--installation", "--ownership", "--api-base", "--target", "--receiver-target", "--config", "--data-dir", "--output", "operator-declared", "DIRTY"} {
			if !strings.Contains(stdout.String(), flag) {
				t.Fatalf("missing %s in %s", flag, stdout.String())
			}
		}
	}
}
func TestInstalledPackRunsShowCoverage(t *testing.T) {
	for _, report := range []bool{false, true} {
		run := domain.Run{ID: "crt_installed", Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupDirty, CompiledPlanJSON: []byte(`{"inputPath":"installed-test-pack"}`)}
		if report {
			run.CanonicalReportJSON = []byte(`{"coverage":{"inputPath":"installed-test-pack"}}`)
		}
		runtime := &fakeRuntime{runs: []domain.Run{run}}
		var stdout, stderr bytes.Buffer
		app := NewConfigured(&stdout, &stderr, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) { return runtime, nil }, testGetenv)
		if code := app.Run(context.Background(), []string{"runs", "show", run.ID}); code != 0 {
			t.Fatal(code)
		}
		if !strings.Contains(stdout.String(), "audit-only") || !strings.Contains(stdout.String(), "not full UAT") || strings.Contains(stdout.String(), "Native Alertmanager bypassed") {
			t.Fatal(stdout.String())
		}
	}
}
