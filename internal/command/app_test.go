package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/version"
	"github.com/cmetech/oscar-corrtest/internal/web"
)

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{Version: "v1.2.3", Commit: "abc", BuildDate: "now"}, nil)
	if code := app.Run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != "oscar-corrtest v1.2.3 commit=abc built=now\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{}, nil)
	if code := app.Run(context.Background(), []string{"bogus"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestServeCommandPassesListenAddress(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var got string
	serve := func(_ context.Context, opts web.Options) error {
		got = opts.ListenAddress
		return nil
	}
	app := New(&stdout, &stderr, version.Info{}, serve)
	if code := app.Run(context.Background(), []string{"serve", "--listen", "127.0.0.1:9999"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got != "127.0.0.1:9999" {
		t.Fatalf("listen=%q", got)
	}
	if !strings.Contains(stdout.String(), "listening on http://127.0.0.1:9999") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestServeAcceptsOnlyLiteralLoopbackAddresses(t *testing.T) {
	tests := []struct {
		address string
		wantOK  bool
	}{
		{"127.0.0.1:8787", true},
		{"127.0.0.2:8787", true},
		{"[::1]:8787", true},
		{"localhost:8787", false},
		{"0.0.0.0:8787", false},
		{"[::]:8787", false},
		{":8787", false},
		{"192.0.2.10:8787", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			called := false
			serve := func(_ context.Context, _ web.Options) error {
				called = true
				return nil
			}
			code := New(&stdout, &stderr, version.Info{}, serve).Run(
				context.Background(), []string{"serve", "--listen", tt.address},
			)
			if tt.wantOK {
				if code != 0 || !called {
					t.Fatalf("exit=%d called=%v stderr=%q", code, called, stderr.String())
				}
				return
			}
			if code != 2 || called {
				t.Fatalf("exit=%d called=%v stderr=%q", code, called, stderr.String())
			}
			if !strings.Contains(stderr.String(), "authenticated remote serving is not implemented") {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
	}
}

func TestServeRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	serve := func(_ context.Context, _ web.Options) error { called = true; return nil }
	code := New(&stdout, &stderr, version.Info{}, serve).Run(
		context.Background(), []string{"serve", "extra"},
	)
	if code != 2 || called {
		t.Fatalf("exit=%d called=%v stderr=%q", code, called, stderr.String())
	}
}

func TestServeReportsServerFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	serve := func(_ context.Context, _ web.Options) error { return errors.New("boom") }
	code := New(&stdout, &stderr, version.Info{}, serve).Run(context.Background(), []string{"serve"})
	if code != 1 || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}
