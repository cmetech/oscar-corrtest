package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type normalizedEvidenceWriter struct {
	database  artifactRepository
	artifacts *artifact.Store
}

type artifactRepository interface {
	CreateArtifactPending(context.Context, domain.Artifact) error
	MarkArtifactAvailable(context.Context, string, string, int64) error
}

func (r *Runtime) evidenceWriter() *normalizedEvidenceWriter {
	return &normalizedEvidenceWriter{database: r.database, artifacts: r.artifacts}
}

func (w *normalizedEvidenceWriter) WriteEvidence(ctx context.Context, runID string, facts domain.ExecutionFacts, at time.Time) (domain.Artifact, error) {
	if err := facts.Validate(); err != nil {
		return domain.Artifact{}, err
	}
	content, err := json.Marshal(struct {
		APIVersion string                `json:"apiVersion"`
		RunID      string                `json:"runId"`
		Facts      domain.ExecutionFacts `json:"facts"`
	}{APIVersion: "corrtest.oscar/v1alpha1", RunID: runID, Facts: facts})
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("marshal normalized evidence: %w", err)
	}
	record := domain.Artifact{
		ID: "artifact:" + runID + ":normalized", RunID: runID, Kind: "normalized-oscar-evidence",
		RelativePath: "runs/" + runID + "/evidence/normalized.json", MIMEType: "application/json",
		RedactionState: "credential-free", Availability: domain.ArtifactPending, CreatedAt: at,
	}
	if err := w.database.CreateArtifactPending(ctx, record); err != nil {
		return domain.Artifact{}, err
	}
	manifest, err := w.artifacts.Write(ctx, runID, "evidence/normalized.json", record.MIMEType, bytes.NewReader(content))
	if err != nil {
		return domain.Artifact{}, err
	}
	if err := w.database.MarkArtifactAvailable(ctx, record.ID, manifest.SHA256, manifest.ByteSize); err != nil {
		return domain.Artifact{}, err
	}
	record.RelativePath, record.SHA256, record.ByteSize, record.Availability = manifest.RelativePath, manifest.SHA256, manifest.ByteSize, domain.ArtifactAvailable
	return record, nil
}
