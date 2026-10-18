package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func exportInstalledRuntimeRun(t *testing.T) (*Runtime, domain.Run) {
	t.Helper()
	ctx := context.Background()
	r, err := Open(ctx, config.Settings{DataDir: t.TempDir()}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	run, err := r.history.CreateRun(ctx, "", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := json.Marshal(map[string]string{"runId": run.ID, "inputPath": "installed-test-pack", "pattern": "flood"})
	if err = r.database.SetRunExecutionDocuments(ctx, run.ID, json.RawMessage(`{}`), plan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return r, run
}

func TestInstalledExportInterruptedBeforeInputRetainsMissingReport(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "partial-original"}[pending], func(t *testing.T) {
			r, run := exportInstalledRuntimeRun(t)
			ctx := context.Background()
			if pending {
				name := "installed/original/attempt.json"
				a := domain.Artifact{ID: run.ID + ":" + name, RunID: run.ID, Kind: "installed-pack", RelativePath: "runs/" + run.ID + "/" + name, MIMEType: "application/octet-stream", RedactionState: "private-original-response", Availability: domain.ArtifactPending, CreatedAt: run.CreatedAt}
				if err := r.database.CreateArtifactPending(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.database.RecoverInterruptedRuns(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			before, err := r.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "interrupted.zip")
			result, err := r.ExportRun(ctx, run.ID, destination)
			if err != nil {
				t.Fatal(err)
			}
			if err = evidence.Verify(ctx, result.Path); err != nil {
				t.Fatal(err)
			}
			stored, err := r.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored, before) || stored.Status != domain.RunInterrupted || len(stored.CanonicalReportJSON) != 0 || stored.Verdict != "" || stored.CleanupStatus != domain.CleanupNotRequired {
				t.Fatalf("export rewrote interrupted state: %+v", stored)
			}
		})
	}
}

func TestInstalledExportCannotUseOrdinaryHashOnlyPASS(t *testing.T) {
	r, run := exportInstalledRuntimeRun(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, status := range []domain.RunStatus{domain.RunPreflight, domain.RunCancelling, domain.RunCleaningUp} {
		if err := r.database.TransitionRun(ctx, run.ID, status, now, "test terminal claim"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.database.CompleteRun(ctx, run.ID, domain.VerdictPass, domain.CleanupNotRequired, json.RawMessage(`{"inputPath":"installed-test-pack","verdict":"PASS"}`), "", now); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "unproved.zip")
	if _, err := r.ExportRun(ctx, run.ID, destination); err == nil {
		t.Fatal("installed PASS without original registration/evidence exported through hash-only writer")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("refused export published destination")
	}
}

func TestInstalledExportRefusesActiveOrDamagedCapture(t *testing.T) {
	for _, kind := range []string{"active", "cancelled", "missing", "changed", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			r, run := exportInstalledRuntimeRun(t)
			ctx := context.Background()
			if _, err := r.database.RecoverInterruptedRuns(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "active":
				r.activeMu.Lock()
				r.active[run.ID] = func() {}
				r.activeMu.Unlock()
				t.Cleanup(func() { r.activeMu.Lock(); delete(r.active, run.ID); r.activeMu.Unlock() })
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			default:
				name := "installed/original/attempt.json"
				raw := []byte(`{}`)
				a := domain.Artifact{ID: run.ID + ":" + name, RunID: run.ID, Kind: "installed-pack", RelativePath: "runs/" + run.ID + "/" + name, MIMEType: "application/octet-stream", RedactionState: "private-original-response", Availability: domain.ArtifactPending, CreatedAt: run.CreatedAt}
				if err := r.database.CreateArtifactPending(ctx, a); err != nil {
					t.Fatal(err)
				}
				m, err := r.artifacts.Write(ctx, run.ID, name, a.MIMEType, bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				size := m.ByteSize
				if kind == "oversized" {
					size = 1<<20 + 1
				}
				if err = r.database.MarkArtifactAvailable(ctx, a.ID, m.SHA256, size); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(r.settings.DataDir, filepath.FromSlash(m.RelativePath))
				if kind == "missing" {
					err = os.Remove(path)
				} else if kind == "changed" {
					err = os.WriteFile(path, []byte(`[]`), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "refused.zip")
			if _, err := r.ExportRun(ctx, run.ID, destination); err == nil {
				t.Fatal("invalid capture exported")
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("refused capture published output")
			}
		})
	}
}
