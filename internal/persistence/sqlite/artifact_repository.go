package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// CreateArtifactPending records intent before filesystem publication.
func (d *Database) CreateArtifactPending(ctx context.Context, artifact domain.Artifact) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	if artifact.Availability != domain.ArtifactPending {
		return fmt.Errorf("new artifact must be pending")
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO artifacts(
        id, run_id, case_id, kind, relative_path, mime_type, sha256, byte_size,
        redaction_state, availability_state, created_at
    ) VALUES(?, ?, ?, ?, ?, ?, NULL, NULL, ?, 'PENDING', ?)`, artifact.ID, artifact.RunID,
		nullable(artifact.CaseID), artifact.Kind, artifact.RelativePath, artifact.MIMEType,
		artifact.RedactionState, formatTime(artifact.CreatedAt))
	if err != nil {
		return fmt.Errorf("create pending artifact: %w", err)
	}
	return nil
}

// MarkArtifactAvailable records the immutable hash after atomic publication.
func (d *Database) MarkArtifactAvailable(ctx context.Context, id, digest string, size int64) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	if id == "" || digest == "" || size < 0 {
		return fmt.Errorf("available artifact metadata is invalid")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE artifacts SET sha256=?, byte_size=?, availability_state='AVAILABLE'
        WHERE id=? AND availability_state='PENDING'`, digest, size, id)
	if err != nil {
		return fmt.Errorf("mark artifact available: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read artifact update result: %w", err)
	}
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

// ListArtifacts returns all durable manifests for one run.
func (d *Database) ListArtifacts(ctx context.Context, runID string) ([]domain.Artifact, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, run_id, case_id, kind, relative_path, mime_type,
        sha256, byte_size, redaction_state, availability_state, created_at
        FROM artifacts WHERE run_id=? ORDER BY relative_path, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()
	var artifacts []domain.Artifact
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, rows.Err()
}

// SaveCanonicalReport validates and stores the authoritative report JSON.
func (d *Database) SaveCanonicalReport(ctx context.Context, runID string, reportJSON []byte) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	if !json.Valid(reportJSON) {
		return fmt.Errorf("canonical report is not valid JSON")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE runs SET canonical_report_json=?, updated_at=updated_at WHERE id=?`, string(reportJSON), runID)
	if err != nil {
		return fmt.Errorf("save canonical report: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

func scanArtifact(row rowScanner) (domain.Artifact, error) {
	var artifact domain.Artifact
	var caseID, digest sql.NullString
	var size sql.NullInt64
	var created string
	if err := row.Scan(&artifact.ID, &artifact.RunID, &caseID, &artifact.Kind, &artifact.RelativePath,
		&artifact.MIMEType, &digest, &size, &artifact.RedactionState, &artifact.Availability, &created); err != nil {
		return domain.Artifact{}, err
	}
	artifact.CaseID, artifact.SHA256 = caseID.String, digest.String
	if size.Valid {
		artifact.ByteSize = size.Int64
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return domain.Artifact{}, err
	}
	artifact.CreatedAt = parsed
	return artifact, nil
}
