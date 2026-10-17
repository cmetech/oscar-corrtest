package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func feedManifest(t *testing.T, origin string) oscar.ExistingFeedManifestSpec {
	t.Helper()
	body, err := os.ReadFile("../../oscar/testdata/existing_feed/splunk.json")
	if err != nil {
		t.Fatal(err)
	}
	return oscar.ExistingFeedManifestSpec{CohortID: "0123456789abcdef0123456789abcdef", TargetID: "owned-receiver", ReceiverOrigin: origin,
		Profile: "splunk", ProfileHash: "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b",
		Mapping:  oscar.ExistingFeedMappingSnapshot{Source: "tests/oscar-webhookreceiver/fixtures/splunk/baseline.json", SHA256: "11a7c673edb751f5c0ce3524457977b503a39ee36979b51ce9a1c6a1f5213eae", BodyBase64: base64.StdEncoding.EncodeToString(body)},
		StartsAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), UserAgent: "owned-existing-feed/1", RequestHost: "owned-receiver"}
}

func feedRun(t *testing.T, db *Database, spec oscar.ExistingFeedManifestSpec) (domain.Run, oscar.ExistingFeedManifest) {
	t.Helper()
	run := testRun("crt_01234567000000000000000000", spec.StartsAt)
	run.Status = domain.RunSettingUp
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterExistingFeed(context.Background(), run.ID, spec, spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	if err := db.TransitionRun(context.Background(), run.ID, domain.RunInjecting, spec.StartsAt, "owned feed"); err != nil {
		t.Fatal(err)
	}
	manifest, err := oscar.FreezeExistingFeedManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	return run, manifest
}

func feedInput(t *testing.T, manifest oscar.ExistingFeedManifest, spec oscar.ExistingFeedManifestSpec) *oscar.ExistingFeedLabInput {
	t.Helper()
	t.Setenv("CORRTEST_OWNED_FEED_KEY", "owned-fixture")
	input, err := oscar.NewExistingFeedLabInput(manifest, domain.Target{ID: spec.TargetID, BaseURL: spec.ReceiverOrigin, APIProfile: "public-v1",
		Credential: domain.CredentialRef{Kind: domain.CredentialEnvironment, Reference: "CORRTEST_OWNED_FEED_KEY"}}, oscar.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestExistingFeedSQLiteRecordsBeforeHTTPAndRefusesReopenReplay(t *testing.T) {
	ctx := context.Background()
	db := openRepositoryDatabase(t)
	var calls atomic.Int32
	var run domain.Run
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		stored, err := db.ListExistingFeedAttempts(ctx, run.ID, "splunk")
		if err != nil || len(stored) != 1 || stored[0].Result != nil {
			t.Errorf("POST before full committed UNKNOWN: %v %+v", err, stored)
		}
		if r.Host != "owned-receiver" || r.Header.Get("User-Agent") != "owned-existing-feed/1" {
			t.Error("normalization headers changed")
		}
		w.Header().Set("X-Oscar-Profile-Revision", "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	spec := feedManifest(t, server.URL)
	var manifest oscar.ExistingFeedManifest
	run, manifest = feedRun(t, db, spec)
	record := func(e oscar.ExistingFeedRequestEvidence) error {
		return db.RecordExistingFeedAttempt(ctx, run.ID, e, spec.StartsAt)
	}
	result, err := feedInput(t, manifest, spec).SendRecorded(ctx, 0, record)
	if err != nil || result.Class != oscar.InjectionForwarded {
		t.Fatalf("send: %+v %v", result, err)
	}
	evidence, _, _ := manifest.Prepare(0)
	if err = db.CompleteExistingFeedAttempt(ctx, run.ID, "splunk", 0, evidence.Request.BodySHA256, result, "", spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	path := db.path
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stored, err := db.ListExistingFeedAttempts(ctx, run.ID, "splunk")
	if err != nil || len(stored) != 1 || !reflect.DeepEqual(stored[0].Evidence, evidence) || stored[0].Result == nil {
		t.Fatalf("lost full evidence: %v %+v", err, stored)
	}
	if _, err = feedInput(t, manifest, spec).SendRecorded(ctx, 0, record); err == nil {
		t.Fatal("reopened attempt replayed")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST count=%d", calls.Load())
	}
	events, err := db.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "alert.attempting" {
			var saved oscar.ExistingFeedRequestEvidence
			if json.Unmarshal([]byte(event.DetailJSON), &saved) != nil || !reflect.DeepEqual(saved, evidence) {
				t.Fatal("incomplete attempt timeline")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing input responsibility event")
	}
}

func TestExistingFeedSQLiteUnknownBlocksNextAndRecoveryKeepsResponsibility(t *testing.T) {
	db := openRepositoryDatabase(t)
	spec := feedManifest(t, "http://127.0.0.1:9000")
	run, manifest := feedRun(t, db, spec)
	ctx := context.Background()
	evidence, _, _ := manifest.Prepare(0)
	if err := db.RecordExistingFeedAttempt(ctx, run.ID, evidence, spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	path := db.path
	_ = db.Close()
	var err error
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, index := range []int{0, 1} {
		next, _, _ := manifest.Prepare(index)
		if err := db.RecordExistingFeedAttempt(ctx, run.ID, next, spec.StartsAt); err == nil {
			t.Fatal("unknown allowed replay/advance")
		}
	}
	if _, err := db.RecoverInterruptedRuns(ctx, spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetRun(ctx, run.ID)
	if err != nil || saved.CleanupStatus != domain.CleanupUnknown || saved.Status != domain.RunInterrupted {
		t.Fatalf("lost responsibility: %+v %v", saved, err)
	}
}

func TestExistingFeedSQLiteCompetingConnectionsCommitOneAttempt(t *testing.T) {
	db := openRepositoryDatabase(t)
	spec := feedManifest(t, "http://127.0.0.1:9000")
	run, manifest := feedRun(t, db, spec)
	ctx := context.Background()
	second, err := Open(ctx, db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	evidence, _, _ := manifest.Prepare(0)
	var committed atomic.Int32
	var wg sync.WaitGroup
	for _, connection := range []*Database{db, second} {
		wg.Add(1)
		go func(d *Database) {
			defer wg.Done()
			if d.RecordExistingFeedAttempt(ctx, run.ID, evidence, spec.StartsAt) == nil {
				committed.Add(1)
			}
		}(connection)
	}
	wg.Wait()
	if committed.Load() != 1 {
		t.Fatalf("committed=%d", committed.Load())
	}
}

func TestExistingFeedSQLiteRejectsForeignEvidenceAndCohortReuse(t *testing.T) {
	for _, fault := range []string{"body", "identity", "mapping", "origin", "index", "skip"} {
		t.Run(fault, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			spec := feedManifest(t, "http://127.0.0.1:9000")
			run, manifest := feedRun(t, db, spec)
			e, _, _ := manifest.Prepare(0)
			switch fault {
			case "body":
				e.Request.BodyBase64 = "e30="
			case "identity":
				e.Expected.Labels["foreign"] = "true"
			case "mapping":
				e.Manifest.Mapping.BodyBase64 = ""
			case "origin":
				e.Manifest.ReceiverOrigin = "http://foreign.invalid"
			case "index":
				e.FixtureIndex = 8
			case "skip":
				e, _, _ = manifest.Prepare(1)
			}
			if err := db.RecordExistingFeedAttempt(context.Background(), run.ID, e, spec.StartsAt); err == nil {
				t.Fatal("foreign or out-of-order evidence accepted")
			}
			stored, err := db.ListExistingFeedAttempts(context.Background(), run.ID, "splunk")
			if err != nil || len(stored) != 0 {
				t.Fatalf("partial attempt: %v %+v", err, stored)
			}
		})
	}
	db := openRepositoryDatabase(t)
	spec := feedManifest(t, "http://127.0.0.1:9000")
	run, _ := feedRun(t, db, spec)
	other := testRun("crt_abcdef01000000000000000000", spec.StartsAt)
	if err := db.CreateRun(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterExistingFeed(context.Background(), other.ID, spec, spec.StartsAt); err == nil {
		t.Fatal("same cohort rebound to new run")
	}
	spec.StartsAt = spec.StartsAt.Add(time.Second)
	if err := db.RegisterExistingFeed(context.Background(), run.ID, spec, spec.StartsAt); err == nil {
		t.Fatal("changed frozen manifest")
	}
}

func TestExistingFeedSQLiteRecorderFailureSendsNothing(t *testing.T) {
	for _, fault := range []string{"missing-manifest", "closed", "read-only"} {
		t.Run(fault, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer server.Close()
			db := openRepositoryDatabase(t)
			spec := feedManifest(t, server.URL)
			run, manifest := feedRun(t, db, spec)
			switch fault {
			case "missing-manifest":
				run.ID = "unknown"
			case "closed":
				_ = db.Close()
			case "read-only":
				db.readyErr = context.Canceled
			}
			_, err := feedInput(t, manifest, spec).SendRecorded(context.Background(), 0, func(e oscar.ExistingFeedRequestEvidence) error {
				return db.RecordExistingFeedAttempt(context.Background(), run.ID, e, spec.StartsAt)
			})
			if err == nil || calls.Load() != 0 {
				t.Fatalf("recorder failure sent: %v %d", err, calls.Load())
			}
		})
	}
}

func TestExistingFeedSQLiteProcessLossKeepsUnknown(t *testing.T) {
	ctx := context.Background()
	if path := os.Getenv("CORRTEST_FEED_CRASH_DB"); path != "" {
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		spec := feedManifest(t, os.Getenv("CORRTEST_FEED_CRASH_ORIGIN"))
		run, manifest := feedRun(t, db, spec)
		_, err = feedInput(t, manifest, spec).SendRecorded(ctx, 0, func(e oscar.ExistingFeedRequestEvidence) error {
			return db.RecordExistingFeedAttempt(ctx, run.ID, e, spec.StartsAt)
		})
		t.Fatalf("crash helper unexpectedly returned: %v", err)
	}
	posted, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		posted <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	path := filepath.Join(t.TempDir(), "crash.db")
	command := exec.Command(os.Args[0], "-test.run=^TestExistingFeedSQLiteProcessLossKeepsUnknown$")
	command.Env = append(os.Environ(), "CORRTEST_FEED_CRASH_DB="+path, "CORRTEST_FEED_CRASH_ORIGIN="+server.URL)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	select {
	case <-posted:
	case <-time.After(20 * time.Second):
		t.Fatal("child did not reach recorded HTTP attempt")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runID := "crt_01234567000000000000000000"
	attempts, err := db.ListExistingFeedAttempts(ctx, runID, "splunk")
	if err != nil || len(attempts) != 1 || attempts[0].Result != nil {
		t.Fatalf("process loss discarded UNKNOWN: %+v %v", attempts, err)
	}
	spec := feedManifest(t, server.URL)
	manifest, err := oscar.FreezeExistingFeedManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = feedInput(t, manifest, spec).SendRecorded(ctx, 0, func(e oscar.ExistingFeedRequestEvidence) error {
		return db.RecordExistingFeedAttempt(ctx, runID, e, spec.StartsAt)
	})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("process-loss replay: %v posts=%d", err, calls.Load())
	}
	if _, err = db.RecoverInterruptedRuns(ctx, spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	run, err := db.GetRun(ctx, runID)
	if err != nil || run.CleanupStatus != domain.CleanupUnknown {
		t.Fatalf("process-loss responsibility: %+v %v", run, err)
	}
}
