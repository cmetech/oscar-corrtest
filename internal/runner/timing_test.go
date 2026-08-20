package runner_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

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
	if slept < 13*time.Second {
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
