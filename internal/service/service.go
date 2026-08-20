// Package service manages oscar-corrtest as a current-user background service.
package service

import (
	"context"
	"io"

	"github.com/cmetech/oscar-corrtest/internal/platformpaths"
)

// State is a normalized platform-independent service state.
type State string

const (
	StateUnsupported  State = "unsupported"
	StateNotInstalled State = "not-installed"
	StateStopped      State = "stopped"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateFailed       State = "failed"
	StateUnknown      State = "unknown"
)

// Status is safe to show in CLI and UI diagnostics.
type Status struct {
	State     State  `json:"state"`
	Mechanism string `json:"mechanism"`
	PID       int    `json:"pid,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Runner executes a platform service-manager command.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// Manager is the stable lifecycle surface shared by CLI and UI.
type Manager interface {
	Install(context.Context) error
	Start(context.Context) error
	Stop(context.Context) error
	Restart(context.Context) error
	Status(context.Context) (Status, error)
	Logs(context.Context, int, bool) error
	Uninstall(context.Context) error
	DefinitionPath() string
}

// Options supplies platform and process dependencies.
type Options struct {
	GOOS       string
	Executable string
	Paths      platformpaths.Paths
	Runner     Runner
	Stdout     io.Writer
	Stderr     io.Writer
}
