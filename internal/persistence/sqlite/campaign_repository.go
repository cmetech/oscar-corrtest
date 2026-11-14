package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/campaign/lane"
	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func (d *Database) CreateCampaign(ctx context.Context, c domain.Campaign) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin campaign create: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO campaigns(id, scenario_id, scenario_hash, custom, trigger, run_by, trigger_identity, status, started_at)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.ID, c.ScenarioID, c.ScenarioHash, c.Custom, c.Trigger, c.RunBy, c.TriggerIdentity, string(c.Status), formatTime(c.StartedAt))
	if err != nil {
		return fmt.Errorf("insert campaign: %w", err)
	}
	if err := campaignEvent(ctx, tx, c.ID, "campaign.created", "info", "campaign created", nil, c.StartedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func campaignEvent(ctx context.Context, tx *sql.Tx, id, typ, level, summary string, detail json.RawMessage, at time.Time) error {
	if id == "" || typ == "" || level == "" || summary == "" || at.IsZero() {
		return fmt.Errorf("campaign event metadata is incomplete")
	}
	if len(detail) > 0 && !json.Valid(detail) {
		return fmt.Errorf("campaign event detail is not valid JSON")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO campaign_events(campaign_id, sequence, type, level, occurred_at, summary, detail_json)
        SELECT ?, COALESCE(MAX(sequence), 0) + 1, ?, ?, ?, ?, ? FROM campaign_events WHERE campaign_id=?`,
		id, typ, level, formatTime(at), summary, nullableJSON(detail), id)
	if err != nil {
		return fmt.Errorf("append campaign event: %w", err)
	}
	return nil
}

func (d *Database) AppendCampaignEvent(ctx context.Context, id, typ, level, summary string, detail json.RawMessage, at time.Time) error {
	return d.campaignTx(ctx, func(tx *sql.Tx) error { return campaignEvent(ctx, tx, id, typ, level, summary, detail, at) })
}

func (d *Database) campaignTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin campaign tx: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *Database) TransitionCampaign(ctx context.Context, id string, next domain.CampaignStatus, at time.Time, summary string) error {
	if next == domain.CampaignCompleted {
		return fmt.Errorf("campaign completes only through CompleteCampaign")
	}
	return d.campaignTx(ctx, func(tx *sql.Tx) error {
		var current domain.CampaignStatus
		if err := tx.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE id=?`, id).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("read campaign status: %w", err)
		}
		if !domain.CanTransitionCampaign(current, next) {
			return fmt.Errorf("invalid campaign transition %s -> %s", current, next)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE campaigns SET status=? WHERE id=?`, string(next), id); err != nil {
			return fmt.Errorf("update campaign status: %w", err)
		}
		detail, _ := json.Marshal(map[string]string{"from": string(current), "to": string(next)})
		return campaignEvent(ctx, tx, id, "campaign.transition", "info", summary, detail, at)
	})
}

func (d *Database) RecordPrecheck(ctx context.Context, id string, r domain.PrecheckResult) error {
	return d.campaignTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO campaign_prechecks(campaign_id, passed, detail_json, recorded_at) VALUES(?, ?, ?, ?)`,
			id, r.Passed, nullableJSON(r.Detail), formatTime(r.At)); err != nil {
			return fmt.Errorf("record precheck: %w", err)
		}
		return nil
	})
}

func (d *Database) LinkCampaignRun(ctx context.Context, campaignID, runID, kind string) error {
	return d.campaignTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO campaign_runs(campaign_id, run_id, kind) VALUES(?, ?, ?)`, campaignID, runID, kind); err != nil {
			return fmt.Errorf("link campaign run: %w", err)
		}
		return nil
	})
}

func (d *Database) RecordMutation(ctx context.Context, m domain.CampaignMutation) (int64, error) {
	if m.RecordedAt.IsZero() {
		m.RecordedAt = time.Now()
	}
	var id int64
	err := d.campaignTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO campaign_mutations(campaign_id, kind, object_id, prior_json, recorded_at) VALUES(?, ?, ?, ?, ?)`,
			m.CampaignID, m.Kind, m.ObjectID, nullableJSON(m.Prior), formatTime(m.RecordedAt))
		if err != nil {
			return fmt.Errorf("record mutation: %w", err)
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

func (d *Database) MarkMutationRestored(ctx context.Context, id int64, at time.Time) error {
	return d.campaignTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE campaign_mutations SET restored_at=? WHERE id=? AND restored_at IS NULL`, formatTime(at), id)
		if err != nil {
			return fmt.Errorf("mark mutation restored: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			var one int
			if tx.QueryRowContext(ctx, `SELECT 1 FROM campaign_mutations WHERE id=?`, id).Scan(&one) == nil {
				return fmt.Errorf("mutation %d already restored", id)
			}
			return ErrNotFound
		}
		return nil
	})
}

func (d *Database) OpenMutations(ctx context.Context, campaignID string) ([]domain.CampaignMutation, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, campaign_id, kind, object_id, prior_json, recorded_at FROM campaign_mutations
        WHERE campaign_id=? AND restored_at IS NULL ORDER BY id`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("list open mutations: %w", err)
	}
	defer rows.Close()
	var out []domain.CampaignMutation
	for rows.Next() {
		var m domain.CampaignMutation
		var prior sql.NullString
		var rec string
		if err := rows.Scan(&m.ID, &m.CampaignID, &m.Kind, &m.ObjectID, &prior, &rec); err != nil {
			return nil, err
		}
		m.Prior = rawJSON(prior)
		if m.RecordedAt, err = time.Parse(time.RFC3339Nano, rec); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *Database) CompleteCampaign(ctx context.Context, id string, v domain.Verdict, cleanup domain.CleanupStatus, comparable bool, scorecard, report json.RawMessage, at time.Time) error {
	return d.campaignTx(ctx, func(tx *sql.Tx) error {
		var current domain.CampaignStatus
		if err := tx.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE id=?`, id).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !domain.CanTransitionCampaign(current, domain.CampaignCompleted) {
			return fmt.Errorf("invalid campaign transition %s -> %s", current, domain.CampaignCompleted)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE campaigns SET status='COMPLETED', verdict=?, cleanup_status=?, comparable=?, scorecard_json=?, report_json=?, ended_at=? WHERE id=?`,
			string(v), string(cleanup), comparable, nullableJSON(scorecard), nullableJSON(report), formatTime(at), id); err != nil {
			return fmt.Errorf("complete campaign: %w", err)
		}
		detail, _ := json.Marshal(map[string]string{"from": string(current), "to": "COMPLETED", "verdict": string(v)})
		return campaignEvent(ctx, tx, id, "campaign.transition", "info", "campaign completed", detail, at)
	})
}

const campaignSelect = `SELECT id, scenario_id, scenario_hash, custom, trigger, run_by, trigger_identity, status, verdict, cleanup_status,
    comparable, started_at, ended_at, scorecard_json, report_json FROM campaigns`

func (d *Database) queryCampaigns(ctx context.Context, where string, args ...any) ([]domain.Campaign, error) {
	rows, err := d.db.QueryContext(ctx, campaignSelect+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	defer rows.Close()
	var out []domain.Campaign
	for rows.Next() {
		var c domain.Campaign
		var verdict, cleanup, ended, score, report sql.NullString
		var started string
		if err := rows.Scan(&c.ID, &c.ScenarioID, &c.ScenarioHash, &c.Custom, &c.Trigger, &c.RunBy, &c.TriggerIdentity, &c.Status,
			&verdict, &cleanup, &c.Comparable, &started, &ended, &score, &report); err != nil {
			return nil, err
		}
		c.Verdict, c.Cleanup = domain.Verdict(verdict.String), domain.CleanupStatus(cleanup.String)
		c.Scorecard, c.Report = rawJSON(score), rawJSON(report)
		if c.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return nil, err
		}
		t, err := parseNullableTime(ended)
		if err != nil {
			return nil, err
		}
		if t != nil {
			c.EndedAt = *t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *Database) ListCampaigns(ctx context.Context, f domain.CampaignFilter) ([]domain.Campaign, error) {
	where, args := "", []any{}
	if f.Status != "" {
		where, args = " WHERE status=?", append(args, string(f.Status))
	}
	where += " ORDER BY started_at DESC, id"
	if f.Limit > 0 {
		where, args = where+" LIMIT ?", append(args, f.Limit)
	}
	return d.queryCampaigns(ctx, where, args...)
}

// InterruptedCampaigns returns every campaign that never completed, for startup recovery.
func (d *Database) InterruptedCampaigns(ctx context.Context) ([]domain.Campaign, error) {
	return d.queryCampaigns(ctx, ` WHERE status <> 'COMPLETED' ORDER BY started_at, id`)
}

// LaneIntent records a send intent before the send. A duplicate (lane, seq) errors.
func (d *Database) LaneIntent(ctx context.Context, laneID string, e lane.Event, at time.Time) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO campaign_lane_ledger(lane_id, seq, kind, identity, severity, instance, scheduled_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		laneID, e.Seq, string(e.Kind), e.Identity, e.Severity, e.Instance, formatTime(at))
	if err != nil {
		return fmt.Errorf("lane intent: %w", err)
	}
	return nil
}

func (d *Database) LaneOutcome(ctx context.Context, laneID string, seq int, o lane.Outcome) error {
	if err := d.Ready(); err != nil {
		return fmt.Errorf("database is read-only: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `UPDATE campaign_lane_ledger SET sent_at=?, status=?, error_class=?, latency_ms=?, missed=?
        WHERE lane_id=? AND seq=? AND sent_at IS NULL`, formatTime(o.SentAt), o.Status, o.ErrorClass, o.LatencyMillis, o.Missed, laneID, seq)
	if err != nil {
		return fmt.Errorf("lane outcome: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("lane outcome: no pending intent for %s/%d", laneID, seq)
	}
	return nil
}

func (d *Database) LaneLedger(ctx context.Context, laneID string) ([]domain.LaneRow, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT lane_id, seq, kind, identity, severity, instance, scheduled_at, sent_at, status, error_class, latency_ms, missed
        FROM campaign_lane_ledger WHERE lane_id=? ORDER BY seq`, laneID)
	if err != nil {
		return nil, fmt.Errorf("lane ledger: %w", err)
	}
	defer rows.Close()
	var out []domain.LaneRow
	for rows.Next() {
		var r domain.LaneRow
		var sched string
		var sent, class sql.NullString
		var status, lat, missed sql.NullInt64
		if err := rows.Scan(&r.LaneID, &r.Seq, &r.Kind, &r.Identity, &r.Severity, &r.Instance, &sched, &sent, &status, &class, &lat, &missed); err != nil {
			return nil, err
		}
		if r.ScheduledAt, err = time.Parse(time.RFC3339Nano, sched); err != nil {
			return nil, err
		}
		if sent.Valid {
			if r.SentAt, err = time.Parse(time.RFC3339Nano, sent.String); err != nil {
				return nil, err
			}
			r.Done = true
		}
		r.Status, r.ErrorClass, r.LatencyMillis, r.Missed = int(status.Int64), class.String, lat.Int64, missed.Int64 == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

type laneLedger struct{ d *Database }

// NewLaneLedger adapts the database to lane.Ledger.
func NewLaneLedger(db *Database) lane.Ledger { return laneLedger{db} }

func (l laneLedger) Intent(ctx context.Context, laneID string, e lane.Event, at time.Time) error {
	return l.d.LaneIntent(ctx, laneID, e, at)
}

func (l laneLedger) Outcome(ctx context.Context, laneID string, seq int, o lane.Outcome) error {
	return l.d.LaneOutcome(ctx, laneID, seq, o)
}

// GetCampaign returns one campaign or ErrNotFound.
func (d *Database) GetCampaign(ctx context.Context, id string) (domain.Campaign, error) {
	list, err := d.queryCampaigns(ctx, ` WHERE id=?`, id)
	if err != nil {
		return domain.Campaign{}, err
	}
	if len(list) == 0 {
		return domain.Campaign{}, ErrNotFound
	}
	return list[0], nil
}

// RecentCampaignEvents returns up to limit newest events, oldest first.
func (d *Database) RecentCampaignEvents(ctx context.Context, id string, limit int) ([]domain.CampaignEvent, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT campaign_id, sequence, type, level, occurred_at, summary, detail_json FROM (
        SELECT * FROM campaign_events WHERE campaign_id=? ORDER BY sequence DESC LIMIT ?) ORDER BY sequence`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list campaign events: %w", err)
	}
	defer rows.Close()
	var out []domain.CampaignEvent
	for rows.Next() {
		var e domain.CampaignEvent
		var at string
		var detail sql.NullString
		if err := rows.Scan(&e.CampaignID, &e.Sequence, &e.Type, &e.Level, &at, &e.Summary, &detail); err != nil {
			return nil, err
		}
		if e.OccurredAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		e.Detail = rawJSON(detail)
		out = append(out, e)
	}
	return out, rows.Err()
}
