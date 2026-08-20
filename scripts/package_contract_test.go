package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestPackageAndCheckerIncludeOperatorExperienceGuides(t *testing.T) {
	packageScript, err := os.ReadFile("package.sh")
	if err != nil {
		t.Fatal(err)
	}
	checker, err := os.ReadFile("check-package.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scenario-authoring.md", "service-management.md"} {
		if !strings.Contains(string(packageScript), name) {
			t.Errorf("package script missing %s", name)
		}
		if !strings.Contains(string(checker), name) {
			t.Errorf("package checker missing %s", name)
		}
	}
}

func TestOperatorExperienceGateSelectsExactNonemptyPackages(t *testing.T) {
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(makefile)
	if !strings.Contains(text, "operator-experience-gate:") {
		t.Fatal("operator-experience-gate target missing")
	}
	for _, packageName := range []string{"./internal/platformpaths", "./internal/envfile", "./internal/service", "./internal/applog", "./internal/operations", "./internal/scenario", "./internal/runtime", "./internal/web", "./internal/command", "./scripts", "./internal/docs"} {
		if !strings.Contains(text, packageName) {
			t.Errorf("operator gate missing %s", packageName)
		}
	}
}
