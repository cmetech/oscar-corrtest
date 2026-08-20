package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func (d *Database) CreateResource(ctx context.Context, resource domain.Resource) error {
	if err := resource.Validate(); err != nil {
		return err
	}
	var existing int
	if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM resources WHERE external_name=? AND deleted_at IS NULL`, resource.ExternalName).Scan(&existing); err != nil {
		return fmt.Errorf("check resource name ownership: %w", err)
	}
	if existing != 0 {
		return fmt.Errorf("active resource name %q already exists in the ledger", resource.ExternalName)
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO resources(id,run_id,kind,external_id,external_name,ownership_token,lifecycle_state,created_at,deleted_at,cleanup_error)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, resource.ID, resource.RunID, resource.Kind, nullable(resource.ExternalID), resource.ExternalName,
		resource.OwnershipToken, string(resource.LifecycleState), nullableTime(resource.CreatedAt), nullableTime(resource.DeletedAt), nullable(resource.CleanupError))
	if err != nil {
		return fmt.Errorf("create resource: %w", err)
	}
	return nil
}

func (d *Database) AdoptResource(ctx context.Context, id, externalID string, at time.Time) error {
	if externalID == "" || at.IsZero() {
		return fmt.Errorf("external creation evidence is incomplete")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE resources SET external_id=?, lifecycle_state='CREATED', created_at=?, cleanup_error=NULL
		WHERE id=? AND lifecycle_state='PROPOSED' AND external_id IS NULL`, externalID, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("adopt resource: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("resource is not eligible for adoption")
	}
	return nil
}

func (d *Database) MarkResourceDeleted(ctx context.Context, id string, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("resource deletion time is required")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE resources SET lifecycle_state='DELETED', deleted_at=?, cleanup_error=NULL
		WHERE id=? AND lifecycle_state IN ('PROPOSED','CREATED','UNKNOWN')`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("mark resource deleted: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("resource is not eligible for deletion evidence")
	}
	return nil
}

func (d *Database) MarkResourceCleanupError(ctx context.Context, id, message string) error {
	if message == "" {
		return fmt.Errorf("cleanup error is required")
	}
	_, err := d.db.ExecContext(ctx, `UPDATE resources SET lifecycle_state='UNKNOWN', cleanup_error=? WHERE id=? AND deleted_at IS NULL`, message, id)
	return err
}

func (d *Database) ListResources(ctx context.Context, runID string) ([]domain.Resource, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id,run_id,kind,external_id,external_name,ownership_token,lifecycle_state,created_at,deleted_at,cleanup_error
		FROM resources WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer rows.Close()
	var result []domain.Resource
	for rows.Next() {
		var resource domain.Resource
		var externalID, created, deleted, cleanup sql.NullString
		if err := rows.Scan(&resource.ID, &resource.RunID, &resource.Kind, &externalID, &resource.ExternalName, &resource.OwnershipToken,
			&resource.LifecycleState, &created, &deleted, &cleanup); err != nil {
			return nil, fmt.Errorf("scan resource: %w", err)
		}
		resource.ExternalID, resource.CleanupError = externalID.String, cleanup.String
		if created.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, created.String)
			if parseErr != nil {
				return nil, parseErr
			}
			resource.CreatedAt = &value
		}
		if deleted.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, deleted.String)
			if parseErr != nil {
				return nil, parseErr
			}
			resource.DeletedAt = &value
		}
		result = append(result, resource)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return result, nil
}
