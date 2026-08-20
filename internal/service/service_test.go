package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
