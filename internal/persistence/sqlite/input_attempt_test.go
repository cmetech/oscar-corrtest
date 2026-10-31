package sqlite

import (
	"context"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"testing"
	"time"
)

func TestPreparedInputRetainsCleanupResponsibilityOnCrashOrResultFailure(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "preflight-crash", true: "send-result-write-failure"}[interrupted], func(t *testing.T) {
			d := openRepositoryDatabase(t)
			ctx := context.Background()
			now := time.Now().UTC()
			run := testRun("crt_00000000000000000000000000", now)
			run.Status = domain.RunPreflight
			if err := d.CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			if err := d.PrepareRunInput(ctx, run.ID, now, `{"kind":"problem","episode":0}`); err != nil {
				t.Fatal(err)
			}
			if interrupted {
				if err := d.TransitionRun(ctx, run.ID, domain.RunInterrupted, now, "result write failed"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := d.RecoverInterruptedRuns(ctx, now); err != nil {
					t.Fatal(err)
				}
			}
			got, err := d.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.CleanupStatus != domain.CleanupUnknown || got.Status != domain.RunInterrupted {
				t.Fatalf("lost responsibility: %+v", got)
			}
			if err := d.DeleteTerminalRun(ctx, run.ID); err == nil {
				t.Fatal("unresolved source evidence deleted")
			}
		})
	}
}

func TestPreparedInputRollsBackIfLedgerCannotBeWritten(t *testing.T) {
	d := openRepositoryDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := testRun("crt_00000000000000000000000000", now)
	run.Status = domain.RunPreflight
	if err := d.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`CREATE TRIGGER reject_preparation BEFORE INSERT ON run_events WHEN NEW.type='alert.prepared' BEGIN SELECT RAISE(ABORT,'simulated storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.PrepareRunInput(ctx, run.ID, now, `{"kind":"problem"}`); err == nil {
		t.Fatal("unrecorded send authorized")
	}
	got, err := d.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CleanupStatus != run.CleanupStatus {
		t.Fatal("partial transaction committed")
	}
	var count int
	if err := d.db.QueryRow(`SELECT count(*) FROM run_events WHERE type='alert.prepared'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial ledger %d: %v", count, err)
	}
}
