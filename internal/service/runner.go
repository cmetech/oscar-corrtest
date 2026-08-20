package service

import (
	"context"
	"fmt"
	"os/exec"
)

// ExecRunner is the production external-command adapter.
type ExecRunner struct{}

// Run executes a command without a shell and returns combined diagnostic output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "systemctl", "launchctl", "schtasks.exe":
	default:
		return nil, fmt.Errorf("unsupported service manager command")
	}
	// #nosec G204 -- name is constrained above to the three platform service
	// managers; every argument is assembled by the closed service adapter.
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
