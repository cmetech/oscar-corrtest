package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestExistingFeedSQLiteResultsPermitOnlyKnownForwardedAdvance(t *testing.T) {
	for _, class := range []oscar.InjectionClass{oscar.InjectionForwarded, oscar.InjectionRejected, oscar.InjectionIndeterminate} {
		t.Run(string(class), func(t *testing.T) {
			db := openRepositoryDatabase(t)
			spec := feedManifest(t, "http://127.0.0.1:9000")
			run, manifest := feedRun(t, db, spec)
			ctx := context.Background()
			evidence, _, _ := manifest.Prepare(0)
			if err := db.RecordExistingFeedAttempt(ctx, run.ID, evidence, spec.StartsAt); err != nil {
				t.Fatal(err)
			}
			body := []byte(`{"status":"ok"}`)
			if class == oscar.InjectionRejected {
				body = []byte(`{"status":"acl_denied"}`)
			}
			result := oscar.InjectionResult{Class: class, StatusCode: 200, Body: body}
			if err := db.CompleteExistingFeedAttempt(ctx, run.ID, "splunk", 0, evidence.Request.BodySHA256, result, "", spec.StartsAt); err != nil {
				t.Fatal(err)
			}
			if err := db.CompleteExistingFeedAttempt(ctx, run.ID, "splunk", 0, evidence.Request.BodySHA256, result, "", spec.StartsAt); err == nil {
				t.Fatal("outcome overwritten")
			}
			next, _, _ := manifest.Prepare(1)
			err := db.RecordExistingFeedAttempt(ctx, run.ID, next, spec.StartsAt)
			if (err == nil) != (class == oscar.InjectionForwarded) {
				t.Fatalf("class=%s advance=%v", class, err)
			}
			saved, err := db.GetRun(ctx, run.ID)
			if err != nil || saved.CleanupStatus != domain.CleanupUnknown {
				t.Fatalf("result discharged responsibility: %+v %v", saved, err)
			}
		})
	}
}

func TestExistingFeedSQLiteRejectsConflictingOrUnboundResults(t *testing.T) {
	for _, fault := range []string{"hash", "index", "class", "status", "raw-body", "transport-error", "size"} {
		t.Run(fault, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			spec := feedManifest(t, "http://127.0.0.1:9000")
			run, manifest := feedRun(t, db, spec)
			ctx := context.Background()
			evidence, _, _ := manifest.Prepare(0)
			if err := db.RecordExistingFeedAttempt(ctx, run.ID, evidence, spec.StartsAt); err != nil {
				t.Fatal(err)
			}
			result := oscar.InjectionResult{Class: oscar.InjectionForwarded, StatusCode: 200, Body: []byte(`{"status":"ok"}`)}
			digest, index, detail := evidence.Request.BodySHA256, 0, ""
			switch fault {
			case "hash":
				digest = "foreign"
			case "index":
				index = 1
			case "class":
				result.Class = "accepted"
			case "status":
				result.StatusCode = 503
			case "raw-body":
				result.Body = []byte(`{"status":"acl_denied"}`)
			case "transport-error":
				detail = "timeout"
			case "size":
				result.Body = []byte(strings.Repeat("x", (1<<20)+1))
			}
			if err := db.CompleteExistingFeedAttempt(ctx, run.ID, "splunk", index, digest, result, detail, spec.StartsAt); err == nil {
				t.Fatal("invalid outcome accepted")
			}
			attempts, err := db.ListExistingFeedAttempts(ctx, run.ID, "splunk")
			if err != nil || len(attempts) != 1 || attempts[0].Result != nil {
				t.Fatalf("UNKNOWN lost: %+v %v", attempts, err)
			}
		})
	}
}

func TestExistingFeedSQLiteCorruptionRefusesReadsAndAdvancement(t *testing.T) {
	for _, fault := range []string{"manifest", "evidence", "result", "gap"} {
		t.Run(fault, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			spec := feedManifest(t, "http://127.0.0.1:9000")
			run, manifest := feedRun(t, db, spec)
			ctx := context.Background()
			evidence, _, _ := manifest.Prepare(0)
			if err := db.RecordExistingFeedAttempt(ctx, run.ID, evidence, spec.StartsAt); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fault {
			case "manifest":
				_, err = db.db.Exec(`UPDATE existing_feeds SET manifest_json='{}'`)
			case "evidence":
				_, err = db.db.Exec(`UPDATE existing_feed_attempts SET evidence_json='{}'`)
			case "gap":
				_, err = db.db.Exec(`UPDATE existing_feed_attempts SET fixture_index=2`)
			case "result":
				raw, _ := json.Marshal(existingFeedResult{BodySHA256: evidence.Request.BodySHA256, Result: oscar.InjectionResult{Class: oscar.InjectionForwarded, StatusCode: 200, Body: []byte(`{"status":"rejected"}`)}})
				_, err = db.db.Exec(`UPDATE existing_feed_attempts SET result_json=?,completed_at=?`, string(raw), formatTime(spec.StartsAt))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ListExistingFeedAttempts(ctx, run.ID, "splunk"); err == nil {
				t.Fatal("corrupt record returned as valid")
			}
			next, _, _ := manifest.Prepare(1)
			if err = db.RecordExistingFeedAttempt(ctx, run.ID, next, spec.StartsAt); err == nil {
				t.Fatal("advanced corrupt journal")
			}
		})
	}
}

func TestExistingFeedSQLiteRollsBackReservationIfResponsibilityEventFails(t *testing.T) {
	db := openRepositoryDatabase(t)
	spec := feedManifest(t, "http://127.0.0.1:9000")
	run, manifest := feedRun(t, db, spec)
	ctx := context.Background()
	if _, err := db.db.Exec(`CREATE TRIGGER refuse_feed_event BEFORE INSERT ON run_events WHEN NEW.type='alert.attempting' BEGIN SELECT RAISE(ABORT,'journal unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	evidence, _, _ := manifest.Prepare(0)
	if err := db.RecordExistingFeedAttempt(ctx, run.ID, evidence, spec.StartsAt); err == nil {
		t.Fatal("event failure ignored")
	}
	attempts, err := db.ListExistingFeedAttempts(ctx, run.ID, "splunk")
	if err != nil || len(attempts) != 0 {
		t.Fatalf("partial reservation: %v %+v", err, attempts)
	}
	saved, err := db.GetRun(ctx, run.ID)
	if err != nil || saved.CleanupStatus != domain.CleanupNotRequired {
		t.Fatalf("partial envelope update: %+v %v", saved, err)
	}
}
