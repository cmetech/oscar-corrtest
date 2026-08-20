package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestBackupProducesConsistentSnapshotDuringWALWrites(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(context.Background(), filepath.Join(dir, "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	var committed atomic.Int64
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := database.AppendRunEvent(context.Background(), domain.RunEvent{
					RunID: run.ID, Type: "writer", Level: "info", OccurredAt: time.Now().UTC(), Summary: "write during backup",
				}); err == nil {
					committed.Add(1)
				}
			}
		}
	}()
	for committed.Load() < 5 {
		time.Sleep(time.Millisecond)
	}
	backupPath := filepath.Join(dir, "backup.db")
	if err := database.Backup(context.Background(), backupPath); err != nil {
		close(stop)
		<-done
		t.Fatal(err)
	}
	close(stop)
	<-done

	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	backup, err := openSQL(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := integrityCheck(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	var events, migrations int64
	if err := backup.QueryRow(`SELECT count(*) FROM run_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := backup.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if events < 1 || events > committed.Load() || migrations != 2 {
		t.Fatalf("backup events=%d committed=%d migrations=%d", events, committed.Load(), migrations)
	}
}

func TestBackupRefusesOverwriteAndCleansCancelledTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(context.Background(), filepath.Join(dir, "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	destination := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.Backup(context.Background(), destination); err == nil {
		t.Fatal("overwrite accepted")
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "keep" {
		t.Fatalf("destination=%q err=%v", content, err)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := database.Backup(cancelled, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backup()=%v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled destination err=%v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".corrtest-backup-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary backups=%v err=%v", matches, err)
	}
}
