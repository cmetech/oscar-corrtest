package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

// CreateRun inserts a validated run summary.
func (d *Database) CreateRun(ctx context.Context, run domain.Run) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	if err := run.Validate(); err != nil {
		return err
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO runs(
        id, short_token, target_id, scenario_id, status, verdict, cleanup_status,
        harness_version, capability_snapshot_json, compiled_plan_json, canonical_report_json,
        started_at, ended_at, created_at, updated_at, terminal_error
    ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ShortToken, nullable(run.TargetID), nullable(run.ScenarioID), string(run.Status), nullable(string(run.Verdict)),
		string(run.CleanupStatus), run.HarnessVersion, nullableJSON(run.CapabilitySnapshotJSON), nullableJSON(run.CompiledPlanJSON),
		nullableJSON(run.CanonicalReportJSON), nullableTime(run.StartedAt), nullableTime(run.EndedAt), formatTime(run.CreatedAt),
		formatTime(run.UpdatedAt), nullable(run.TerminalError))
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

// GetRun returns one durable run summary.
func (d *Database) GetRun(ctx context.Context, id string) (domain.Run, error) {
	run, err := scanRun(d.db.QueryRowContext(ctx, runSelect+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, ErrNotFound
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("get run: %w", err)
	}
	return run, nil
}

// ListRuns returns newest-first durable history using bound filters.
func (d *Database) ListRuns(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	query := runSelect + ` WHERE 1=1`
	var args []any
	add := func(clause string, value any) {
		query += clause
		args = append(args, value)
	}
	if filter.TargetID != "" {
		add(` AND target_id=?`, filter.TargetID)
	}
	if filter.Status != "" {
		add(` AND status=?`, string(filter.Status))
	}
	if filter.Verdict != "" {
		add(` AND verdict=?`, string(filter.Verdict))
	}
	if filter.CleanupStatus != "" {
		add(` AND cleanup_status=?`, string(filter.CleanupStatus))
	}
	if filter.Pattern != "" {
		add(` AND EXISTS (SELECT 1 FROM run_cases WHERE run_cases.run_id=runs.id AND run_cases.pattern=?)`, filter.Pattern)
	}
	if filter.CreatedAfter != nil {
		add(` AND created_at>=?`, formatTime(*filter.CreatedAfter))
	}
	if filter.CreatedBefore != nil {
		add(` AND created_at<=?`, formatTime(*filter.CreatedBefore))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var runs []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return runs, nil
}

// AppendRunEvent appends an event with a per-run monotonic sequence.
func (d *Database) AppendRunEvent(ctx context.Context, event domain.RunEvent) (domain.RunEvent, error) {
	if err := d.Ready(); err != nil {
		return domain.RunEvent{}, fmt.Errorf("database is read-only: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("begin event: %w", err)
	}
	event, err = appendEvent(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback()
		return domain.RunEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunEvent{}, fmt.Errorf("commit event: %w", err)
	}
	return event, nil
}

// ListRunEvents returns the persisted timeline in sequence order.
func (d *Database) ListRunEvents(ctx context.Context, runID string) ([]domain.RunEvent, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT run_id, case_id, sequence, type, level, occurred_at, summary, detail_json
        FROM run_events WHERE run_id=? ORDER BY sequence`, runID)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	var events []domain.RunEvent
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// TransitionRun atomically changes lifecycle state and records the transition.
func (d *Database) TransitionRun(ctx context.Context, id string, next domain.RunStatus, at time.Time, summary string) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin run transition: %w", err)
	}
	var current domain.RunStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, id).Scan(&current); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read run status: %w", err)
	}
	if !domain.CanTransition(current, next) {
		_ = tx.Rollback()
		return fmt.Errorf("invalid run transition %s -> %s", current, next)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status=?, updated_at=? WHERE id=?`, string(next), formatTime(at), id); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update run status: %w", err)
	}
	detail, _ := json.Marshal(map[string]string{"from": string(current), "to": string(next)})
	if _, err := appendEvent(ctx, tx, domain.RunEvent{
		RunID: id, Type: "run.transition", Level: "info", OccurredAt: at, Summary: summary, DetailJSON: string(detail),
	}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run transition: %w", err)
	}
	return nil
}

// RecoverInterruptedRuns marks active runs interrupted exactly once.
func (d *Database) RecoverInterruptedRuns(ctx context.Context, at time.Time) (int, error) {
	if err := d.Ready(); err != nil {
		return 0, fmt.Errorf("database is read-only: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin interrupted-run recovery: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, status FROM runs WHERE status NOT IN ('INTERRUPTED','COMPLETED') ORDER BY id`)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("list active runs: %w", err)
	}
	type activeRun struct {
		id     string
		status domain.RunStatus
	}
	var active []activeRun
	for rows.Next() {
		var item activeRun
		if err := rows.Scan(&item.id, &item.status); err != nil {
			rows.Close()
			_ = tx.Rollback()
			return 0, fmt.Errorf("scan active run: %w", err)
		}
		active = append(active, item)
	}
	if err := rows.Close(); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	for _, item := range active {
		var resources int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM resources WHERE run_id=? AND deleted_at IS NULL`, item.id).Scan(&resources); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("inspect run resources: %w", err)
		}
		cleanup := domain.CleanupNotRequired
		if resources > 0 {
			cleanup = domain.CleanupUnknown
		}
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='INTERRUPTED', cleanup_status=?, updated_at=? WHERE id=?`, string(cleanup), formatTime(at), item.id); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("interrupt run: %w", err)
		}
		detail, _ := json.Marshal(map[string]string{"previousStatus": string(item.status)})
		if _, err := appendEvent(ctx, tx, domain.RunEvent{
			RunID: item.id, Type: "run.interrupted", Level: "warning", OccurredAt: at,
			Summary: "Run interrupted by process restart", DetailJSON: string(detail),
		}); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit interrupted-run recovery: %w", err)
	}
	return len(active), nil
}

const runSelect = `SELECT id, short_token, target_id, scenario_id, status, verdict, cleanup_status,
    harness_version, capability_snapshot_json, compiled_plan_json, canonical_report_json,
    started_at, ended_at, created_at, updated_at, terminal_error FROM runs`

func appendEvent(ctx context.Context, tx *sql.Tx, event domain.RunEvent) (domain.RunEvent, error) {
	if event.RunID == "" || event.Type == "" || event.Level == "" || event.OccurredAt.IsZero() || event.Summary == "" {
		return domain.RunEvent{}, fmt.Errorf("run event metadata is incomplete")
	}
	if event.DetailJSON != "" && !json.Valid([]byte(event.DetailJSON)) {
		return domain.RunEvent{}, fmt.Errorf("run event detail is not valid JSON")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM run_events WHERE run_id=?`, event.RunID).Scan(&event.Sequence); err != nil {
		return domain.RunEvent{}, fmt.Errorf("allocate event sequence: %w", err)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO run_events(run_id, case_id, sequence, type, level, occurred_at, summary, detail_json)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, event.RunID, nullable(event.CaseID), event.Sequence, event.Type, event.Level,
		formatTime(event.OccurredAt), event.Summary, nullable(event.DetailJSON))
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("append run event: %w", err)
	}
	return event, nil
}

func scanRun(row rowScanner) (domain.Run, error) {
	var run domain.Run
	var targetID, scenarioID, verdict, capability, plan, report, started, ended, terminal sql.NullString
	var created, updated string
	err := row.Scan(&run.ID, &run.ShortToken, &targetID, &scenarioID, &run.Status, &verdict, &run.CleanupStatus,
		&run.HarnessVersion, &capability, &plan, &report, &started, &ended, &created, &updated, &terminal)
	if err != nil {
		return domain.Run{}, err
	}
	run.TargetID, run.ScenarioID, run.Verdict, run.TerminalError = targetID.String, scenarioID.String, domain.Verdict(verdict.String), terminal.String
	run.CapabilitySnapshotJSON = rawJSON(capability)
	run.CompiledPlanJSON = rawJSON(plan)
	run.CanonicalReportJSON = rawJSON(report)
	var parseErr error
	if run.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, created); parseErr != nil {
		return domain.Run{}, parseErr
	}
	if run.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updated); parseErr != nil {
		return domain.Run{}, parseErr
	}
	if run.StartedAt, parseErr = parseNullableTime(started); parseErr != nil {
		return domain.Run{}, parseErr
	}
	if run.EndedAt, parseErr = parseNullableTime(ended); parseErr != nil {
		return domain.Run{}, parseErr
	}
	return run, nil
}

func scanEvent(row rowScanner) (domain.RunEvent, error) {
	var event domain.RunEvent
	var caseID, detail sql.NullString
	var occurred string
	if err := row.Scan(&event.RunID, &caseID, &event.Sequence, &event.Type, &event.Level, &occurred, &event.Summary, &detail); err != nil {
		return domain.RunEvent{}, err
	}
	event.CaseID, event.DetailJSON = caseID.String, detail.String
	parsed, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		return domain.RunEvent{}, err
	}
	event.OccurredAt = parsed
	return event, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func rawJSON(value sql.NullString) json.RawMessage {
	if !value.Valid {
		return nil
	}
	return json.RawMessage(value.String)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
