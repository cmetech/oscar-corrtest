package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestInitialMigrationCreatesLedgerSchema(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	want := []string{
		"schema_migrations", "targets", "scenarios", "runs", "run_cases", "run_events",
		"alert_attempts", "assertions", "resources", "artifacts",
	}
	for _, table := range want {
		var count int
		if err := database.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("table %q count=%d", table, count)
		}
	}
	var migrations int
	if err := database.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 1 {
		t.Fatalf("migration count=%d", migrations)
	}
}

func TestMigrateIsIdempotentAndRejectsChangedHistory(t *testing.T) {
	db := openTestSQL(t)
	first := fstest.MapFS{
		"migrations/0001_first.sql": {Data: []byte(`CREATE TABLE example (id INTEGER PRIMARY KEY);`)},
	}
	if err := migrate(context.Background(), db, first); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db, first); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
	changed := fstest.MapFS{
		"migrations/0001_first.sql": {Data: []byte(`CREATE TABLE changed (id INTEGER PRIMARY KEY);`)},
	}
	if err := migrate(context.Background(), db, changed); err == nil {
		t.Fatal("changed applied migration was accepted")
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	db := openTestSQL(t)
	migrations := fstest.MapFS{
		"migrations/0001_first.sql":  {Data: []byte(`CREATE TABLE stable (id INTEGER PRIMARY KEY);`)},
		"migrations/0002_broken.sql": {Data: []byte(`CREATE TABLE transient (id INTEGER); THIS IS NOT SQL;`)},
	}
	if err := migrate(context.Background(), db, migrations); err == nil {
		t.Fatal("broken migration was accepted")
	}
	var transient, applied int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='transient'`).Scan(&transient); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if transient != 0 || applied != 1 {
		t.Fatalf("transient=%d applied=%d", transient, applied)
	}
}

func TestMigrateRejectsVersionGaps(t *testing.T) {
	db := openTestSQL(t)
	migrations := fstest.MapFS{
		"migrations/0002_second.sql": {Data: []byte(`CREATE TABLE gap (id INTEGER);`)},
	}
	if err := migrate(context.Background(), db, migrations); err == nil {
		t.Fatal("migration gap was accepted")
	}
}

func openTestSQL(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openSQL(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
