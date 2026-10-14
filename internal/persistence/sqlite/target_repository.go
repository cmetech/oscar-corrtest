package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// ErrNotFound is returned when a requested durable record does not exist.
var ErrNotFound = errors.New("record not found")

// CreateTarget inserts sanitized target metadata.
func (d *Database) CreateTarget(ctx context.Context, target domain.Target) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	input := domain.TargetInput{
		OrdinaryWebhook: target.OrdinaryWebhook,
		DisplayName:     target.DisplayName,
		BaseURL:         target.BaseURL,
		APIProfile:      target.APIProfile,
		TLS:             target.TLS,
		Credential:      target.Credential,
	}
	if err := input.Validate(); err != nil {
		return err
	}
	var kind, ref, ca any
	if target.Credential.Kind != "" {
		kind, ref = string(target.Credential.Kind), target.Credential.Reference
	}
	if target.TLS.CAPath != "" {
		ca = target.TLS.CAPath
	}
	var receiver any
	if target.OrdinaryWebhook != nil {
		raw, err := json.Marshal(target.OrdinaryWebhook)
		if err != nil {
			return err
		}
		receiver = string(raw)
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO targets(
        id, display_name, base_url, api_profile, tls_insecure, ca_path,
        credential_kind, credential_ref, created_at, updated_at, ordinary_webhook_json
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		target.ID, target.DisplayName, target.BaseURL, target.APIProfile, target.TLS.Insecure,
		ca, kind, ref, formatTime(target.CreatedAt), formatTime(target.UpdatedAt), receiver)
	if err != nil {
		return fmt.Errorf("create target: %w", err)
	}
	return nil
}

// GetTarget returns one sanitized target.
func (d *Database) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	row := d.db.QueryRowContext(ctx, targetSelect+` WHERE id=?`, id)
	target, err := scanTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Target{}, ErrNotFound
	}
	if err != nil {
		return domain.Target{}, fmt.Errorf("get target: %w", err)
	}
	return target, nil
}

// ListTargets returns targets ordered by display name.
func (d *Database) ListTargets(ctx context.Context) ([]domain.Target, error) {
	rows, err := d.db.QueryContext(ctx, targetSelect+` ORDER BY display_name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	defer rows.Close()
	var targets []domain.Target
	for rows.Next() {
		target, err := scanTarget(rows)
		if err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	return targets, nil
}

const targetSelect = `SELECT id, display_name, base_url, api_profile, tls_insecure,
    ca_path, credential_kind, credential_ref, created_at, updated_at, ordinary_webhook_json FROM targets`

type rowScanner interface{ Scan(...any) error }

func scanTarget(row rowScanner) (domain.Target, error) {
	var target domain.Target
	var insecure bool
	var ca, kind, ref, receiver sql.NullString
	var created, updated string
	err := row.Scan(&target.ID, &target.DisplayName, &target.BaseURL, &target.APIProfile, &insecure,
		&ca, &kind, &ref, &created, &updated, &receiver)
	if err != nil {
		return domain.Target{}, err
	}
	if receiver.Valid {
		var binding domain.OrdinaryWebhookBinding
		if err := json.Unmarshal([]byte(receiver.String), &binding); err != nil {
			return domain.Target{}, err
		}
		if err := binding.Validate(); err != nil {
			return domain.Target{}, err
		}
		target.OrdinaryWebhook = &binding
	}
	target.TLS = domain.TLSPolicy{Insecure: insecure, CAPath: ca.String}
	target.Credential = domain.CredentialRef{Kind: domain.CredentialKind(kind.String), Reference: ref.String}
	if target.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return domain.Target{}, err
	}
	if target.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return domain.Target{}, err
	}
	return target, nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
