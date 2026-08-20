package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestFinalizeRunAtomicallyPersistsTerminalFacts(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)
	run := testRun("crt_00000000000000000000000000", now)
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan := json.RawMessage(`{
		"runId":"crt_00000000000000000000000000","pattern":"flood",
		"cases":[{"name":"positive","code":"P01",
			"assertions":[{"kind":"synthetic-alert-count","equals":1}],
			"alerts":[{"name":"CORRTEST_FLOOD_P01_SOURCE_00000000","labels":{"oscar_test_alert_role":"source"},"annotations":{"oscar_test_event_id":"evt-1"}}]}]}`)
	if err := database.SetRunExecutionDocuments(context.Background(), run.ID, json.RawMessage(`{"ready":true}`), plan, now); err != nil {
		t.Fatal(err)
	}
	transitionRunToCleaningUp(t, database, run)
	completed := now.Add(time.Minute)
	facts := domain.ExecutionFacts{
		Cases: []domain.CaseFact{{StableKey: "P01", Verdict: domain.VerdictPass, StartedAt: now, EndedAt: completed,
			Evidence: json.RawMessage(`{"sourceHistory":[{"fingerprint":"server-fingerprint"}]}`),
			Assertions: []domain.AssertionFact{{StableKey: "synthetic-alert-count:1", Kind: "synthetic-alert-count",
				ExpectedJSON: json.RawMessage(`{"equals":1}`), ObservedJSON: json.RawMessage(`{"count":1}`), Verdict: domain.VerdictPass,
				Explanation: "observed 1, expected exactly 1", ObservationStart: now, ObservationEnd: completed}}}},
		Attempts: []domain.AlertAttemptFact{{CaseStableKey: "P01", EventID: "evt-1", EventIndex: 1, SendState: "OBSERVED",
			InjectionClass: "accepted", StatusCode: 200, Fingerprint: "server-fingerprint"}},
	}
	if err := database.FinalizeRun(context.Background(), run.ID, facts, domain.VerdictPass, domain.CleanupClean, json.RawMessage(`{"verdict":"PASS"}`), "", completed); err != nil {
		t.Fatal(err)
	}
	var caseStatus, caseVerdict, assertionVerdict, observed, sendState, fingerprint string
	if err := database.db.QueryRow(`SELECT status,verdict FROM run_cases WHERE run_id=?`, run.ID).Scan(&caseStatus, &caseVerdict); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT verdict,observed_json FROM assertions WHERE run_id=?`, run.ID).Scan(&assertionVerdict, &observed); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT send_state,fingerprint FROM alert_attempts WHERE run_id=?`, run.ID).Scan(&sendState, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if caseStatus != "COMPLETED" || caseVerdict != "PASS" || assertionVerdict != "PASS" || observed != `{"count":1}` || sendState != "OBSERVED" || fingerprint != "server-fingerprint" {
		t.Fatalf("case=%s/%s assertion=%s/%s attempt=%s/%s", caseStatus, caseVerdict, assertionVerdict, observed, sendState, fingerprint)
	}
}

func TestFinalizeRunRollsBackOnUnknownFactIdentity(t *testing.T) {
	database := openRepositoryDatabase(t)
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	run := testRun("crt_00000000000000000000000000", now)
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	transitionRunToCleaningUp(t, database, run)
	facts := domain.ExecutionFacts{Cases: []domain.CaseFact{{StableKey: "UNKNOWN", Verdict: domain.VerdictPass, StartedAt: now, EndedAt: now, Evidence: json.RawMessage(`{}`)}}}
	if err := database.FinalizeRun(context.Background(), run.ID, facts, domain.VerdictPass, domain.CleanupClean, json.RawMessage(`{}`), "", now); err == nil {
		t.Fatal("unknown normalized fact identity was accepted")
	}
	stored, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.RunCleaningUp || stored.Verdict.Valid() {
		t.Fatalf("partial finalization escaped rollback: %+v", stored)
	}
}
