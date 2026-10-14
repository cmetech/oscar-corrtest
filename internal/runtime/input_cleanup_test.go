package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestRuleOnlyRetryCannotEraseInputResponsibility(t *testing.T) {
	ctx := context.Background()
	app, err := Open(ctx, config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	target, err := app.CreateTarget(ctx, domain.TargetInput{DisplayName: "unused", BaseURL: "http://127.0.0.1:1", APIProfile: "public-v1"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := app.CreateRun(ctx, target.ID, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.database.AppendRunEvent(ctx, domain.RunEvent{RunID: run.ID, Type: "alert.attempting", Level: "info", OccurredAt: time.Now().UTC(), Summary: "before send", DetailJSON: `{"caseStableKey":"P01","eventId":"owned","eventIndex":1,"sendState":"ATTEMPTED","injectionClass":"indeterminate","statusCode":0}`}); err != nil {
		t.Fatal(err)
	}
	completeTestRun(t, app, run.ID, domain.CleanupDirty)
	updated, err := app.RetryCleanup(ctx, run.ID)
	if err == nil || updated.CleanupStatus != domain.CleanupDirty {
		t.Fatalf("rule-only retry lost input responsibility: %+v err=%v", updated, err)
	}
	if err := app.DeleteRun(ctx, run.ID); err == nil {
		t.Fatal("unresolved input evidence became deletable")
	}
}
