package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpenEnforcesSQLiteVersionAndPragmas(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Ready(); err != nil {
		t.Fatalf("Ready()=%v", err)
	}

	var version, journalMode string
	var foreignKeys, busyTimeout, synchronous int
	if err := database.db.QueryRow(`SELECT sqlite_version()`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	majorMinorPatch := strings.Split(version, ".")
	if len(majorMinorPatch) != 3 {
		t.Fatalf("sqlite_version=%q", version)
	}
	minor, _ := strconv.Atoi(majorMinorPatch[1])
	patch, _ := strconv.Atoi(majorMinorPatch[2])
	if majorMinorPatch[0] != "3" || minor < 51 || (minor == 51 && patch < 3) {
		t.Fatalf("sqlite_version=%q, need >=3.51.3", version)
	}
	queries := []struct {
		query string
		dest  any
	}{
		{`PRAGMA journal_mode`, &journalMode},
		{`PRAGMA foreign_keys`, &foreignKeys},
		{`PRAGMA busy_timeout`, &busyTimeout},
		{`PRAGMA synchronous`, &synchronous},
	}
	for _, query := range queries {
		if err := database.db.QueryRow(query.query).Scan(query.dest); err != nil {
			t.Fatalf("%s: %v", query.query, err)
		}
	}
	if journalMode != "wal" || foreignKeys != 1 || busyTimeout != 5000 || synchronous != 2 {
		t.Fatalf("pragmas mode=%q fk=%d busy=%d sync=%d", journalMode, foreignKeys, busyTimeout, synchronous)
	}
}

func TestEveryPooledConnectionHasForeignKeysAndBusyTimeout(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	connections := make([]interface{ Close() error }, 0, 4)
	for range 4 {
		conn, err := database.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("fk=%d busy=%d", foreignKeys, busyTimeout)
		}
	}
	for _, conn := range connections {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
