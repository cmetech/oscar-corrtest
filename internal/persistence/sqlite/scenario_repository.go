package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// ErrScenarioInUse is returned when historical evidence still references a scenario.
var ErrScenarioInUse = errors.New("scenario is referenced by historical runs")

func (d *Database) CreateScenario(ctx context.Context, item domain.ScenarioRecord) error {
	if item.ID == "" || item.Name == "" || item.APIVersion == "" || item.SourceDocument == "" || item.SHA256 == "" || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		return fmt.Errorf("scenario metadata is incomplete")
	}
	builtIn := 0
	if item.BuiltIn {
		builtIn = 1
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO scenarios(id,name,api_version,source_document,sha256,built_in,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		item.ID, item.Name, item.APIVersion, item.SourceDocument, item.SHA256, builtIn, formatTime(item.CreatedAt), formatTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("create scenario: %w", err)
	}
	return nil
}

func (d *Database) GetScenario(ctx context.Context, id string) (domain.ScenarioRecord, error) {
	var item domain.ScenarioRecord
	var builtIn int
	var created, updated string
	err := d.db.QueryRowContext(ctx, `SELECT id,name,api_version,source_document,sha256,built_in,created_at,updated_at FROM scenarios WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.APIVersion, &item.SourceDocument, &item.SHA256, &builtIn, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	item.BuiltIn = builtIn == 1
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return item, nil
}

func (d *Database) FindScenarioByDigest(ctx context.Context, digest string) (domain.ScenarioRecord, error) {
	var id string
	err := d.db.QueryRowContext(ctx, `SELECT id FROM scenarios WHERE sha256=? ORDER BY created_at LIMIT 1`, digest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScenarioRecord{}, ErrNotFound
	}
	if err != nil {
		return domain.ScenarioRecord{}, err
	}
	return d.GetScenario(ctx, id)
}

func (d *Database) ListScenarios(ctx context.Context) ([]domain.ScenarioRecord, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id FROM scenarios ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ScenarioRecord
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		item, err := d.GetScenario(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// DeleteScenario removes one exact custom scenario record.
func (d *Database) DeleteScenario(ctx context.Context, id string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin scenario deletion: %w", err)
	}
	defer tx.Rollback()
	var builtIn int
	if err := tx.QueryRowContext(ctx, `SELECT built_in FROM scenarios WHERE id=?`, id).Scan(&builtIn); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read scenario for deletion: %w", err)
	}
	if builtIn == 1 {
		return fmt.Errorf("built-in scenarios cannot be deleted")
	}
	var references int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE scenario_id=?`, id).Scan(&references); err != nil {
		return fmt.Errorf("count scenario references: %w", err)
	}
	if references > 0 {
		return fmt.Errorf("%w (%d); delete the associated runs first or keep this scenario", ErrScenarioInUse, references)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM scenarios WHERE id=? AND built_in=0`, id)
	if err != nil {
		return fmt.Errorf("delete scenario: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm scenario deletion: %w", err)
	}
	if deleted != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scenario deletion: %w", err)
	}
	return nil
}
