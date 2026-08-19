package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestArtifactManifestAndCanonicalReportSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrtest.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	artifact := domain.Artifact{
		ID: "art_one", RunID: run.ID, Kind: "report-json", RelativePath: "runs/" + run.ID + "/report.json",
		MIMEType: "application/json", RedactionState: "redacted", Availability: domain.ArtifactPending, CreatedAt: run.CreatedAt,
	}
	if err := database.CreateArtifactPending(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkArtifactAvailable(context.Background(), artifact.ID, "abc123", 42); err != nil {
		t.Fatal(err)
	}
	reportJSON := []byte(`{"apiVersion":"corrtest.oscar/v1alpha1","run":{"id":"` + run.ID + `"}}`)
	if err := database.SaveCanonicalReport(context.Background(), run.ID, reportJSON); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	artifacts, err := database.ListArtifacts(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Availability != domain.ArtifactAvailable || artifacts[0].SHA256 != "abc123" || artifacts[0].ByteSize != 42 {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	got, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.CanonicalReportJSON) != string(reportJSON) {
		t.Fatalf("report=%s", got.CanonicalReportJSON)
	}
}

func TestSaveCanonicalReportRejectsInvalidJSON(t *testing.T) {
	database := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveCanonicalReport(context.Background(), run.ID, []byte(`{`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
