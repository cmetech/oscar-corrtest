package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

// TestCampaignParentChildCasesPassOnCurrentOSCAR: since 2026-10-19 OSCAR
// refuses enabled parent_child rules that suppress children for notifiers (no
// callback owner can enforce it), so every campaign parent_child case
// (builtin, basic, advanced) went INCONCLUSIVE. They are tag-only now and
// assert the "enriched" parent link.
func TestCampaignParentChildCasesPassOnCurrentOSCAR(t *testing.T) {
	docs := map[string]scenario.Scenario{"builtin": scenario.Builtin("parent_child")}
	for _, level := range []string{"basic", "advanced"} {
		ex, ok := scenario.LookupExample("parent_child", level)
		if !ok {
			t.Fatalf("no parent_child %s example", level)
		}
		docs[level] = ex.Scenario
	}
	for level, document := range docs {
		t.Run(level, func(t *testing.T) {
			now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
			run := domain.Run{ID: "crt_000000000000000000000000PC", ShortToken: "000000PC", Status: domain.RunQueued,
				CleanupStatus: domain.CleanupNotRequired, HarnessVersion: "integration", CreatedAt: now, UpdatedAt: now}
			plan, err := compiler.Compile(run, document, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
			if err != nil {
				t.Fatal(err)
			}
			database, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := database.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			model := testoscar.NewModel(now)
			engine := runner.New(database, model, runner.Options{PollInterval: time.Second, Stabilization: time.Second, Now: model.Now, Sleep: model.Sleep})
			_ = engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true})
			stored, err := database.GetRun(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Verdict != domain.VerdictPass || stored.CleanupStatus != domain.CleanupClean {
				t.Fatalf("%s: verdict %s cleanup %s report=%s", level, stored.Verdict, stored.CleanupStatus, stored.CanonicalReportJSON)
			}
		})
	}
}
