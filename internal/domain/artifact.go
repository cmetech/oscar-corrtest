package domain

import (
	"fmt"
	"time"
)

// ArtifactAvailability records whether filesystem publication completed.
type ArtifactAvailability string

const (
	ArtifactPending   ArtifactAvailability = "PENDING"
	ArtifactAvailable ArtifactAvailability = "AVAILABLE"
)

// Artifact is the durable database manifest for one run-scoped file.
type Artifact struct {
	ID             string               `json:"id"`
	RunID          string               `json:"runId"`
	CaseID         string               `json:"caseId,omitempty"`
	Kind           string               `json:"kind"`
	RelativePath   string               `json:"relativePath"`
	MIMEType       string               `json:"mimeType"`
	SHA256         string               `json:"sha256,omitempty"`
	ByteSize       int64                `json:"byteSize"`
	RedactionState string               `json:"redactionState"`
	Availability   ArtifactAvailability `json:"availability"`
	CreatedAt      time.Time            `json:"createdAt"`
}

// ArtifactIntegrity is the current relationship between a manifest and its file.
type ArtifactIntegrity string

const (
	ArtifactIntegrityPending      ArtifactIntegrity = "pending"
	ArtifactIntegrityValid        ArtifactIntegrity = "valid"
	ArtifactIntegrityMissing      ArtifactIntegrity = "missing"
	ArtifactIntegrityHashMismatch ArtifactIntegrity = "hash_mismatch"
	ArtifactIntegrityError        ArtifactIntegrity = "error"
)

// ArtifactEvidence keeps damaged or incomplete artifacts visible to operators.
type ArtifactEvidence struct {
	Artifact  Artifact          `json:"artifact"`
	Integrity ArtifactIntegrity `json:"integrity"`
	Error     string            `json:"error,omitempty"`
}

// Validate verifies database-manifest invariants.
func (artifact Artifact) Validate() error {
	if artifact.ID == "" || artifact.RunID == "" || artifact.Kind == "" || artifact.RelativePath == "" || artifact.MIMEType == "" || artifact.RedactionState == "" || artifact.CreatedAt.IsZero() {
		return fmt.Errorf("artifact metadata is incomplete")
	}
	switch artifact.Availability {
	case ArtifactPending:
		if artifact.SHA256 != "" || artifact.ByteSize != 0 {
			return fmt.Errorf("pending artifact cannot have final hash metadata")
		}
	case ArtifactAvailable:
		if artifact.SHA256 == "" || artifact.ByteSize < 0 {
			return fmt.Errorf("available artifact requires hash metadata")
		}
	default:
		return fmt.Errorf("artifact availability %q is invalid", artifact.Availability)
	}
	return nil
}
