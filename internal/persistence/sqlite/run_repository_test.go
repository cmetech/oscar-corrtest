package sqlite

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

func TestRunRepositoryListsFiltersAndPersistsEvents(t *testing.T) {
	database := openRepositoryDatabase(t)
	older := testRun("crt_00000000000000000000000000", time.Date(2026, 8, 19, 19, 0, 0, 0, time.UTC))
	newer := testRun("crt_10000000000000000000000000", older.CreatedAt.Add(time.Hour))
	newer.Status = domain.RunCompleted
	newer.Verdict = domain.VerdictFail
	newer.CleanupStatus = domain.CleanupDirty
	for _, run := range []domain.Run{older, newer} {
		if err := database.CreateRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	event, err := database.AppendRunEvent(context.Background(), domain.RunEvent{
		RunID: older.ID, Type: "run.created", Level: "info", OccurredAt: older.CreatedAt, Summary: "created",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 1 {
		t.Fatalf("sequence=%d", event.Sequence)
	}
	list, err := database.ListRuns(context.Background(), domain.RunFilter{Verdict: domain.VerdictFail})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != newer.ID {
		t.Fatalf("runs=%+v", list)
	}
	all, err := database.ListRuns(context.Background(), domain.RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != newer.ID || all[1].ID != older.ID {
		t.Fatalf("order=%+v", all)
	}
	events, err := database.ListRunEvents(context.Background(), older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Summary != "created" {
		t.Fatalf("events=%+v", events)
	}
}

func TestAppendRunEventAllocatesMonotonicSequencesConcurrently(t *testing.T) {
	database := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	const count = 20
	sequences := make(chan int64, count)
	errors := make(chan error, count)
	var wait sync.WaitGroup
	for i := range count {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			event, err := database.AppendRunEvent(context.Background(), domain.RunEvent{
				RunID: run.ID, Type: "test", Level: "info", OccurredAt: run.CreatedAt, Summary: fmt.Sprintf("event %d", index),
			})
			if err != nil {
				errors <- err
				return
			}
			sequences <- event.Sequence
		}(i)
	}
	wait.Wait()
	close(errors)
	close(sequences)
	for err := range errors {
		t.Error(err)
	}
	var got []int
	for sequence := range sequences {
		got = append(got, int(sequence))
	}
	sort.Ints(got)
	if len(got) != count {
		t.Fatalf("sequence count=%d", len(got))
	}
	for i, sequence := range got {
		if sequence != i+1 {
			t.Fatalf("sequences=%v", got)
		}
	}
}

func TestTransitionRunChangesStateAndAppendsOneEvent(t *testing.T) {
	database := openRepositoryDatabase(t)
	run := testRun("crt_00000000000000000000000000", time.Now().UTC())
	if err := database.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := database.TransitionRun(context.Background(), run.ID, domain.RunPreflight, run.CreatedAt.Add(time.Second), "preflight started"); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunPreflight {
		t.Fatalf("status=%s", got.Status)
	}
	events, err := database.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "run.transition" {
		t.Fatalf("events=%+v", events)
	}
	if err := database.TransitionRun(context.Background(), run.ID, domain.RunCompleted, run.CreatedAt.Add(time.Second), "invalid"); err == nil {
		t.Fatal("invalid transition accepted")
	}
}

func TestInterruptedRunSurvivesProcessLoss(t *testing.T) {
	if os.Getenv("GO_WANT_CORRTEST_CRASH_HELPER") == "1" {
		path := os.Getenv("CORRTEST_CRASH_DB")
		database, err := Open(context.Background(), path)
		if err != nil {
			panic(err)
		}
		run := testRun("crt_00000000000000000000000000", time.Now().UTC())
		run.Status = domain.RunObserving
		if err := database.CreateRun(context.Background(), run); err != nil {
			panic(err)
		}
		fmt.Println("READY")
		select {}
	}

	path := filepath.Join(t.TempDir(), "corrtest.db")
	command := exec.Command(os.Args[0], "-test.run=TestInterruptedRunSurvivesProcessLoss")
	command.Env = append(os.Environ(), "GO_WANT_CORRTEST_CRASH_HELPER=1", "CORRTEST_CRASH_DB="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "READY" {
		_ = command.Process.Kill()
		t.Fatalf("helper output=%q err=%v", scanner.Text(), scanner.Err())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	count, err := database.RecoverInterruptedRuns(context.Background(), time.Date(2026, 8, 19, 21, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recovered=%d", count)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	count, err = database.RecoverInterruptedRuns(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("second recovery=%d", count)
	}
	run, err := database.GetRun(context.Background(), "crt_00000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunInterrupted || run.CleanupStatus != domain.CleanupNotRequired {
		t.Fatalf("run=%+v", run)
	}
	events, err := database.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "run.interrupted" || events[0].DetailJSON != `{"previousStatus":"OBSERVING"}` {
		t.Fatalf("events=%+v", events)
	}
}

func openRepositoryDatabase(t *testing.T) *Database {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "corrtest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func testRun(id string, created time.Time) domain.Run {
	return domain.Run{
		ID:             id,
		ShortToken:     id[4:12],
		Status:         domain.RunQueued,
		CleanupStatus:  domain.CleanupNotRequired,
		HarnessVersion: "test",
		CreatedAt:      created,
		UpdatedAt:      created,
	}
}
