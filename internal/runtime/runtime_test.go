package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
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

func TestStartBuiltinRejectsUnknownTargetWithoutCreatingRun(t *testing.T) {
	runtime, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if _, err := runtime.StartBuiltin(context.Background(), "tgt_missing", "flood", "phase_b_dispatch"); err == nil {
		t.Fatal("unknown target accepted")
	}
	runs, err := runtime.ListRuns(context.Background(), domain.RunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("failed start created run: %+v err=%v", runs, err)
	}
}

func TestRetryCleanupRequiresReadBackOwnershipBeforeDelete(t *testing.T) {
	var deletes int
	var ownedRunID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/correlation_rules/71":
			_, _ = fmt.Fprintf(w, `{"id":71,"name":"corrtest-flood-p01-00000001","pattern":"flood","description":"Temporary OSCAR correlation test rule; run=%s"}`, ownedRunID)
		case request.Method == http.MethodDelete && request.URL.Path == "/api/v1/correlation_rules/71":
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runtime, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	target, err := runtime.CreateTarget(context.Background(), domain.TargetInput{DisplayName: "Lab", BaseURL: server.URL, APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runtime.CreateRun(context.Background(), target.ID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	ownedRunID = run.ID
	now := time.Now().UTC()
	for _, state := range []domain.RunStatus{domain.RunPreflight, domain.RunSettingUp, domain.RunInjecting, domain.RunObserving, domain.RunAsserting, domain.RunCleaningUp, domain.RunCompleted} {
		if err := runtime.database.TransitionRun(context.Background(), run.ID, state, now, "test transition"); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.database.CompleteRun(context.Background(), run.ID, domain.VerdictPass, domain.CleanupDirty, json.RawMessage(`{}`), "cleanup failed", now); err != nil {
		t.Fatal(err)
	}
	resource := domain.Resource{ID: "res_00000001_001", RunID: run.ID, Kind: "correlation_rule", ExternalName: "corrtest-flood-p01-00000001", OwnershipToken: run.ID, LifecycleState: domain.ResourceProposed}
	if err := runtime.database.CreateResource(context.Background(), resource); err != nil {
		t.Fatal(err)
	}
	if err := runtime.database.AdoptResource(context.Background(), resource.ID, "71", now); err != nil {
		t.Fatal(err)
	}
	updated, err := runtime.RetryCleanup(context.Background(), run.ID)
	if err != nil || updated.CleanupStatus != domain.CleanupClean || deletes != 1 {
		t.Fatalf("updated=%+v deletes=%d err=%v", updated, deletes, err)
	}
	if err := runtime.DeleteRun(context.Background(), run.ID); err != nil {
		t.Fatalf("delete clean run: %v", err)
	}
	if _, err := runtime.GetRun(context.Background(), run.ID); err == nil {
		t.Fatal("deleted run remained in history")
	}
}

func TestImportScenarioPersistsOriginalSourceOnceByDigest(t *testing.T) {
	runtime, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	source := []byte("apiVersion: corrtest.oscar/v1alpha1\nkind: CorrelationScenario\nname: sample\nsuite: custom\npattern: flood\nmaxDuration: 90s\ncases: []\n")
	document, err := scenario.Decode(strings.NewReader(string(source)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.ImportScenario(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.ImportScenario(context.Background(), source, document)
	if err != nil || first.ID != second.ID || first.SourceDocument != string(source) {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	items, err := runtime.ListScenarios(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}
