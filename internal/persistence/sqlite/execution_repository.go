package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// FinalizeRun atomically writes normalized execution facts and the terminal
// run envelope. Unknown stable identities fail the entire transaction.
func (d *Database) FinalizeRun(ctx context.Context, id string, facts domain.ExecutionFacts, verdict domain.Verdict, cleanup domain.CleanupStatus, report json.RawMessage, terminalError string, at time.Time) error {
	if !verdict.Valid() || !cleanup.Valid() || !json.Valid(report) || at.IsZero() {
		return fmt.Errorf("run completion metadata is invalid")
	}
	if err := facts.Validate(); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run completion: %w", err)
	}
	defer tx.Rollback()

	for _, item := range facts.Cases {
		result, err := tx.ExecContext(ctx, `UPDATE run_cases SET status='COMPLETED',verdict=?,started_at=?,ended_at=?
			WHERE run_id=? AND stable_key=? AND status='PLANNED'`, string(item.Verdict), formatTime(item.StartedAt), formatTime(item.EndedAt), id, item.StableKey)
		if err != nil || exactlyOne(result) != nil {
			return fmt.Errorf("finalize case %q: unknown or duplicate fact identity", item.StableKey)
		}
		for _, assertion := range item.Assertions {
			result, err = tx.ExecContext(ctx, `UPDATE assertions SET expected_json=?,observed_json=?,verdict=?,explanation=?,observation_start=?,observation_end=?
				WHERE run_id=? AND case_id=? AND stable_key=? AND kind=? AND verdict IS NULL`, string(assertion.ExpectedJSON), string(assertion.ObservedJSON),
				string(assertion.Verdict), assertion.Explanation, formatTime(assertion.ObservationStart), formatTime(assertion.ObservationEnd),
				id, id+":case:"+item.StableKey, assertion.StableKey, assertion.Kind)
			if err != nil || exactlyOne(result) != nil {
				return fmt.Errorf("finalize assertion %q/%q: unknown or duplicate fact identity", item.StableKey, assertion.StableKey)
			}
		}
	}
	for _, attempt := range facts.Attempts {
		result, err := tx.ExecContext(ctx, `UPDATE alert_attempts SET fingerprint=?,send_state=?,injection_class=?,response_status=?,updated_at=?
			WHERE run_id=? AND case_id=? AND event_id=? AND event_index=? AND send_state='PLANNED'`, nullable(attempt.Fingerprint), attempt.SendState,
			attempt.InjectionClass, attempt.StatusCode, formatTime(at), id, id+":case:"+attempt.CaseStableKey, attempt.EventID, attempt.EventIndex)
		if err != nil || exactlyOne(result) != nil {
			return fmt.Errorf("finalize alert attempt %q/%d: unknown or duplicate fact identity", attempt.CaseStableKey, attempt.EventIndex)
		}
	}

	fallback := domain.VerdictError
	if verdict == domain.VerdictInconclusive {
		fallback = domain.VerdictSkipped
	}
	if _, err := tx.ExecContext(ctx, `UPDATE run_cases SET status='COMPLETED',verdict=?,started_at=COALESCE(started_at,?),ended_at=? WHERE run_id=? AND status='PLANNED'`, string(fallback), formatTime(at), formatTime(at), id); err != nil {
		return fmt.Errorf("finalize unexecuted cases: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE assertions SET observed_json='{"reason":"not evaluated"}',verdict=?,explanation='run ended before assertion evaluation',observation_start=?,observation_end=? WHERE run_id=? AND verdict IS NULL`, string(fallback), formatTime(at), formatTime(at), id); err != nil {
		return fmt.Errorf("finalize unexecuted assertions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE alert_attempts SET send_state='NOT_SENT',updated_at=? WHERE run_id=? AND send_state='PLANNED'`, formatTime(at), id); err != nil {
		return fmt.Errorf("finalize unattempted alerts: %w", err)
	}

	result, err := tx.ExecContext(ctx, `UPDATE runs SET status='COMPLETED', verdict=?, cleanup_status=?, canonical_report_json=?, terminal_error=?, ended_at=?, updated_at=?
		WHERE id=? AND status='CLEANING_UP'`, string(verdict), string(cleanup), string(report), nullable(terminalError), formatTime(at), formatTime(at), id)
	if err != nil || exactlyOne(result) != nil {
		return fmt.Errorf("run is not eligible for completion")
	}
	detail, _ := json.Marshal(map[string]string{"from": string(domain.RunCleaningUp), "to": string(domain.RunCompleted)})
	if _, err := appendEvent(ctx, tx, domain.RunEvent{RunID: id, Type: "run.transition", Level: "info", OccurredAt: at, Summary: "Run completed", DetailJSON: string(detail)}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run completion: %w", err)
	}
	return nil
}

func exactlyOne(result interface{ RowsAffected() (int64, error) }) error {
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("affected rows=%d: %w", count, err)
	}
	return nil
}
