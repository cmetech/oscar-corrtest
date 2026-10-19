package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	storage "github.com/cmetech/oscar-corrtest/internal/persistence/sqlite"
	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

const InstalledPackInputPath = "installed-test-pack"
const installedOriginalCount = 332
const installedOriginalBytes = 42 << 20
const installedObservationCount = 1024
const installedObservationBytes = 64 << 20
const installedEntryBytes = 16 << 20

// InstalledPackRunnerOptions supplies the same clock, cancellation-aware sleep,
// entropy and credential-environment seams used by local runners. It cannot
// replace HTTP, scenario semantics, evidence persistence or the oracle.
type InstalledPackRunnerOptions struct {
	Now            func() time.Time
	MonotonicNow   func() time.Time
	Sleep          func(context.Context, time.Duration) error
	Random         io.Reader
	Getenv         func(string) string
	HarnessVersion string
}

type InstalledPackRunner struct {
	mu            sync.Mutex
	used          bool
	kit           *testpack.Kit
	installation  *testpack.Installation
	api, receiver domain.Target
	database      *storage.Database
	artifacts     *artifact.Store
	options       InstalledPackRunnerOptions
	preflight     *oscar.InstalledPackReader
	active        *oscar.InstalledPackActiveReader
	history       *oscar.InstalledPackHistoryReader
	audit         *oscar.InstalledPackAuditReader
	expected      oscar.InstalledPackExpected
	binding       oscar.InstalledPackBinding
}

type installedPoll = oscar.InstalledPackPoll
type installedRead = oscar.InstalledPackRead
type installedRunReport struct {
	oscar.InstalledPackRunReport
	observationCount int
	observationBytes int64
}

// NewInstalledPackRunner constructs fixed, origin-bound real adapters without
// network I/O. The original installer declaration is not proof of continued
// exclusion: a dedicated lab, controlled destinations and unchanged config and
// processes throughout observation/export remain operator prerequisites.
func NewInstalledPackRunner(kit *testpack.Kit, installation *testpack.Installation, api, receiver domain.Target, database *storage.Database, artifacts *artifact.Store, opts InstalledPackRunnerOptions) (*InstalledPackRunner, error) {
	if kit == nil || installation == nil || database == nil || artifacts == nil {
		return nil, fmt.Errorf("installed runner requires verified original objects and durable stores")
	}
	b, i := kit.Bindings(), installation.Binding()
	_, _, fixed := testpack.FixedInstalledInputContract()
	if b.ManifestSHA256 == "" || kit.Scenario() != fixed || i.ManifestSHA256 != b.ManifestSHA256 || i.TargetOrigin != api.BaseURL || i.RuleID <= 0 || len(installation.Files()) == 0 || len(installation.Files()) > installedOriginalCount {
		return nil, fmt.Errorf("installed runner original installation/kit/target binding differs")
	}
	// A different self-consistent kit cannot substitute for the exact original copy.
	for _, name := range []string{"manifest.json", "manifest.sha256", "README.md", "build_operational.py", "correlation-rules.zip", "corrtest_operational-0.1.0.tar.gz", "corrtest_operational-0.1.0.tar.gz.sha256", "flood-installed.json", "hash-vectors.json", "install_operational.py"} {
		raw, ok := kit.Artifact(name)
		original, present := installation.Evidence("kit/" + name)
		if !ok || !present || !bytes.Equal(raw, original) {
			return nil, fmt.Errorf("installed runner original kit bytes differ: %s", name)
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MonotonicNow == nil {
		opts.MonotonicNow = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	options := oscar.Options{Now: opts.Now, Getenv: opts.Getenv, HarnessVersion: opts.HarnessVersion}
	// Validation uses the real input constructor, with no send and no recorded
	// attempt. The actual start/token and sender are captured after live preflight.
	if _, err := oscar.NewInstalledPackInput(kit, strings.Repeat("0", 32), opts.Now().UTC().Truncate(time.Second), receiver, options); err != nil {
		return nil, err
	}
	preflight, err := oscar.NewInstalledPackReader(api, options)
	if err != nil {
		return nil, err
	}
	active, err := oscar.NewInstalledPackActiveReader(api, options)
	if err != nil {
		return nil, err
	}
	history, err := oscar.NewInstalledPackHistoryReader(api, options)
	if err != nil {
		return nil, err
	}
	audit, err := oscar.NewInstalledPackAuditReader(api, options)
	if err != nil {
		return nil, err
	}
	return &InstalledPackRunner{kit: kit, installation: installation, api: api, receiver: receiver, database: database, artifacts: artifacts, options: opts, preflight: preflight, active: active, history: history, audit: audit,
		expected: oscar.InstalledPackExpected{ManifestSHA256: b.ManifestSHA256, PackID: b.PackID, Version: b.Version, PackContentHash: b.PackContentHash, MappingNamespace: b.ProfileNamespace, MappingName: b.ProfileName, MappingSourceFile: b.MappingSourceFile, MappingStateSHA256: b.MappingStateSHA256, MappingRowKey: b.MappingRowKey, RuleJSON: kit.RuleJSON(), MappingStateJSON: kit.MappingStateJSON()},
		binding:  oscar.InstalledPackBinding{TargetOrigin: i.TargetOrigin, ManifestSHA256: i.ManifestSHA256, LoadID: i.LoadID, RuleID: i.RuleID, Runtime: oscar.InstalledRuntimeBinding{LoadedAt: i.LoadedAt, LastAttemptMS: i.LastAttemptMS, LoadedRuleIDs: []int{i.RuleID}}}}, nil
}

// Execute cannot resume or resend. A fresh run gets a distinct qualification
// token; a persisted unresolved send always remains UNKNOWN. No physical clear
// is attempted, including when all fixed audit assertions pass.
func (r *InstalledPackRunner) Execute(ctx context.Context, run domain.Run) (executionErr error) {
	r.mu.Lock()
	if r.used {
		r.mu.Unlock()
		return fmt.Errorf("installed runner is single-use")
	}
	r.used = true
	r.mu.Unlock()
	if run.Status != domain.RunQueued || run.TargetID != r.api.ID {
		return fmt.Errorf("installed runner requires a fresh queued API-target run")
	}
	// Cancellation while waiting for runtime serialization still leaves this mode
	// and a no-input terminal report durably identifiable, without any HTTP.
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := r.database.TransitionRun(setupCtx, run.ID, domain.RunPreflight, r.now(), "Installed fixed audit preflight"); err != nil {
		setupCancel()
		return err
	}
	report := installedRunReport{InstalledPackRunReport: oscar.InstalledPackRunReport{APIVersion: "corrtest.oscar/installed-run-v1", RunID: run.ID, InputPath: InstalledPackInputPath, Stage: "originals", StartedAt: r.now(), Verdict: domain.VerdictInconclusive, Coverage: oscar.InstalledPackCoverage()}}
	defer func() { executionErr = r.finish(run.ID, &report, executionErr) }()
	document, _ := json.Marshal(struct {
		RunID            string                       `json:"runId"`
		Pattern          string                       `json:"pattern"`
		InputPath        string                       `json:"inputPath"`
		Kit              testpack.Bindings            `json:"kit"`
		Installation     testpack.InstallationBinding `json:"installation"`
		APITargetID      string                       `json:"apiTargetId"`
		ReceiverTargetID string                       `json:"receiverTargetId"`
		ReceiverBaseURL  string                       `json:"receiverBaseUrl"`
	}{run.ID, "flood", InstalledPackInputPath, r.kit.Bindings(), r.installation.Binding(), r.api.ID, r.receiver.ID, r.receiver.BaseURL})
	coverage, _ := json.Marshal(report.Coverage)
	err := r.database.SetRunExecutionDocuments(setupCtx, run.ID, coverage, document, r.now())
	setupCancel()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.kit.Scenario().Budgets.ExecutionSeconds)*time.Second)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return err
	}
	token := make([]byte, 16)
	if _, err = io.ReadFull(r.options.Random, token); err != nil {
		return fmt.Errorf("qualification entropy: %w", err)
	}
	report.QualificationRun = hex.EncodeToString(token)
	var originalBytes int64
	for _, f := range r.installation.Files() {
		raw, ok := r.installation.Evidence(f.Path)
		originalBytes += int64(len(raw))
		if !ok || int64(len(raw)) != f.Size || len(raw) > 1<<20 || originalBytes > installedOriginalBytes || installedBytesHash(raw) != f.SHA256 {
			return fmt.Errorf("original evidence inventory changed or exceeded bounds")
		}
		if err = r.publish(ctx, &report, "installed/original/"+f.Path, raw, true); err != nil {
			return err
		}
	}
	report.Stage = "preflight"
	if err = r.observeConfiguration(ctx, &report, "before"); err != nil {
		return err
	}
	if err = r.absent(ctx, &report); err != nil {
		return err
	}
	start := r.now().Truncate(time.Second)
	input, err := oscar.NewInstalledPackInput(r.kit, report.QualificationRun, start, r.receiver, oscar.Options{Now: r.options.Now, Getenv: r.options.Getenv, HarnessVersion: r.options.HarnessVersion})
	if err != nil {
		return err
	}
	spec := oscar.InstalledPackInputSpec{APIVersion: "corrtest.oscar/installed-input-v1", ManifestSHA256: r.kit.Bindings().ManifestSHA256, QualificationRun: report.QualificationRun, StartsAt: start, ReceiverTargetID: r.receiver.ID, ReceiverBaseURL: r.receiver.BaseURL}
	manifest, err := oscar.FreezeInstalledPackInput(spec)
	if err != nil {
		return err
	}
	report.Spec = &spec
	if err = r.database.RegisterInstalledPack(ctx, run.ID, r.kit, r.installation, spec, r.now()); err != nil {
		return err
	}
	if err = r.database.TransitionRun(ctx, run.ID, domain.RunSettingUp, r.now(), "Original installation retained; no remote configuration writes"); err != nil {
		return err
	}
	if err = r.database.TransitionRun(ctx, run.ID, domain.RunInjecting, r.now(), "Record UNKNOWN and send fifteen fixed firings"); err != nil {
		return err
	}
	report.Stage = "input"
	var lastAckMonotonic time.Time
	for index := 0; index < 15; index++ {
		// This fixed correctness scenario must not burst through ordinary ingress limits.
		if index > 0 {
			if err = r.options.Sleep(ctx, time.Second); err != nil {
				return err
			}
		}
		recorded := false
		result, sendErr := input.SendRecorded(ctx, index, func(e oscar.InstalledPackInputEvidence) error {
			err := r.database.RecordInstalledPackAttempt(ctx, run.ID, e, r.now())
			recorded = err == nil
			return err
		})
		// A recorder error may have committed. Never infer NOT_REQUIRED from this
		// local flag; finalization reads the transactional snapshot independently.
		if !recorded {
			if sendErr == nil {
				return fmt.Errorf("input recorder was not called")
			}
			return sendErr
		}
		if index == 14 && sendErr == nil && result.Class == oscar.InjectionForwarded {
			lastAckMonotonic = r.options.MonotonicNow()
		}
		e, _, _ := manifest.Prepare(index)
		outcomeCtx, outcomeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		detail := ""
		if sendErr != nil {
			detail = sendErr.Error()
		}
		err = r.database.CompleteInstalledPackAttempt(outcomeCtx, run.ID, index, e.Request.BodySHA256, result, detail, r.now())
		outcomeCancel()
		if err != nil {
			return errors.Join(sendErr, err)
		}
		if sendErr != nil || result.Class != oscar.InjectionForwarded {
			return errors.Join(sendErr, fmt.Errorf("input %d has no confirmed exact-revision forwarding; no retry", index))
		}
	}
	forwarded, err := r.database.ReadInstalledPackSnapshot(ctx, run.ID)
	if err != nil {
		return err
	}
	if len(forwarded.Attempts) != 15 || installedSnapshotCleanup(forwarded) != domain.CleanupDirty || forwarded.Attempts[14].CompletedAt == nil {
		return fmt.Errorf("fifteen durable forwarding outcomes required before observation")
	}
	lastForward := *forwarded.Attempts[14].CompletedAt
	report.LastForwardAt = &lastForward
	if err = r.database.TransitionRun(ctx, run.ID, domain.RunObserving, r.now(), "Observe bounded complete audit interval"); err != nil {
		return err
	}
	report.Stage = "observation"
	if err = r.observe(ctx, &report, manifest, lastAckMonotonic); err != nil {
		return err
	}
	report.Stage = "postflight"
	if err = r.observeConfiguration(ctx, &report, "after"); err != nil {
		return err
	}
	// All outputs must still be present before any PASS claim, including original
	// installation bytes. A runtime exporter will independently check them again.
	report.Stage = "evidence"
	for _, a := range report.Artifacts {
		valid, err := r.artifacts.Verify(ctx, a)
		if err != nil {
			return err
		}
		if valid != artifact.IntegrityValid {
			return fmt.Errorf("retained evidence no longer verifies")
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	snapshot, err := r.database.ReadInstalledPackSnapshot(ctx, run.ID)
	if err != nil {
		return err
	}
	if len(snapshot.Attempts) != 15 || installedSnapshotCleanup(snapshot) != domain.CleanupDirty {
		return fmt.Errorf("fifteen durable forwarding outcomes required")
	}
	evaluation := report.Evaluation
	if report.Adverse != nil {
		evaluation = *report.Adverse
	}
	switch evaluation.Disposition {
	case oscar.InstalledFloodEligible:
		report.Verdict = domain.VerdictPass
	case oscar.InstalledFloodViolation:
		report.Verdict = domain.VerdictFail
	default:
		report.Verdict = domain.VerdictInconclusive
	}
	if err = r.database.TransitionRun(ctx, run.ID, domain.RunAsserting, r.now(), "Fixed audit assertions evaluated; no clear or delivery claim"); err != nil {
		return err
	}
	report.Stage = "complete"
	return nil
}

func (r *InstalledPackRunner) now() time.Time { return r.options.Now().UTC() }
func installedBytesHash(raw []byte) string    { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

func (r *InstalledPackRunner) publish(ctx context.Context, report *installedRunReport, name string, raw []byte, original bool) error {
	if len(raw) > installedEntryBytes {
		return fmt.Errorf("installed artifact exceeds fixed entry bound")
	}
	if !original {
		if report.observationCount >= installedObservationCount || report.observationBytes+int64(len(raw)) > installedObservationBytes {
			return fmt.Errorf("installed observation retention budget exhausted; evidence not truncated")
		}
		report.observationCount++
		report.observationBytes += int64(len(raw))
	}
	id := report.RunID + ":" + name
	a := domain.Artifact{ID: id, RunID: report.RunID, Kind: "installed-pack", RelativePath: "runs/" + report.RunID + "/" + name, MIMEType: "application/octet-stream", RedactionState: "private-original-response", Availability: domain.ArtifactPending, CreatedAt: r.now()}
	if err := r.database.CreateArtifactPending(ctx, a); err != nil {
		return err
	}
	m, err := r.artifacts.Write(ctx, report.RunID, name, a.MIMEType, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if m.RelativePath != a.RelativePath || m.ByteSize != int64(len(raw)) || m.SHA256 != installedBytesHash(raw) {
		return fmt.Errorf("installed artifact publication differs")
	}
	if err = r.database.MarkArtifactAvailable(ctx, id, m.SHA256, m.ByteSize); err != nil {
		return err
	}
	report.Artifacts = append(report.Artifacts, m)
	return nil
}

func (r *InstalledPackRunner) observeConfiguration(ctx context.Context, report *installedRunReport, stage string) (readErr error) {
	before := len(report.Artifacts)
	read := installedRead{Kind: "configuration-" + stage, StartedAt: r.now()}
	defer func() {
		read.EndedAt = r.now()
		for _, a := range report.Artifacts[before:] {
			read.Artifacts = append(read.Artifacts, a.RelativePath)
		}
		if readErr != nil {
			read.Error = readErr.Error()
		}
		report.Reads = append(report.Reads, read)
	}()
	observed, err := r.preflight.Read(ctx, r.expected, r.binding)
	if err != nil {
		return err
	}
	for _, v := range []struct {
		name string
		raw  json.RawMessage
	}{{"packs", observed.Packs}, {"load", observed.Load}, {"mapping", observed.Mapping}, {"rules", observed.Rules}, {"rule", observed.Rule}, {"runtime", observed.Runtime}} {
		if err = r.publish(ctx, report, "installed/preflight/"+stage+"/"+v.name+".json", v.raw, false); err != nil {
			return err
		}
	}
	return nil
}

// Raw row arrays retain exact row bytes supplied by the readers; they are not
// invented representations of the original pagination envelopes.
func installedRawRows(rows []json.RawMessage) ([]byte, error) {
	size := 2
	for _, row := range rows {
		size += len(row) + 1
		if size > installedEntryBytes {
			return nil, fmt.Errorf("installed raw-row evidence exceeds entry bound")
		}
	}
	raw := make([]byte, 0, size)
	raw = append(raw, '[')
	for index, row := range rows {
		if index > 0 {
			raw = append(raw, ',')
		}
		raw = append(raw, row...)
	}
	return append(raw, ']'), nil
}
func (r *InstalledPackRunner) saveActive(ctx context.Context, report *installedRunReport, prefix string, records []oscar.InstalledPackActiveRecord) error {
	rows := make([]json.RawMessage, len(records))
	for n, row := range records {
		rows[n] = row.Raw
	}
	raw, err := installedRawRows(rows)
	if err != nil {
		return err
	}
	return r.publish(ctx, report, prefix+"/active-raw-rows.json", raw, false)
}
func (r *InstalledPackRunner) saveHistory(ctx context.Context, report *installedRunReport, prefix string, observation *oscar.InstalledPackHistoryObservation) error {
	for index, raw := range observation.Pages {
		if err := r.publish(ctx, report, fmt.Sprintf("%s/history-page-%03d.json", prefix, index+1), raw, false); err != nil {
			return err
		}
	}
	return nil
}
func (r *InstalledPackRunner) historyQuery(report *installedRunReport) oscar.HistoryQuery {
	start := report.StartedAt.Add(-10 * time.Minute)
	if report.Spec != nil {
		start = report.Spec.StartsAt.Add(-time.Minute)
	}
	return oscar.HistoryQuery{AlertName: "CorrTestOperationalFlood", Start: start, End: r.now().Add(time.Minute)}
}
func (r *InstalledPackRunner) absent(ctx context.Context, report *installedRunReport) (readErr error) {
	before := len(report.Artifacts)
	read := installedRead{Kind: "token-absence-full-datetime-range", StartedAt: r.now()}
	defer func() {
		read.EndedAt = r.now()
		for _, a := range report.Artifacts[before:] {
			read.Artifacts = append(read.Artifacts, a.RelativePath)
		}
		if readErr != nil {
			read.Error = readErr.Error()
		}
		report.Reads = append(report.Reads, read)
	}()
	rows, err := r.active.FindActive(ctx, report.QualificationRun)
	if err != nil {
		return err
	}
	if err = r.saveActive(ctx, report, "installed/preflight/absence", rows); err != nil {
		return err
	}
	if len(rows) != 0 {
		return fmt.Errorf("qualification token is not absent from Active")
	}
	// Absence must not miss an older reused token. Query the full supported
	// MySQL DATETIME range, still under the reader's strict row/page/byte limits.
	history, err := r.history.FindHistory(ctx, oscar.HistoryQuery{AlertName: "CorrTestOperationalFlood", Start: time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)})
	if err != nil {
		return err
	}
	if err = r.saveHistory(ctx, report, "installed/preflight/absence", history); err != nil {
		return err
	}
	for _, row := range history.Records {
		if row.Labels["qualification_run"] == report.QualificationRun || strings.HasPrefix(row.Labels["source_id"], report.QualificationRun+"-") {
			return fmt.Errorf("qualification token is not absent from History")
		}
	}
	return nil
}
func (r *InstalledPackRunner) observe(ctx context.Context, report *installedRunReport, manifest oscar.InstalledPackManifest, lastAckMonotonic time.Time) error {
	budgets := r.kit.Scenario().Budgets
	if report.LastForwardAt == nil || lastAckMonotonic.IsZero() {
		return fmt.Errorf("missing durable last forwarding time")
	}
	completeAfter := report.LastForwardAt.Add(time.Duration(budgets.WindowSeconds+budgets.ObservationLagSeconds) * time.Second)
	deadline := report.StartedAt.Add(time.Duration(budgets.ExecutionSeconds) * time.Second)
	previous := r.now()
	rule := oscar.Rule{ID: r.binding.RuleID, Name: r.kit.Bindings().RuleName, Pattern: "flood"}
	for index := 0; index <= budgets.ExecutionSeconds; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := r.now()
		if now.Before(previous) || !now.Before(deadline) {
			return fmt.Errorf("installed observation clock/budget exhausted")
		}
		previous = now
		poll := installedPoll{Index: index, StartedAt: now}
		before := len(report.Artifacts)
		prefix := fmt.Sprintf("installed/observations/%03d", index)
		active, err := r.active.FindActive(ctx, report.QualificationRun)
		if err != nil {
			return err
		}
		if err = r.saveActive(ctx, report, prefix, active); err != nil {
			return err
		}
		if len(active) > 15 {
			return fmt.Errorf("installed Active population exceeds fixed fifteen")
		}
		history, err := r.history.FindHistory(ctx, r.historyQuery(report))
		if err != nil {
			return err
		}
		if err = r.saveHistory(ctx, report, prefix, history); err != nil {
			return err
		}
		audits := []oscar.InstalledPackAuditRecord{}
		var auditErr error
		for _, row := range active {
			found, err := r.audit.FindAudit(ctx, row.Fingerprint, rule)
			if err != nil {
				auditErr = err
				break
			}
			audits = append(audits, found...)
			if len(audits) > 30 {
				auditErr = fmt.Errorf("installed audit population is ambiguous and exceeds finite bound")
				break
			}
		}
		rawRows := make([]json.RawMessage, len(audits))
		for n, row := range audits {
			rawRows[n] = row.Raw
		}
		raw, err := installedRawRows(rawRows)
		if err != nil {
			return err
		}
		// Preserve earlier complete per-fingerprint reads even if a later read
		// fails. The collection is explicitly incomplete and never evaluated.
		saveCtx := ctx
		saveCancel := func() {}
		if auditErr != nil {
			saveCtx, saveCancel = context.WithTimeout(context.Background(), 5*time.Second)
		}
		err = r.publish(saveCtx, report, prefix+"/audit-raw-rows.json", raw, false)
		saveCancel()
		if err != nil || auditErr != nil {
			return errors.Join(auditErr, err)
		}
		report.Evaluation = oscar.EvaluateInstalledFlood(manifest, rule, oscar.InstalledFloodEvidence{Active: active, History: *history, Audit: audits})
		if report.Evaluation.Disposition == oscar.InstalledFloodViolation || report.Evaluation.Disposition == oscar.InstalledFloodAmbiguous {
			if report.Adverse == nil || report.Evaluation.Disposition == oscar.InstalledFloodViolation {
				copy := report.Evaluation
				report.Adverse = &copy
			}
		}
		if r.now().Before(previous) || !r.now().Before(deadline) {
			return fmt.Errorf("installed observation clock/budget changed during reads")
		}
		poll.ElapsedSinceLastForward = r.options.MonotonicNow().Sub(lastAckMonotonic)
		if poll.ElapsedSinceLastForward < 0 {
			return fmt.Errorf("installed monotonic observation clock moved backward")
		}
		poll.EndedAt = r.now()
		poll.Evaluation = report.Evaluation
		for _, a := range report.Artifacts[before:] {
			poll.Artifacts = append(poll.Artifacts, a.RelativePath)
		}
		report.Polls = append(report.Polls, poll)
		detail, _ := json.Marshal(poll)
		if _, err = r.database.AppendRunEvent(ctx, domain.RunEvent{RunID: report.RunID, Type: "installed_pack.observed", Level: "info", OccurredAt: r.now(), Summary: "Fixed installed audit observation (not dispatch proof)", DetailJSON: string(detail)}); err != nil {
			return err
		}
		if !r.now().Before(completeAfter) && poll.ElapsedSinceLastForward >= time.Duration(budgets.WindowSeconds+budgets.ObservationLagSeconds)*time.Second {
			return nil
		}
		if err = r.options.Sleep(ctx, time.Duration(budgets.PollSeconds)*time.Second); err != nil {
			return err
		}
	}
	return fmt.Errorf("installed observation iteration budget exhausted")
}

func installedSnapshotCleanup(snapshot storage.InstalledPackSnapshot) domain.CleanupStatus {
	if len(snapshot.Attempts) == 0 {
		return domain.CleanupNotRequired
	}
	for _, attempt := range snapshot.Attempts {
		if attempt.Result == nil || attempt.TransportError != "" || attempt.Result.Class != oscar.InjectionForwarded {
			return domain.CleanupUnknown
		}
	}
	return domain.CleanupDirty
}
func (r *InstalledPackRunner) finish(runID string, report *installedRunReport, executionErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved, err := r.database.GetRun(ctx, runID)
	if err != nil {
		return errors.Join(executionErr, err)
	}
	snapshot, readErr := r.database.ReadInstalledPackSnapshot(ctx, runID)
	switch {
	case readErr == nil:
		report.Cleanup = installedSnapshotCleanup(snapshot)
	case errors.Is(readErr, sql.ErrNoRows):
		report.Cleanup = domain.CleanupNotRequired
		if saved.CleanupStatus == domain.CleanupUnknown {
			report.Cleanup = domain.CleanupUnknown
		}
	default:
		report.Cleanup = domain.CleanupUnknown
		executionErr = errors.Join(executionErr, readErr)
	}
	if executionErr != nil {
		report.Verdict = domain.VerdictInconclusive
		report.Failure = executionErr.Error()
	}
	if saved.Status != domain.RunAsserting || executionErr != nil {
		if err = r.database.TransitionRun(ctx, runID, domain.RunCancelling, r.now(), "Installed run stopped; no automatic resend or clear"); err != nil {
			return errors.Join(executionErr, err)
		}
	}
	if err = r.database.TransitionRun(ctx, runID, domain.RunCleaningUp, r.now(), "Retain original input responsibility; physical cleanup unsupported"); err != nil {
		return errors.Join(executionErr, err)
	}
	report.EndedAt = r.now()
	raw, err := json.Marshal(report)
	if err != nil {
		return errors.Join(executionErr, err)
	}
	return errors.Join(executionErr, r.database.CompleteRun(ctx, runID, report.Verdict, report.Cleanup, raw, report.Failure, r.now()))
}
