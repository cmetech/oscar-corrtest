//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessInstalledPlanBypassesEnvironmentAndFilesystemBootstrap(t *testing.T) {
	root := t.TempDir()
	kit := filepath.Join(root, "kit")
	original := filepath.Join(root, "original")
	for _, dir := range []string{kit, original, filepath.Join(original, "kit")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	copyDir := func(from, to string) {
		entries, err := os.ReadDir(from)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			b, err := os.ReadFile(filepath.Join(from, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(to, entry.Name()), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	copyDir("../../internal/testpack/testdata/kit", kit)
	copyDir("../../internal/testpack/testdata/kit", filepath.Join(original, "kit"))
	copyDir("../../internal/testpack/testdata/installation/records", original)
	b, err := os.ReadFile(filepath.Join(original, "completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var completed map[string]json.RawMessage
	if err = json.Unmarshal(b, &completed); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(root, "owner.json")
	if err = os.WriteFile(owner, completed["ownership_declaration"], 0600); err != nil {
		t.Fatal(err)
	}
	pin, err := os.ReadFile(filepath.Join(kit, "manifest.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"test-pack", "plan", "--manifest", filepath.Join(kit, "manifest.json"), "--manifest-sha256", strings.TrimSpace(string(pin)), "--installation", original, "--ownership", owner, "--api-base", "https://owned-target.test", "--target", "api", "--receiver-target", "receiver", "--config", "/dev/null/unusable", "--data-dir", "/dev/null/unusable", "--output", "json"}
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid", false: "invalid"}[valid], func(t *testing.T) {
			input := args
			if !valid {
				input = []string{"test-pack", "plan", "--manifest", "/missing/manifest.json"}
			}
			var stdout, stderr bytes.Buffer
			lookup := func(name string) (string, bool) {
				t.Fatalf("offline plan consulted process bootstrap environment %q", name)
				return "", false
			}
			code := run(input, &stdout, &stderr, lookup)
			if valid {
				if code != 0 || !strings.Contains(stdout.String(), "audit-only") {
					t.Fatalf("%d %s %s", code, stdout.String(), stderr.String())
				}
			} else if code != 2 {
				t.Fatalf("invalid plan exit%d", code)
			}
		})
	}
}
