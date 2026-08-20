// Package operations composes value-safe operator controls for CLI and web UI.
package operations

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/applog"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/envfile"
	"github.com/cmetech/oscar-corrtest/internal/service"
)

// PathSnapshot contains only non-secret effective user locations.
type PathSnapshot struct {
	ConfigFile string `json:"configFile"`
	EnvFile    string `json:"envFile"`
	DataDir    string `json:"dataDir"`
	LogDir     string `json:"logDir"`
}

// Snapshot is the complete value-safe Operations state.
type Snapshot struct {
	Key        envfile.KeyStatus `json:"key"`
	Paths      PathSnapshot      `json:"paths"`
	Service    service.Status    `json:"service"`
	LogSources []applog.Source   `json:"logSources"`
}

// Controller owns no secret value; it delegates writes and returns status only.
type Controller struct {
	settings config.Settings
	store    *envfile.Store
	service  service.Manager
	logs     *applog.System
}

// New composes existing process-owned services.
func New(settings config.Settings, store *envfile.Store, manager service.Manager, logs *applog.System) *Controller {
	return &Controller{settings: settings, store: store, service: manager, logs: logs}
}

// Snapshot reads current status without exposing credential values.
func (c *Controller) Snapshot(ctx context.Context) (Snapshot, error) {
	result := Snapshot{Paths: PathSnapshot{ConfigFile: c.settings.ConfigPath, EnvFile: c.settings.EnvFile, DataDir: c.settings.DataDir, LogDir: c.settings.LogDir}}
	if c.store != nil {
		result.Key = c.store.Status("OSCAR_API_KEY")
	}
	if c.service == nil {
		result.Service = service.Status{State: service.StateUnsupported, Detail: "user service manager unavailable"}
	} else {
		status, err := c.service.Status(ctx)
		if err != nil {
			return result, err
		}
		result.Service = status
	}
	if c.logs != nil {
		result.LogSources = c.logs.Sources()
	}
	return result, nil
}

// ReplaceAPIKey atomically persists a nonempty key and returns only status.
func (c *Controller) ReplaceAPIKey(ctx context.Context, value string) (Snapshot, error) {
	if c.store == nil {
		return Snapshot{}, fmt.Errorf("managed environment is unavailable")
	}
	if strings.TrimSpace(value) == "" {
		return Snapshot{}, fmt.Errorf("OSCAR API key is required")
	}
	if err := c.store.Replace("OSCAR_API_KEY", value); err != nil {
		return Snapshot{}, err
	}
	return c.Snapshot(ctx)
}

// ClearAPIKey removes the managed key and masks startup external state until restart.
func (c *Controller) ClearAPIKey(ctx context.Context) (Snapshot, error) {
	if c.store == nil {
		return Snapshot{}, fmt.Errorf("managed environment is unavailable")
	}
	if err := c.store.Clear("OSCAR_API_KEY"); err != nil {
		return Snapshot{}, err
	}
	return c.Snapshot(ctx)
}

// ServiceAction executes one closed lifecycle action.
func (c *Controller) ServiceAction(ctx context.Context, action string) (Snapshot, error) {
	if c.service == nil {
		return Snapshot{}, fmt.Errorf("user service manager is unavailable")
	}
	var err error
	switch action {
	case "install":
		err = c.service.Install(ctx)
	case "start":
		err = c.service.Start(ctx)
	case "stop":
		err = c.service.Stop(ctx)
	case "restart":
		err = c.service.Restart(ctx)
	case "uninstall":
		err = c.service.Uninstall(ctx)
	default:
		return Snapshot{}, fmt.Errorf("unsupported service action")
	}
	if err != nil {
		return Snapshot{}, err
	}
	return c.Snapshot(ctx)
}

// RecentLogs returns at most 500 already-redacted records.
func (c *Controller) RecentLogs(limit int) []applog.Record {
	if c.logs == nil {
		return nil
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	return c.logs.Recent(limit)
}

// SubscribeLogs starts a bounded already-redacted live stream.
func (c *Controller) SubscribeLogs() applog.Subscription {
	if c.logs != nil {
		return c.logs.Subscribe()
	}
	channel := make(chan applog.Record)
	close(channel)
	return applog.Subscription{C: channel, Cancel: func() {}}
}

// OpenLogSource opens one exact allowlisted application log.
func (c *Controller) OpenLogSource(name string) (*os.File, error) {
	if c.logs == nil {
		return nil, fmt.Errorf("application logs are unavailable")
	}
	return c.logs.OpenSource(name)
}
