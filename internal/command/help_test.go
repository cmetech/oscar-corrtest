package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/service"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestGlobalHelpMakesTheCLIDiscoverable(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments"},
		{name: "help command", args: []string{"help"}},
		{name: "long flag", args: []string{"--help"}},
		{name: "short flag", args: []string{"-h"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := New(&stdout, &stderr, version.Info{}, nil)
			if code := app.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			got := stdout.String()
			for _, want := range []string{
				"OSCAR Correlation Test Harness",
				"Usage:\n  oscar-corrtest <command> [options]",
				"Commands:",
				"serve",
				"service",
				"scenario",
				"Getting started:",
				"oscar-corrtest help <command>",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("stdout missing %q:\n%s", want, got)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
	}
}

func TestEveryCommandPathHasUsefulHelp(t *testing.T) {
	tests := []struct {
		path  string
		usage string
	}{
		{"version", "oscar-corrtest version"},
		{"serve", "oscar-corrtest serve [options]"},
		{"service", "oscar-corrtest service <action>"},
		{"service install", "oscar-corrtest service install"},
		{"service start", "oscar-corrtest service start"},
		{"service stop", "oscar-corrtest service stop"},
		{"service restart", "oscar-corrtest service restart"},
		{"service status", "oscar-corrtest service status"},
		{"service logs", "oscar-corrtest service logs [options]"},
		{"service uninstall", "oscar-corrtest service uninstall"},
		{"target", "oscar-corrtest target <action>"},
		{"target add", "oscar-corrtest target add [options]"},
		{"target list", "oscar-corrtest target list [options]"},
		{"doctor", "oscar-corrtest doctor --target <id> --pipeline-mode <mode> [options]"},
		{"scenario", "oscar-corrtest scenario <action>"},
		{"scenario list", "oscar-corrtest scenario list [options]"},
		{"scenario validate", "oscar-corrtest scenario validate <file> [options]"},
		{"scenario import", "oscar-corrtest scenario import <file> [options]"},
		{"plan", "oscar-corrtest plan <builtin:pattern|scenario-file> --target <id> --pipeline-mode <mode> [options]"},
		{"run", "oscar-corrtest run <builtin:pattern|scenario-file> --target <id> --pipeline-mode <mode> [options]"},
		{"runs", "oscar-corrtest runs <action>"},
		{"runs list", "oscar-corrtest runs list [options]"},
		{"runs show", "oscar-corrtest runs show <run-id> [options]"},
		{"runs delete", "oscar-corrtest runs delete <exact-run-id> --yes [options]"},
		{"cleanup", "oscar-corrtest cleanup <action>"},
		{"cleanup retry", "oscar-corrtest cleanup retry <run-id> [options]"},
		{"retention", "oscar-corrtest retention <action>"},
		{"retention preview", "oscar-corrtest retention preview --before <RFC3339> [options]"},
		{"retention apply", "oscar-corrtest retention apply --before <RFC3339> --yes [options]"},
		{"export", "oscar-corrtest export <run-id> --output <zip-path> [options]"},
		{"verify-bundle", "oscar-corrtest verify-bundle <zip-path> [options]"},
		{"backup", "oscar-corrtest backup --output <path> [options]"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			path := strings.Fields(test.path)
			invocations := [][]string{
				append([]string{"help"}, path...),
				append(append([]string{}, path...), "--help"),
			}
			for _, args := range invocations {
				var stdout, stderr bytes.Buffer
				app := New(&stdout, &stderr, version.Info{}, nil)
				if code := app.Run(context.Background(), args); code != 0 {
					t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
				}
				got := stdout.String()
				if !strings.Contains(got, "Usage:\n  "+test.usage) {
					t.Fatalf("args=%v stdout missing usage %q:\n%s", args, test.usage, got)
				}
				if !strings.Contains(got, "Description:") {
					t.Fatalf("args=%v stdout missing description:\n%s", args, got)
				}
				if !strings.Contains(got, "Examples:") {
					t.Fatalf("args=%v stdout missing examples:\n%s", args, got)
				}
				if stderr.Len() != 0 {
					t.Fatalf("args=%v stderr=%q", args, stderr.String())
				}
			}
		})
	}
}

func TestCommandHelpSupportsNaturalInvocationForms(t *testing.T) {
	for _, args := range [][]string{
		{"help", "service", "start"},
		{"service", "start", "--help"},
		{"service", "start", "-h"},
		{"service", "help"},
	} {
		var stdout, stderr bytes.Buffer
		app := New(&stdout, &stderr, version.Info{}, nil)
		if code := app.Run(context.Background(), args); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		want := "Usage:\n  oscar-corrtest service start"
		if len(args) == 2 {
			want = "Usage:\n  oscar-corrtest service <action>"
		}
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("args=%v stdout=%q", args, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("args=%v stderr=%q", args, stderr.String())
		}
	}
}

func TestHelpExplainsOptionsAndExamplesForOperationalCommands(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"serve", "--help"}, []string{"Options:", "--listen", "--remote-mode", "oscar-corrtest serve"}},
		{[]string{"service", "logs", "--help"}, []string{"--lines", "--no-follow", "oscar-corrtest service logs --lines 100 --no-follow"}},
		{[]string{"target", "add", "--help"}, []string{"--url", "--credential-env", "OSCAR_API_KEY"}},
		{[]string{"run", "--help"}, []string{"--target", "--pipeline-mode", "builtin:flood"}},
		{[]string{"scenario", "validate", "--help"}, []string{"<file>", "--output", "scenario.yaml"}},
	}
	for _, test := range tests {
		var stdout, stderr bytes.Buffer
		app := New(&stdout, &stderr, version.Info{}, nil)
		if code := app.Run(context.Background(), test.args); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", test.args, code, stderr.String())
		}
		for _, want := range test.want {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("args=%v stdout missing %q:\n%s", test.args, want, stdout.String())
			}
		}
	}
}

func TestHelpNeverInitializesRuntimeOrServiceManagement(t *testing.T) {
	var runtimeCalls, serviceCalls int
	app := NewApplication(&bytes.Buffer{}, &bytes.Buffer{}, version.Info{}, Dependencies{
		Open: func(context.Context, config.Settings) (ApplicationRuntime, error) {
			runtimeCalls++
			return nil, nil
		},
		Service: func() (service.Manager, error) {
			serviceCalls++
			return nil, nil
		},
	})
	for _, args := range [][]string{
		{"target", "list", "--help"},
		{"run", "--help"},
		{"service", "status", "--help"},
	} {
		if code := app.Run(context.Background(), args); code != 0 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
	if runtimeCalls != 0 || serviceCalls != 0 {
		t.Fatalf("help initialized dependencies: runtime=%d service=%d", runtimeCalls, serviceCalls)
	}
}

func TestUnknownHelpTopicFailsWithAUsefulHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{}, nil)
	if code := app.Run(context.Background(), []string{"help", "service", "launch"}); code != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q", stdout.String())
	}
	for _, want := range []string{`unknown help topic "service launch"`, `oscar-corrtest service --help`} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %q", want, stderr.String())
		}
	}
}

func TestUnknownCommandsPointToTheNearestHelp(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"bogus"}, "oscar-corrtest --help"},
		{[]string{"service", "launch"}, "oscar-corrtest service --help"},
		{[]string{"target", "remove"}, "oscar-corrtest target --help"},
		{[]string{"scenario", "edit"}, "oscar-corrtest scenario --help"},
		{[]string{"runs", "archive"}, "oscar-corrtest runs --help"},
		{[]string{"cleanup", "all"}, "oscar-corrtest cleanup --help"},
		{[]string{"retention", "prune"}, "oscar-corrtest retention --help"},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			serviceCalls := 0
			app := NewApplication(&stdout, &stderr, version.Info{}, Dependencies{Service: func() (service.Manager, error) {
				serviceCalls++
				return nil, nil
			}})
			if code := app.Run(context.Background(), test.args); code != 2 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr=%q missing %q", stderr.String(), test.want)
			}
			if serviceCalls != 0 {
				t.Fatalf("invalid action initialized service manager %d times", serviceCalls)
			}
		})
	}
}
