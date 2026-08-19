package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	modernsqlite "modernc.org/sqlite"
)

type onlineBackuper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

// Backup creates a consistent online database snapshot without copying live WAL files.
func (d *Database) Backup(ctx context.Context, destination string) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(destination) {
		return fmt.Errorf("backup destination %q must be absolute", destination)
	}
	destination = filepath.Clean(destination)
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("backup destination %q already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	parent := filepath.Dir(destination)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect backup directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup directory %q must be a real directory", parent)
	}

	reservation, err := os.CreateTemp(parent, ".corrtest-backup-*")
	if err != nil {
		return fmt.Errorf("reserve backup path: %w", err)
	}
	temporary := reservation.Name()
	if err := reservation.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close backup reservation: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("prepare backup path: %w", err)
	}
	defer func() {
		_ = os.Remove(temporary)
		_ = os.Remove(temporary + "-wal")
		_ = os.Remove(temporary + "-shm")
	}()

	connection, err := d.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve source backup connection: %w", err)
	}
	defer connection.Close()
	err = connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(onlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(temporary)
		if err != nil {
			return fmt.Errorf("initialize online backup: %w", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil {
				return fmt.Errorf("copy online backup pages: %w", err)
			}
			if !more {
				break
			}
		}
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("finish online backup: %w", err)
		}
		finished = true
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("restrict backup permissions: %w", err)
	}
	if err := verifyBackup(ctx, temporary); err != nil {
		return err
	}
	file, err := os.Open(temporary)
	if err != nil {
		return fmt.Errorf("open backup for sync: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.Link(temporary, destination); err != nil {
		return fmt.Errorf("install backup without overwrite: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("remove backup staging name: %w", err)
	}
	if err := syncBackupDirectory(parent); err != nil {
		return err
	}
	return nil
}

func verifyBackup(ctx context.Context, path string) error {
	db, err := openSQL(path)
	if err != nil {
		return fmt.Errorf("open backup verification database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("open backup verification database: %w", err)
	}
	if err := integrityCheck(ctx, db); err != nil {
		return fmt.Errorf("verify backup: %w", err)
	}
	expected, err := readMigrations(embeddedMigrations)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT version, name, sha256 FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("verify backup migrations: %w", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		if index >= len(expected) {
			return fmt.Errorf("backup contains migration newer than binary")
		}
		var version int
		var name, digest string
		if err := rows.Scan(&version, &name, &digest); err != nil {
			return fmt.Errorf("verify backup migration: %w", err)
		}
		want := expected[index]
		if version != want.version || name != want.name || digest != want.digest {
			return fmt.Errorf("backup migration %04d does not match binary", version)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("verify backup migrations: %w", err)
	}
	if index != len(expected) {
		return fmt.Errorf("backup migration history is incomplete")
	}
	return nil
}

func syncBackupDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open backup directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	return nil
}
