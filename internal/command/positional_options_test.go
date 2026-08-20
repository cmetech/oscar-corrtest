package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestDocumentedCommandsAcceptOptionsAfterTheirPositionalArgument(t *testing.T) {
	const runID = "crt_0000000000000000000000042"
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"runs show", []string{"runs", "show", runID, "--output", "json"}, runID},
		{"runs delete", []string{"runs", "delete", runID, "--yes"}, "Deleted run " + runID},
		{"cleanup retry", []string{"cleanup", "retry", runID, "--data-dir", "/tmp/corrtest-state"}, "cleanup: CLEAN"},
		{"export", []string{"export", runID, "--output", "/tmp/corrtest-evidence.zip"}, "Evidence bundle: /tmp/corrtest-evidence.zip"},
		{"verify bundle", []string{"verify-bundle", "/tmp/corrtest-evidence.zip", "--data-dir", "/tmp/corrtest-state"}, "Evidence bundle verified"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			runtime := &positionalOptionsRuntime{fakeRuntime: &fakeRuntime{runs: []domain.Run{{ID: runID}}}}
			open := func(context.Context, config.Settings) (ApplicationRuntime, error) { return runtime, nil }
			app := NewConfigured(&stdout, &stderr, version.Info{}, nil, open, testGetenv)
			if code := app.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("args=%v exit=%d stderr=%q", test.args, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("args=%v stdout=%q missing %q", test.args, stdout.String(), test.want)
			}
		})
	}
}

type positionalOptionsRuntime struct {
	*fakeRuntime
}

func (r *positionalOptionsRuntime) DeleteRun(context.Context, string) error { return nil }

func (r *positionalOptionsRuntime) RetryCleanup(_ context.Context, id string) (domain.Run, error) {
	return domain.Run{ID: id, CleanupStatus: domain.CleanupClean}, nil
}

func (r *positionalOptionsRuntime) ExportRun(_ context.Context, _ string, path string) (evidence.Result, error) {
	return evidence.Result{Path: path, SHA256: "sha256"}, nil
}

func (r *positionalOptionsRuntime) VerifyBundle(context.Context, string) error { return nil }
