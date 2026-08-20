package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/platformpaths"
)

func TestRenderDefinitionsContainResolvedPathsAndNoSecrets(t *testing.T) {
	paths := platformpaths.Paths{StateDir: "/Users/Test User/state", LogDir: "/Users/Test User/state/logs", BootstrapLog: "/Users/Test User/state/logs/bootstrap.log"}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		doc, err := renderDefinition(goos, "/Users/Test User/bin/oscar-corrtest", paths)
		if err != nil {
			t.Fatal(err)
		}
		text := string(doc)
		for _, required := range []string{"oscar-corrtest", "serve", "bootstrap.log"} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s definition missing %q: %s", goos, required, text)
			}
		}
		for _, forbidden := range []string{"OSCAR_API_KEY", "--tls-cert", "--remote-mode"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s definition contains %q", goos, forbidden)
			}
		}
	}
}

func TestExecRunnerRejectsCommandsOutsideServiceManagerAllowlist(t *testing.T) {
	_, err := (ExecRunner{}).Run(context.Background(), "operator-controlled-command")
	if err == nil || !strings.Contains(err.Error(), "unsupported service manager command") {
		t.Fatalf("error=%v", err)
	}
}

func TestInstallEnablesWithoutStarting(t *testing.T) {
	for _, tt := range []struct {
		goos      string
		forbidden string
	}{
		{"linux", "start"},
		{"darwin", "kickstart"},
		{"windows", "/Run"},
	} {
		t.Run(tt.goos, func(t *testing.T) {
			runner := &recordingRunner{}
			manager, err := NewManager(testOptions(t, tt.goos, runner))
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Install(context.Background()); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(runner.commands, "\n")
			if strings.Contains(joined, tt.forbidden) {
				t.Fatalf("install started service: %s", joined)
			}
			if _, err := os.Stat(manager.DefinitionPath()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagerNormalizesStatusAndLifecycleCommands(t *testing.T) {
	runner := &recordingRunner{responses: [][]byte{[]byte("ActiveState=active\nMainPID=42\n")}}
	options := testOptions(t, "linux", runner)
	if err := os.WriteFile(options.Paths.ServiceDefinition, []byte("unit"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil || status.State != StateRunning || status.PID != 42 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDarwinMissingLoadedServiceIsStoppedWhenDefinitionExists(t *testing.T) {
	runner := &recordingRunner{responses: [][]byte{[]byte("Could not find service io.cmetech.oscar-corrtest in domain for user")}, err: errors.New("exit status 113")}
	options := testOptions(t, "darwin", runner)
	if err := os.WriteFile(options.Paths.ServiceDefinition, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil || status.State != StateStopped {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestDarwinStartKickstartsLoadedStoppedService(t *testing.T) {
	runner := &launchdLifecycleRunner{loaded: true}
	manager := newInstalledDarwinManager(t, runner)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.running {
		t.Fatal("start returned without making the loaded service run")
	}
	if runner.bootstraps != 0 {
		t.Fatalf("loaded service was bootstrapped %d times", runner.bootstraps)
	}
}

func TestDarwinStartBootstrapsUnloadedService(t *testing.T) {
	runner := &launchdLifecycleRunner{}
	manager := newInstalledDarwinManager(t, runner)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.loaded || !runner.running {
		t.Fatalf("start left service loaded=%t running=%t", runner.loaded, runner.running)
	}
	if runner.bootstraps != 1 {
		t.Fatalf("bootstrap count=%d, want 1", runner.bootstraps)
	}
}

func TestDarwinRestartDoesNotRaceAsynchronousStop(t *testing.T) {
	runner := &launchdLifecycleRunner{loaded: true, running: true, asynchronousStop: true}
	manager := newInstalledDarwinManager(t, runner)

	if err := manager.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateRunning || !runner.running {
		t.Fatalf("restart returned with status=%s running=%t", status.State, runner.running)
	}
	if runner.kills != 0 {
		t.Fatalf("restart used the racy stop path %d times", runner.kills)
	}
}

func TestDarwinStartRejectsSuccessfulKickstartWithoutRunningState(t *testing.T) {
	runner := &launchdLifecycleRunner{suppressStart: true}
	manager := newInstalledDarwinManager(t, runner)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := manager.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("error=%v, want final running-state failure", err)
	}
}

func TestUninstallPreservesState(t *testing.T) {
	runner := &recordingRunner{}
	options := testOptions(t, "linux", runner)
	if err := os.MkdirAll(options.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(options.Paths.StateDir, "corrtest.db")
	if err := os.WriteFile(data, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, _ := NewManager(options)
	if err := manager.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.DefinitionPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("definition remains: %v", err)
	}
}

type recordingRunner struct {
	commands  []string
	responses [][]byte
	err       error
}

// launchdLifecycleRunner models the observable launchctl boundary. An
// asynchronous kill remains visible as running for the next status read,
// matching the race that left the real LaunchAgent stopped after restart.
type launchdLifecycleRunner struct {
	loaded           bool
	running          bool
	asynchronousStop bool
	stopPending      bool
	suppressStart    bool
	bootstraps       int
	kills            int
}

func (r *launchdLifecycleRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "launchctl" || len(args) == 0 {
		return nil, fmt.Errorf("unexpected command %s %s", name, strings.Join(args, " "))
	}
	switch args[0] {
	case "print":
		if !r.loaded {
			return []byte("Could not find service io.cmetech.oscar-corrtest in domain for user"), errors.New("exit status 113")
		}
		if r.running {
			output := []byte("state = running\npid = 42\n")
			if r.stopPending {
				r.running = false
				r.stopPending = false
			}
			return output, nil
		}
		return []byte("state = not running\nlast exit code = 0\n"), nil
	case "bootstrap":
		if r.loaded {
			return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
		}
		r.loaded = true
		r.bootstraps++
		return nil, nil
	case "kickstart":
		if !r.loaded {
			return []byte("Could not find service io.cmetech.oscar-corrtest in domain for user"), errors.New("exit status 113")
		}
		if !r.suppressStart {
			r.running = true
			r.stopPending = false
		}
		return nil, nil
	case "kill":
		r.kills++
		if r.asynchronousStop {
			r.stopPending = true
		} else {
			r.running = false
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected launchctl action %q", args[0])
	}
}

func newInstalledDarwinManager(t *testing.T, runner Runner) Manager {
	t.Helper()
	options := testOptions(t, "darwin", runner)
	if err := os.WriteFile(options.Paths.ServiceDefinition, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	if len(r.responses) > 0 {
		response := r.responses[0]
		r.responses = r.responses[1:]
		return response, r.err
	}
	return nil, r.err
}

func testOptions(t *testing.T, goos string, runner Runner) Options {
	t.Helper()
	root := t.TempDir()
	definition := filepath.Join(root, "service-definition")
	return Options{
		GOOS: goos, Executable: filepath.Join(root, "bin", "oscar-corrtest"), Runner: runner,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Paths: platformpaths.Paths{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), LogDir: filepath.Join(root, "state", "logs"), BootstrapLog: filepath.Join(root, "state", "logs", "bootstrap.log"), ApplicationLog: filepath.Join(root, "state", "logs", "application.jsonl"), ServiceDefinition: definition},
	}
}
