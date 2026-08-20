package releasearchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWriteZipIsDeterministicAndPreservesExecutableMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(filepath.Join(root, "oscar-corrtest", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "oscar-corrtest", "README.md"), []byte("read me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "oscar-corrtest", "bin", "oscar-corrtest.exe"), []byte("binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(1_787_180_400, 0).UTC()
	first := filepath.Join(t.TempDir(), "first.zip")
	second := filepath.Join(t.TempDir(), "second.zip")
	if err := WriteZip(first, root, epoch); err != nil {
		t.Fatal(err)
	}
	if err := WriteZip(second, root, epoch); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(firstBytes) != sha256.Sum256(secondBytes) || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("archives are not byte-identical")
	}
	archiveInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if archiveInfo.Mode().Perm() != 0o644 {
		t.Fatalf("archive mode=%#o", archiveInfo.Mode().Perm())
	}

	names, err := ListZip(first)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"oscar-corrtest/", "oscar-corrtest/README.md", "oscar-corrtest/bin/", "oscar-corrtest/bin/oscar-corrtest.exe"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names=%v want=%v", names, wantNames)
	}
	reader, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if !file.Modified.Equal(epoch) {
			t.Fatalf("%s modified=%s want=%s", file.Name, file.Modified, epoch)
		}
		if file.Name == "oscar-corrtest/bin/oscar-corrtest.exe" && file.Mode().Perm() != 0o755 {
			t.Fatalf("executable mode=%#o", file.Mode().Perm())
		}
	}
}

func TestWriteZipRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := WriteZip(filepath.Join(t.TempDir(), "archive.zip"), root, time.Unix(0, 0).UTC())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
}
