// Package runtime composes configuration, persistence, artifacts, and application services.
package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/history"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

// Readiness is the sanitized initialization state exposed to CLI and HTTP diagnostics.
type Readiness struct {
	Ready        bool   `json:"ready"`
	DatabasePath string `json:"databasePath"`
	Error        string `json:"error,omitempty"`
}

// Runtime owns one process's durable services.
type Runtime struct {
	settings  config.Settings
	database  *storage.Database
	history   *history.Service
	artifacts *artifact.Store
	readiness Readiness
}

// Open initializes local state, migrations, artifact storage, and interrupted-run recovery.
func Open(ctx context.Context, settings config.Settings, _ version.Info) (*Runtime, error) {
	if settings.DataDir == "" || !filepath.IsAbs(settings.DataDir) {
		return nil, fmt.Errorf("data directory %q must be absolute", settings.DataDir)
	}
	if err := os.MkdirAll(settings.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.Chmod(settings.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict data directory: %w", err)
	}
	databasePath := filepath.Join(settings.DataDir, "corrtest.db")
	database, err := storage.Open(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	artifacts, err := artifact.New(settings.DataDir)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	service := history.New(database, nil, nil)
	result := &Runtime{
		settings: settings, database: database, history: service, artifacts: artifacts,
		readiness: Readiness{Ready: database.Ready() == nil, DatabasePath: databasePath},
	}
	if readyErr := database.Ready(); readyErr != nil {
		result.readiness.Error = readyErr.Error()
		return result, nil
	}
	if _, err := service.RecoverInterruptedRuns(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("recover interrupted runs: %w", err)
	}
	return result, nil
}

// Readiness returns the immutable startup readiness snapshot.
func (r *Runtime) Readiness() Readiness { return r.readiness }

// ReadyStatus implements the transport-neutral readiness interface.
func (r *Runtime) ReadyStatus() (bool, string) { return r.readiness.Ready, r.readiness.Error }

// Settings returns non-secret effective settings.
func (r *Runtime) Settings() config.Settings { return r.settings }

// Artifacts returns the traversal-safe local artifact store.
func (r *Runtime) Artifacts() *artifact.Store { return r.artifacts }

// Close releases the database pool.
func (r *Runtime) Close() error { return r.database.Close() }

// CreateTarget delegates to the shared history service.
func (r *Runtime) CreateTarget(ctx context.Context, input domain.TargetInput) (domain.Target, error) {
	return r.history.CreateTarget(ctx, input)
}

// ListTargets delegates to the shared history service.
func (r *Runtime) ListTargets(ctx context.Context) ([]domain.Target, error) {
	return r.history.ListTargets(ctx)
}

// GetTarget delegates to the shared history service.
func (r *Runtime) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	return r.history.GetTarget(ctx, id)
}

// CreateRun creates a queued durable run without contacting OSCAR.
func (r *Runtime) CreateRun(ctx context.Context, targetID, scenarioID, harnessVersion string) (domain.Run, error) {
	return r.history.CreateRun(ctx, targetID, scenarioID, harnessVersion)
}

// GetRun returns one durable run.
func (r *Runtime) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return r.history.GetRun(ctx, id)
}

// ListRuns returns filtered durable history.
func (r *Runtime) ListRuns(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	return r.history.ListRuns(ctx, filter)
}

// ListRunEvents returns a durable run timeline.
func (r *Runtime) ListRunEvents(ctx context.Context, id string) ([]domain.RunEvent, error) {
	return r.history.ListRunEvents(ctx, id)
}

// ListArtifactEvidence verifies available files while preserving incomplete manifests.
func (r *Runtime) ListArtifactEvidence(ctx context.Context, runID string) ([]domain.ArtifactEvidence, error) {
	records, err := r.database.ListArtifacts(ctx, runID)
	if err != nil {
		return nil, err
	}
	evidence := make([]domain.ArtifactEvidence, 0, len(records))
	for _, record := range records {
		item := domain.ArtifactEvidence{Artifact: record, Integrity: domain.ArtifactIntegrityPending}
		if record.Availability == domain.ArtifactAvailable {
			integrity, verifyErr := r.artifacts.Verify(ctx, artifact.Manifest{
				RelativePath: record.RelativePath, MIMEType: record.MIMEType, SHA256: record.SHA256, ByteSize: record.ByteSize,
			})
			if verifyErr != nil {
				item.Integrity, item.Error = domain.ArtifactIntegrityError, verifyErr.Error()
			} else {
				switch integrity {
				case artifact.IntegrityValid:
					item.Integrity = domain.ArtifactIntegrityValid
				case artifact.IntegrityMissing:
					item.Integrity = domain.ArtifactIntegrityMissing
				case artifact.IntegrityHashMismatch:
					item.Integrity = domain.ArtifactIntegrityHashMismatch
				}
			}
		}
		evidence = append(evidence, item)
	}
	return evidence, nil
}

// Backup creates a coordinated SQLite snapshot.
func (r *Runtime) Backup(ctx context.Context, destination string) error {
	return r.database.Backup(ctx, destination)
}
