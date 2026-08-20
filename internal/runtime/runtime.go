// Package runtime composes configuration, persistence, artifacts, and application services.
package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
	"github.com/cmetech/oscar-corrtest/internal/history"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

// Readiness is the sanitized initialization state exposed to CLI and HTTP diagnostics.
type Readiness struct {
	Ready        bool   `json:"ready"`
	DatabasePath string `json:"databasePath"`
	Error        string `json:"error,omitempty"`
}

// Diagnostic is the fail-closed target compatibility result used by doctor and runs.
type Diagnostic struct {
	TargetID       string                 `json:"targetId"`
	APIProfile     string                 `json:"apiProfile"`
	PipelineMode   string                 `json:"pipelineMode"`
	RuleValidation bool                   `json:"ruleValidation"`
	LabelProbe     oscar.LabelProbeResult `json:"labelProbe"`
	Compatible     bool                   `json:"compatible"`
	Detail         string                 `json:"detail,omitempty"`
}

// Runtime owns one process's durable services.
type Runtime struct {
	settings  config.Settings
	database  *storage.Database
	history   *history.Service
	artifacts *artifact.Store
	readiness Readiness
	version   string
	runMu     sync.Mutex
	rootCtx   context.Context
}

// Open initializes local state, migrations, artifact storage, and interrupted-run recovery.
func Open(ctx context.Context, settings config.Settings, info version.Info) (*Runtime, error) {
	if settings.DataDir == "" || !filepath.IsAbs(settings.DataDir) {
		return nil, fmt.Errorf("data directory %q must be absolute", settings.DataDir)
	}
	if err := os.MkdirAll(settings.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	// #nosec G302 -- directories require execute permission; 0700 is the restrictive state-directory contract.
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
		readiness: Readiness{Ready: database.Ready() == nil, DatabasePath: databasePath}, version: info.Version, rootCtx: ctx,
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

// PreviewBuiltin compiles an isolated plan without creating a durable run or contacting OSCAR.
func (r *Runtime) PreviewBuiltin(ctx context.Context, targetID, pattern, pipelineMode string) (compiler.Plan, error) {
	if _, err := r.GetTarget(ctx, targetID); err != nil {
		return compiler.Plan{}, err
	}
	id, err := domain.NewRunID(rand.Reader)
	if err != nil {
		return compiler.Plan{}, fmt.Errorf("create preview identity: %w", err)
	}
	preview := domain.Run{ID: id.String(), ShortToken: id.Short()}
	return compiler.Compile(preview, scenario.Builtin(pattern), compiler.Capabilities{PipelineMode: pipelineMode})
}

// PreviewScenario compiles a strict custom document without persistence or network activity.
func (r *Runtime) PreviewScenario(ctx context.Context, targetID string, document scenario.Scenario, pipelineMode string) (compiler.Plan, error) {
	if _, err := r.GetTarget(ctx, targetID); err != nil {
		return compiler.Plan{}, err
	}
	id, err := domain.NewRunID(rand.Reader)
	if err != nil {
		return compiler.Plan{}, fmt.Errorf("create preview identity: %w", err)
	}
	return compiler.Compile(domain.Run{ID: id.String(), ShortToken: id.Short()}, document, compiler.Capabilities{PipelineMode: pipelineMode})
}

// ExecuteBuiltin serializes live mutation runs through the same durable runtime used by CLI and UI.
func (r *Runtime) ExecuteBuiltin(ctx context.Context, targetID, pattern, pipelineMode string) (domain.Run, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	target, err := r.GetTarget(ctx, targetID)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := r.history.CreateRun(ctx, targetID, "", r.version)
	if err != nil {
		return domain.Run{}, err
	}
	plan, err := compiler.Compile(run, scenario.Builtin(pattern), compiler.Capabilities{PipelineMode: pipelineMode})
	if err != nil {
		return run, err
	}
	client, err := oscar.New(target, oscar.Options{HarnessVersion: r.version})
	if err != nil {
		return run, err
	}
	diagnostic := r.diagnose(ctx, client, target, run, plan, pipelineMode)
	engine := runner.New(r.database, client, runner.Options{})
	executeErr := engine.Execute(ctx, run, plan, diagnostic.snapshot())
	stored, getErr := r.GetRun(context.Background(), run.ID)
	if getErr != nil {
		return run, getErr
	}
	return stored, executeErr
}

// ExecuteScenario runs a strict custom document through the identical safety lifecycle as built-ins.
func (r *Runtime) ExecuteScenario(ctx context.Context, targetID string, document scenario.Scenario, pipelineMode string) (domain.Run, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	target, err := r.GetTarget(ctx, targetID)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := r.history.CreateRun(ctx, targetID, "", r.version)
	if err != nil {
		return domain.Run{}, err
	}
	plan, err := compiler.Compile(run, document, compiler.Capabilities{PipelineMode: pipelineMode})
	if err != nil {
		return run, err
	}
	client, err := oscar.New(target, oscar.Options{HarnessVersion: r.version})
	if err != nil {
		return run, err
	}
	diagnostic := r.diagnose(ctx, client, target, run, plan, pipelineMode)
	executeErr := runner.New(r.database, client, runner.Options{}).Execute(ctx, run, plan, diagnostic.snapshot())
	stored, getErr := r.GetRun(context.Background(), run.ID)
	if getErr != nil {
		return run, getErr
	}
	return stored, executeErr
}

// StartBuiltin validates and persists a queued run, then executes independently
// from the initiating browser request. The process root context still controls shutdown.
func (r *Runtime) StartBuiltin(ctx context.Context, targetID, pattern, pipelineMode string) (domain.Run, error) {
	target, err := r.GetTarget(ctx, targetID)
	if err != nil {
		return domain.Run{}, err
	}
	if _, err := r.PreviewBuiltin(ctx, targetID, pattern, pipelineMode); err != nil {
		return domain.Run{}, err
	}
	client, err := oscar.New(target, oscar.Options{HarnessVersion: r.version})
	if err != nil {
		return domain.Run{}, err
	}
	run, err := r.history.CreateRun(ctx, targetID, "", r.version)
	if err != nil {
		return domain.Run{}, err
	}
	plan, err := compiler.Compile(run, scenario.Builtin(pattern), compiler.Capabilities{PipelineMode: pipelineMode})
	if err != nil {
		return domain.Run{}, err
	}
	go func() {
		r.runMu.Lock()
		defer r.runMu.Unlock()
		diagnostic := r.diagnose(r.rootCtx, client, target, run, plan, pipelineMode)
		engine := runner.New(r.database, client, runner.Options{})
		_ = engine.Execute(r.rootCtx, run, plan, diagnostic.snapshot())
	}()
	return run, nil
}

// Doctor performs the same rule-schema and label-survival preflight used by execution.
func (r *Runtime) Doctor(ctx context.Context, targetID, pipelineMode string) (Diagnostic, error) {
	target, err := r.GetTarget(ctx, targetID)
	if err != nil {
		return Diagnostic{}, err
	}
	id, err := domain.NewRunID(rand.Reader)
	if err != nil {
		return Diagnostic{}, err
	}
	run := domain.Run{ID: id.String(), ShortToken: id.Short()}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: pipelineMode})
	if err != nil {
		return Diagnostic{}, err
	}
	client, err := oscar.New(target, oscar.Options{HarnessVersion: r.version})
	if err != nil {
		return Diagnostic{}, err
	}
	return r.diagnose(ctx, client, target, run, plan, pipelineMode), nil
}

func (r *Runtime) diagnose(ctx context.Context, client *oscar.Client, target domain.Target, run domain.Run, plan compiler.Plan, pipelineMode string) Diagnostic {
	result := Diagnostic{TargetID: target.ID, APIProfile: target.APIProfile, PipelineMode: pipelineMode}
	if len(plan.Cases) == 0 {
		result.Detail = "compiled plan contained no validation rule"
		return result
	}
	if err := client.ValidateRule(ctx, plan.Cases[0].Rule); err != nil {
		result.Detail = err.Error()
		return result
	}
	result.RuleValidation = true
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	probe, err := client.ProbeLabelSurvival(probeCtx, run.ID, run.ShortToken)
	result.LabelProbe = probe
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	result.Compatible = probe.Accepted && probe.HistoryFound && probe.Fingerprint != "" && len(probe.MissingLabels) == 0
	if !result.Compatible {
		result.Detail = "public-v1 injection/history did not preserve the reserved identity contract"
	}
	return result
}

func (diagnostic Diagnostic) snapshot() runner.CapabilitySnapshot {
	return runner.CapabilitySnapshot{APIProfile: diagnostic.APIProfile, PipelineMode: diagnostic.PipelineMode,
		Ready: diagnostic.RuleValidation, LabelsSurvived: diagnostic.Compatible, Compatibility: diagnostic.Compatible, ReadinessDetail: diagnostic.Detail}
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

// ExportRun writes a portable, self-verifying evidence bundle for a terminal run.
func (r *Runtime) ExportRun(ctx context.Context, runID, destination string) (evidence.Result, error) {
	run, err := r.GetRun(ctx, runID)
	if err != nil {
		return evidence.Result{}, err
	}
	events, err := r.ListRunEvents(ctx, runID)
	if err != nil {
		return evidence.Result{}, err
	}
	return evidence.Write(ctx, destination, run, events)
}

// VerifyBundle validates an exported bundle without trusting its filenames or manifest.
func (r *Runtime) VerifyBundle(ctx context.Context, path string) error {
	return evidence.Verify(ctx, path)
}

// RetryCleanup deletes only resources whose exact full-run ownership can be read back.
func (r *Runtime) RetryCleanup(ctx context.Context, runID string) (domain.Run, error) {
	run, err := r.GetRun(ctx, runID)
	if err != nil {
		return domain.Run{}, err
	}
	if (run.Status != domain.RunCompleted && run.Status != domain.RunInterrupted) || (run.CleanupStatus != domain.CleanupDirty && run.CleanupStatus != domain.CleanupUnknown) {
		return run, fmt.Errorf("run is not eligible for cleanup retry")
	}
	target, err := r.GetTarget(ctx, run.TargetID)
	if err != nil {
		return run, err
	}
	client, err := oscar.New(target, oscar.Options{HarnessVersion: r.version})
	if err != nil {
		return run, err
	}
	resources, err := r.database.ListResources(ctx, run.ID)
	if err != nil {
		return run, err
	}
	var failures []string
	for _, resource := range resources {
		if resource.DeletedAt != nil {
			continue
		}
		if resource.OwnershipToken != run.ID || resource.Kind != "correlation_rule" {
			failures = append(failures, resource.ExternalName+": ownership mismatch")
			continue
		}
		if resource.ExternalID == "" {
			matches, findErr := client.FindRules(ctx, resource.ExternalName)
			if findErr != nil || len(matches) > 1 {
				failures = append(failures, resource.ExternalName+": unresolved create outcome")
				continue
			}
			if len(matches) == 0 {
				if err := r.database.MarkResourceDeleted(ctx, resource.ID, time.Now().UTC()); err != nil {
					failures = append(failures, resource.ExternalName+": ledger update failed")
				}
				continue
			}
			if !ownedRule(matches[0], resource, run.ID) {
				failures = append(failures, resource.ExternalName+": lookalike rule refused")
				continue
			}
			if err := r.database.AdoptResource(ctx, resource.ID, strconv.Itoa(matches[0].ID), time.Now().UTC()); err != nil {
				failures = append(failures, resource.ExternalName+": adoption failed")
				continue
			}
			resource.ExternalID = strconv.Itoa(matches[0].ID)
		}
		id, parseErr := strconv.Atoi(resource.ExternalID)
		if parseErr != nil || id <= 0 {
			failures = append(failures, resource.ExternalName+": invalid external ID")
			continue
		}
		read, readErr := client.GetRule(ctx, id)
		if readErr != nil {
			var machine *oscar.MachineError
			if errors.As(readErr, &machine) && machine.StatusCode == http.StatusNotFound {
				if err := r.database.MarkResourceDeleted(ctx, resource.ID, time.Now().UTC()); err != nil {
					failures = append(failures, resource.ExternalName+": ledger update failed")
				}
				continue
			}
			failures = append(failures, resource.ExternalName+": read-back failed")
			continue
		}
		if !ownedRule(read, resource, run.ID) {
			failures = append(failures, resource.ExternalName+": read-back ownership mismatch")
			continue
		}
		if err := client.DeleteRule(ctx, id); err != nil {
			_ = r.database.MarkResourceCleanupError(ctx, resource.ID, "cleanup retry delete failed")
			failures = append(failures, resource.ExternalName+": delete failed")
			continue
		}
		if err := r.database.MarkResourceDeleted(ctx, resource.ID, time.Now().UTC()); err != nil {
			failures = append(failures, resource.ExternalName+": ledger update failed")
		}
	}
	status := domain.CleanupClean
	summary := "Owned temporary rules were deleted"
	if len(failures) > 0 {
		status, summary = domain.CleanupDirty, "Cleanup retry remains dirty: "+strings.Join(failures, "; ")
	}
	if err := r.database.SetTerminalRunCleanup(ctx, run.ID, status, time.Now().UTC(), summary); err != nil {
		return run, err
	}
	updated, getErr := r.GetRun(ctx, run.ID)
	if len(failures) > 0 {
		return updated, fmt.Errorf("%s", summary)
	}
	return updated, getErr
}

// ImportScenario persists a validated source document once by content digest.
func (r *Runtime) ImportScenario(ctx context.Context, source []byte, document scenario.Scenario) (domain.ScenarioRecord, error) {
	if len(source) == 0 || len(source) > 1<<20 || document.APIVersion != "corrtest.oscar/v1alpha1" || document.Name == "" {
		return domain.ScenarioRecord{}, fmt.Errorf("scenario source is invalid")
	}
	// Re-run the strict decoder so callers cannot pair arbitrary bytes with a parsed value.
	decoded, err := scenario.Decode(bytes.NewReader(source))
	if err != nil || decoded.Name != document.Name || decoded.Pattern != document.Pattern {
		return domain.ScenarioRecord{}, fmt.Errorf("scenario source does not match the validated document")
	}
	digest := sha256.Sum256(source)
	hexDigest := hex.EncodeToString(digest[:])
	if existing, err := r.database.FindScenarioByDigest(ctx, hexDigest); err == nil {
		return existing, nil
	}
	now := time.Now().UTC()
	item := domain.ScenarioRecord{ID: "scn_" + hexDigest[:24], Name: document.Name, APIVersion: document.APIVersion, SourceDocument: string(source), SHA256: hexDigest, CreatedAt: now, UpdatedAt: now}
	if err := r.database.CreateScenario(ctx, item); err != nil {
		return domain.ScenarioRecord{}, err
	}
	return item, nil
}

// ListScenarios returns imported scenario metadata with source content omitted by callers as needed.
func (r *Runtime) ListScenarios(ctx context.Context) ([]domain.ScenarioRecord, error) {
	return r.database.ListScenarios(ctx)
}

func ownedRule(rule oscar.Rule, resource domain.Resource, runID string) bool {
	return rule.ID > 0 && rule.Name == resource.ExternalName && strings.Contains(rule.Description, "run="+runID)
}
