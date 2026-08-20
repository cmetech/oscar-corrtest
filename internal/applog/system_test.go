package applog

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSystemRedactsBeforeEverySink(t *testing.T) {
	var stderr bytes.Buffer
	logs, err := Open(t.TempDir(), &stderr, Options{MaxBytes: 1 << 20, Backups: 2, RingSize: 10, SubscriberBuffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close() })
	subscription := logs.Subscribe()
	defer subscription.Cancel()
	logger := logs.Logger("web")
	logger.Info("request completed",
		"api_key", "secret-api-sentinel",
		"authorization", "secret-auth-sentinel",
		"nested", slog.GroupValue(slog.String("csrf_token", "secret-csrf-sentinel")),
		"route", "/operations",
	)
	records := logs.Recent(10)
	if len(records) != 1 || records[0].Source != "web" || records[0].Attributes["route"] != "/operations" {
		t.Fatalf("records=%+v", records)
	}
	select {
	case <-subscription.C:
	case <-time.After(time.Second):
		t.Fatal("subscriber received no record")
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(filepath.Join(logs.logDir, "application.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(disk) + stderr.String()
	for _, secret := range []string{"secret-api-sentinel", "secret-auth-sentinel", "secret-csrf-sentinel"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	if !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %s", combined)
	}
}

func TestSystemRingAndSlowSubscriberAreBounded(t *testing.T) {
	logs, err := Open(t.TempDir(), nil, Options{MaxBytes: 1 << 20, Backups: 1, RingSize: 3, SubscriberBuffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	_ = logs.Subscribe() // deliberately never read
	for index := 0; index < 10; index++ {
		logs.Logger("runtime").Info("transition", "index", index)
	}
	records := logs.Recent(50)
	if len(records) != 3 || records[0].Sequence >= records[2].Sequence {
		t.Fatalf("records=%+v", records)
	}
}

func TestSystemAllowlistsLogSources(t *testing.T) {
	dir := t.TempDir()
	logs, err := Open(dir, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	logs.Logger("main").Info("started")
	if _, err := logs.OpenSource("../.env"); err == nil {
		t.Fatal("traversal source accepted")
	}
	sources := logs.Sources()
	if len(sources) == 0 || sources[0].Name != "application.jsonl" {
		t.Fatalf("sources=%+v", sources)
	}
	file, err := logs.OpenSource("application.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
}

func TestStderrOnlySystemRemainsUsable(t *testing.T) {
	var output bytes.Buffer
	logs := StderrOnly(&output)
	logs.Logger("main").ErrorContext(context.Background(), "bootstrap failure", "error", "safe")
	if !strings.Contains(output.String(), "bootstrap failure") {
		t.Fatalf("output=%q", output.String())
	}
}
