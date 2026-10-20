//go:build linux || darwin

package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/config"
	appruntime "github.com/cmetech/oscar-corrtest/internal/runtime"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestCompactObservationHelpHasNoInjectionOrFreeScope(t *testing.T) {
	var out, errout bytes.Buffer
	app := New(&out, &errout, version.Info{}, nil)
	code := app.Run(context.Background(), []string{"test-pack", "observe-compact", "--help"})
	if code != 0 {
		t.Fatalf("observe help unavailable: %d %s", code, errout.String())
	}
	for _, required := range []string{"--evidence", "--manifest-sha256", "observation", "not scenario PASS"} {
		if !strings.Contains(out.String(), required) {
			t.Fatalf("help missing %s: %s", required, out.String())
		}
	}
	for _, forbidden := range []string{"--epoch", "--revision", "--receiver-target"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("observation accepts unsupported input %s", forbidden)
		}
	}
}

type compactCommandRuntime struct {
	fakeRuntime
	calls  int
	result appruntime.CompactObservationResult
	err    error
}

func (r *compactCommandRuntime) ObserveCompact(_ context.Context, kit *testpack.Kit, original *testpack.Installation, target, destination string) (appruntime.CompactObservationResult, error) {
	r.calls++
	if kit.Bindings().ManifestSHA256 != original.Binding().ManifestSHA256 || original.Binding().RuleID != 7 || target != "tgt_api" || destination == "" {
		return appruntime.CompactObservationResult{}, errors.New("invalid actual caller binding")
	}
	return r.result, r.err
}

func TestCompactObservationCommandCallsBoundRuntimeAndKeepsFailuresPrivate(t *testing.T) {
	for _, mode := range []string{"observed", "unavailable", "missing-result"} {
		t.Run(mode, func(t *testing.T) {
			args, _, _ := installedCommandFiles(t)
			for i := 0; i < len(args); i++ {
				if args[i] == "--receiver-target" {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			path := t.TempDir() + "/observation.json"
			args = append(args, "--evidence", path, "--output", "json")
			observer := &compactCommandRuntime{result: appruntime.CompactObservationResult{
				Path: path, SHA256: strings.Repeat("a", 64), Bytes: 123, Outcome: mode}}
			want := 0
			if mode == "unavailable" {
				observer.err, want = errors.New("private upstream credential detail"), 3
			}
			if mode == "missing-result" {
				observer.result, want = appruntime.CompactObservationResult{}, 3
			}
			var out, errout bytes.Buffer
			app := NewConfigured(&out, &errout, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) {
				return observer, nil
			}, testGetenv)
			if code := app.Run(context.Background(), append([]string{"test-pack", "observe-compact"}, args...)); code != want || observer.calls != 1 || !observer.closed {
				t.Fatalf("actual command binding failed: code=%d calls=%d closed=%v %s", code, observer.calls, observer.closed, errout.String())
			}
			if strings.Contains(out.String()+errout.String(), "credential detail") {
				t.Fatal("terminal exposed private runtime failure")
			}
		})
	}
}

func TestCompactObservationRejectsUnverifiedOriginalsBeforeRuntime(t *testing.T) {
	args, _, _ := installedCommandFiles(t)
	args = installedCommandArg(args, "--manifest-sha256", strings.Repeat("0", 64))
	for i := 0; i < len(args); i++ {
		if args[i] == "--receiver-target" {
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	args = append(args, "--evidence", t.TempDir()+"/observation.json")
	var out, errout bytes.Buffer
	app := NewConfigured(&out, &errout, version.Info{}, nil, func(context.Context, config.Settings) (ApplicationRuntime, error) {
		t.Fatal("invalid original artifacts opened runtime")
		return nil, nil
	}, func(string) string { return "" })
	code := app.Run(context.Background(), append([]string{"test-pack", "observe-compact"}, args...))
	if code != 3 || !strings.Contains(errout.String(), "kit") {
		t.Fatalf("expected pinned kit refusal: %d %s", code, errout.String())
	}
}
