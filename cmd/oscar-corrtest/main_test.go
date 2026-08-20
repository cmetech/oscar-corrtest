package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestProcessHelpBypassesEnvironmentAndFilesystemInitialization(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lookupCalled := false
	lookup := func(string) (string, bool) {
		lookupCalled = true
		return "", false
	}
	if code := run([]string{"service", "start", "--help"}, &stdout, &stderr, lookup); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if lookupCalled {
		t.Fatal("help resolved platform paths or opened the managed environment")
	}
	if !strings.Contains(stdout.String(), "Usage:\n  oscar-corrtest service start") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
