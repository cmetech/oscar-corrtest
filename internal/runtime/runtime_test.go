package runtime

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestOpenCreatesRestrictiveDurableRuntime(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "state")
	runtime, err := Open(context.Background(), config.Settings{DataDir: dataDir, ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if !runtime.Readiness().Ready {
		t.Fatalf("readiness=%+v", runtime.Readiness())
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("data mode=%o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "corrtest.db")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecoversActiveRunsBeforeReady(t *testing.T) {
	settings := config.Settings{DataDir: filepath.Join(t.TempDir(), "state"), ListenAddress: "127.0.0.1:8787"}
	first, err := Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := first.CreateRun(context.Background(), "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	got, err := second.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunInterrupted {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestOpenKeepsMigrationFailureInDiagnosticMode(t *testing.T) {
	settings := config.Settings{DataDir: filepath.Join(t.TempDir(), "state"), ListenAddress: "127.0.0.1:8787"}
	first, err := Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(settings.DataDir, "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET sha256='tampered' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	diagnostic, err := Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = diagnostic.Close() })
	if diagnostic.Readiness().Ready || diagnostic.Readiness().Error == "" {
		t.Fatalf("readiness=%+v", diagnostic.Readiness())
	}
	if _, err := diagnostic.CreateRun(context.Background(), "", "", "test"); err == nil {
		t.Fatal("diagnostic runtime accepted mutation")
	}
}

func TestPreviewBuiltinUsesStoredTargetWithoutMutation(t *testing.T) {
	settings := config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}
	runtime, err := Open(context.Background(), settings, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	target, err := runtime.CreateTarget(context.Background(), domain.TargetInput{DisplayName: "Lab", BaseURL: "https://oscar.example", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.PreviewBuiltin(context.Background(), target.ID, "threshold", "phase_b_dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pattern != "threshold" || plan.MutationBudget.Rules != 2 || plan.MutationBudget.Alerts != 5 || plan.RunID == "" {
		t.Fatalf("plan=%+v", plan)
	}
	runs, err := runtime.ListRuns(context.Background(), domain.RunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("preview persisted a run: %+v err=%v", runs, err)
	}
}
