package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{Version: "v1.2.3", Commit: "abc", BuildDate: "now"})
	if code := app.Run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != "oscar-corrtest v1.2.3 commit=abc built=now\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{})
	if code := app.Run(context.Background(), []string{"bogus"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
