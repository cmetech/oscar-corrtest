package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterRetainsFiveBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.jsonl")
	writer, err := newRotatingWriter(path, 64, 5)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		line := fmt.Sprintf(`{"index":%02d,"padding":"xxxxxxxxxxxxxxxx"}`+"\n", index)
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= 5; index++ {
		candidate := path
		if index > 0 {
			candidate += fmt.Sprintf(".%d", index)
		}
		data, err := os.ReadFile(candidate)
		if err != nil {
			t.Fatalf("read %s: %v", candidate, err)
		}
		if !strings.HasSuffix(string(data), "\n") {
			t.Fatalf("partial line in %s: %q", candidate, data)
		}
	}
	if _, err := os.Stat(path + ".6"); !os.IsNotExist(err) {
		t.Fatalf("unexpected sixth backup: %v", err)
	}
}

func TestRenameReplacingPreservesDestinationWhenSourceIsAbsent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "missing")
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameReplacing(source, destination); !os.IsNotExist(err) {
		t.Fatalf("error=%v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "existing" {
		t.Fatalf("destination=%q err=%v", data, err)
	}
}

func TestRenameReplacingOverwritesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameReplacing(source, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "new" {
		t.Fatalf("destination=%q err=%v", data, err)
	}
}
