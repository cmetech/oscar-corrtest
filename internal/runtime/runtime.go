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
	activeMu  sync.Mutex
	active    map[string]context.CancelFunc
	runWG     sync.WaitGroup
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
		active: make(map[string]context.CancelFunc),
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
	engine := runner.New(r.database, client, runner.Options{EvidenceWriter: r.evidenceWriter()})
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
	executeErr := runner.New(r.database, client, runner.Options{EvidenceWriter: r.evidenceWriter()}).Execute(ctx, run, plan, diagnostic.snapshot())
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
	executionCtx, cancel := context.WithCancel(r.rootCtx)
	r.activeMu.Lock()
	r.active[run.ID] = cancel
	r.activeMu.Unlock()
	r.runWG.Add(1)
	go func() {
		defer r.runWG.Done()
		defer func() {
			r.activeMu.Lock()
			delete(r.active, run.ID)
			r.activeMu.Unlock()
			cancel()
		}()
		r.runMu.Lock()
		defer r.runMu.Unlock()
		diagnostic := r.diagnose(executionCtx, client, target, run, plan, pipelineMode)
		engine := runner.New(r.database, client, runner.Options{EvidenceWriter: r.evidenceWriter()})
		_ = engine.Execute(executionCtx, run, plan, diagnostic.snapshot())
	}()
	return run, nil
}

// CancelRun requests cancellation for a run owned by this process. The runner
// persists its terminal error and bounded cleanup evidence before it exits.
func (r *Runtime) CancelRun(ctx context.Context, runID string) error {
	run, err := r.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunInterrupted {
		return fmt.Errorf("run is already terminal")
	}
	r.activeMu.Lock()
	cancel, ok := r.active[runID]
	r.activeMu.Unlock()
	if !ok {
		return fmt.Errorf("run is not active in this process")
	}
	cancel()
	return nil
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

// Close cancels active executions, waits for their bounded cleanup, then
// releases the database pool.
func (r *Runtime) Close() error {
	r.activeMu.Lock()
	for _, cancel := range r.active {
		cancel()
	}
	r.activeMu.Unlock()
	r.runWG.Wait()
	return r.database.Close()
}

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
	records, err := r.database.ListArtifacts(ctx, runID)
	if err != nil {
		return evidence.Result{}, err
	}
	attachments := make([]evidence.Attachment, 0, len(records))
	for index, record := range records {
		if record.Availability != domain.ArtifactAvailable {
			return evidence.Result{}, fmt.Errorf("artifact %q is not available for export", record.RelativePath)
		}
		integrity, verifyErr := r.artifacts.Verify(ctx, artifact.Manifest{RelativePath: record.RelativePath, MIMEType: record.MIMEType, SHA256: record.SHA256, ByteSize: record.ByteSize})
		if verifyErr != nil || integrity != artifact.IntegrityValid {
			return evidence.Result{}, fmt.Errorf("artifact %q failed export verification", record.RelativePath)
		}
		content, readErr := os.ReadFile(filepath.Join(r.settings.DataDir, filepath.FromSlash(record.RelativePath)))
		if readErr != nil {
			return evidence.Result{}, fmt.Errorf("read verified artifact: %w", readErr)
		}
		name := fmt.Sprintf("artifact-%02d-%s", index+1, filepath.Base(record.RelativePath))
		if record.Kind == "normalized-oscar-evidence" {
			name = "normalized-evidence.json"
		}
		attachments = append(attachments, evidence.Attachment{Name: name, Content: content, SHA256: record.SHA256, ByteSize: record.ByteSize})
	}
	return evidence.WriteWithArtifacts(ctx, destination, run, events, attachments)
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

// PreviewRetention returns at most 500 old runs that are terminal and whose
// independent cleanup state proves local deletion is safe.
func (r *Runtime) PreviewRetention(ctx context.Context, before time.Time) ([]domain.Run, error) {
	if before.IsZero() {
		return nil, fmt.Errorf("retention cutoff is required")
	}
	runs, err := r.ListRuns(ctx, domain.RunFilter{CreatedBefore: &before, Limit: 500})
	if err != nil {
		return nil, err
	}
	eligible := make([]domain.Run, 0, len(runs))
	for _, run := range runs {
		terminal := run.Status == domain.RunCompleted || run.Status == domain.RunInterrupted
		cleanupSafe := run.CleanupStatus == domain.CleanupClean || run.CleanupStatus == domain.CleanupNotRequired
		if terminal && cleanupSafe {
			eligible = append(eligible, run)
		}
	}
	return eligible, nil
}

// ApplyRetention deletes exactly the run IDs returned by the safety preview;
// every artifact is reverified by DeleteRun immediately before deletion.
func (r *Runtime) ApplyRetention(ctx context.Context, before time.Time) ([]string, error) {
	candidates, err := r.PreviewRetention(ctx, before)
	if err != nil {
		return nil, err
	}
	deleted := make([]string, 0, len(candidates))
	for _, run := range candidates {
		if err := r.DeleteRun(ctx, run.ID); err != nil {
			return deleted, fmt.Errorf("retention stopped at run %s: %w", run.ID, err)
		}
		deleted = append(deleted, run.ID)
	}
	return deleted, nil
}

// DeleteRun removes one exact terminal, cleanup-safe run after artifact verification.
func (r *Runtime) DeleteRun(ctx context.Context, runID string) error {
	run, err := r.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if (run.Status != domain.RunCompleted && run.Status != domain.RunInterrupted) || (run.CleanupStatus != domain.CleanupClean && run.CleanupStatus != domain.CleanupNotRequired) {
		return fmt.Errorf("run is active or cleanup is not proven safe")
	}
	records, err := r.database.ListArtifacts(ctx, runID)
	if err != nil {
		return err
	}
	manifests := make([]artifact.Manifest, 0, len(records))
	for _, record := range records {
		if record.Availability != domain.ArtifactAvailable {
			return fmt.Errorf("pending artifact %q prevents run deletion", record.RelativePath)
		}
		manifests = append(manifests, artifact.Manifest{RelativePath: record.RelativePath, MIMEType: record.MIMEType, SHA256: record.SHA256, ByteSize: record.ByteSize})
	}
	staged, err := r.artifacts.StageRunDeletion(ctx, runID, manifests)
	if err != nil {
		return err
	}
	if err := r.database.DeleteTerminalRun(ctx, runID); err != nil {
		if rollbackErr := staged.Rollback(); rollbackErr != nil {
			return fmt.Errorf("delete run: %v; restore evidence: %w", err, rollbackErr)
		}
		return err
	}
	if err := staged.Finalize(); err != nil {
		return fmt.Errorf("run metadata deleted but staged evidence removal failed: %w", err)
	}
	return nil
}

func ownedRule(rule oscar.Rule, resource domain.Resource, runID string) bool {
	return rule.ID > 0 && rule.Name == resource.ExternalName && strings.Contains(rule.Description, "run="+runID)
}
