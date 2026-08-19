package domain

import (
	"testing"
	"time"
)

func TestRunStatusTransitions(t *testing.T) {
	allowed := [][2]RunStatus{
		{RunQueued, RunPreflight}, {RunPreflight, RunSettingUp}, {RunSettingUp, RunInjecting},
		{RunInjecting, RunObserving}, {RunObserving, RunAsserting}, {RunAsserting, RunCleaningUp},
		{RunCleaningUp, RunCompleted}, {RunObserving, RunCancelling}, {RunCancelling, RunCleaningUp},
		{RunInterrupted, RunRecovering}, {RunRecovering, RunCleaningUp}, {RunInjecting, RunInterrupted},
	}
	for _, transition := range allowed {
		if !CanTransition(transition[0], transition[1]) {
			t.Errorf("expected %s -> %s", transition[0], transition[1])
		}
	}
	disallowed := [][2]RunStatus{
		{RunCompleted, RunQueued}, {RunQueued, RunCompleted}, {RunInterrupted, RunInjecting},
		{RunObserving, RunSettingUp}, {RunCleaningUp, RunObserving},
	}
	for _, transition := range disallowed {
		if CanTransition(transition[0], transition[1]) {
			t.Errorf("unexpected %s -> %s", transition[0], transition[1])
		}
	}
}

func TestVerdictAndCleanupAreIndependent(t *testing.T) {
	now := time.Date(2026, 8, 19, 20, 0, 0, 0, time.UTC)
	run := Run{
		ID: "crt_00000000000000000000000000", ShortToken: "00000000", Status: RunCompleted,
		Verdict: VerdictPass, CleanupStatus: CleanupDirty, HarnessVersion: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("PASS with DIRTY cleanup rejected: %v", err)
	}
}
