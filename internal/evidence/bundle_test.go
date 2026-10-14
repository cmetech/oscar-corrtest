package evidence

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestWriteAndVerifyBundle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	destination := filepath.Join(dir, "run.zip")
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	run := domain.Run{
		ID: "crt_01K00000000000000000000000", ShortToken: "01K00000", Status: domain.RunCompleted,
		Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean, HarnessVersion: "v1.0.0",
		CompiledPlanJSON:    json.RawMessage(`{"pattern":"flood"}`),
		CanonicalReportJSON: json.RawMessage(`{"apiVersion":"corrtest.oscar/v1alpha1","verdict":"PASS"}`),
		CreatedAt:           now, UpdatedAt: now,
	}
	events := []domain.RunEvent{{RunID: run.ID, Sequence: 1, Type: "run.transition", Level: "info", OccurredAt: now, Summary: "done"}}

	result, err := Write(context.Background(), destination, run, events)
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA256 == "" || result.ByteSize == 0 {
		t.Fatalf("invalid result: %+v", result)
	}
	if err := Verify(context.Background(), destination); err != nil {
		t.Fatalf("verify: %v", err)
	}
	reader, err := zip.OpenReader(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	want := map[string]bool{"report.json": false, "plan.json": false, "events.json": false, "report.html": false, "junit.xml": false, "manifest.json": false}
	for _, file := range reader.File {
		if _, ok := want[file.Name]; ok {
			want[file.Name] = true
		}
		if strings.Contains(file.Name, "..") || strings.HasPrefix(file.Name, "/") {
			t.Fatalf("unsafe archive name %q", file.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing %s", name)
		}
	}

	if _, err := Write(context.Background(), destination, run, events); err == nil {
		t.Fatal("expected overwrite refusal")
	}
}

func TestWriteWithArtifactsIncludesOnlyHashVerifiedEvidence(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	run := domain.Run{ID: "crt_01K00000000000000000000000", ShortToken: "01K00000", Status: domain.RunCompleted,
		Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean, CompiledPlanJSON: json.RawMessage(`{"pattern":"flood"}`),
		CanonicalReportJSON: json.RawMessage(`{"verdict":"PASS"}`), CreatedAt: now, UpdatedAt: now}
	content := []byte(`{"sourceHistory":[{"fingerprint":"server-fp"}]}`)
	digest := sha256.Sum256(content)
	attachment := Attachment{Name: "normalized-evidence.json", Content: content, SHA256: hex.EncodeToString(digest[:]), ByteSize: int64(len(content))}
	destination := filepath.Join(dir, "with-artifact.zip")
	if _, err := WriteWithArtifacts(context.Background(), destination, run, nil, []Attachment{attachment}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	attachment.Content = append([]byte(nil), content...)
	attachment.Content[0] = '['
	if _, err := WriteWithArtifacts(context.Background(), filepath.Join(dir, "tampered-input.zip"), run, nil, []Attachment{attachment}); err == nil {
		t.Fatal("artifact content that differed from its durable manifest was exported")
	}
}

func TestVerifyDetectsTamperedBundle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tampered.zip")
	if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), path); err == nil {
		t.Fatal("expected invalid bundle error")
	}
}

func TestRecordedRequestExportRefusesOversizedTimelineBeforePublication(t *testing.T) {
	now := time.Now().UTC()
	run := domain.Run{ID: "crt_01K00000000000000000000000", Status: domain.RunCompleted, Verdict: domain.VerdictPass, CleanupStatus: domain.CleanupClean, CompiledPlanJSON: json.RawMessage(`{}`), CanonicalReportJSON: json.RawMessage(`{}`), UpdatedAt: now}
	labels := map[string]string{"alertname": "A"}
	for i := 0; i < 63; i++ {
		labels[fmt.Sprintf("label_%030d", i)] = strings.Repeat("x", 500)
	}
	request, _, err := oscar.PrepareInjection(compiler.AlertPlan{Name: "A", Status: "firing", Labels: labels}, "test", "https://model.invalid/api/v1/alerts")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := json.Marshal(map[string]any{"request": request})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]domain.RunEvent, 200)
	for i := range events {
		events[i] = domain.RunEvent{RunID: run.ID, Sequence: int64(i + 1), Type: "alert.attempting", OccurredAt: now, DetailJSON: string(detail)}
	}
	dir := t.TempDir()
	existing := filepath.Join(dir, "valid.zip")
	if _, err := Write(context.Background(), existing, run, nil); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "oversized.zip")
	if _, err := Write(context.Background(), destination, run, events); err == nil || !strings.Contains(err.Error(), "events.json") {
		t.Fatalf("oversized timeline export error=%v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("invalid export was published: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "valid.zip" {
		t.Fatalf("partial export left behind: %v", entries)
	}
	current, err := os.ReadFile(existing)
	if err != nil || string(original) != string(current) {
		t.Fatal("prior valid export changed")
	}
	if err := Verify(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
}
