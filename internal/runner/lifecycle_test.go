package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestRunnerBindsNamedEpisodeTimesAndReportsDownstreamCoverage(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	doc := scenario.Builtin("persistence")
	doc.Cases[1].Events[0].Source = "A"
	doc.Cases[1].Events[1].Source = "A"
	plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 13, 10, 0, 0, 123456789, time.UTC)
	api := &fakeAPI{}
	engine := runner.New(database, api, runner.Options{Now: func() time.Time { return now }, Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, ObservationWindow: time.Second, Stabilization: time.Millisecond})
	if err := engine.Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_a_audit_only", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	firing, clear := api.injected[1], api.injected[2]
	if firing.StartsAt.Nanosecond()%1000000 != 0 || firing.StartsAt.IsZero() || !firing.StartsAt.Equal(clear.StartsAt) || !clear.EndsAt.Equal(clear.StartsAt.Add(10*time.Second)) {
		t.Fatalf("episode times incorrect: firing=%+v clear=%+v", firing, clear)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Coverage map[string]string `json:"coverage"`
	}
	if err := json.Unmarshal(stored.CanonicalReportJSON, &report); err != nil {
		t.Fatal(err)
	}
	if report.Coverage["inputPath"] != "alert-engine" || report.Coverage["receiverAdmission"] != "NOT_TESTED" || report.Coverage["nativeAlertmanager"] != "BYPASSED" || report.Coverage["lifecycleTransitions"] != "NOT_PROVEN" {
		t.Fatalf("misleading scope: %s", stored.CanonicalReportJSON)
	}
	events, err := database.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared := 0
	for _, event := range events {
		if event.Type == "alert.prepared" {
			prepared++
		}
	}
	if prepared != 2 {
		t.Fatalf("named lifecycle timing evidence not retained: %d", prepared)
	}
}

func TestRunnerRejectsReceiverInputBeforeMutation(t *testing.T) {
	database := openDatabase(t)
	run := newRun()
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	plan.InputPath = "receiver-admission"
	api := &fakeAPI{}
	if err := runner.New(database, api, runner.Options{}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_b_dispatch", Ready: true, LabelsSurvived: true}); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Verdict != domain.VerdictError || api.created != 0 || len(api.injected) != 0 {
		t.Fatalf("unsupported transport mutated target: run=%+v api=%+v", stored, api)
	}
}

type episodeOrderAPI struct {
	fakeAPI
	descending bool
	failAudit  bool
}

func (api *episodeOrderAPI) FindHistory(ctx context.Context, query oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	rows, err := api.fakeAPI.FindHistory(ctx, query)
	if err != nil {
		return nil, err
	}
	var result []oscar.HistoryRecord
	for _, row := range rows {
		if row.Labels["oscar_test_alert_class"] != "source" {
			result = append(result, row)
			continue
		}
		old, newer := row, row
		old.StartsAt = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
		newer.StartsAt = old.StartsAt.Add(time.Second)
		if api.descending {
			result = append(result, newer, old)
		} else {
			result = append(result, old, newer)
		}
	}
	return result, nil
}
func (api *episodeOrderAPI) CorrelationAudit(ctx context.Context, key string) ([]oscar.AuditRecord, error) {
	if api.failAudit {
		return nil, fmt.Errorf("observation failed")
	}
	return api.fakeAPI.CorrelationAudit(ctx, key)
}

func TestCleanupRetainsLatestHistoryEpisodeInEitherRowOrder(t *testing.T) {
	for _, descending := range []bool{false, true} {
		for _, failAudit := range []bool{false, true} {
			t.Run(fmt.Sprintf("descending=%v/failure=%v", descending, failAudit), func(t *testing.T) {
				database := openDatabase(t)
				run := newRun()
				if err := database.CreateRun(context.Background(), run); err != nil {
					t.Fatal(err)
				}
				plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
				if err != nil {
					t.Fatal(err)
				}
				api := &episodeOrderAPI{descending: descending, failAudit: failAudit}
				err = runner.New(database, api, runner.Options{ObservationWindow: time.Millisecond, PollInterval: time.Millisecond, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{PipelineMode: "phase_a_audit_only", Ready: true, LabelsSurvived: true})
				if (err != nil) != failAudit {
					t.Fatalf("unexpected execution error: %v", err)
				}
				checked := 0
				for _, row := range api.resolved {
					if row.Labels["oscar_test_alert_class"] == "source" {
						checked++
						if !row.StartsAt.Equal(time.Date(2026, 10, 13, 10, 0, 1, 0, time.UTC)) {
							t.Fatalf("cleanup selected old firing episode: %v", row.StartsAt)
						}
					}
				}
				if checked != 9 {
					t.Fatalf("expected nine source cleanup targets, got %d", checked)
				}
			})
		}
	}
}
