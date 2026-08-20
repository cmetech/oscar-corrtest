package service

import (
	"context"
	"os/exec"
)

// ExecRunner is the production external-command adapter.
type ExecRunner struct{}

// Run executes a command without a shell and returns combined diagnostic output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
