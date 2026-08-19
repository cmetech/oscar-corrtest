package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// RunStatus is the durable execution lifecycle, independent of verdict.
type RunStatus string

const (
	RunQueued      RunStatus = "QUEUED"
	RunPreflight   RunStatus = "PREFLIGHT"
	RunSettingUp   RunStatus = "SETTING_UP"
	RunInjecting   RunStatus = "INJECTING"
	RunObserving   RunStatus = "OBSERVING"
	RunAsserting   RunStatus = "ASSERTING"
	RunCancelling  RunStatus = "CANCELLING"
	RunCleaningUp  RunStatus = "CLEANING_UP"
	RunInterrupted RunStatus = "INTERRUPTED"
	RunRecovering  RunStatus = "RECOVERING"
	RunCompleted   RunStatus = "COMPLETED"
)

// Verdict is the behavioral result and does not encode cleanup state.
type Verdict string

const (
	VerdictPass         Verdict = "PASS"
	VerdictFail         Verdict = "FAIL"
	VerdictInconclusive Verdict = "INCONCLUSIVE"
	VerdictError        Verdict = "ERROR"
	VerdictSkipped      Verdict = "SKIPPED"
)

// CleanupStatus records resource cleanup independently of behavioral verdict.
type CleanupStatus string

const (
	CleanupClean       CleanupStatus = "CLEAN"
	CleanupDirty       CleanupStatus = "DIRTY"
	CleanupNotRequired CleanupStatus = "NOT_REQUIRED"
	CleanupUnknown     CleanupStatus = "UNKNOWN"
)

// Run is the durable summary of one correlation test execution.
type Run struct {
	ID                     string          `json:"id"`
	ShortToken             string          `json:"shortToken"`
	TargetID               string          `json:"targetId,omitempty"`
	ScenarioID             string          `json:"scenarioId,omitempty"`
	Status                 RunStatus       `json:"status"`
	Verdict                Verdict         `json:"verdict,omitempty"`
	CleanupStatus          CleanupStatus   `json:"cleanupStatus"`
	HarnessVersion         string          `json:"harnessVersion"`
	CapabilitySnapshotJSON json.RawMessage `json:"capabilitySnapshot,omitempty"`
	CompiledPlanJSON       json.RawMessage `json:"compiledPlan,omitempty"`
	CanonicalReportJSON    json.RawMessage `json:"canonicalReport,omitempty"`
	StartedAt              *time.Time      `json:"startedAt,omitempty"`
	EndedAt                *time.Time      `json:"endedAt,omitempty"`
	CreatedAt              time.Time       `json:"createdAt"`
	UpdatedAt              time.Time       `json:"updatedAt"`
	TerminalError          string          `json:"terminalError,omitempty"`
}

// RunEvent is one append-only lifecycle observation.
type RunEvent struct {
	RunID      string    `json:"runId"`
	CaseID     string    `json:"caseId,omitempty"`
	Sequence   int64     `json:"sequence"`
	Type       string    `json:"type"`
	Level      string    `json:"level"`
	OccurredAt time.Time `json:"occurredAt"`
	Summary    string    `json:"summary"`
	DetailJSON string    `json:"detail,omitempty"`
}

// RunFilter contains bound-value history filters.
type RunFilter struct {
	TargetID      string
	Status        RunStatus
	Verdict       Verdict
	CleanupStatus CleanupStatus
	Pattern       string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	Limit         int
}

// Validate verifies enum and identity invariants while keeping verdict and cleanup independent.
func (run Run) Validate() error {
	if run.ID == "" || len(run.ShortToken) != 8 {
		return fmt.Errorf("run identity is invalid")
	}
	if !run.Status.Valid() {
		return fmt.Errorf("run status %q is invalid", run.Status)
	}
	if run.Verdict != "" && !run.Verdict.Valid() {
		return fmt.Errorf("run verdict %q is invalid", run.Verdict)
	}
	if !run.CleanupStatus.Valid() {
		return fmt.Errorf("cleanup status %q is invalid", run.CleanupStatus)
	}
	if run.HarnessVersion == "" || run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() {
		return fmt.Errorf("run metadata is incomplete")
	}
	return nil
}

// Valid reports whether the status belongs to the documented lifecycle.
func (status RunStatus) Valid() bool {
	switch status {
	case RunQueued, RunPreflight, RunSettingUp, RunInjecting, RunObserving, RunAsserting,
		RunCancelling, RunCleaningUp, RunInterrupted, RunRecovering, RunCompleted:
		return true
	default:
		return false
	}
}

// Active reports whether process loss should convert the status to INTERRUPTED.
func (status RunStatus) Active() bool {
	return status.Valid() && status != RunInterrupted && status != RunCompleted
}

// Valid reports whether the verdict is documented.
func (verdict Verdict) Valid() bool {
	switch verdict {
	case VerdictPass, VerdictFail, VerdictInconclusive, VerdictError, VerdictSkipped:
		return true
	default:
		return false
	}
}

// Valid reports whether the cleanup state is documented.
func (status CleanupStatus) Valid() bool {
	switch status {
	case CleanupClean, CleanupDirty, CleanupNotRequired, CleanupUnknown:
		return true
	default:
		return false
	}
}

// CanTransition applies the explicit lifecycle state machine.
func CanTransition(from, to RunStatus) bool {
	if to == RunInterrupted && from.Active() {
		return true
	}
	allowed := map[RunStatus][]RunStatus{
		RunQueued:      {RunPreflight, RunCancelling},
		RunPreflight:   {RunSettingUp, RunCancelling},
		RunSettingUp:   {RunInjecting, RunCancelling},
		RunInjecting:   {RunObserving, RunCancelling},
		RunObserving:   {RunAsserting, RunCancelling},
		RunAsserting:   {RunCleaningUp, RunCancelling},
		RunCancelling:  {RunCleaningUp},
		RunCleaningUp:  {RunCompleted},
		RunInterrupted: {RunRecovering},
		RunRecovering:  {RunCleaningUp},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
