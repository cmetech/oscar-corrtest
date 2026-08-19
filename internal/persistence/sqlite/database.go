// Package sqlite persists harness facts in a local CGO-free SQLite database.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

const minimumSQLiteVersion = "3.51.3"

// Database owns the SQLite pool and its initialization readiness state.
type Database struct {
	db       *sql.DB
	path     string
	readyErr error
}

// Open opens, verifies, and migrates a local database.
func Open(ctx context.Context, path string) (*Database, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("database path %q must be absolute", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("database path %q is not a regular file", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect database path: %w", err)
	}
	db, err := openSQL(path)
	if err != nil {
		return nil, err
	}
	database := &Database{db: db, path: path}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("restrict database permissions: %w", err)
	}
	if err := verifySQLite(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := integrityCheck(ctx, db); err != nil {
		database.readyErr = err
		return database, nil
	}
	if err := migrate(ctx, db, embeddedMigrations); err != nil {
		database.readyErr = fmt.Errorf("migrate database: %w", err)
	}
	return database, nil
}

func openSQL(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := uri.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Set("_txlock", "immediate")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("configure sqlite database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	return db, nil
}

func verifySQLite(ctx context.Context, db *sql.DB) error {
	var version, journalMode string
	var foreignKeys, busyTimeout, synchronous int
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version); err != nil {
		return fmt.Errorf("read sqlite version: %w", err)
	}
	if compareVersions(version, minimumSQLiteVersion) < 0 {
		return fmt.Errorf("sqlite %s is unsupported; need %s or newer", version, minimumSQLiteVersion)
	}
	checks := []struct {
		query string
		dest  any
	}{
		{`PRAGMA journal_mode`, &journalMode},
		{`PRAGMA foreign_keys`, &foreignKeys},
		{`PRAGMA busy_timeout`, &busyTimeout},
		{`PRAGMA synchronous`, &synchronous},
	}
	for _, check := range checks {
		if err := db.QueryRowContext(ctx, check.query).Scan(check.dest); err != nil {
			return fmt.Errorf("verify %s: %w", check.query, err)
		}
	}
	if journalMode != "wal" || foreignKeys != 1 || busyTimeout != 5000 || synchronous != 2 {
		return fmt.Errorf("unsafe sqlite settings: journal=%s foreign_keys=%d busy_timeout=%d synchronous=%d", journalMode, foreignKeys, busyTimeout, synchronous)
	}
	return nil
}

func integrityCheck(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("sqlite integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("sqlite integrity check: %s", result)
	}
	return nil
}

func compareVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for i := range 3 {
		var l, r int
		if i < len(leftParts) {
			l, _ = strconv.Atoi(leftParts[i])
		}
		if i < len(rightParts) {
			r, _ = strconv.Atoi(rightParts[i])
		}
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	}
	return 0
}

// Ready reports initialization failures that require diagnostic-only operation.
func (d *Database) Ready() error { return d.readyErr }

// Close closes all pooled database connections.
func (d *Database) Close() error { return d.db.Close() }
