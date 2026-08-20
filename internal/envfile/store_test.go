package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStorePrecedenceAndLiveOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# operator note\nOSCAR_API_KEY=managed\nOTHER=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, func(key string) (string, bool) {
		if key == "OSCAR_API_KEY" {
			return "external", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Getenv("OSCAR_API_KEY"); got != "external" {
		t.Fatalf("Getenv=%q", got)
	}
	if status := store.Status("OSCAR_API_KEY"); status.Source != SourceExternal || !status.Configured {
		t.Fatalf("status=%+v", status)
	}
	if err := store.Replace("OSCAR_API_KEY", "replacement"); err != nil {
		t.Fatal(err)
	}
	if got := store.Getenv("OSCAR_API_KEY"); got != "replacement" {
		t.Fatalf("Getenv after replace=%q", got)
	}
	status := store.Status("OSCAR_API_KEY")
	if status.Source != SourceLiveOverride || !status.ExternalResumesOnRestart {
		t.Fatalf("status after replace=%+v", status)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "# operator note\n") || !strings.Contains(text, "OTHER=value\n") || strings.Count(text, "OSCAR_API_KEY=") != 1 || !strings.Contains(text, "OSCAR_API_KEY=replacement") {
		t.Fatalf("unexpected file %q", text)
	}
}

func TestStoreParsesQuotesExportDuplicatesAndCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	doc := "export FIRST='one two'\r\nSECOND=\"two\\nlines\"\r\nFIRST=last\r\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.Getenv("FIRST") != "last" || store.Getenv("SECOND") != "two\nlines" {
		t.Fatalf("parsed FIRST=%q SECOND=%q", store.Getenv("FIRST"), store.Getenv("SECOND"))
	}
	if err := store.Replace("FIRST", "new value"); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(path)
	if !strings.Contains(string(contents), "\r\n") || strings.Count(string(contents), "FIRST=") != 1 {
		t.Fatalf("replacement did not preserve CRLF/collapse duplicates: %q", contents)
	}
}

func TestStoreClearMasksExternalUntilRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	store, err := Open(path, func(key string) (string, bool) { return "external", key == "OSCAR_API_KEY" })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Clear("OSCAR_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if store.Getenv("OSCAR_API_KEY") != "" {
		t.Fatal("external value was not masked")
	}
	status := store.Status("OSCAR_API_KEY")
	if status.Configured || !status.ExternalResumesOnRestart {
		t.Fatalf("status=%+v", status)
	}
}

func TestStoreRejectsUnsafeInputAndOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, nil); err == nil {
		t.Fatal("Open oversized error=nil")
	}
	path = filepath.Join(t.TempDir(), ".env")
	store, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{{"bad-key", "x"}, {"OK", "line\nbreak"}, {"OK", strings.Repeat("x", maxValueBytes+1)}} {
		if err := store.Replace(tc.key, tc.value); err == nil {
			t.Fatalf("Replace(%q) error=nil", tc.key)
		}
	}
}

func TestStoreConcurrentReadersDuringReplacement(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), ".env"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = store.Getenv("OSCAR_API_KEY")
			}
		}()
	}
	if err := store.Replace("OSCAR_API_KEY", "value"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}
