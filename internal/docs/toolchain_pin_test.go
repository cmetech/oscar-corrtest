package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The builder images (CI and Containerfile) must match go.mod's toolchain:
// CI runs with GOTOOLCHAIN=local, so an older image cannot build at all.
func TestBuilderImagesMatchToolchain(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no toolchain line")
	}
	want := "golang:" + string(m[1]) + "-bookworm@sha256:"
	for _, f := range []string{"../../.gitlab-ci.yml", "../../Containerfile"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not pin %s<digest>", f, want)
		}
	}
}
