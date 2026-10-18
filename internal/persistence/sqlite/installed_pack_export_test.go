//go:build linux || darwin

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func installedExportRun(t *testing.T, db *Database) domain.Run {
	t.Helper()
	run := testRun("crt_01234567000000000000000000", time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	run.Status = domain.RunInterrupted
	run.CompiledPlanJSON = json.RawMessage(fmt.Sprintf(`{"runId":%q,"inputPath":"installed-test-pack","pattern":"flood"}`, run.ID))
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestInstalledExportCaptureRetainsInterruptedUnregisteredRun(t *testing.T) {
	db := openRepositoryDatabase(t)
	run := installedExportRun(t, db)
	a := domain.Artifact{ID: "partial", RunID: run.ID, Kind: "installed-pack", RelativePath: "runs/" + run.ID + "/installed/original/attempt.json", MIMEType: "application/octet-stream", RedactionState: "private-original-response", Availability: domain.ArtifactPending, CreatedAt: run.CreatedAt}
	if err := db.CreateArtifactPending(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	s, err := db.ReadInstalledPackExport(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Snapshot != nil || len(s.Artifacts) != 1 || s.Artifacts[0].Availability != domain.ArtifactPending || len(s.Run.CanonicalReportJSON) != 0 || s.Run.Status != domain.RunInterrupted {
		t.Fatalf("rewritten incomplete evidence: %+v", s)
	}
}

func TestInstalledExportCaptureRetainsUnknownAttempt(t *testing.T) {
	db := openRepositoryDatabase(t)
	run, _, _, spec := installedRun(t, db, true)
	ctx := context.Background()
	if err := db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, spec, 0), spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecoverInterruptedRuns(ctx, spec.StartsAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s, err := db.ReadInstalledPackExport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Snapshot == nil || len(s.Snapshot.Attempts) != 1 || s.Snapshot.Attempts[0].Result != nil || s.Run.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("uncertain responsibility lost: %+v", s)
	}
}

func TestInstalledExportCaptureRejectsUnboundedOrForeignState(t *testing.T) {
	for _, kind := range []string{"ordinary", "active", "events", "artifacts", "metadata", "outcome", "unregistered-attempt-event", "corrupt-registration"} {
		t.Run(kind, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			run := installedExportRun(t, db)
			var err error
			switch kind {
			case "ordinary":
				_, err = db.db.Exec(`UPDATE runs SET compiled_plan_json=NULL WHERE id=?`, run.ID)
			case "active":
				_, err = db.db.Exec(`UPDATE runs SET status='QUEUED' WHERE id=?`, run.ID)
			case "events":
				_, err = db.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<2049) INSERT INTO run_events(run_id,sequence,type,level,occurred_at,summary) SELECT ?,x,'test','info',?,'event' FROM n`, run.ID, formatTime(run.CreatedAt))
			case "artifacts":
				_, err = db.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1357) INSERT INTO artifacts(id,run_id,kind,relative_path,mime_type,redaction_state,availability_state,created_at) SELECT 'a'||x,?,'installed-pack','runs/x/'||x,'application/octet-stream','private','PENDING',? FROM n`, run.ID, formatTime(run.CreatedAt))
			case "metadata":
				_, err = db.db.Exec(`UPDATE runs SET terminal_error=? WHERE id=?`, strings.Repeat("x", (16<<20)+1), run.ID)
			case "unregistered-attempt-event":
				_, err = db.AppendRunEvent(context.Background(), domain.RunEvent{RunID: run.ID, Type: "alert.attempting", Level: "info", OccurredAt: run.CreatedAt, Summary: "unbound attempt", DetailJSON: `{}`})
			case "outcome", "corrupt-registration":
				// Reuse a separately valid registration so corruption cannot be treated as absence.
				if _, err = db.db.Exec(`DELETE FROM runs WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
				r, _, _, spec := installedRun(t, db, true)
				if kind == "outcome" {
					if err = db.RecordInstalledPackAttempt(context.Background(), r.ID, installedEvidence(t, spec, 0), spec.StartsAt); err != nil {
						t.Fatal(err)
					}
					_, err = db.db.Exec(`UPDATE installed_pack_attempts SET result_json=?,completed_at=? WHERE run_id=?`, `{}`, formatTime(spec.StartsAt), r.ID)
				} else {
					_, err = db.db.Exec(`UPDATE installed_pack_runs SET registration_json='{}' WHERE run_id=?`, r.ID)
				}
				if err == nil {
					_, err = db.RecoverInterruptedRuns(context.Background(), spec.StartsAt.Add(time.Minute))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ReadInstalledPackExport(context.Background(), run.ID); err == nil {
				t.Fatal("invalid export capture accepted")
			}
		})
	}
}
