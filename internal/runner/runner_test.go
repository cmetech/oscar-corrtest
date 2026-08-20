package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"path/filepath"
	"strconv"
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
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
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
	if len(api.resolved) == 0 {
		t.Fatal("observed run-owned alert residue was not resolved during cleanup")
	}
	if !strings.Contains(string(stored.CanonicalReportJSON), `"resolutions":[`) {
		t.Fatalf("cleanup classifications missing from report: %s", stored.CanonicalReportJSON)
	}
	filtered, err := database.ListRuns(context.Background(), domain.RunFilter{Pattern: "flood"})
	if err != nil || len(filtered) != 1 || filtered[0].ID != run.ID {
		t.Fatalf("normalized plan rows do not support pattern filtering: runs=%+v err=%v", filtered, err)
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

func TestRunnerCannotPassAnEmptyCustomPlan(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan := compiler.Plan{APIVersion: "corrtest.oscar/v1alpha1", RunID: run.ID, ShortToken: run.ShortToken, Pattern: "flood", Digest: "empty"}
	api := &fakeAPI{}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupNotRequired || api.created != 0 {
		t.Fatalf("empty plan produced unsafe outcome: run=%+v created=%d", stored, api.created)
	}
}

func TestMissingEligibilityProducesRunInconclusiveNotFail(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{runID: run.ID, hideHistory: true}
	if err := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictInconclusive {
		t.Fatalf("missing eligibility verdict=%s report=%s", stored.Verdict, stored.CanonicalReportJSON)
	}
}

func TestRunnerReconcilesLostCreateResponseByExactOwnership(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	api := &fakeAPI{createError: errors.New("response lost after create")}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond, Sleep: func(context.Context, time.Duration) error { return nil }})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	resources, err := database.ListResources(context.Background(), run.ID)
	if err != nil || len(resources) != 2 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
	for _, resource := range resources {
		if resource.ExternalID == "" || resource.LifecycleState != domain.ResourceDeleted {
			t.Fatalf("lost create was not safely adopted and deleted: %+v", resource)
		}
	}
}

func TestRunnerDoesNotClaimCleanWhenLostCreateCannotBeReconciled(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	api := &fakeAPI{createError: errors.New("response lost after create"), hideReconciled: true}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err == nil {
		t.Fatal("expected unreconciled create to fail")
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupDirty {
		t.Fatalf("unreconciled create must remain dirty: %+v", stored)
	}
	resources, err := database.ListResources(context.Background(), run.ID)
	if err != nil || len(resources) != 1 || resources[0].LifecycleState != domain.ResourceUnknown {
		t.Fatalf("unknown resource evidence was lost: resources=%+v err=%v", resources, err)
	}
}

func TestRunnerUsesDetachedBoundedContextToCleanupAfterCancellation(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeAPI{}
	engine := runner.New(database, api, runner.Options{
		PollInterval:      time.Millisecond,
		ObservationWindow: time.Millisecond,
		CleanupTimeout:    time.Second,
		Sleep: func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	})
	if err := engine.Execute(ctx, run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error=%v, want context cancellation", err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.RunCompleted || stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupClean {
		t.Fatalf("canceled run did not persist clean terminal evidence: %+v", stored)
	}
	if len(api.deleted) != 2 {
		t.Fatalf("created rules were not cleaned after cancellation: deleted=%v", api.deleted)
	}
	if len(api.resolved) == 0 {
		t.Fatal("accepted alert was not resolved after cancellation")
	}
}

func TestRunnerResolvesInjectedAlertsWhenObservationFails(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	api := &fakeAPI{auditError: errors.New("audit unavailable")}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond, CleanupTimeout: time.Second})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err == nil {
		t.Fatal("expected observation failure")
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupClean {
		t.Fatalf("run=%+v", stored)
	}
	if len(api.resolved) == 0 {
		t.Fatal("failure cleanup did not resolve exact run-owned history")
	}
	for _, record := range api.resolved {
		if record.Labels["oscar_test_run_id"] != run.ID || record.Fingerprint == "" {
			t.Fatalf("unsafe resolution record: %+v", record)
		}
	}
}

func TestRunnerMarksCleanupDirtyWhenInjectedHistoryCannotBeRecovered(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	api := &fakeAPI{auditError: errors.New("audit unavailable"), hideHistoryOnAuditError: true}
	engine := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond, CleanupTimeout: time.Second})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err == nil {
		t.Fatal("expected observation failure")
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupDirty {
		t.Fatalf("unrecoverable alert residue must stay visible: %+v", stored)
	}
	if len(api.resolved) != 0 {
		t.Fatalf("unexpected resolutions: %+v", api.resolved)
	}
}

func TestRunnerPersistsTerminalNoMutationEvidenceWhenAlreadyCancelled(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakeAPI{}
	err := runner.New(database, api, runner.Options{CleanupTimeout: time.Second}).Execute(ctx, run, plan,
		runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error=%v, want context cancellation", err)
	}
	stored, getErr := database.GetRun(context.Background(), run.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.Status != domain.RunCompleted || stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupNotRequired || api.created != 0 {
		t.Fatalf("pre-cancelled run did not terminate safely: run=%+v created=%d", stored, api.created)
	}
}

func TestRunnerUsesHistoryFingerprintNotDiagnosticHash(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, _ := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	api := &fakeAPI{requiredAuditFingerprint: "server-" + run.ID + "-P01-001"}
	if err := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: 3 * time.Millisecond, Stabilization: time.Millisecond}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	if !api.sawRequiredFingerprint {
		t.Fatal("runner did not query audit using the server history fingerprint")
	}
}

func TestRunnerExecutesEveryBuiltinPatternThroughOneCoordinator(t *testing.T) {
	for index, input := range scenario.AllBuiltins() {
		t.Run(input.Pattern, func(t *testing.T) {
			database := openDatabase(t)
			run := newRun()
			run.ID = strings.TrimSuffix(run.ID, "01") + strconv.Itoa(index+10)
			run.ShortToken = fmt.Sprintf("%08d", index+10)
			if err := database.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, input, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
			if err != nil {
				t.Fatal(err)
			}
			api := testoscar.NewModel(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
			engine := runner.New(database, api, runner.Options{PollInterval: time.Second, Stabilization: time.Second, Now: api.Now, Sleep: api.Sleep})
			if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
				t.Fatal(err)
			}
			stored, _ := database.GetRun(context.Background(), run.ID)
			if stored.Verdict != domain.VerdictPass || stored.CleanupStatus != domain.CleanupClean {
				t.Fatalf("run=%+v report=%s", stored, stored.CanonicalReportJSON)
			}
			if input.Pattern == "parent_child" && !strings.Contains(string(stored.CanonicalReportJSON), `"notificationStatuses":["suppressed"]`) {
				t.Fatalf("parent-child notification evidence missing: %s", stored.CanonicalReportJSON)
			}
		})
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
	runID                    string
	createError              error
	reconciled               oscar.Rule
	hideReconciled           bool
	hideHistory              bool
	auditError               error
	hideHistoryOnAuditError  bool
	resolved                 []oscar.HistoryRecord
}

func (f *fakeAPI) ValidateRule(context.Context, compiler.RulePlan) error { return nil }
func (f *fakeAPI) CreateRule(_ context.Context, rule compiler.RulePlan) (oscar.Rule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	if f.createError != nil {
		f.reconciled = oscar.Rule{ID: f.created, Name: rule.Name, Pattern: rule.Pattern, Description: rule.Description}
		return oscar.Rule{}, f.createError
	}
	return oscar.Rule{ID: f.created, Name: rule.Name, Pattern: rule.Pattern, Description: rule.Description}, nil
}

func (f *fakeAPI) FindRules(_ context.Context, name string) ([]oscar.Rule, error) {
	if f.hideReconciled {
		return nil, nil
	}
	if f.reconciled.Name == name {
		return []oscar.Rule{f.reconciled}, nil
	}
	return nil, nil
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
func (f *fakeAPI) ResolveHistory(_ context.Context, record oscar.HistoryRecord) (oscar.InjectionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, record)
	return oscar.InjectionResult{Class: oscar.InjectionAccepted, StatusCode: 200}, nil
}
func (f *fakeAPI) FindHistory(_ context.Context, query oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hideHistory {
		return nil, nil
	}
	if strings.Contains(query.AlertName, "SYNTHETIC") {
		if strings.Contains(query.AlertName, "_P01_") {
			return []oscar.HistoryRecord{{ID: "parent", AlertName: query.AlertName, Fingerprint: "parent-" + query.AlertName, CreatedAt: query.Start.Add(time.Millisecond), Labels: map[string]string{"oscar_test_run_id": f.effectiveRunID(), "oscar_test_alert_class": "synthetic"}}}, nil
		}
		return nil, nil
	}
	if query.AlertName == "" {
		return nil, nil
	}
	byEvent := map[string]oscar.HistoryRecord{}
	for _, alert := range f.injected {
		if alert.Name != query.AlertName {
			continue
		}
		eventID := alert.Labels["oscar_test_event_id"]
		labels := make(map[string]string, len(alert.Labels))
		for key, value := range alert.Labels {
			labels[key] = value
		}
		byEvent[eventID] = oscar.HistoryRecord{ID: "source-" + eventID, AlertName: query.AlertName, Fingerprint: "server-" + eventID, Status: alert.Status, CreatedAt: query.Start.Add(time.Millisecond), Labels: labels}
	}
	result := make([]oscar.HistoryRecord, 0, len(byEvent))
	for _, record := range byEvent {
		result = append(result, record)
	}
	return result, nil
}
func (f *fakeAPI) CorrelationAudit(_ context.Context, fingerprint string) ([]oscar.AuditRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auditError != nil {
		if f.hideHistoryOnAuditError {
			f.hideHistory = true
		}
		return nil, f.auditError
	}
	if f.requiredAuditFingerprint != "" && fingerprint == f.requiredAuditFingerprint {
		f.sawRequiredFingerprint = true
	}
	var matching *compiler.AlertPlan
	for index := range f.injected {
		candidate := &f.injected[index]
		if fingerprint == "server-"+candidate.Labels["oscar_test_event_id"] {
			matching = candidate
			break
		}
	}
	if matching == nil {
		return nil, nil
	}
	outcome := "enriched"
	caseCode := matching.Labels["oscar_test_case_code"]
	pattern := matching.Labels["oscar_test_pattern"]
	role := matching.Labels["oscar_test_alert_role"]
	if pattern == "parent_child" {
		outcome = "released_no_trigger"
		if caseCode == "P01" && role == "child" {
			outcome = "suppressed_per_notifier"
		}
	} else if caseCode == "P01" && f.isLastPositiveEvent(matching.Labels["oscar_test_event_index"]) {
		outcome = "parent_emitted"
	}
	return []oscar.AuditRecord{{ID: int(crc32.ChecksumIEEE([]byte(fingerprint))), AlertFingerprint: fingerprint, RuleID: 1, RuleName: matching.Labels["oscar_test_rule_name"], Pattern: pattern, Outcome: outcome}}, nil
}

func (f *fakeAPI) NotificationAudit(_ context.Context, fingerprint string, _, _ time.Time) ([]oscar.NotificationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, alert := range f.injected {
		if fingerprint == "server-"+alert.Labels["oscar_test_event_id"] && alert.Labels["oscar_test_case_code"] == "P01" && alert.Labels["oscar_test_alert_role"] == "child" {
			return []oscar.NotificationRecord{{ID: "notification-1", AlertFingerprint: fingerprint, NotifierType: "email", Status: "suppressed", Labels: map[string]string{"oscar_test_run_id": f.effectiveRunID()}}}, nil
		}
	}
	return nil, nil
}

func (f *fakeAPI) effectiveRunID() string {
	if f.runID != "" {
		return f.runID
	}
	return "crt_00000000000000000000000001"
}

func (f *fakeAPI) isLastPositiveEvent(index string) bool {
	want, _ := strconv.Atoi(index)
	for _, alert := range f.injected {
		if alert.Labels["oscar_test_case_code"] != "P01" || strings.EqualFold(alert.Status, "resolved") {
			continue
		}
		candidate, _ := strconv.Atoi(alert.Labels["oscar_test_event_index"])
		if candidate > want {
			return false
		}
	}
	return true
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
