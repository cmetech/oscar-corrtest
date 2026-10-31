package sqlite

import (
	"context"
	"fmt"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"time"
)

// PrepareRunInput commits both the attempt ledger and cleanup responsibility before external I/O.
func (d *Database) PrepareRunInput(ctx context.Context, id string, at time.Time, detail string) error {
	if err := d.Ready(); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE runs SET cleanup_status='UNKNOWN', updated_at=? WHERE id=? AND status NOT IN ('COMPLETED','INTERRUPTED')`, formatTime(at), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("input preparation requires an active run")
	}
	if _, err = appendEvent(ctx, tx, domain.RunEvent{RunID: id, Type: "alert.prepared", Level: "info", OccurredAt: at, Summary: "External input prepared; cleanup responsibility retained", DetailJSON: detail}); err != nil {
		return err
	}
	return tx.Commit()
}
