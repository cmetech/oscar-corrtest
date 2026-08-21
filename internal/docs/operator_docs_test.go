package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorDocsContainPlatformAndLifecycleContracts(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	files := []string{"README.md", "docs/install.md", "docs/operator.md", "docs/scenario-authoring.md", "docs/service-management.md"}
	combined := ""
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		combined += "\n" + string(data)
	}
	for _, required := range []string{
		"$HOME/.config/oscar-corrtest/.env",
		"%LOCALAPPDATA%\\oscar-corrtest\\.env",
		"OSCAR_API_KEY",
		"service install", "service start", "service stop", "service restart", "service status", "service logs", "service uninstall",
		"0.0.0.0:8787", "unauthenticated", "does not start",
		"Clone as custom", "P01", "N01", "oscar_test_run_id", "category=corrtest_", "CORRTEST_<PATTERN_CODE>",
		"Operations", "application.jsonl",
		"/authoring", "basic", "advanced", "P01", "N01",
		"two temporary correlation rules", "does not create ordinary OSCAR alert rules",
		"go run ./cmd/generate-scenario-schema",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("operator documentation missing %q", required)
		}
	}
}
