package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/service"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestServiceStatusExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state service.State
		err   error
		code  int
	}{
		{"running", service.StateRunning, nil, 0},
		{"stopped", service.StateStopped, nil, 3},
		{"not installed", service.StateNotInstalled, nil, 3},
		{"error", service.StateUnknown, errors.New("manager unavailable"), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			manager := &fakeServiceManager{status: service.Status{State: tt.state}, err: tt.err}
			app := NewApplication(&stdout, &stderr, version.Info{}, Dependencies{Service: func() (service.Manager, error) { return manager, nil }})
			if code := app.Run(context.Background(), []string{"service", "status"}); code != tt.code {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestServiceInstallDoesNotStart(t *testing.T) {
	var stdout, stderr bytes.Buffer
	manager := &fakeServiceManager{}
	app := NewApplication(&stdout, &stderr, version.Info{}, Dependencies{Service: func() (service.Manager, error) { return manager, nil }})
	if code := app.Run(context.Background(), []string{"service", "install"}); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if manager.install != 1 || manager.start != 0 {
		t.Fatalf("install=%d start=%d", manager.install, manager.start)
	}
}

func TestServiceLogsFlags(t *testing.T) {
	manager := &fakeServiceManager{}
	app := NewApplication(io.Discard, io.Discard, version.Info{}, Dependencies{Service: func() (service.Manager, error) { return manager, nil }})
	if code := app.Run(context.Background(), []string{"service", "logs", "--lines", "75", "--no-follow"}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if manager.lines != 75 || manager.follow {
		t.Fatalf("lines=%d follow=%t", manager.lines, manager.follow)
	}
}

type fakeServiceManager struct {
	status                                   service.Status
	err                                      error
	install, start, stop, restart, uninstall int
	lines                                    int
	follow                                   bool
}

func (m *fakeServiceManager) Install(context.Context) error                  { m.install++; return m.err }
func (m *fakeServiceManager) Start(context.Context) error                    { m.start++; return m.err }
func (m *fakeServiceManager) Stop(context.Context) error                     { m.stop++; return m.err }
func (m *fakeServiceManager) Restart(context.Context) error                  { m.restart++; return m.err }
func (m *fakeServiceManager) Status(context.Context) (service.Status, error) { return m.status, m.err }
func (m *fakeServiceManager) Logs(_ context.Context, lines int, follow bool) error {
	m.lines, m.follow = lines, follow
	return m.err
}
func (m *fakeServiceManager) Uninstall(context.Context) error { m.uninstall++; return m.err }
func (m *fakeServiceManager) DefinitionPath() string          { return "/user/service" }
