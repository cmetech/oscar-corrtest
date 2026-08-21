package scripts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
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
	for _, name := range []string{"scenario-authoring.md", "builtins.md", "operator.md", "schema/correlation-scenario.schema.json", "service-management.md"} {
		if !strings.Contains(string(packageScript), name) {
			t.Errorf("package script missing %s", name)
		}
		if !strings.Contains(string(checker), name) {
			t.Errorf("package checker missing %s", name)
		}
	}
}

func TestPackageArchivesIncludeAuthoringGuidesAndGeneratedSchema(t *testing.T) {
	root := filepath.Clean("..")
	temporary := t.TempDir()
	binary := filepath.Join(temporary, "oscar-corrtest")
	if err := os.WriteFile(binary, []byte("contract binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantSchema, err := scenario.GenerateJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	const version = "package-contract"
	required := []string{
		"oscar-corrtest/docs/scenario-authoring.md",
		"oscar-corrtest/docs/builtins.md",
		"oscar-corrtest/docs/operator.md",
		"oscar-corrtest/docs/schema/correlation-scenario.schema.json",
	}
	for _, platform := range []struct {
		os, arch, extension string
	}{
		{"linux", "amd64", "tar.gz"},
		{"linux", "arm64", "tar.gz"},
		{"darwin", "amd64", "tar.gz"},
		{"darwin", "arm64", "tar.gz"},
		{"windows", "amd64", "zip"},
	} {
		archive := filepath.Join(root, "dist", "oscar-corrtest_"+version+"_"+platform.os+"_"+platform.arch+"."+platform.extension)
		if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(archive) })
		command := exec.Command("sh", "package.sh", version, platform.os, platform.arch, binary, "0")
		command.Dir = "."
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("package %s/%s: %v\n%s", platform.os, platform.arch, err, output)
		}
		members, err := archiveMembers(archive, platform.extension)
		if err != nil {
			t.Fatalf("inspect %s/%s archive: %v", platform.os, platform.arch, err)
		}
		for _, name := range required {
			if _, ok := members[name]; !ok {
				t.Errorf("%s/%s archive missing %s", platform.os, platform.arch, name)
			}
		}
		if got := members["oscar-corrtest/docs/schema/correlation-scenario.schema.json"]; !bytes.Equal(got, wantSchema) {
			t.Errorf("%s/%s packaged schema differs from scenario.GenerateJSONSchema()", platform.os, platform.arch)
		}
	}
}

func archiveMembers(path, extension string) (map[string][]byte, error) {
	if extension == "zip" {
		archive, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer archive.Close()
		members := make(map[string][]byte, len(archive.File))
		for _, file := range archive.File {
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			data, err := readArchiveMember(reader)
			_ = reader.Close()
			if err != nil {
				return nil, err
			}
			members[file.Name] = data
		}
		return members, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	members := map[string][]byte{}
	for {
		header, err := archive.Next()
		if err != nil {
			if err == io.EOF {
				return members, nil
			}
			return nil, err
		}
		data, err := readArchiveMember(archive)
		if err != nil {
			return nil, err
		}
		members[header.Name] = data
	}
}

func readArchiveMember(reader io.Reader) ([]byte, error) {
	return io.ReadAll(reader)
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
