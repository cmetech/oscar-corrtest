package runner_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestNegativeFailsWhenParentAppearsBeforeDeadline(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Cases {
		plan.Cases[index].ObservationWindow = 3 * time.Second
	}
	now := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	api := &timingAPI{runID: run.ID, now: func() time.Time { return now }, lateNegativeParentAt: now.Add(2 * time.Second)}
	sleep := func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil }
	if err := runner.New(database, api, runner.Options{Now: api.now, Sleep: sleep, PollInterval: time.Second, ObservationWindow: 3 * time.Second, Stabilization: time.Second}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictFail {
		t.Fatalf("late negative parent produced verdict %s; report=%s", stored.Verdict, stored.CanonicalReportJSON)
	}
}

func TestPositivePassesWhenAuditFlushesLate(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Cases {
		plan.Cases[index].ObservationWindow = 4 * time.Second
	}
	now := time.Date(2026, 8, 20, 3, 0, 0, 0, time.UTC)
	api := &timingAPI{runID: run.ID, now: func() time.Time { return now }, latePositiveAuditAt: now.Add(2 * time.Second)}
	sleep := func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil }
	if err := runner.New(database, api, runner.Options{Now: api.now, Sleep: sleep, PollInterval: time.Second, ObservationWindow: 4 * time.Second, Stabilization: time.Second}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictPass {
		t.Fatalf("late audit produced verdict %s; report=%s", stored.Verdict, stored.CanonicalReportJSON)
	}
}

func TestRunnerUsesDeclaredAssertionValues(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	plan.Cases[0].Assertions[1].Equals = 7
	api := &fakeAPI{runID: run.ID}
	if err := runner.New(database, api, runner.Options{PollInterval: time.Millisecond, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond, Sleep: func(context.Context, time.Duration) error { return nil }}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictFail {
		t.Fatalf("changed assertion was ignored: verdict=%s report=%s", stored.Verdict, stored.CanonicalReportJSON)
	}
}

type timingAPI struct {
	fakeAPI
	runID                string
	now                  func() time.Time
	lateNegativeParentAt time.Time
	latePositiveAuditAt  time.Time
}

func (api *timingAPI) FindHistory(_ context.Context, query oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	if strings.Contains(query.AlertName, "SYNTHETIC") {
		if strings.Contains(query.AlertName, "_P01_") || (!api.lateNegativeParentAt.IsZero() && !api.now().Before(api.lateNegativeParentAt)) {
			return []oscar.HistoryRecord{{ID: "parent", AlertName: query.AlertName, Fingerprint: "parent-" + query.AlertName, CreatedAt: api.now(), Labels: map[string]string{"oscar_test_run_id": api.runID}}}, nil
		}
		return nil, nil
	}
	api.fakeAPI.mu.Lock()
	defer api.fakeAPI.mu.Unlock()
	byEvent := map[string]oscar.HistoryRecord{}
	for _, alert := range api.fakeAPI.injected {
		if alert.Name != query.AlertName {
			continue
		}
		eventID := alert.Labels["oscar_test_event_id"]
		labels := make(map[string]string, len(alert.Labels))
		for key, value := range alert.Labels {
			labels[key] = value
		}
		byEvent[eventID] = oscar.HistoryRecord{ID: "source-" + eventID, AlertName: query.AlertName, Fingerprint: "server-" + eventID, Status: alert.Status, CreatedAt: api.now(), Labels: labels}
	}
	result := make([]oscar.HistoryRecord, 0, len(byEvent))
	for _, record := range byEvent {
		result = append(result, record)
	}
	return result, nil
}

func (api *timingAPI) CorrelationAudit(_ context.Context, fingerprint string) ([]oscar.AuditRecord, error) {
	outcome := "enriched"
	if strings.Contains(fingerprint, "-P01-005") && (api.latePositiveAuditAt.IsZero() || !api.now().Before(api.latePositiveAuditAt)) {
		outcome = "parent_emitted"
	}
	id := 1
	if len(fingerprint) >= 3 {
		if parsed, err := strconv.Atoi(fingerprint[len(fingerprint)-3:]); err == nil {
			id = parsed
		}
	}
	ruleName := ""
	api.fakeAPI.mu.Lock()
	for _, alert := range api.fakeAPI.injected {
		if fingerprint == "server-"+alert.Labels["oscar_test_event_id"] {
			ruleName = alert.Labels["oscar_test_rule_name"]
			break
		}
	}
	api.fakeAPI.mu.Unlock()
	return []oscar.AuditRecord{{ID: id, AlertFingerprint: fingerprint, RuleID: 1, RuleName: ruleName, Pattern: "flood", Outcome: outcome}}, nil
}

func TestTimerStimulusScheduleIsDurableBeforeWaiting(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("persistence"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	var slept time.Duration
	clockNow := func() time.Time { return now }
	sleep := func(_ context.Context, duration time.Duration) error {
		slept += duration
		now = now.Add(duration)
		return nil
	}
	api := &fakeAPI{runID: run.ID}
	if err := runner.New(database, api, runner.Options{Now: clockNow, Sleep: sleep, PollInterval: time.Second, ObservationWindow: 2 * time.Second, Stabilization: time.Second}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	if slept < 10*time.Second {
		t.Fatalf("timer schedule was not honored: slept=%s", slept)
	}
	events, err := database.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var scheduled bool
	for _, event := range events {
		if event.Type != "alert.scheduled" {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(event.DetailJSON), &detail); err != nil {
			t.Fatal(err)
		}
		if detail["delay"] == "10s" && detail["status"] == "resolved" {
			scheduled = true
		}
	}
	if !scheduled {
		t.Fatalf("resolved schedule missing from events: %+v", events)
	}
	stored, _ := database.GetRun(context.Background(), run.ID)
	if stored.Verdict != domain.VerdictPass {
		t.Fatalf("run=%+v", stored)
	}
}

func TestPlanMaxDurationStopsInjectionAndRunsDetachedCleanup(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	plan.MaxDuration = 2 * time.Second
	plan.Cases[0].Alerts[0].Delay = 3 * time.Second
	now := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	clockNow := func() time.Time { return now }
	sleep := func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil }
	api := &fakeAPI{runID: run.ID}
	err = runner.New(database, api, runner.Options{Now: clockNow, Sleep: sleep, PollInterval: time.Second, CleanupTimeout: time.Second}).Execute(
		context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true})
	if err == nil || !strings.Contains(err.Error(), "maximum duration") {
		t.Fatalf("duration overrun err=%v", err)
	}
	stored, getErr := database.GetRun(context.Background(), run.ID)
	if getErr != nil || stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupClean || len(api.deleted) != 2 {
		t.Fatalf("stored=%+v deleted=%v getErr=%v", stored, api.deleted, getErr)
	}
}
