package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// ExistingFeedAttempt retains UNKNOWN when Result is nil. An acknowledged result
// never proves Native/History observation, ownership, or source cleanup.
type ExistingFeedAttempt struct {
	Evidence       oscar.ExistingFeedRequestEvidence `json:"evidence"`
	Result         *oscar.InjectionResult            `json:"result,omitempty"`
	TransportError string                            `json:"transportError,omitempty"`
}

type existingFeedResult struct {
	BodySHA256 string                `json:"bodySha256"`
	Result     oscar.InjectionResult `json:"result"`
	Error      string                `json:"error,omitempty"`
}

// RegisterExistingFeed freezes one owned-lab fixture binding. The caller must
// independently establish the owned receiver and installed immutable snapshot.
func (d *Database) RegisterExistingFeed(ctx context.Context, runID string, spec oscar.ExistingFeedManifestSpec, at time.Time) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("existing-feed recording time required")
	}
	if _, err := oscar.FreezeExistingFeedManifest(spec); err != nil {
		return err
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status domain.RunStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil {
		return err
	}
	if status != domain.RunQueued && status != domain.RunPreflight && status != domain.RunSettingUp {
		return fmt.Errorf("existing-feed registration requires a run before injection")
	}
	var installed int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM installed_pack_runs WHERE run_id=?`, runID).Scan(&installed); err != nil {
		return err
	}
	if installed != 0 {
		return fmt.Errorf("run already has an installed-pack contract")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO existing_feeds(run_id,profile,cohort_id,receiver_origin,manifest_json,created_at) VALUES(?,?,?,?,?,?)`, runID, spec.Profile, spec.CohortID, spec.ReceiverOrigin, string(encoded), formatTime(at)); err != nil {
		return fmt.Errorf("freeze unique existing-feed binding: %w", err)
	}
	return tx.Commit()
}

// RecordExistingFeedAttempt commits complete canonical evidence, an input
// responsibility event and UNKNOWN cleanup atomically before the HTTP callback returns.
func (d *Database) RecordExistingFeedAttempt(ctx context.Context, runID string, evidence oscar.ExistingFeedRequestEvidence, at time.Time) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("existing-feed recording time required")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	manifest, attempts, err := readExistingFeed(ctx, tx, runID, evidence.Manifest.Profile)
	if err != nil {
		return err
	}
	expected, _, err := manifest.Prepare(evidence.FixtureIndex)
	if err != nil {
		return err
	}
	actualJSON, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		return fmt.Errorf("existing-feed evidence differs from frozen request or identity")
	}
	if evidence.FixtureIndex != len(attempts) {
		return fmt.Errorf("existing-feed fixture already attempted or out of order")
	}
	for _, attempt := range attempts {
		if attempt.Result == nil || attempt.Result.Class != oscar.InjectionForwarded || attempt.TransportError != "" {
			return fmt.Errorf("existing-feed prior attempt is unresolved or not forwarded")
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE runs SET cleanup_status='UNKNOWN',updated_at=? WHERE id=? AND status='INJECTING'`, formatTime(at), runID)
	if err != nil {
		return err
	}
	if err = exactlyOne(result); err != nil {
		return fmt.Errorf("existing-feed run is not injecting: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO existing_feed_attempts(run_id,profile,fixture_index,evidence_json,recorded_at) VALUES(?,?,?,?,?)`, runID, evidence.Manifest.Profile, evidence.FixtureIndex, string(actualJSON), formatTime(at)); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, domain.RunEvent{RunID: runID, Type: "alert.attempting", Level: "info", OccurredAt: at, Summary: "Owned existing-feed request committed before send", DetailJSON: string(actualJSON)}); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteExistingFeedAttempt adds one response outcome without discharging
// cleanup responsibility. Missing, failed or ambiguous results never permit replay.
func (d *Database) CompleteExistingFeedAttempt(ctx context.Context, runID, profile string, index int, bodySHA256 string, result oscar.InjectionResult, transportError string, at time.Time) error {
	if err := d.Ready(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("existing-feed result time required")
	}
	saved := existingFeedResult{BodySHA256: bodySHA256, Result: result, Error: transportError}
	if err := validateExistingFeedResult(saved); err != nil {
		return err
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, attempts, err := readExistingFeed(ctx, tx, runID, profile)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(attempts) || attempts[index].Result != nil || attempts[index].Evidence.Request.BodySHA256 != bodySHA256 {
		return fmt.Errorf("existing-feed result has no matching unresolved attempt")
	}
	updated, err := tx.ExecContext(ctx, `UPDATE existing_feed_attempts SET result_json=?,completed_at=? WHERE run_id=? AND profile=? AND fixture_index=? AND result_json IS NULL`, string(encoded), formatTime(at), runID, profile, index)
	if err != nil {
		return err
	}
	if err = exactlyOne(updated); err != nil {
		return err
	}
	return tx.Commit()
}

// ListExistingFeedAttempts validates all retained bytes against the frozen
// manifest. Corrupt evidence is an error; an absent result remains visible UNKNOWN.
func (d *Database) ListExistingFeedAttempts(ctx context.Context, runID, profile string) ([]ExistingFeedAttempt, error) {
	if err := d.Ready(); err != nil {
		return nil, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, attempts, err := readExistingFeed(ctx, tx, runID, profile)
	return attempts, err
}

// ExistingFeedSnapshot is one validated transactional view for evidence export.
// Missing Result remains UNKNOWN; absence of an attempt is never synthesized.
type ExistingFeedSnapshot struct {
	Manifest oscar.ExistingFeedManifestSpec `json:"manifest"`
	Attempts []ExistingFeedAttempt          `json:"attempts"`
}

func (d *Database) ReadExistingFeedSnapshot(ctx context.Context, runID, profile string) (ExistingFeedSnapshot, error) {
	var snapshot ExistingFeedSnapshot
	if err := d.Ready(); err != nil {
		return snapshot, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return snapshot, err
	}
	defer tx.Rollback()
	manifest, attempts, err := readExistingFeed(ctx, tx, runID, profile)
	if err != nil {
		return snapshot, err
	}
	prepared, _, err := manifest.Prepare(0)
	if err != nil {
		return snapshot, err
	}
	if attempts == nil {
		attempts = []ExistingFeedAttempt{}
	}
	return ExistingFeedSnapshot{Manifest: prepared.Manifest, Attempts: attempts}, nil
}

func readExistingFeed(ctx context.Context, tx *sql.Tx, runID, profile string) (oscar.ExistingFeedManifest, []ExistingFeedAttempt, error) {
	var raw, cohort, origin string
	var zero oscar.ExistingFeedManifest
	if err := tx.QueryRowContext(ctx, `SELECT manifest_json,cohort_id,receiver_origin FROM existing_feeds WHERE run_id=? AND profile=?`, runID, profile).Scan(&raw, &cohort, &origin); err != nil {
		return zero, nil, err
	}
	var spec oscar.ExistingFeedManifestSpec
	if len(raw) > 2<<20 || json.Unmarshal([]byte(raw), &spec) != nil {
		return zero, nil, fmt.Errorf("corrupt existing-feed manifest")
	}
	canonical, _ := json.Marshal(spec)
	if string(canonical) != raw || spec.Profile != profile || spec.CohortID != cohort || spec.ReceiverOrigin != origin {
		return zero, nil, fmt.Errorf("changed existing-feed manifest binding")
	}
	manifest, err := oscar.FreezeExistingFeedManifest(spec)
	if err != nil {
		return zero, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT fixture_index,evidence_json,result_json FROM existing_feed_attempts WHERE run_id=? AND profile=? ORDER BY fixture_index`, runID, profile)
	if err != nil {
		return zero, nil, err
	}
	defer rows.Close()
	var attempts []ExistingFeedAttempt
	for rows.Next() {
		var index int
		var evidenceJSON string
		var resultJSON sql.NullString
		if err := rows.Scan(&index, &evidenceJSON, &resultJSON); err != nil {
			return zero, nil, err
		}
		if index != len(attempts) || index > 5 {
			return zero, nil, fmt.Errorf("existing-feed journal has an index gap")
		}
		evidence, _, err := manifest.Prepare(index)
		if err != nil {
			return zero, nil, err
		}
		canonical, _ := json.Marshal(evidence)
		if string(canonical) != evidenceJSON {
			return zero, nil, fmt.Errorf("corrupt existing-feed request evidence")
		}
		attempt := ExistingFeedAttempt{Evidence: evidence}
		if resultJSON.Valid {
			var saved existingFeedResult
			if len(resultJSON.String) > 2<<20 || json.Unmarshal([]byte(resultJSON.String), &saved) != nil {
				return zero, nil, fmt.Errorf("corrupt existing-feed result")
			}
			canonical, _ := json.Marshal(saved)
			if string(canonical) != resultJSON.String || saved.BodySHA256 != evidence.Request.BodySHA256 {
				return zero, nil, fmt.Errorf("changed existing-feed result binding")
			}
			if err := validateExistingFeedResult(saved); err != nil {
				return zero, nil, err
			}
			attempt.Result, attempt.TransportError = &saved.Result, saved.Error
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return zero, nil, err
	}
	return manifest, attempts, nil
}

func validateExistingFeedResult(saved existingFeedResult) error {
	if len(saved.Result.Body) > 1<<20 || len(saved.Error) > 4096 || saved.Result.StatusCode < 0 || saved.Result.StatusCode > 599 {
		return fmt.Errorf("existing-feed result exceeds bounds")
	}
	var response struct {
		Status string `json:"status"`
	}
	switch saved.Result.Class {
	case oscar.InjectionForwarded, oscar.InjectionRejected:
		status := "ok"
		if saved.Result.Class == oscar.InjectionRejected {
			status = "acl_denied"
		}
		if saved.Error != "" || saved.Result.StatusCode != 200 || json.Unmarshal(saved.Result.Body, &response) != nil || response.Status != status {
			return fmt.Errorf("existing-feed result conflicts with classification")
		}
	case oscar.InjectionIndeterminate:
	default:
		return fmt.Errorf("unsupported existing-feed result classification")
	}
	return nil
}
