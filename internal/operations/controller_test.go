package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/applog"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/envfile"
	"github.com/cmetech/oscar-corrtest/internal/service"
)

func TestReplaceAPIKeyReturnsNoSecretDerivedData(t *testing.T) {
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
	controller := New(config.Settings{ConfigPath: filepath.Join(root, "config.json"), EnvFile: filepath.Join(root, ".env"), DataDir: filepath.Join(root, "data"), LogDir: filepath.Join(root, "logs")}, store, &fakeManager{status: service.Status{State: service.StateStopped}}, logs)
	const secret = "unique-sentinel-secret"
	snapshot, err := controller.ReplaceAPIKey(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if !snapshot.Key.Configured || strings.Contains(string(encoded), secret) {
		t.Fatalf("unsafe snapshot=%s", encoded)
	}
	if got := store.Getenv("OSCAR_API_KEY"); got != secret {
		t.Fatalf("stored key=%q", got)
	}
}

func TestControllerSnapshotsPathsServiceAndLogs(t *testing.T) {
	root := t.TempDir()
	store, _ := envfile.Open(filepath.Join(root, ".env"), nil)
	logs, _ := applog.Open(filepath.Join(root, "logs"), nil, applog.Options{})
	defer logs.Close()
	logs.Logger("runtime").Info("ready")
	manager := &fakeManager{status: service.Status{State: service.StateRunning, PID: 42}}
	settings := config.Settings{ConfigPath: filepath.Join(root, "config.json"), EnvFile: filepath.Join(root, ".env"), DataDir: filepath.Join(root, "data"), LogDir: filepath.Join(root, "logs")}
	controller := New(settings, store, manager, logs)
	snapshot, err := controller.Snapshot(context.Background())
	if err != nil || snapshot.Service.State != service.StateRunning || snapshot.Service.PID != 42 || snapshot.Paths.EnvFile != settings.EnvFile || len(snapshot.LogSources) == 0 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if len(controller.RecentLogs(50)) != 1 {
		t.Fatal("recent logs missing")
	}
}

func TestControllerValidatesMutationsAndSources(t *testing.T) {
	root := t.TempDir()
	store, _ := envfile.Open(filepath.Join(root, ".env"), nil)
	logs, _ := applog.Open(filepath.Join(root, "logs"), nil, applog.Options{})
	defer logs.Close()
	manager := &fakeManager{}
	controller := New(config.Settings{EnvFile: filepath.Join(root, ".env"), LogDir: filepath.Join(root, "logs")}, store, manager, logs)
	if _, err := controller.ReplaceAPIKey(context.Background(), ""); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := controller.ServiceAction(context.Background(), "arbitrary"); err == nil {
		t.Fatal("arbitrary service action accepted")
	}
	if _, err := controller.OpenLogSource("../.env"); err == nil {
		t.Fatal("traversal source accepted")
	}
}

type fakeManager struct {
	status  service.Status
	err     error
	actions []string
	output  bytes.Buffer
}

func (m *fakeManager) Install(context.Context) error {
	m.actions = append(m.actions, "install")
	return m.err
}
func (m *fakeManager) Start(context.Context) error {
	m.actions = append(m.actions, "start")
	return m.err
}
func (m *fakeManager) Stop(context.Context) error {
	m.actions = append(m.actions, "stop")
	return m.err
}
func (m *fakeManager) Restart(context.Context) error {
	m.actions = append(m.actions, "restart")
	return m.err
}
func (m *fakeManager) Status(context.Context) (service.Status, error) { return m.status, m.err }
func (m *fakeManager) Logs(context.Context, int, bool) error          { return m.err }
func (m *fakeManager) Uninstall(context.Context) error {
	m.actions = append(m.actions, "uninstall")
	return m.err
}
func (m *fakeManager) DefinitionPath() string { return "/service" }

var _ service.Manager = (*fakeManager)(nil)
