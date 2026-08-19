package artifact

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRunID = "crt_00000000000000000000000000"

func TestWritePublishesHashedArtifactWithRestrictivePermissions(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.Write(context.Background(), testRunID, "evidence/001-request.json", "application/json", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RelativePath != "runs/"+testRunID+"/evidence/001-request.json" || manifest.ByteSize != 11 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if manifest.SHA256 != "4062edaf750fb8074e7e83e0c9028c94e32468a8b6f1614774328ef045150f93" {
		t.Fatalf("sha256=%q", manifest.SHA256)
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(manifest.RelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	result, err := store.Verify(context.Background(), manifest)
	if err != nil || result != IntegrityValid {
		t.Fatalf("Verify()=%s, %v", result, err)
	}
}

func TestWriteRejectsUnsafePathsAndOverwrite(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unsafe := []string{"", "/absolute", "../escape", "evidence/../escape", "evidence//x", `evidence\\x`, "."}
	for _, path := range unsafe {
		if _, err := store.Write(context.Background(), testRunID, path, "text/plain", strings.NewReader("x")); err == nil {
			t.Errorf("path %q accepted", path)
		}
	}
	if _, err := store.Write(context.Background(), "not-a-run", "report.json", "application/json", strings.NewReader("{}")); err == nil {
		t.Fatal("invalid run ID accepted")
	}
	if _, err := store.Write(context.Background(), testRunID, "report.json", "application/json", strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), testRunID, "report.json", "application/json", strings.NewReader("second")); err == nil {
		t.Fatal("overwrite accepted")
	}
}

func TestWriteRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "runs", testRunID)); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), testRunID, "report.json", "application/json", strings.NewReader("{}")); err == nil {
		t.Fatal("symlink escape accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "report.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside write err=%v", err)
	}
}

func TestVerifyReportsMissingAndHashMismatch(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.Write(context.Background(), testRunID, "report.json", "application/json", strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(manifest.RelativePath))
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Verify(context.Background(), manifest); err != nil || got != IntegrityHashMismatch {
		t.Fatalf("changed Verify()=%s, %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Verify(context.Background(), manifest); err != nil || got != IntegrityMissing {
		t.Fatalf("missing Verify()=%s, %v", got, err)
	}
}

func TestWriteFailureLeavesNoTemporaryArtifact(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), testRunID, "evidence/failure.json", "application/json", failingReader{}); err == nil {
		t.Fatal("reader failure accepted")
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	})
	if len(files) != 0 {
		t.Fatalf("leftover files=%v", files)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
