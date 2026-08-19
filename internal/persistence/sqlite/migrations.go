package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

var migrationNamePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

type migration struct {
	version int
	name    string
	digest  string
	content []byte
}

func migrate(ctx context.Context, db *sql.DB, source fs.FS) error {
	migrations, err := readMigrations(source)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        name TEXT NOT NULL,
        sha256 TEXT NOT NULL,
        applied_at TEXT NOT NULL
    ) STRICT`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, item := range migrations {
		var name, digest string
		err := db.QueryRowContext(ctx, `SELECT name, sha256 FROM schema_migrations WHERE version=?`, item.version).Scan(&name, &digest)
		switch {
		case err == nil:
			if name != item.name || digest != item.digest {
				return fmt.Errorf("migration %04d history mismatch", item.version)
			}
			continue
		case err != sql.ErrNoRows:
			return fmt.Errorf("read migration %04d: %w", item.version, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %04d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, string(item.content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %04d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, sha256, applied_at) VALUES(?, ?, ?, ?)`,
			item.version, item.name, item.digest, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %04d: %w", item.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %04d: %w", item.version, err)
		}
	}

	var newest int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&newest); err != nil {
		return fmt.Errorf("read newest migration: %w", err)
	}
	if newest > len(migrations) {
		return fmt.Errorf("database migration %04d is newer than this binary", newest)
	}
	return nil
}

func readMigrations(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("unexpected migration directory %q", entry.Name())
		}
		matches := migrationNamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, _ := strconv.Atoi(matches[1])
		if version != len(result)+1 {
			return nil, fmt.Errorf("migration sequence gap at %q", entry.Name())
		}
		content, err := fs.ReadFile(source, filepath.ToSlash(filepath.Join("migrations", entry.Name())))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(content)
		result = append(result, migration{
			version: version,
			name:    matches[2],
			digest:  hex.EncodeToString(sum[:]),
			content: content,
		})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	return result, nil
}
