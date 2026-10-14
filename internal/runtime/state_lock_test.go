package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestSecondRuntimeCannotInterruptActiveRun(t *testing.T) {
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir()}
	first, err := Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	run, err := first.CreateRun(ctx, "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.RunStatus{domain.RunPreflight, domain.RunSettingUp, domain.RunInjecting, domain.RunObserving} {
		if err := first.database.TransitionRun(ctx, run.ID, state, time.Now().UTC(), "test execution"); err != nil {
			t.Fatal(err)
		}
	}
	second, openErr := Open(ctx, settings, version.Info{})
	if second != nil {
		defer second.Close()
	}
	got, err := first.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunObserving {
		t.Errorf("second runtime changed live run status to %s, want OBSERVING", got.Status)
	}
	if openErr == nil || !strings.Contains(openErr.Error(), "already in use") {
		t.Errorf("second runtime error = %v, want explicit already in use refusal", openErr)
	}
}

func TestRuntimeStateOwnershipReleaseAndIsolation(t *testing.T) {
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir()}
	first, err := Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	other, err := Open(ctx, config.Settings{DataDir: t.TempDir()}, version.Info{})
	if err != nil {
		t.Fatalf("independent state refused: %v", err)
	}
	defer other.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatalf("close did not release: %v", err)
	}
	defer second.Close()
	_ = first.Close()
	third, err := Open(ctx, settings, version.Info{})
	if third != nil {
		defer third.Close()
	}
	if err == nil {
		t.Fatal("repeated old Close released current owner's lock")
	}
}

func TestRuntimeStateOwnershipFailedOpenReleasesLock(t *testing.T) {
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir()}
	badDB := filepath.Join(settings.DataDir, "corrtest.db")
	if err := os.Mkdir(badDB, 0700); err != nil {
		t.Fatal(err)
	}
	if app, err := Open(ctx, settings, version.Info{}); err == nil {
		app.Close()
		t.Fatal("invalid database opened")
	}
	if err := os.Remove(badDB); err != nil {
		t.Fatal(err)
	}
	app, err := Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatalf("failed open leaked ownership: %v", err)
	}
	defer app.Close()
}

func TestRuntimeStateOwnershipRefusesNonregularLock(t *testing.T) {
	settings := config.Settings{DataDir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(settings.DataDir, ".corrtest.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if app, err := Open(context.Background(), settings, version.Info{}); err == nil {
		app.Close()
		t.Fatal("nonregular lock accepted")
	}
	if _, err := os.Stat(filepath.Join(settings.DataDir, "corrtest.db")); !os.IsNotExist(err) {
		t.Fatalf("SQLite touched before lock validation: %v", err)
	}
}

func TestRuntimeStateOwnershipDiagnosticRetainsLock(t *testing.T) {
	ctx := context.Background()
	settings := config.Settings{DataDir: t.TempDir()}
	initial, err := Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	if err = initial.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(settings.DataDir, "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE schema_migrations SET sha256='tampered' WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := Open(ctx, settings, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer diagnostic.Close()
	if diagnostic.Readiness().Ready {
		t.Fatal("expected diagnostic mode")
	}
	second, err := Open(ctx, settings, version.Info{})
	if second != nil {
		defer second.Close()
	}
	if err == nil {
		t.Fatal("diagnostic runtime did not retain ownership")
	}
}

func TestRuntimeStateOwnershipAcrossProcessDeath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	state := filepath.Join(directory, "state")
	ready := filepath.Join(directory, "ready")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeStateOwnerSubprocess$")
	cmd.Env = append(os.Environ(), "CORRTEST_LOCK_TEST_STATE="+state, "CORRTEST_LOCK_TEST_READY="+ready)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	pipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		_ = pipe.Close()
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var id []byte
	for {
		id, err = os.ReadFile(ready)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("child not ready: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	blocked, err := Open(context.Background(), config.Settings{DataDir: state}, version.Info{})
	if blocked != nil {
		blocked.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Errorf("second process not refused: %v", err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	recovered, err := Open(context.Background(), config.Settings{DataDir: state}, version.Info{})
	if err != nil {
		t.Fatalf("dead owner lock not released: %v", err)
	}
	defer recovered.Close()
	run, err := recovered.GetRun(context.Background(), string(id))
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunInterrupted || run.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("crashed source responsibility=%s/%s", run.Status, run.CleanupStatus)
	}
}

func TestRuntimeStateOwnerSubprocess(t *testing.T) {
	state := os.Getenv("CORRTEST_LOCK_TEST_STATE")
	if state == "" {
		return
	}
	ctx := context.Background()
	app, err := Open(ctx, config.Settings{DataDir: state}, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	run, err := app.CreateRun(ctx, "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunPreflight, domain.RunSettingUp, domain.RunInjecting, domain.RunObserving} {
		if err = app.database.TransitionRun(ctx, run.ID, status, time.Now().UTC(), "child execution"); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(os.Getenv("CORRTEST_LOCK_TEST_READY"), []byte(run.ID), 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
