package runner_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestRunnerExecutesFloodCasesAndCleansOwnedRules(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: 3 * time.Millisecond, Stabilization: time.Millisecond})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.RunCompleted || stored.Verdict != domain.VerdictPass || stored.CleanupStatus != domain.CleanupClean {
		t.Fatalf("stored run=%+v", stored)
	}
	if !json.Valid(stored.CompiledPlanJSON) || !json.Valid(stored.CapabilitySnapshotJSON) || !json.Valid(stored.CanonicalReportJSON) {
		t.Fatalf("durable documents missing: %+v", stored)
	}
	resources, err := database.ListResources(context.Background(), run.ID)
	if err != nil || len(resources) != 2 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
	for _, resource := range resources {
		if resource.LifecycleState != domain.ResourceDeleted || resource.DeletedAt == nil {
			t.Fatalf("resource not cleaned: %+v", resource)
		}
	}
	if len(api.deleted) != 2 || api.imported || api.updated {
		t.Fatalf("unsafe API lifecycle: %+v", api)
	}
	events, err := database.ListRunEvents(context.Background(), run.ID)
	if err != nil || len(events) < 7 || events[len(events)-1].Summary != "Run completed" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestRunnerCannotPassWhenPipelineModeIsPhaseA(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_a_audit_only", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictInconclusive || stored.CleanupStatus != domain.CleanupNotRequired {
		t.Fatalf("phase A run=%+v", stored)
	}
	if api.created != 0 || len(api.injected) != 0 {
		t.Fatalf("phase A mutated OSCAR: %+v", api)
	}
}

func TestRunnerUsesHistoryFingerprintNotDiagnosticHash(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	api := &fakeAPI{requiredAuditFingerprint: "server-CORRTEST_FLOOD_P01_INTERFACEDOWN_00000001"}
	if err := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: 3 * time.Millisecond, Stabilization: time.Millisecond}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	if !api.sawRequiredFingerprint {
		t.Fatal("runner did not query audit using the server history fingerprint")
	}
}

type fakeAPI struct {
	mu                       sync.Mutex
	created                  int
	deleted                  []int
	injected                 []compiler.AlertPlan
	imported                 bool
	updated                  bool
	requiredAuditFingerprint string
	sawRequiredFingerprint   bool
}

func (f *fakeAPI) ValidateRule(context.Context, compiler.RulePlan) error { return nil }
func (f *fakeAPI) CreateRule(_ context.Context, rule compiler.RulePlan) (oscar.Rule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	return oscar.Rule{ID: f.created, Name: rule.Name, Pattern: rule.Pattern, Description: rule.Description}, nil
}
func (f *fakeAPI) GetRule(_ context.Context, id int) (oscar.Rule, error) {
	return oscar.Rule{ID: id}, nil
}
func (f *fakeAPI) DeleteRule(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeAPI) Inject(_ context.Context, alert compiler.AlertPlan) (oscar.InjectionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.injected = append(f.injected, alert)
	return oscar.InjectionResult{Class: oscar.InjectionAccepted, StatusCode: 200}, nil
}
func (f *fakeAPI) FindHistory(_ context.Context, query oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(query.AlertName, "SYNTHETIC") {
		if strings.Contains(query.AlertName, "_P01_") {
			return []oscar.HistoryRecord{{ID: "parent", AlertName: query.AlertName, Fingerprint: "parent-fp", CreatedAt: query.Start.Add(time.Millisecond), Labels: map[string]string{"oscar_test_run_id": "crt_00000000000000000000000001"}}}, nil
		}
		return nil, nil
	}
	return []oscar.HistoryRecord{{ID: "source", AlertName: query.AlertName, Fingerprint: "server-" + query.AlertName, CreatedAt: query.Start.Add(time.Millisecond), Labels: map[string]string{"oscar_test_run_id": "crt_00000000000000000000000001"}}}, nil
}
func (f *fakeAPI) CorrelationAudit(_ context.Context, fingerprint string) ([]oscar.AuditRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requiredAuditFingerprint != "" && fingerprint == f.requiredAuditFingerprint {
		f.sawRequiredFingerprint = true
	}
	outcome := "flood_window_advancing"
	if strings.Contains(fingerprint, "_P01_") {
		outcome = "parent_emitted"
	}
	return []oscar.AuditRecord{{ID: 1, AlertFingerprint: fingerprint, RuleID: 1, Pattern: "flood", Outcome: outcome}}, nil
}

func openDatabase(t *testing.T) *storage.Database {
	t.Helper()
	database, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func newRun() domain.Run {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	return domain.Run{ID: "crt_00000000000000000000000001", ShortToken: "00000001", Status: domain.RunQueued,
		CleanupStatus: domain.CleanupNotRequired, HarnessVersion: "test", CreatedAt: now, UpdatedAt: now}
}
