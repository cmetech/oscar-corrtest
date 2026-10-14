// Package runner owns serial, recoverable correlation run execution.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

type API interface {
	ValidateRule(context.Context, compiler.RulePlan) error
	CreateRule(context.Context, compiler.RulePlan) (oscar.Rule, error)
	GetRule(context.Context, int) (oscar.Rule, error)
	FindRules(context.Context, string) ([]oscar.Rule, error)
	DeleteRule(context.Context, int) error
	Inject(context.Context, compiler.AlertPlan) (oscar.InjectionResult, error)
	ResolveHistory(context.Context, oscar.HistoryRecord) (oscar.InjectionResult, error)
	FindHistory(context.Context, oscar.HistoryQuery) ([]oscar.HistoryRecord, error)
	CorrelationAudit(context.Context, string) ([]oscar.AuditRecord, error)
	NotificationAudit(context.Context, string, time.Time, time.Time) ([]oscar.NotificationRecord, error)
}

type Store interface {
	TransitionRun(context.Context, string, domain.RunStatus, time.Time, string) error
	AppendRunEvent(context.Context, domain.RunEvent) (domain.RunEvent, error)
	SetRunExecutionDocuments(context.Context, string, json.RawMessage, json.RawMessage, time.Time) error
	FinalizeRun(context.Context, string, domain.ExecutionFacts, domain.Verdict, domain.CleanupStatus, json.RawMessage, string, time.Time) error
	CreateResource(context.Context, domain.Resource) error
	AdoptResource(context.Context, string, string, time.Time) error
	MarkResourceDeleted(context.Context, string, time.Time) error
	MarkResourceCleanupError(context.Context, string, string) error
}

type EvidenceWriter interface {
	WriteEvidence(context.Context, string, domain.ExecutionFacts, time.Time) (domain.Artifact, error)
}

type CapabilitySnapshot struct {
	APIProfile      string `json:"apiProfile"`
	PipelineMode    string `json:"pipelineMode"`
	Ready           bool   `json:"ready"`
	LabelsSurvived  bool   `json:"labelsSurvived"`
	Compatibility   bool   `json:"compatibilityMode"`
	ReadinessDetail string `json:"readinessDetail,omitempty"`
}

type Options struct {
	PollInterval      time.Duration
	ObservationWindow time.Duration
	Stabilization     time.Duration
	CleanupTimeout    time.Duration
	Now               func() time.Time
	Sleep             func(context.Context, time.Duration) error
	EvidenceWriter    EvidenceWriter
}

type Runner struct {
	store Store
	api   API
	opts  Options
}

func New(store Store, api API, options Options) *Runner {
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.Stabilization <= 0 {
		options.Stabilization = 5 * time.Second
	}
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = 30 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	return &Runner{store: store, api: api, opts: options}
}

type caseResult struct {
	Name                 string                     `json:"name"`
	Code                 string                     `json:"code"`
	Verdict              string                     `json:"verdict"`
	SourceFingerprints   []string                   `json:"sourceFingerprints"`
	SyntheticCount       int                        `json:"syntheticCount"`
	AuditOutcomes        []string                   `json:"auditOutcomes"`
	NotificationStatuses []string                   `json:"notificationStatuses,omitempty"`
	ObservationComplete  bool                       `json:"observationComplete"`
	Explanation          string                     `json:"explanation"`
	Assertions           []assertionResult          `json:"assertions"`
	SourceEpisodes       []sourceEpisodeEvidence    `json:"sourceEpisodes,omitempty"`
	SourceHistory        []oscar.HistoryRecord      `json:"sourceHistory"`
	SyntheticHistory     []oscar.HistoryRecord      `json:"syntheticHistory"`
	Audits               []oscar.AuditRecord        `json:"audits"`
	Notifications        []oscar.NotificationRecord `json:"notifications,omitempty"`
	StartedAt            time.Time                  `json:"startedAt"`
	EndedAt              time.Time                  `json:"endedAt"`
}

type checkpointResult struct {
	AfterEvent int           `json:"afterEvent"`
	Window     time.Duration `json:"window"`
	caseResult
}

type canonicalReport struct {
	Checkpoints  []checkpointResult           `json:"checkpoints,omitempty"`
	Coverage     map[string]string            `json:"coverage"`
	APIVersion   string                       `json:"apiVersion"`
	RunID        string                       `json:"runId"`
	Pattern      string                       `json:"pattern"`
	PlanDigest   string                       `json:"planDigest"`
	Verdict      domain.Verdict               `json:"verdict"`
	Cleanup      domain.CleanupStatus         `json:"cleanupStatus"`
	Capabilities CapabilitySnapshot           `json:"capabilities"`
	Cases        []caseResult                 `json:"cases"`
	Attempts     []domain.AlertAttemptFact    `json:"attempts"`
	Artifacts    []domain.Artifact            `json:"artifacts,omitempty"`
	Resolutions  []domain.AlertResolutionFact `json:"resolutions,omitempty"`
	CompletedAt  time.Time                    `json:"completedAt"`
}

// Execute runs one immutable plan. It never resumes injection after interruption.
func (r *Runner) Execute(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot) error {
	attempts := make([]domain.AlertAttemptFact, 0, plan.MutationBudget.Alerts)
	var checkpoints []checkpointResult
	started := r.opts.Now().UTC()
	run.StartedAt = &started
	budgetDuration := plan.MaxDuration
	if budgetDuration <= 0 {
		budgetDuration = r.opts.CleanupTimeout
	}
	executionCtx, executionCancel := context.WithTimeout(ctx, budgetDuration)
	defer executionCancel()
	ctx = executionCtx
	budgetDeadline := started.Add(budgetDuration)
	budgetError := func() error {
		if ctx.Err() != nil {
			return fmt.Errorf("plan maximum duration exceeded: %w", ctx.Err())
		}
		if !r.opts.Now().UTC().Before(budgetDeadline) {
			return fmt.Errorf("plan maximum duration exceeded")
		}
		return nil
	}
	capJSON, _ := json.Marshal(capabilities)
	planJSON, _ := json.Marshal(plan)
	startupCtx, startupCancel := context.WithTimeout(context.WithoutCancel(ctx), r.opts.CleanupTimeout)
	defer startupCancel()
	if err := r.store.SetRunExecutionDocuments(startupCtx, run.ID, capJSON, planJSON, started); err != nil {
		return err
	}
	if err := r.transition(startupCtx, run.ID, domain.RunPreflight, "Target preflight started"); err != nil {
		return err
	}
	if ctx.Err() != nil {
		if err := r.finishWithoutMutation(startupCtx, run, plan, capabilities, domain.VerdictError, ctx.Err().Error()); err != nil {
			return err
		}
		return ctx.Err()
	}
	if plan.InputPath != "" && plan.InputPath != "alert-engine" {
		return r.finishWithoutMutation(ctx, run, plan, capabilities, domain.VerdictError, "unsupported input path: "+plan.InputPath)
	}
	if err := compiler.ValidateControlExclusion(plan); err != nil {
		return r.finishWithoutMutation(ctx, run, plan, capabilities, domain.VerdictError, err.Error())
	}
	if len(plan.Cases) == 0 {
		return r.finishWithoutMutation(ctx, run, plan, capabilities, domain.VerdictError, "compiled plan contains no executable cases")
	}
	if plan.MaxDuration <= 0 {
		return r.finishWithoutMutation(ctx, run, plan, capabilities, domain.VerdictError, "compiled plan maximum duration is invalid")
	}
	if !capabilities.Ready || !capabilities.LabelsSurvived || (capabilities.PipelineMode != "phase_b_dispatch" && capabilities.PipelineMode != "phase_a_audit_only") {
		explanation := "required Phase-A/Phase-B readiness and reserved-label survival were not proven"
		return r.finishWithoutMutation(ctx, run, plan, capabilities, domain.VerdictInconclusive, explanation)
	}

	if err := r.transition(startupCtx, run.ID, domain.RunSettingUp, "Creating temporary correlation rules"); err != nil {
		return err
	}
	resources := make([]domain.Resource, 0, len(plan.Cases))
	for index, item := range plan.Cases {
		if err := budgetError(); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
		}
		resource := domain.Resource{ID: fmt.Sprintf("res_%s_%03d", run.ShortToken, index+1), RunID: run.ID, Kind: "correlation_rule",
			ExternalName: item.Rule.Name, OwnershipToken: run.ID, LifecycleState: domain.ResourceProposed}
		if err := r.store.CreateResource(ctx, resource); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
		}
		resources = append(resources, resource)
		if err := r.api.ValidateRule(ctx, item.Rule); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
		}
		created, err := r.api.CreateRule(ctx, item.Rule)
		if err != nil {
			candidates, reconcileErr := r.api.FindRules(ctx, item.Rule.Name)
			if reconcileErr != nil || len(candidates) != 1 || candidates[0].Name != item.Rule.Name || candidates[0].Pattern != item.Rule.Pattern || candidates[0].Description != item.Rule.Description {
				_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "rule create outcome unknown")
				resources[len(resources)-1].LifecycleState = domain.ResourceUnknown
				return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, fmt.Errorf("rule create outcome could not be safely reconciled: %w", err))
			}
			created = candidates[0]
		}
		if created.Name != item.Rule.Name || created.Description != item.Rule.Description {
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "created rule ownership did not round-trip")
			resources[len(resources)-1].LifecycleState = domain.ResourceUnknown
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, fmt.Errorf("created rule ownership mismatch"))
		}
		resources[len(resources)-1].ExternalID = strconv.Itoa(created.ID)
		if err := r.store.AdoptResource(ctx, resource.ID, strconv.Itoa(created.ID), r.opts.Now().UTC()); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
		}
		resources[len(resources)-1].LifecycleState = domain.ResourceCreated
	}

	if err := r.transition(ctx, run.ID, domain.RunInjecting, "Sending deterministic source alerts"); err != nil {
		return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
	}
	caseStarted := make(map[string]time.Time, len(plan.Cases))
	for _, item := range plan.Cases {
		var elapsed time.Duration
		for alertIndex, alert := range item.Alerts {
			if err := budgetError(); err != nil {
				return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
			}
			if alertIndex == 0 {
				caseStarted[item.Code] = r.opts.Now().UTC()
				item.EpisodeBase = caseStarted[item.Code].Truncate(time.Millisecond)
			}
			if len(item.Checkpoints) > 0 {
				elapsed = r.opts.Now().Sub(caseStarted[item.Code])
			}
			if alert.Delay > elapsed {
				detail, _ := json.Marshal(map[string]string{"alertName": alert.Name, "status": alert.Status, "delay": alert.Delay.String()})
				if _, err := r.store.AppendRunEvent(ctx, domain.RunEvent{RunID: run.ID, Type: "alert.scheduled", Level: "info", OccurredAt: r.opts.Now().UTC(), Summary: "Delayed alert stimulus scheduled", DetailJSON: string(detail)}); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
				if err := r.opts.Sleep(ctx, alert.Delay-elapsed); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
				if err := budgetError(); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
				elapsed = alert.Delay
			}
			if alert.EpisodeOffset != nil {
				alert.StartsAt = caseStarted[item.Code].Truncate(time.Millisecond).Add(*alert.EpisodeOffset)
				if alert.Status == "resolved" {
					alert.EndsAt = caseStarted[item.Code].Truncate(time.Millisecond).Add(alert.Delay)
				}
				detail, _ := json.Marshal(map[string]any{"caseCode": item.Code, "eventId": alert.Labels["oscar_test_event_id"], "attempt": alertIndex + 1, "status": alert.Status, "startsAt": alert.StartsAt, "endsAt": alert.EndsAt})
				if _, err := r.store.AppendRunEvent(ctx, domain.RunEvent{RunID: run.ID, Type: "alert.prepared", Level: "info", OccurredAt: r.opts.Now().UTC(), Summary: "Named source episode prepared for OSCAR Alert Engine ingress", DetailJSON: string(detail)}); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
			}
			// Retain uncertain work before the external call. This is in-memory evidence,
			// not a durable before-send ledger or permission to retry the POST.
			attempts = append(attempts, domain.AlertAttemptFact{CaseStableKey: item.Code, EventID: alert.Labels["oscar_test_event_id"], EventIndex: alertIndex + 1,
				SendState: "ATTEMPTED", InjectionClass: string(oscar.InjectionIndeterminate), StartsAt: alert.StartsAt})
			result, err := r.api.Inject(ctx, alert)
			attempt := &attempts[len(attempts)-1]
			attempt.StatusCode = result.StatusCode
			if err != nil {
				var remote *oscar.MachineError
				if errors.As(err, &remote) {
					attempt.StatusCode = remote.StatusCode
				}
				return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
			}
			attempt.InjectionClass = string(result.Class)
			if result.Class != oscar.InjectionAccepted {
				return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, fmt.Errorf("injection was %s", result.Class))
			}
			attempt.SendState = "SENT"
			for _, point := range item.Checkpoints {
				if point.AfterEvent != alertIndex+1 {
					continue
				}
				prefix := item
				prefix.Name, prefix.Alerts, prefix.Assertions, prefix.ObservationWindow = point.Name, item.Alerts[:point.AfterEvent], point.Assertions, point.Window
				checkpointStart := r.opts.Now().UTC()
				detail, _ := json.Marshal(point)
				if _, err := r.store.AppendRunEvent(ctx, domain.RunEvent{RunID: run.ID, Type: "checkpoint.started", Level: "info", OccurredAt: checkpointStart, Summary: "Observing checkpoint " + point.Name, DetailJSON: string(detail)}); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
				observed, observeErr := r.observe(ctx, run, prefix, started, checkpointStart, capabilities.PipelineMode, true)
				if observeErr != nil {
					observed = caseResult{Name: point.Name, Code: item.Code, Verdict: string(domain.VerdictError), StartedAt: checkpointStart, EndedAt: r.opts.Now().UTC(), Explanation: observeErr.Error()}
				}
				checkpoints = append(checkpoints, checkpointResult{AfterEvent: point.AfterEvent, Window: point.Window, caseResult: observed})
				detail, _ = json.Marshal(checkpoints[len(checkpoints)-1])
				evidenceCtx, evidenceCancel := context.WithTimeout(context.WithoutCancel(ctx), r.opts.CleanupTimeout)
				_, evidenceErr := r.store.AppendRunEvent(evidenceCtx, domain.RunEvent{RunID: run.ID, Type: "checkpoint.completed", Level: "info", OccurredAt: r.opts.Now().UTC(), Summary: "Checkpoint " + point.Name + ": " + observed.Verdict, DetailJSON: string(detail)})
				evidenceCancel()
				if err := evidenceErr; err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
				if observeErr != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, observeErr)
				}
				if err := budgetError(); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
				}
				if observed.Verdict != string(domain.VerdictPass) {
					cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.opts.CleanupTimeout)
					defer cancel()
					if err := r.transition(cleanupCtx, run.ID, domain.RunCancelling, "Checkpoint did not pass; stopping later stimuli"); err != nil {
						return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
					}
					cleanup, resolutions, cleanupErr := r.cleanup(cleanupCtx, run.ID, resources, nil, func(ctx context.Context) ([]oscar.HistoryRecord, error) {
						return r.discoverAttemptedHistory(ctx, run, plan, attempts)
					})
					return r.complete(cleanupCtx, run, plan, capabilities, nil, checkpoints, attempts, resolutions, domain.Verdict(observed.Verdict), cleanup, errorString(cleanupErr))
				}
			}
		}
	}

	if err := r.transition(ctx, run.ID, domain.RunObserving, "Observing OSCAR history and correlation audit"); err != nil {
		return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
	}
	results := make([]caseResult, 0, len(plan.Cases))
	for _, item := range plan.Cases {
		item.EpisodeBase = caseStarted[item.Code].Truncate(time.Millisecond)
		result, err := r.observeCase(ctx, run, item, started, caseStarted[item.Code], capabilities.PipelineMode)
		if err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
		}
		if err := budgetError(); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
		}
		results = append(results, result)
	}
	if err := r.transition(ctx, run.ID, domain.RunAsserting, "Evaluating terminal evidence"); err != nil {
		return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, err)
	}
	verdict := domain.VerdictPass
	for _, result := range results {
		if result.Verdict == string(domain.VerdictFail) || result.Verdict == string(domain.VerdictError) {
			verdict = domain.VerdictFail
			break
		}
		if result.Verdict == string(domain.VerdictInconclusive) || result.Verdict == string(domain.VerdictSkipped) {
			verdict = domain.VerdictInconclusive
		}
	}
	if ctx.Err() != nil {
		return r.failAndCleanup(ctx, run, plan, capabilities, resources, attempts, checkpoints, ctx.Err())
	}
	completionCtx, completionCancel := context.WithTimeout(context.WithoutCancel(ctx), r.opts.CleanupTimeout)
	defer completionCancel()
	cleanup, resolutions, cleanupError := r.cleanup(completionCtx, run.ID, resources, observedHistory(results), nil)
	if cleanupError != nil {
		cleanup = domain.CleanupDirty
	}
	return r.complete(completionCtx, run, plan, capabilities, results, checkpoints, attempts, resolutions, verdict, cleanup, errorString(cleanupError))
}

func (r *Runner) cleanup(ctx context.Context, runID string, resources []domain.Resource, histories []oscar.HistoryRecord, discover func(context.Context) ([]oscar.HistoryRecord, error)) (domain.CleanupStatus, []domain.AlertResolutionFact, error) {
	var failures []string
	var resolutions []domain.AlertResolutionFact
	if err := r.transition(ctx, runID, domain.RunCleaningUp, "Deleting owned temporary rules and resolving test alerts"); err != nil {
		failures = append(failures, "persist CLEANING_UP: "+err.Error())
	}
	if len(resources) == 0 && len(histories) == 0 && discover == nil && len(failures) == 0 {
		return domain.CleanupNotRequired, nil, nil
	}
	for _, resource := range resources {
		if resource.ExternalID == "" {
			if resource.LifecycleState == domain.ResourceProposed {
				if err := r.store.MarkResourceDeleted(ctx, resource.ID, r.opts.Now().UTC()); err != nil {
					failures = append(failures, resource.ExternalName)
				}
				continue
			}
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "external rule identity is unknown")
			failures = append(failures, resource.ExternalName)
			continue
		}
		id, err := strconv.Atoi(resource.ExternalID)
		if err != nil || id <= 0 {
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "external rule identity is invalid")
			failures = append(failures, resource.ExternalName)
			continue
		}
		if err := r.api.DeleteRule(ctx, id); err != nil {
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "delete failed")
			failures = append(failures, resource.ExternalName)
			continue
		}
		if err := r.store.MarkResourceDeleted(ctx, resource.ID, r.opts.Now().UTC()); err != nil {
			failures = append(failures, resource.ExternalName)
		}
	}
	if discover != nil {
		recovered, err := discover(ctx)
		histories = append(histories, recovered...)
		if err != nil {
			failures = append(failures, "alert history recovery: "+err.Error())
		}
	}
	for _, record := range histories {
		if record.Labels["oscar_test_run_id"] != runID || record.Fingerprint == "" {
			failures = append(failures, record.AlertName+": unsafe alert cleanup identity")
			continue
		}
		result, err := r.api.ResolveHistory(ctx, record)
		fact := domain.AlertResolutionFact{AlertName: record.AlertName, Fingerprint: record.Fingerprint, InjectionClass: string(result.Class), StatusCode: result.StatusCode, Accepted: err == nil && result.Class == oscar.InjectionAccepted}
		if err != nil {
			fact.InjectionClass = string(oscar.InjectionIndeterminate)
			if fact.StatusCode == 0 {
				fact.StatusCode = 500
			}
		}
		resolutions = append(resolutions, fact)
		if err != nil || result.Class != oscar.InjectionAccepted {
			failures = append(failures, record.AlertName+": alert resolution was not accepted")
		}
	}
	if len(failures) != 0 {
		return domain.CleanupDirty, resolutions, fmt.Errorf("cleanup failed for %s", strings.Join(failures, ", "))
	}
	return domain.CleanupClean, resolutions, nil
}

// Refires share a fingerprint; API row order must not select an obsolete cleanup episode.
func retainLatestHistory(records map[string]oscar.HistoryRecord, next oscar.HistoryRecord) {
	if previous, found := records[next.Fingerprint]; !found || !previous.StartsAt.After(next.StartsAt) {
		records[next.Fingerprint] = next
	}
}

func uniqueHistory(records []oscar.HistoryRecord) []oscar.HistoryRecord {
	byFingerprint := make(map[string]oscar.HistoryRecord, len(records))
	for _, record := range records {
		if record.Fingerprint != "" {
			retainLatestHistory(byFingerprint, record)
		}
	}
	keys := make([]string, 0, len(byFingerprint))
	for fingerprint := range byFingerprint {
		keys = append(keys, fingerprint)
	}
	sort.Strings(keys)
	result := make([]oscar.HistoryRecord, 0, len(keys))
	for _, fingerprint := range keys {
		result = append(result, byFingerprint[fingerprint])
	}
	return result
}

func observedHistory(results []caseResult) []oscar.HistoryRecord {
	var records []oscar.HistoryRecord
	for _, result := range results {
		records = append(records, result.SourceHistory...)
		records = append(records, result.SyntheticHistory...)
	}
	return uniqueHistory(records)
}

func (r *Runner) failAndCleanup(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot, resources []domain.Resource, attempts []domain.AlertAttemptFact, checkpoints []checkpointResult, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.opts.CleanupTimeout)
	defer cancel()
	if err := r.transition(cleanupCtx, run.ID, domain.RunCancelling, "Run failed or was cancelled; cleanup required"); err != nil {
		cause = fmt.Errorf("persist cancellation before cleanup: %w (original error: %v)", err, cause)
	}
	var discover func(context.Context) ([]oscar.HistoryRecord, error)
	if len(attempts) != 0 {
		discover = func(discoveryCtx context.Context) ([]oscar.HistoryRecord, error) {
			return r.discoverAttemptedHistory(discoveryCtx, run, plan, attempts)
		}
	}
	cleanup, resolutions, cleanupErr := r.cleanup(cleanupCtx, run.ID, resources, nil, discover)
	if cleanupErr != nil {
		cleanup = domain.CleanupDirty
	}
	terminal := cause.Error()
	if cleanupErr != nil {
		terminal += "; cleanup: " + cleanupErr.Error()
	}
	if err := r.complete(cleanupCtx, run, plan, capabilities, nil, checkpoints, attempts, resolutions, domain.VerdictError, cleanup, terminal); err != nil {
		return err
	}
	return cause
}

func (r *Runner) discoverAttemptedHistory(ctx context.Context, run domain.Run, plan compiler.Plan, attempts []domain.AlertAttemptFact) ([]oscar.HistoryRecord, error) {
	type expectedSource struct {
		alert   compiler.AlertPlan
		attempt domain.AlertAttemptFact
	}
	// The latest attempt for a named source supersedes prior episodes. An old
	// firing row cannot satisfy an uncertain refire with the same event ID.
	expected := map[string]map[string]expectedSource{}
	syntheticNames := map[string]bool{}
	for _, item := range plan.Cases {
		if item.Rule.EmitAlertName != "" {
			syntheticNames[item.Rule.EmitAlertName] = true
		}
		for _, attempt := range attempts {
			if attempt.CaseStableKey != item.Code || attempt.EventIndex < 1 || attempt.EventIndex > len(item.Alerts) {
				continue
			}
			alert := item.Alerts[attempt.EventIndex-1]
			if attempt.EventID == "" || attempt.EventID != alert.Labels["oscar_test_event_id"] {
				continue
			}
			if expected[alert.Name] == nil {
				expected[alert.Name] = map[string]expectedSource{}
			}
			expected[alert.Name][attempt.EventID] = expectedSource{alert: alert, attempt: attempt}
		}
	}
	if len(expected) == 0 {
		return nil, fmt.Errorf("sent alert attempts could not be mapped to the immutable plan")
	}
	start := run.CreatedAt.UTC()
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		start = run.StartedAt.UTC()
	}
	end := r.opts.Now().UTC().Add(time.Minute)
	byFingerprint := map[string]oscar.HistoryRecord{}
	var failures []string
	alertNames := make([]string, 0, len(expected))
	for alertName := range expected {
		alertNames = append(alertNames, alertName)
	}
	sort.Strings(alertNames)
	for _, alertName := range alertNames {
		records, err := r.api.FindHistory(ctx, oscar.HistoryQuery{AlertName: alertName, Start: start.Add(-time.Second), End: end})
		if err != nil {
			failures = append(failures, alertName+": "+err.Error())
			continue
		}
		candidates := map[string]map[string]oscar.HistoryRecord{}
		for _, record := range records {
			eventID := record.Labels["oscar_test_event_id"]
			wanted, exists := expected[alertName][eventID]
			if record.AlertName != alertName || record.Labels["oscar_test_run_id"] != run.ID || record.Fingerprint == "" || !exists {
				continue
			}
			if wanted.attempt.SendState == "ATTEMPTED" {
				if wanted.attempt.StartsAt.IsZero() || !record.StartsAt.Equal(wanted.attempt.StartsAt) {
					continue
				}
				labelsMatch := true
				for label, value := range wanted.alert.Labels {
					actual, present := record.Labels[label]
					if !present || actual != value {
						labelsMatch = false
						break
					}
				}
				if !labelsMatch {
					continue
				}
			}
			if candidates[eventID] == nil {
				candidates[eventID] = map[string]oscar.HistoryRecord{}
			}
			retainLatestHistory(candidates[eventID], record)
		}
		for eventID, wanted := range expected[alertName] {
			rows := candidates[eventID]
			if len(rows) == 0 || (wanted.attempt.SendState == "ATTEMPTED" && len(rows) != 1) {
				failures = append(failures, alertName+"/"+eventID+": exact attempted history not found or ambiguous")
				continue
			}
			for _, record := range rows {
				retainLatestHistory(byFingerprint, record)
			}
		}
	}
	alertNames = alertNames[:0]
	for alertName := range syntheticNames {
		alertNames = append(alertNames, alertName)
	}
	sort.Strings(alertNames)
	for _, alertName := range alertNames {
		records, err := r.api.FindHistory(ctx, oscar.HistoryQuery{AlertName: alertName, Start: start.Add(-time.Second), End: end})
		if err != nil {
			failures = append(failures, alertName+": "+err.Error())
			continue
		}
		for _, record := range records {
			if record.AlertName == alertName && record.Labels["oscar_test_run_id"] == run.ID && record.Fingerprint != "" {
				retainLatestHistory(byFingerprint, record)
			}
		}
	}
	recovered := make([]oscar.HistoryRecord, 0, len(byFingerprint))
	for _, record := range byFingerprint {
		recovered = append(recovered, record)
	}
	recovered = uniqueHistory(recovered)
	if len(failures) != 0 {
		sort.Strings(failures)
		return recovered, fmt.Errorf("%s", strings.Join(failures, ", "))
	}
	return recovered, nil
}

func (r *Runner) finishWithoutMutation(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot, verdict domain.Verdict, explanation string) error {
	if err := r.transition(ctx, run.ID, domain.RunCancelling, "Preflight prevented target mutation"); err != nil {
		return err
	}
	if err := r.transition(ctx, run.ID, domain.RunCleaningUp, "No external resources require cleanup"); err != nil {
		return err
	}
	return r.complete(ctx, run, plan, capabilities, nil, nil, nil, nil, verdict, domain.CleanupNotRequired, explanation)
}

func (r *Runner) complete(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot, results []caseResult, checkpoints []checkpointResult, attempts []domain.AlertAttemptFact, resolutions []domain.AlertResolutionFact, verdict domain.Verdict, cleanup domain.CleanupStatus, terminal string) error {
	completed := r.opts.Now().UTC()
	facts, err := normalizedFacts(plan, results, attempts)
	if err != nil {
		return err
	}
	facts.Resolutions = append(facts.Resolutions, resolutions...)
	if r.opts.EvidenceWriter != nil {
		item, writeErr := r.opts.EvidenceWriter.WriteEvidence(ctx, run.ID, facts, completed)
		if writeErr != nil {
			verdict = domain.VerdictError
			if terminal != "" {
				terminal += "; "
			}
			terminal += "normalized evidence publication failed: " + writeErr.Error()
		} else {
			facts.Artifacts = append(facts.Artifacts, item)
		}
	}
	report, err := json.Marshal(canonicalReport{Coverage: map[string]string{
		"inputPath": "alert-engine", "endpoint": "POST /api/v1/alerts", "receiverAdmission": "NOT_TESTED", "nativeAlertmanager": "BYPASSED", "lifecycleTransitions": "NOT_PROVEN", "producerBarriers": "NOT_TESTED", "physicalClear": "NOT_PROVEN", "selectorRevision": "NOT_PROVEN", "selectedRowIsolation": "NOT_PROVEN", "notificationDelivery": "NOT_PROVEN", "noMatchScope": "DOWNSTREAM_ONLY_IF_ASSERTED",
	}, APIVersion: "corrtest.oscar/v1alpha1", RunID: run.ID, Pattern: plan.Pattern,
		Checkpoints: checkpoints, PlanDigest: plan.Digest, Verdict: verdict, Cleanup: cleanup, Capabilities: capabilities, Cases: results, Attempts: facts.Attempts, Artifacts: facts.Artifacts, Resolutions: facts.Resolutions, CompletedAt: completed})
	if err != nil {
		return err
	}
	return r.store.FinalizeRun(ctx, run.ID, facts, verdict, cleanup, report, terminal, completed)
}

func normalizedFacts(plan compiler.Plan, results []caseResult, attempts []domain.AlertAttemptFact) (domain.ExecutionFacts, error) {
	facts := domain.ExecutionFacts{Attempts: append([]domain.AlertAttemptFact(nil), attempts...)}
	fingerprints := map[string]string{}
	for _, result := range results {
		for _, record := range result.SourceHistory {
			fingerprints[record.Labels["oscar_test_event_id"]] = record.Fingerprint
		}
		evidence, err := json.Marshal(result)
		if err != nil {
			return domain.ExecutionFacts{}, err
		}
		item := domain.CaseFact{StableKey: result.Code, Verdict: domain.Verdict(result.Verdict), StartedAt: result.StartedAt, EndedAt: result.EndedAt, Evidence: evidence}
		for index, assertion := range result.Assertions {
			var expected json.RawMessage
			for _, planned := range plan.Cases {
				if planned.Code == result.Code && index < len(planned.Assertions) {
					expected, _ = json.Marshal(planned.Assertions[index])
					break
				}
			}
			if len(expected) == 0 {
				expected, _ = json.Marshal(map[string]any{"kind": assertion.Kind, "outcome": assertion.Outcome, "equals": assertion.Expected})
			}
			observed, _ := json.Marshal(map[string]int{"count": assertion.Observed})
			item.Assertions = append(item.Assertions, domain.AssertionFact{StableKey: assertion.Kind + ":" + strconv.Itoa(index+1), Kind: assertion.Kind,
				ExpectedJSON: expected, ObservedJSON: observed, Verdict: assertion.Verdict, Explanation: assertion.Explanation,
				ObservationStart: result.StartedAt, ObservationEnd: result.EndedAt})
		}
		facts.Cases = append(facts.Cases, item)
	}
	for index := range facts.Attempts {
		if fingerprint := fingerprints[facts.Attempts[index].EventID]; fingerprint != "" {
			facts.Attempts[index].Fingerprint = fingerprint
			facts.Attempts[index].SendState = "OBSERVED"
		}
	}
	return facts, nil
}

func (r *Runner) transition(ctx context.Context, runID string, status domain.RunStatus, summary string) error {
	return r.store.TransitionRun(ctx, runID, status, r.opts.Now().UTC(), summary)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
