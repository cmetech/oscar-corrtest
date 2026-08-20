// Package runner owns serial, recoverable correlation run execution.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
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
	DeleteRule(context.Context, int) error
	Inject(context.Context, compiler.AlertPlan) (oscar.InjectionResult, error)
	FindHistory(context.Context, oscar.HistoryQuery) ([]oscar.HistoryRecord, error)
	CorrelationAudit(context.Context, string) ([]oscar.AuditRecord, error)
}

type Store interface {
	TransitionRun(context.Context, string, domain.RunStatus, time.Time, string) error
	AppendRunEvent(context.Context, domain.RunEvent) (domain.RunEvent, error)
	SetRunExecutionDocuments(context.Context, string, json.RawMessage, json.RawMessage, time.Time) error
	CompleteRun(context.Context, string, domain.Verdict, domain.CleanupStatus, json.RawMessage, string, time.Time) error
	CreateResource(context.Context, domain.Resource) error
	AdoptResource(context.Context, string, string, time.Time) error
	MarkResourceDeleted(context.Context, string, time.Time) error
	MarkResourceCleanupError(context.Context, string, string) error
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
	Now               func() time.Time
	Sleep             func(context.Context, time.Duration) error
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
	if options.ObservationWindow <= 0 {
		options.ObservationWindow = 35 * time.Second
	}
	if options.Stabilization <= 0 {
		options.Stabilization = 5 * time.Second
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
	Name                string   `json:"name"`
	Code                string   `json:"code"`
	Verdict             string   `json:"verdict"`
	SourceFingerprints  []string `json:"sourceFingerprints"`
	SyntheticCount      int      `json:"syntheticCount"`
	AuditOutcomes       []string `json:"auditOutcomes"`
	ObservationComplete bool     `json:"observationComplete"`
	Explanation         string   `json:"explanation"`
}

type canonicalReport struct {
	APIVersion   string               `json:"apiVersion"`
	RunID        string               `json:"runId"`
	Pattern      string               `json:"pattern"`
	PlanDigest   string               `json:"planDigest"`
	Verdict      domain.Verdict       `json:"verdict"`
	Cleanup      domain.CleanupStatus `json:"cleanupStatus"`
	Capabilities CapabilitySnapshot   `json:"capabilities"`
	Cases        []caseResult         `json:"cases"`
	CompletedAt  time.Time            `json:"completedAt"`
}

// Execute runs one immutable plan. It never resumes injection after interruption.
func (r *Runner) Execute(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot) error {
	started := r.opts.Now().UTC()
	capJSON, _ := json.Marshal(capabilities)
	planJSON, _ := json.Marshal(plan)
	if err := r.store.SetRunExecutionDocuments(ctx, run.ID, capJSON, planJSON, started); err != nil {
		return err
	}
	if err := r.transition(ctx, run.ID, domain.RunPreflight, "Target preflight started"); err != nil {
		return err
	}
	if !capabilities.Ready || !capabilities.LabelsSurvived || capabilities.PipelineMode != "phase_b_dispatch" {
		explanation := "required Phase-B readiness and reserved-label survival were not proven"
		return r.finishWithoutMutation(ctx, run, plan, capabilities, domain.VerdictInconclusive, explanation)
	}

	if err := r.transition(ctx, run.ID, domain.RunSettingUp, "Creating temporary correlation rules"); err != nil {
		return err
	}
	resources := make([]domain.Resource, 0, len(plan.Cases))
	for index, item := range plan.Cases {
		resource := domain.Resource{ID: fmt.Sprintf("res_%s_%03d", run.ShortToken, index+1), RunID: run.ID, Kind: "correlation_rule",
			ExternalName: item.Rule.Name, OwnershipToken: run.ID, LifecycleState: domain.ResourceProposed}
		if err := r.store.CreateResource(ctx, resource); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
		}
		resources = append(resources, resource)
		if err := r.api.ValidateRule(ctx, item.Rule); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
		}
		created, err := r.api.CreateRule(ctx, item.Rule)
		if err != nil {
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "rule create outcome unknown")
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
		}
		if created.Name != item.Rule.Name || created.Description != item.Rule.Description {
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "created rule ownership did not round-trip")
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, fmt.Errorf("created rule ownership mismatch"))
		}
		if err := r.store.AdoptResource(ctx, resource.ID, strconv.Itoa(created.ID), r.opts.Now().UTC()); err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
		}
		resources[len(resources)-1].ExternalID = strconv.Itoa(created.ID)
		resources[len(resources)-1].LifecycleState = domain.ResourceCreated
	}

	if err := r.transition(ctx, run.ID, domain.RunInjecting, "Sending deterministic source alerts"); err != nil {
		return err
	}
	for _, item := range plan.Cases {
		var elapsed time.Duration
		for _, alert := range item.Alerts {
			if alert.Delay > elapsed {
				if err := r.opts.Sleep(ctx, alert.Delay-elapsed); err != nil {
					return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
				}
				elapsed = alert.Delay
			}
			result, err := r.api.Inject(ctx, alert)
			if err != nil {
				return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
			}
			if result.Class != oscar.InjectionAccepted {
				return r.failAndCleanup(ctx, run, plan, capabilities, resources, fmt.Errorf("injection was %s", result.Class))
			}
		}
	}

	if err := r.transition(ctx, run.ID, domain.RunObserving, "Observing OSCAR history and correlation audit"); err != nil {
		return err
	}
	results := make([]caseResult, 0, len(plan.Cases))
	for _, item := range plan.Cases {
		result, err := r.observeCase(ctx, run, item, started)
		if err != nil {
			return r.failAndCleanup(ctx, run, plan, capabilities, resources, err)
		}
		results = append(results, result)
	}
	if err := r.transition(ctx, run.ID, domain.RunAsserting, "Evaluating terminal evidence"); err != nil {
		return err
	}
	verdict := domain.VerdictPass
	for _, result := range results {
		if result.Verdict != string(domain.VerdictPass) {
			verdict = domain.VerdictFail
		}
	}
	cleanup, cleanupError := r.cleanup(ctx, run.ID, resources)
	if cleanupError != nil {
		cleanup = domain.CleanupDirty
	}
	return r.complete(ctx, run, plan, capabilities, results, verdict, cleanup, errorString(cleanupError))
}

func (r *Runner) observeCase(ctx context.Context, run domain.Run, item compiler.CasePlan, started time.Time) (caseResult, error) {
	result := caseResult{Name: item.Name, Code: item.Code}
	query := oscar.HistoryQuery{Start: started.Add(-time.Second), End: r.opts.Now().UTC().Add(r.opts.ObservationWindow)}
	sourceNames := map[string]bool{}
	for _, alert := range item.Alerts {
		sourceNames[alert.Name] = true
	}
	for sourceName := range sourceNames {
		query.AlertName = sourceName
		sources, err := r.pollHistory(ctx, query, true, r.opts.ObservationWindow)
		if err != nil {
			return result, err
		}
		if len(sources) != 1 || sources[0].Labels["oscar_test_run_id"] != run.ID {
			result.Verdict, result.Explanation = string(domain.VerdictInconclusive), "exact source history evidence was not unique"
			return result, nil
		}
		result.SourceFingerprints = append(result.SourceFingerprints, sources[0].Fingerprint)
		audits, err := r.api.CorrelationAudit(ctx, sources[0].Fingerprint)
		if err != nil {
			return result, err
		}
		for _, audit := range audits {
			result.AuditOutcomes = append(result.AuditOutcomes, audit.Outcome)
		}
	}
	if item.Rule.Pattern == "parent_child" {
		return r.evaluateParentChild(ctx, item, result)
	}
	syntheticName := item.Rule.EmitAlertName
	parentQuery := oscar.HistoryQuery{AlertName: syntheticName, Start: started.Add(-time.Second), End: query.End}
	positive := item.Polarity == "positive"
	parents, err := r.pollHistory(ctx, parentQuery, positive, r.opts.ObservationWindow)
	if err != nil {
		return result, err
	}
	if !positive {
		if err := r.opts.Sleep(ctx, r.opts.ObservationWindow); err != nil {
			return result, err
		}
	}
	result.ObservationComplete = true
	result.SyntheticCount = len(parents)
	hasParentAudit := contains(result.AuditOutcomes, "parent_emitted")
	if positive && len(parents) == 1 && hasParentAudit {
		if err := r.opts.Sleep(ctx, r.opts.Stabilization); err != nil {
			return result, err
		}
		stable, err := r.api.FindHistory(ctx, parentQuery)
		if err != nil {
			return result, err
		}
		if len(stable) == 1 {
			result.Verdict, result.Explanation = string(domain.VerdictPass), "exactly one synthetic parent remained through stabilization"
			return result, nil
		}
	}
	if !positive && len(parents) == 0 && !hasParentAudit && len(result.AuditOutcomes) > 0 {
		result.Verdict, result.Explanation = string(domain.VerdictPass), "eligible source remained non-triggering through the full decision window"
		return result, nil
	}
	result.Verdict, result.Explanation = string(domain.VerdictFail), "observed correlation evidence differed from the expected cardinality"
	return result, nil
}

func (r *Runner) evaluateParentChild(ctx context.Context, item compiler.CasePlan, result caseResult) (caseResult, error) {
	if item.Polarity == "negative" {
		if err := r.opts.Sleep(ctx, r.opts.ObservationWindow); err != nil {
			return result, err
		}
	}
	result.ObservationComplete = true
	if item.Polarity == "positive" && contains(result.AuditOutcomes, "suppressed_per_notifier") {
		result.Verdict, result.Explanation = string(domain.VerdictPass), "child was linked to an active parent with per-notifier suppression evidence"
		return result, nil
	}
	if item.Polarity == "negative" && contains(result.AuditOutcomes, "released_no_trigger") {
		result.Verdict, result.Explanation = string(domain.VerdictPass), "unmatched child was positively anchored as released without correlation"
		return result, nil
	}
	result.Verdict, result.Explanation = string(domain.VerdictFail), "parent-child decision evidence differed from the expected outcome"
	return result, nil
}

func (r *Runner) pollHistory(ctx context.Context, query oscar.HistoryQuery, waitForOne bool, window time.Duration) ([]oscar.HistoryRecord, error) {
	deadline := r.opts.Now().Add(window)
	for {
		records, err := r.api.FindHistory(ctx, query)
		if err != nil {
			return nil, err
		}
		if !waitForOne || len(records) > 0 || !r.opts.Now().Before(deadline) {
			return records, nil
		}
		if err := r.opts.Sleep(ctx, r.opts.PollInterval); err != nil {
			return nil, err
		}
	}
}

func (r *Runner) cleanup(ctx context.Context, runID string, resources []domain.Resource) (domain.CleanupStatus, error) {
	if err := r.transition(ctx, runID, domain.RunCleaningUp, "Deleting owned temporary rules"); err != nil {
		return domain.CleanupUnknown, err
	}
	if len(resources) == 0 {
		return domain.CleanupNotRequired, nil
	}
	var failures []string
	for _, resource := range resources {
		if resource.ExternalID == "" {
			continue
		}
		id, _ := strconv.Atoi(resource.ExternalID)
		if err := r.api.DeleteRule(ctx, id); err != nil {
			_ = r.store.MarkResourceCleanupError(ctx, resource.ID, "delete failed")
			failures = append(failures, resource.ExternalName)
			continue
		}
		if err := r.store.MarkResourceDeleted(ctx, resource.ID, r.opts.Now().UTC()); err != nil {
			failures = append(failures, resource.ExternalName)
		}
	}
	if len(failures) != 0 {
		return domain.CleanupDirty, fmt.Errorf("cleanup failed for %s", strings.Join(failures, ", "))
	}
	return domain.CleanupClean, nil
}

func (r *Runner) failAndCleanup(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot, resources []domain.Resource, cause error) error {
	cleanup, cleanupErr := r.cleanup(ctx, run.ID, resources)
	if cleanupErr != nil {
		cleanup = domain.CleanupDirty
	}
	if err := r.complete(ctx, run, plan, capabilities, nil, domain.VerdictError, cleanup, cause.Error()); err != nil {
		return err
	}
	return cause
}

func (r *Runner) finishWithoutMutation(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot, verdict domain.Verdict, explanation string) error {
	if err := r.transition(ctx, run.ID, domain.RunCancelling, "Preflight prevented target mutation"); err != nil {
		return err
	}
	if err := r.transition(ctx, run.ID, domain.RunCleaningUp, "No external resources require cleanup"); err != nil {
		return err
	}
	return r.complete(ctx, run, plan, capabilities, nil, verdict, domain.CleanupNotRequired, explanation)
}

func (r *Runner) complete(ctx context.Context, run domain.Run, plan compiler.Plan, capabilities CapabilitySnapshot, results []caseResult, verdict domain.Verdict, cleanup domain.CleanupStatus, terminal string) error {
	if err := r.transition(ctx, run.ID, domain.RunCompleted, "Run completed"); err != nil {
		return err
	}
	completed := r.opts.Now().UTC()
	report, err := json.Marshal(canonicalReport{APIVersion: "corrtest.oscar/v1alpha1", RunID: run.ID, Pattern: plan.Pattern,
		PlanDigest: plan.Digest, Verdict: verdict, Cleanup: cleanup, Capabilities: capabilities, Cases: results, CompletedAt: completed})
	if err != nil {
		return err
	}
	return r.store.CompleteRun(ctx, run.ID, verdict, cleanup, report, terminal, completed)
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
