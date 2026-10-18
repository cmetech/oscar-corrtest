//go:build linux || darwin

package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func installedObjects(t *testing.T) (*testpack.Kit, *testpack.Installation) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"kit", "original", "original/kit"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	copyFiles := func(src, dst string) {
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dst, e.Name()), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	copyFiles("../../testpack/testdata/kit", filepath.Join(dir, "kit"))
	copyFiles("../../testpack/testdata/kit", filepath.Join(dir, "original/kit"))
	copyFiles("../../testpack/testdata/installation/records", filepath.Join(dir, "original"))
	kit, err := testpack.Load(filepath.Join(dir, "kit"), "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "original/completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]json.RawMessage
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	install, err := testpack.LoadInstallation(filepath.Join(dir, "original"), kit, "https://owned-target.test", c["ownership_declaration"])
	if err != nil {
		t.Fatal(err)
	}
	// Database operations must not reread the source snapshots.
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	return kit, install
}

func installedRun(t *testing.T, db *Database, injecting bool) (domain.Run, *testpack.Kit, *testpack.Installation, oscar.InstalledPackInputSpec) {
	t.Helper()
	ctx := context.Background()
	kit, installation := installedObjects(t)
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, target := range []domain.Target{{ID: "api", DisplayName: "API", BaseURL: installation.Binding().TargetOrigin, APIProfile: "public-v1", CreatedAt: at, UpdatedAt: at}, {ID: "receiver", DisplayName: "Receiver", BaseURL: "https://owned-receiver.test/webhookreceiver", APIProfile: "public-v1", CreatedAt: at, UpdatedAt: at}} {
		if err := db.CreateTarget(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	run := testRun("crt_01234567000000000000000000", at)
	run.Status = domain.RunPreflight
	run.TargetID = "api"
	if err := db.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	spec := oscar.InstalledPackInputSpec{APIVersion: "corrtest.oscar/installed-input-v1", ManifestSHA256: kit.Bindings().ManifestSHA256, QualificationRun: strings.Repeat("a", 32), StartsAt: at, ReceiverTargetID: "receiver", ReceiverBaseURL: "https://owned-receiver.test/webhookreceiver"}
	if injecting {
		if err := db.RegisterInstalledPack(ctx, run.ID, kit, installation, spec, at); err != nil {
			t.Fatal(err)
		}
		if err := db.TransitionRun(ctx, run.ID, domain.RunSettingUp, at, "preflight passed"); err != nil {
			t.Fatal(err)
		}
		if err := db.TransitionRun(ctx, run.ID, domain.RunInjecting, at, "fixed inputs"); err != nil {
			t.Fatal(err)
		}
	}
	return run, kit, installation, spec
}
func installedEvidence(t *testing.T, s oscar.InstalledPackInputSpec, index int) oscar.InstalledPackInputEvidence {
	t.Helper()
	m, e := oscar.FreezeInstalledPackInput(s)
	if e != nil {
		t.Fatal(e)
	}
	v, _, e := m.Prepare(index)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func installedForwarded() oscar.InstalledPackInputResult {
	_, revision, _ := testpack.FixedInstalledInputContract()
	return oscar.InstalledPackInputResult{InjectionResult: oscar.InjectionResult{Class: oscar.InjectionForwarded, StatusCode: 200, Body: []byte(`{"status":"ok"}`)}, ProfileRevisions: []string{revision}}
}

func TestInstalledPackSQLiteFullSequenceAndFrozenTargets(t *testing.T) {
	db := openRepositoryDatabase(t)
	ctx := context.Background()
	run, kit, installation, spec := installedRun(t, db, true)
	for i := range 15 {
		e := installedEvidence(t, spec, i)
		if err := db.RecordInstalledPackAttempt(ctx, run.ID, e, spec.StartsAt); err != nil {
			t.Fatal(err)
		}
		s, err := db.ReadInstalledPackSnapshot(ctx, run.ID)
		if err != nil || len(s.Attempts) != i+1 || s.Attempts[i].Result != nil {
			t.Fatalf("UNKNOWN not committed: %+v %v", s, err)
		}
		r, _ := db.GetRun(ctx, run.ID)
		if r.CleanupStatus != domain.CleanupUnknown {
			t.Fatal("responsibility lost")
		}
		if err = db.CompleteInstalledPackAttempt(ctx, run.ID, i, e.Request.BodySHA256, installedForwarded(), "", spec.StartsAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`UPDATE targets SET base_url='https://changed.test'`); err != nil {
		t.Fatal(err)
	}
	path := db.path
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s, err := reopened.ReadInstalledPackSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Registration.APITargetID != "api" || s.Registration.APIBaseURL != installation.Binding().TargetOrigin || s.Registration.Kit != kit.Bindings() || s.Registration.Installation != installation.Binding() || s.Registration.Spec != spec || len(s.Attempts) != 15 {
		t.Fatalf("binding lost: %+v", s)
	}
	events, err := reopened.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range events {
		if v.Type == "alert.attempting" {
			e := installedEvidence(t, spec, count)
			b, _ := json.Marshal(e)
			if v.DetailJSON != string(b) {
				t.Fatal("event evidence differs")
			}
			count++
		}
	}
	if count != 15 {
		t.Fatalf("events=%d", count)
	}
	s.Attempts[0].Evidence.Expected.Labels["site"] = "changed"
	s.Registration.KitFiles[0].SHA256 = "changed"
	next, err := reopened.ReadInstalledPackSnapshot(ctx, run.ID)
	if err != nil || next.Attempts[0].Evidence.Expected.Labels["site"] == "changed" {
		t.Fatal("snapshot alias")
	}
}

func TestInstalledPackSQLiteRegistrationRefusals(t *testing.T) {
	for _, name := range []string{"nil kit", "zero kit", "nil installation", "zero installation", "manifest", "token", "queued", "injecting", "api origin", "receiver origin", "missing api", "missing receiver"} {
		t.Run(name, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			run, k, i, s := installedRun(t, db, false)
			switch name {
			case "nil kit":
				k = nil
			case "zero kit":
				k = &testpack.Kit{}
			case "nil installation":
				i = nil
			case "zero installation":
				i = &testpack.Installation{}
			case "manifest":
				s.ManifestSHA256 = strings.Repeat("b", 64)
			case "token":
				s.QualificationRun = run.ID
			case "queued":
				_, _ = db.db.Exec(`UPDATE runs SET status='QUEUED'`)
			case "injecting":
				_, _ = db.db.Exec(`UPDATE runs SET status='INJECTING'`)
			case "api origin":
				_, _ = db.db.Exec(`UPDATE targets SET base_url='https://foreign.test' WHERE id='api'`)
			case "receiver origin":
				s.ReceiverBaseURL = "https://foreign.test"
			case "missing api":
				_, _ = db.db.Exec(`UPDATE runs SET target_id=NULL`)
			case "missing receiver":
				s.ReceiverTargetID = "absent"
			}
			if err := db.RegisterInstalledPack(context.Background(), run.ID, k, i, s, s.StartsAt); err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
}
func TestInstalledPackSQLiteInsertOnlyAndUniqueToken(t *testing.T) {
	db := openRepositoryDatabase(t)
	ctx := context.Background()
	run, k, i, s := installedRun(t, db, false)
	if err := db.RegisterInstalledPack(ctx, run.ID, k, i, s, s.StartsAt); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterInstalledPack(ctx, run.ID, k, i, s, s.StartsAt); err == nil {
		t.Fatal("registration overwritten")
	}
	other := testRun("crt_01234568000000000000000001", s.StartsAt)
	other.TargetID = "api"
	other.Status = domain.RunPreflight
	if err := db.CreateRun(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterInstalledPack(ctx, other.ID, k, i, s, s.StartsAt); err == nil {
		t.Fatal("qualification token reused")
	}
}

func TestInstalledPackSQLiteAttemptRefusalsAndRollback(t *testing.T) {
	for _, name := range []string{"body", "labels", "endpoint", "manifest", "token", "index", "status", "event rollback"} {
		t.Run(name, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			run, _, _, s := installedRun(t, db, true)
			e := installedEvidence(t, s, 0)
			switch name {
			case "body":
				e.Request.BodyBase64 = "e30="
			case "labels":
				e.Expected.Labels["site"] = "wrong"
			case "endpoint":
				e.Request.Endpoint += "/wrong"
			case "manifest":
				e.ManifestSHA256 = strings.Repeat("b", 64)
			case "token":
				e.RunID = run.ID
			case "index":
				e = installedEvidence(t, s, 1)
			case "status":
				_, _ = db.db.Exec(`UPDATE runs SET status='OBSERVING'`)
			case "event rollback":
				_, err := db.db.Exec(`CREATE TRIGGER reject_installed_event BEFORE INSERT ON run_events WHEN NEW.type='alert.attempting' BEGIN SELECT RAISE(ABORT,'held event failure'); END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := db.RecordInstalledPackAttempt(context.Background(), run.ID, e, s.StartsAt); err == nil {
				t.Fatal("invalid attempt accepted")
			}
			snap, err := db.ReadInstalledPackSnapshot(context.Background(), run.ID)
			if err != nil || len(snap.Attempts) != 0 {
				t.Fatal("partial attempt committed", err)
			}
			saved, _ := db.GetRun(context.Background(), run.ID)
			if saved.CleanupStatus == domain.CleanupUnknown {
				t.Fatal("partial UNKNOWN committed")
			}
		})
	}
}
func TestInstalledPackSQLiteUnknownAndOutcomeRefusals(t *testing.T) {
	for _, name := range []string{"unknown", "wrong hash", "revision", "duplicate revision", "bad ack", "extra ack", "duplicate ack", "transport error", "unsupported class", "oversize"} {
		t.Run(name, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			run, _, _, s := installedRun(t, db, true)
			ctx := context.Background()
			e := installedEvidence(t, s, 0)
			if err := db.RecordInstalledPackAttempt(ctx, run.ID, e, s.StartsAt); err != nil {
				t.Fatal(err)
			}
			r := installedForwarded()
			hash := e.Request.BodySHA256
			transport := ""
			switch name {
			case "wrong hash":
				hash = strings.Repeat("0", 64)
			case "revision":
				r.ProfileRevisions = []string{strings.Repeat("0", 64)}
			case "duplicate revision":
				r.ProfileRevisions = append(r.ProfileRevisions, r.ProfileRevisions[0])
			case "bad ack":
				r.Body = []byte(`{"status":"acl_denied"}`)
			case "extra ack":
				r.Body = []byte(`{"status":"ok","other":true}`)
			case "duplicate ack":
				r.Body = []byte(`{"status":"ok","status":"ok"}`)
			case "transport error":
				transport = "lost connection"
			case "unsupported class":
				r.Class = oscar.InjectionAccepted
			case "oversize":
				r.Body = make([]byte, 1048577)
			}
			if name != "unknown" {
				if err := db.CompleteInstalledPackAttempt(ctx, run.ID, 0, hash, r, transport, s.StartsAt); err == nil {
					t.Fatal("invalid outcome accepted")
				}
			}
			for _, index := range []int{0, 1} {
				if err := db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, index), s.StartsAt); err == nil {
					t.Fatal("unknown replay/advance")
				}
			}
			snap, err := db.ReadInstalledPackSnapshot(ctx, run.ID)
			if err != nil || len(snap.Attempts) != 1 || snap.Attempts[0].Result != nil {
				t.Fatal("unknown lost", err)
			}
		})
	}
}
func TestInstalledPackSQLiteOutcomeInsertOnceAndIndeterminate(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "forwarded", true: "indeterminate"}[unknown], func(t *testing.T) {
			db := openRepositoryDatabase(t)
			ctx := context.Background()
			run, _, _, s := installedRun(t, db, true)
			e := installedEvidence(t, s, 0)
			if err := db.RecordInstalledPackAttempt(ctx, run.ID, e, s.StartsAt); err != nil {
				t.Fatal(err)
			}
			r := installedForwarded()
			errText := ""
			if unknown {
				r.Class = oscar.InjectionIndeterminate
				errText = "timeout after send"
			}
			if err := db.CompleteInstalledPackAttempt(ctx, run.ID, 0, e.Request.BodySHA256, r, errText, s.StartsAt); err != nil {
				t.Fatal(err)
			}
			if err := db.CompleteInstalledPackAttempt(ctx, run.ID, 0, e.Request.BodySHA256, r, errText, s.StartsAt); err == nil {
				t.Fatal("overwrite")
			}
			snap, err := db.ReadInstalledPackSnapshot(ctx, run.ID)
			if err != nil || !reflect.DeepEqual(*snap.Attempts[0].Result, r) || snap.Attempts[0].TransportError != errText {
				t.Fatal("outcome lost", err)
			}
			err = db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 1), s.StartsAt)
			if (err != nil) != unknown {
				t.Fatal("prior result gate", err)
			}
		})
	}
}
func TestInstalledPackSQLiteCompetingPools(t *testing.T) {
	db := openRepositoryDatabase(t)
	ctx := context.Background()
	run, _, _, s := installedRun(t, db, true)
	other, err := Open(ctx, db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, d := range []*Database{db, other} {
		wg.Add(1)
		go func(d *Database) {
			defer wg.Done()
			results <- d.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 0), s.StartsAt)
		}(d)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("commits=%d", success)
	}
}
func TestInstalledPackSQLiteProcessLoss(t *testing.T) {
	ctx := context.Background()
	if path := os.Getenv("CORRTEST_INSTALLED_CRASH_DB"); path != "" {
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		run, _, _, s := installedRun(t, db, true)
		if err = db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 0), s.StartsAt); err != nil {
			t.Fatal(err)
		}
		os.Exit(37)
	}
	path := filepath.Join(t.TempDir(), "crash.db")
	command := exec.Command(os.Args[0], "-test.run=^TestInstalledPackSQLiteProcessLoss$")
	command.Env = append(os.Environ(), "CORRTEST_INSTALLED_CRASH_DB="+path)
	err := command.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 37 {
		t.Fatalf("child: %v", err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snap, err := db.ReadInstalledPackSnapshot(ctx, "crt_01234567000000000000000000")
	if err != nil || len(snap.Attempts) != 1 || snap.Attempts[0].Result != nil {
		t.Fatal("crash lost UNKNOWN", err)
	}
	for _, n := range []int{0, 1} {
		if err = db.RecordInstalledPackAttempt(ctx, snap.Registration.RunID, installedEvidence(t, snap.Registration.Spec, n), snap.Registration.Spec.StartsAt); err == nil {
			t.Fatal("restart replay")
		}
	}
	if _, err = db.RecoverInterruptedRuns(ctx, snap.Registration.Spec.StartsAt); err != nil {
		t.Fatal(err)
	}
	run, _ := db.GetRun(ctx, snap.Registration.RunID)
	if run.Status != domain.RunInterrupted || run.CleanupStatus != domain.CleanupUnknown {
		t.Fatal("recovery lost responsibility")
	}
}

func TestInstalledPackSQLiteOutcomeFailureKeepsUnknown(t *testing.T) {
	db := openRepositoryDatabase(t)
	ctx := context.Background()
	run, _, _, s := installedRun(t, db, true)
	e := installedEvidence(t, s, 0)
	if err := db.RecordInstalledPackAttempt(ctx, run.ID, e, s.StartsAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER reject_installed_outcome BEFORE UPDATE ON installed_pack_attempts BEGIN SELECT RAISE(ABORT,'outcome write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteInstalledPackAttempt(ctx, run.ID, 0, e.Request.BodySHA256, installedForwarded(), "", s.StartsAt); err == nil {
		t.Fatal("outcome should fail")
	}
	path := db.path
	_ = db.Close()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snap, err := db.ReadInstalledPackSnapshot(ctx, run.ID)
	if err != nil || snap.Attempts[0].Result != nil {
		t.Fatal("failed outcome changed unknown", err)
	}
	if err = db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 1), s.StartsAt); err == nil {
		t.Fatal("failed outcome allowed next")
	}
}

func TestInstalledPackSQLiteCorruptSnapshotRefuses(t *testing.T) {
	for _, name := range []string{"registration unknown field", "registration duplicate", "registration newline", "registration hash", "spec profile", "spec start", "installation manifest", "installation rule", "installation load", "installation receipt", "runtime", "api binding", "kit files", "copied kit files", "missing original receipt", "journal hash", "scenario count", "scenario budget", "column token", "column api", "body drift", "result revision", "result body", "result unknown field", "missing event", "changed event", "uncertain predecessor", "gap", "bad time"} {
		t.Run(name, func(t *testing.T) {
			db := openRepositoryDatabase(t)
			ctx := context.Background()
			run, _, _, s := installedRun(t, db, true)
			e := installedEvidence(t, s, 0)
			if err := db.RecordInstalledPackAttempt(ctx, run.ID, e, s.StartsAt); err != nil {
				t.Fatal(err)
			}
			if err := db.CompleteInstalledPackAttempt(ctx, run.ID, 0, e.Request.BodySHA256, installedForwarded(), "", s.StartsAt); err != nil {
				t.Fatal(err)
			}
			if err := db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 1), s.StartsAt); err != nil {
				t.Fatal(err)
			}
			var raw string
			if err := db.db.QueryRow(`SELECT registration_json FROM installed_pack_runs`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var r InstalledPackRegistration
			if err := json.Unmarshal([]byte(raw), &r); err != nil {
				t.Fatal(err)
			}
			query := ""
			var args []any
			modified := true
			switch name {
			case "registration unknown field":
				raw = strings.TrimSuffix(raw, "}") + `,"unknown":true}`
				modified = false
			case "registration duplicate":
				raw = strings.TrimSuffix(raw, "}") + `,"runId":"` + run.ID + `"}`
				modified = false
			case "registration newline":
				raw += "\n"
				modified = false
			case "registration hash":
				query = `UPDATE installed_pack_runs SET registration_sha256=?`
				args = []any{strings.Repeat("0", 64)}
			case "spec profile":
				r.Kit.ProfileName = "foreign"
			case "spec start":
				r.Spec.StartsAt = r.Spec.StartsAt.Add(time.Second)
			case "installation manifest":
				r.Installation.ManifestSHA256 = strings.Repeat("0", 64)
			case "installation rule":
				r.Installation.RuleID = 0
			case "installation load":
				r.Installation.LoadID = "bad"
			case "installation receipt":
				r.Installation.CompletedSHA256 = strings.Repeat("0", 64)
			case "runtime":
				r.Installation.LoadedAt = "not-time"
			case "api binding":
				r.APIBaseURL = "https://foreign.test"
			case "kit files":
				r.KitFiles[0].Path = "../other"
			case "copied kit files":
				for i := range r.InstallationFiles {
					if r.InstallationFiles[i].Path == "kit/README.md" {
						r.InstallationFiles[i].SHA256 = strings.Repeat("0", 64)
					}
				}
			case "missing original receipt":
				r.InstallationFiles = r.InstallationFiles[1:]
			case "journal hash":
				r.Installation.JournalSHA256 = strings.Repeat("0", 64)
			case "scenario count":
				r.Scenario.Groups[0].Count++
			case "scenario budget":
				r.Scenario.Budgets.WindowSeconds++
			case "column token":
				query = `UPDATE installed_pack_runs SET qualification_token=?`
				args = []any{strings.Repeat("b", 32)}
			case "column api":
				query = `UPDATE installed_pack_runs SET api_base_url='https://foreign.test'`
			case "body drift":
				e.Expected.Labels["site"] = "foreign"
				b, _ := json.Marshal(e)
				query = `UPDATE installed_pack_attempts SET evidence_json=? WHERE input_index=0`
				args = []any{string(b)}
			case "result revision", "result body", "result unknown field":
				var v string
				if err := db.db.QueryRow(`SELECT result_json FROM installed_pack_attempts WHERE input_index=0`).Scan(&v); err != nil {
					t.Fatal(err)
				}
				var out installedPackResult
				_ = json.Unmarshal([]byte(v), &out)
				if name == "result revision" {
					out.Result.ProfileRevisions = []string{strings.Repeat("0", 64)}
				}
				if name == "result body" {
					out.BodySHA256 = strings.Repeat("0", 64)
				}
				b, _ := json.Marshal(out)
				v = string(b)
				if name == "result unknown field" {
					v = strings.TrimSuffix(v, "}") + `,"unknown":true}`
				}
				query = `UPDATE installed_pack_attempts SET result_json=? WHERE input_index=0`
				args = []any{v}
			case "missing event":
				query = `DELETE FROM run_events WHERE type='alert.attempting' AND sequence=(SELECT max(sequence) FROM run_events)`
			case "changed event":
				query = `UPDATE run_events SET detail_json='{}' WHERE type='alert.attempting'`
			case "uncertain predecessor":
				query = `UPDATE installed_pack_attempts SET result_json=NULL,completed_at=NULL WHERE input_index=0`
			case "gap":
				query = `DELETE FROM installed_pack_attempts WHERE input_index=0`
			case "bad time":
				query = `UPDATE installed_pack_attempts SET recorded_at='wrong' WHERE input_index=0`
			}
			if query == "" {
				if modified {
					b, _ := json.Marshal(r)
					raw = string(b)
				}
				query = `UPDATE installed_pack_runs SET registration_json=?,registration_sha256=?`
				args = []any{raw, installedHash([]byte(raw))}
			}
			if _, err := db.db.Exec(query, args...); err != nil {
				t.Fatal(err)
			}
			if snap, err := db.ReadInstalledPackSnapshot(ctx, run.ID); err == nil || snap.Registration.RunID != "" {
				t.Fatal("corrupt snapshot returned", err)
			}
		})
	}
}

func TestInstalledPackSQLiteForeignKeyRetainsTokenAndEvents(t *testing.T) {
	db := openRepositoryDatabase(t)
	ctx := context.Background()
	run, _, _, s := installedRun(t, db, true)
	if err := db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 0), s.StartsAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE runs SET status='COMPLETED',cleanup_status='NOT_REQUIRED',verdict='INCONCLUSIVE'`); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTerminalRun(ctx, run.ID); err == nil {
		t.Fatal("retained installed registration deleted")
	}
	if _, err := db.ReadInstalledPackSnapshot(ctx, run.ID); err != nil {
		t.Fatal("delete rollback lost event/attempt", err)
	}
	if _, err := db.db.Exec(`DELETE FROM installed_pack_runs WHERE run_id=?`, run.ID); err == nil {
		t.Fatal("attempt FK not restricted")
	}
}

func TestInstalledPackSQLiteCancelledTransactionDoesNotRecord(t *testing.T) {
	db := openRepositoryDatabase(t)
	run, _, _, s := installedRun(t, db, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.RecordInstalledPackAttempt(ctx, run.ID, installedEvidence(t, s, 0), s.StartsAt); err == nil {
		t.Fatal("cancelled transaction committed")
	}
	snap, err := db.ReadInstalledPackSnapshot(context.Background(), run.ID)
	if err != nil || len(snap.Attempts) != 0 {
		t.Fatal("cancelled attempt present", err)
	}
}

func TestInstalledPackSQLiteOriginalCoordinateGrammar(t *testing.T) {
	for _, base := range []string{"https://owned.test", "https://owned.test/ext/mw", "http://127.0.0.1:9000/ext/mw"} {
		if !installedAPIBase(base) {
			t.Fatal("valid original base refused", base)
		}
	}
	for _, base := range []string{"https://owned.test/ext/mw/", "https://owned.test/ext%2fmw", "https://owned.test?", "https://owned.test#", "https://user:secret@owned.test", "https://owned.test/other", "https://owned.test:0", "https://owned.test:65536", "https://[fe80::1%25zone]/ext/mw"} {
		t.Run(base, func(t *testing.T) {
			if installedAPIBase(base) {
				t.Fatal("invalid original base accepted")
			}
		})
	}
}

func TestInstalledPackSQLiteProtocolsCannotShareRun(t *testing.T) {
	for _, installedFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "installed then existing", false: "existing then installed"}[installedFirst], func(t *testing.T) {
			db := openRepositoryDatabase(t)
			ctx := context.Background()
			run, kit, installation, spec := installedRun(t, db, false)
			feed := feedManifest(t, "https://owned-receiver.test")
			if installedFirst {
				if err := db.RegisterInstalledPack(ctx, run.ID, kit, installation, spec, spec.StartsAt); err != nil {
					t.Fatal(err)
				}
				if err := db.RegisterExistingFeed(ctx, run.ID, feed, spec.StartsAt); err == nil {
					t.Fatal("installed run acquired existing-feed protocol")
				}
			} else {
				if err := db.RegisterExistingFeed(ctx, run.ID, feed, spec.StartsAt); err != nil {
					t.Fatal(err)
				}
				if err := db.RegisterInstalledPack(ctx, run.ID, kit, installation, spec, spec.StartsAt); err == nil {
					t.Fatal("existing-feed run acquired installed protocol")
				}
			}
			var installed, existing int
			if err := db.db.QueryRow(`SELECT count(*) FROM installed_pack_runs WHERE run_id=?`, run.ID).Scan(&installed); err != nil {
				t.Fatal(err)
			}
			if err := db.db.QueryRow(`SELECT count(*) FROM existing_feeds WHERE run_id=?`, run.ID).Scan(&existing); err != nil {
				t.Fatal(err)
			}
			if installed+existing != 1 {
				t.Fatal("second protocol partially registered")
			}
		})
	}
}
