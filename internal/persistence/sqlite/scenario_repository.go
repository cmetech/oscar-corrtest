package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

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
