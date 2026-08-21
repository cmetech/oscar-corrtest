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

func TestScenarioAuthoringGuideDocumentsValidationBudgets(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "docs/scenario-authoring.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	for _, required := range []string{
		"Up to 16 unique safe label names, each 1–100 characters.",
		"Up to 64 safe, non-reserved source labels; keys are 1–100 characters and values are at most 500 characters.",
		"Label keys are 1–100 characters and values are at most 500 characters.",
		"Up to 16 unique, nonblank names; each is at most 100 characters; `parent_child` only; disjoint from `tagForNotifiers`.",
		"Up to 16 unique, nonblank names; each is at most 100 characters; `parent_child` only; disjoint from `suppressForNotifiers`.",
		"Required, nonblank, and at most 100 characters for `audit-count` and `parent-link-count`; forbidden for `synthetic-alert-count`.",
	} {
		if !strings.Contains(guide, required) {
			t.Errorf("scenario authoring guide missing validation budget %q", required)
		}
	}
}

func TestBuiltinCatalogLinksEveryPatternToAuthoringTutorial(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "docs/builtins.md"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := string(data)
	for _, pattern := range []string{
		"flood", "co_occurrence", "sequence", "persistence",
		"absence", "parent_child", "cross_source", "threshold",
	} {
		if !strings.Contains(catalog, "| `"+pattern+"` ") {
			t.Errorf("built-in catalog missing pattern %q", pattern)
		}
		if !strings.Contains(catalog, "](/authoring?section=patterns&pattern="+pattern+"#pattern-"+pattern+")") {
			t.Errorf("built-in catalog missing Authoring tutorial link for %q", pattern)
		}
	}
}
