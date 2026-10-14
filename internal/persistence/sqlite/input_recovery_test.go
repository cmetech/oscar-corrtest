package sqlite

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestRecoveryKeepsInputResponsibilityAfterRulesDeleted(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	run.Status = domain.RunCleaningUp
	if err := db.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendRunEvent(ctx, domain.RunEvent{RunID: run.ID, Type: "alert.attempting", Level: "info", OccurredAt: run.CreatedAt, Summary: "committed before POST", DetailJSON: `{"eventId":"owned"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecoverInterruptedRuns(ctx, run.CreatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("lost source cleanup responsibility: %s", saved.CleanupStatus)
	}
}

func TestDurableInputAttemptSurvivesProcessLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrtest.db")
	command := exec.Command(os.Args[0], "-test.run=^TestInterruptedRunSurvivesProcessLoss$")
	command.Env = append(os.Environ(), "GO_WANT_CORRTEST_CRASH_HELPER=1", "CORRTEST_WITH_ATTEMPT=1", "CORRTEST_CRASH_DB="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "READY" {
		t.Fatalf("helper output=%q error=%v", scanner.Text(), scanner.Err())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.RecoverInterruptedRuns(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := db.GetRun(context.Background(), "crt_00000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if run.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("recovery lost input responsibility: %s", run.CleanupStatus)
	}
	resources, err := db.ListResources(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].DeletedAt == nil {
		t.Fatal("test did not delete rule before process loss")
	}
	events, err := db.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "alert.attempting" && strings.Contains(event.DetailJSON, `"startsAt":"2026-10-13T12:00:08.123Z"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("committed episode attempt was lost on process death")
	}
}

func TestRecoveryRetainsLegacyCanonicalAttemptWithoutTimeline(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	run.Status = domain.RunCompleted
	run.CanonicalReportJSON = []byte(`{"attempts":[{"eventId":"owned"}]}`)
	if err := db.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecoverInterruptedRuns(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("legacy canonical input forgotten: %s", saved.CleanupStatus)
	}
}
