package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestOwnedExistingFeedRuntimeRefusesGenericCleanup(t *testing.T) {
	ctx := context.Background()
	runtime, err := Open(ctx, config.Settings{DataDir: t.TempDir()}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	target, err := runtime.CreateTarget(ctx, domain.TargetInput{DisplayName: "owned history", BaseURL: "http://127.0.0.1:1", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runtime.CreateRun(ctx, target.ID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := json.Marshal(map[string]string{"runId": run.ID, "pattern": "owned-existing-feed-identity", "inputPath": "owned-existing-feed"})
	if err = runtime.database.SetRunExecutionDocuments(ctx, run.ID, []byte(`{}`), plan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunPreflight, domain.RunSettingUp, domain.RunInjecting, domain.RunCancelling, domain.RunCleaningUp} {
		if err = runtime.database.TransitionRun(ctx, run.ID, status, time.Now().UTC(), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err = runtime.database.CompleteRun(ctx, run.ID, domain.VerdictError, domain.CleanupUnknown, []byte(`{}`), "uncertain transport", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	updated, err := runtime.RetryCleanup(ctx, run.ID)
	if err == nil || updated.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("generic cleanup changed retained UNKNOWN: %+v %v", updated, err)
	}
	events, _ := runtime.ListRunEvents(ctx, run.ID)
	for _, event := range events {
		if event.Type == "cleanup.retry" {
			t.Fatal("generic cleanup attempted reconciliation")
		}
	}
}
func TestOwnedExistingFeedRuntimeInvalidPlanCreatesNoRun(t *testing.T) {
	ctx := context.Background()
	runtime, err := Open(ctx, config.Settings{DataDir: t.TempDir()}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err = runtime.ExecuteOwnedExistingFeed(ctx, runner.OwnedExistingFeedPlan{}); err == nil {
		t.Fatal("invalid plan accepted")
	}
	runs, err := runtime.ListRuns(ctx, domain.RunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("invalid plan created run: %v %v", runs, err)
	}
}
