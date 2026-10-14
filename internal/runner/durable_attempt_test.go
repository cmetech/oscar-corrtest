package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

type attemptStore struct {
	runner.Store
	fail bool
}

func (s attemptStore) AppendRunEvent(ctx context.Context, e domain.RunEvent) (domain.RunEvent, error) {
	if e.Type == "alert.attempting" && s.fail {
		return domain.RunEvent{}, errors.New("attempt persistence failed")
	}
	return s.Store.AppendRunEvent(ctx, e)
}

type attemptCheckingAPI struct {
	fakeAPI
	check func(compiler.AlertPlan)
}

func (a *attemptCheckingAPI) InjectRecorded(ctx context.Context, alert compiler.AlertPlan, record oscar.InjectionRecorder) (oscar.InjectionResult, error) {
	evidence, _, err := oscar.PrepareInjection(alert, "test-model", "https://model.invalid/api/v1/alerts")
	if err != nil {
		return oscar.InjectionResult{}, err
	}
	if err := record(evidence); err != nil {
		return oscar.InjectionResult{}, err
	}
	return a.Inject(ctx, alert)
}

func (a *attemptCheckingAPI) Inject(ctx context.Context, alert compiler.AlertPlan) (oscar.InjectionResult, error) {
	a.check(alert)
	return a.fakeAPI.Inject(ctx, alert)
}
func TestInputAttemptIsCommittedBeforePOST(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "append fails"}[fail], func(t *testing.T) {
			db := openDatabase(t)
			run := newRun()
			ctx := context.Background()
			if err := db.CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			doc := scenario.Builtin("persistence")
			doc.Cases[1].Events[0].Source = "A"
			doc.Cases[1].Events[1].Source = "A"
			plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			api := &attemptCheckingAPI{}
			api.check = func(alert compiler.AlertPlan) {
				events, err := db.ListRunEvents(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				var latest domain.AlertAttemptFact
				for _, event := range events {
					if event.Type == "alert.attempting" {
						if err := json.Unmarshal([]byte(event.DetailJSON), &latest); err != nil {
							t.Fatal(err)
						}
					}
				}
				if latest.EventID != alert.Labels["oscar_test_event_id"] || latest.SendState != "ATTEMPTED" || latest.InjectionClass != "indeterminate" || !latest.StartsAt.Equal(alert.StartsAt) {
					t.Fatalf("POST preceded durable attempt: %+v", latest)
				}
			}
			now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			err = runner.New(attemptStore{Store: db, fail: fail}, api, runner.Options{Now: func() time.Time { return now }, Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if fail {
				if err == nil || len(api.injected) != 0 {
					t.Fatalf("append failure sent input: count=%d err=%v", len(api.injected), err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
