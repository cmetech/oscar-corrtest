package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// InstalledPackExportSnapshot is one coherent local capture, not scenario PASS.
// A nil Snapshot preserves interruption before input registration.
type InstalledPackExportSnapshot struct {
	Run       domain.Run
	Snapshot  *InstalledPackSnapshot
	Events    []domain.RunEvent
	Artifacts []domain.Artifact
}

// HasInstalledPackRegistration detects retained mode without hydrating evidence.
func (d *Database) HasInstalledPackRegistration(ctx context.Context, runID string) (bool, error) {
	var present bool
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM installed_pack_runs WHERE run_id=?)`, runID).Scan(&present)
	return present, err
}

// ReadInstalledPackExport captures bounded terminal metadata in one transaction.
// The runtime must also exclude in-process execution while capturing files.
func (d *Database) ReadInstalledPackExport(ctx context.Context, runID string) (InstalledPackExportSnapshot, error) {
	var s InstalledPackExportSnapshot
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	// Reject oversized persisted text before transferring it into Go. Outcomes
	// have their own finite budget and do not consume the metadata allowance.
	var bytes int64
	err = tx.QueryRowContext(ctx, `SELECT
	 COALESCE((SELECT length(CAST(COALESCE(id,'')||COALESCE(short_token,'')||COALESCE(target_id,'')||COALESCE(scenario_id,'')||status||COALESCE(verdict,'')||cleanup_status||harness_version||COALESCE(capability_snapshot_json,'')||COALESCE(compiled_plan_json,'')||COALESCE(canonical_report_json,'')||COALESCE(started_at,'')||COALESCE(ended_at,'')||created_at||updated_at||COALESCE(terminal_error,'') AS BLOB)) FROM runs WHERE id=?),0)
	 + COALESCE((SELECT SUM(length(CAST(run_id||COALESCE(case_id,'')||type||level||occurred_at||summary||COALESCE(detail_json,'') AS BLOB))) FROM run_events WHERE run_id=?),0)
	 + COALESCE((SELECT SUM(length(CAST(id||run_id||COALESCE(case_id,'')||kind||relative_path||mime_type||COALESCE(sha256,'')||redaction_state||availability_state||created_at AS BLOB))) FROM artifacts WHERE run_id=?),0)
	 + COALESCE((SELECT SUM(length(CAST(run_id||qualification_token||manifest_sha256||api_target_id||api_base_url||registration_sha256||created_at AS BLOB))) FROM installed_pack_runs WHERE run_id=?),0)
	 + COALESCE((SELECT SUM(length(CAST(run_id||recorded_at||COALESCE(completed_at,'') AS BLOB))) FROM installed_pack_attempts WHERE run_id=?),0)`, runID, runID, runID, runID, runID).Scan(&bytes)
	if err != nil {
		return s, err
	}
	if bytes > 16<<20 {
		return s, fmt.Errorf("installed export metadata exceeds 16 MiB; retained data unchanged")
	}
	var excessive bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM installed_pack_runs WHERE run_id=? AND length(CAST(registration_json AS BLOB))>131072)
	 OR EXISTS(SELECT 1 FROM installed_pack_attempts WHERE run_id=? AND (length(CAST(evidence_json AS BLOB))>16384 OR length(CAST(result_json AS BLOB))>2097152))`, runID, runID).Scan(&excessive)
	if err != nil {
		return s, err
	}
	if excessive {
		return s, fmt.Errorf("installed export registration/outcome exceeds bound")
	}
	s.Run, err = scanRun(tx.QueryRowContext(ctx, runSelect+` WHERE id=?`, runID))
	if err != nil {
		return s, err
	}
	if s.Run.Status != domain.RunCompleted && s.Run.Status != domain.RunInterrupted {
		return s, fmt.Errorf("installed export requires a terminal run")
	}
	snapshot, _, err := readInstalledPack(ctx, tx, runID)
	if err == nil {
		s.Snapshot = &snapshot
	} else if !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	var plan struct {
		InputPath string `json:"inputPath"`
	}
	_ = json.Unmarshal(s.Run.CompiledPlanJSON, &plan)
	if s.Snapshot == nil && s.Run.InputPath() != "installed-test-pack" && plan.InputPath != "installed-test-pack" {
		return s, fmt.Errorf("run has no installed test-pack identity")
	}
	events, err := tx.QueryContext(ctx, `SELECT run_id,case_id,sequence,type,level,occurred_at,summary,detail_json FROM run_events WHERE run_id=? ORDER BY sequence LIMIT 2049`, runID)
	if err != nil {
		return s, err
	}
	defer events.Close()
	for events.Next() {
		if len(s.Events) == 2048 {
			return s, fmt.Errorf("installed export event count exceeds 2048")
		}
		e, eErr := scanEvent(events)
		if eErr != nil {
			return s, eErr
		}
		if s.Snapshot == nil && e.Type == "alert.attempting" {
			return s, fmt.Errorf("input event has no installed registration")
		}
		s.Events = append(s.Events, e)
	}
	err = errors.Join(events.Err(), events.Close())
	if err != nil {
		return s, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,run_id,case_id,kind,relative_path,mime_type,sha256,byte_size,redaction_state,availability_state,created_at FROM artifacts WHERE run_id=? ORDER BY relative_path,id LIMIT 1357`, runID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(s.Artifacts) == 1356 {
			return s, fmt.Errorf("installed export artifact count exceeds 1356")
		}
		a, e := scanArtifact(rows)
		if e != nil {
			return s, e
		}
		if e = a.Validate(); e != nil {
			return s, e
		}
		s.Artifacts = append(s.Artifacts, a)
	}
	if err = rows.Err(); err != nil {
		return s, err
	}
	return s, nil
}
