package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

type checkpointAPI struct {
	*testoscar.Model
	sent              []compiler.AlertPlan
	sentAt            []time.Time
	historyReadCounts []int
	lateAudit         bool
	transientAudit    bool
	auditErrorAfter   int
}

func (api *checkpointAPI) InjectRecorded(ctx context.Context, alert compiler.AlertPlan, record oscar.InjectionRecorder) (oscar.InjectionResult, error) {
	evidence, _, err := oscar.PrepareInjection(alert, "test-model", "https://model.invalid/api/v1/alerts")
	if err != nil {
		return oscar.InjectionResult{}, err
	}
	if err := record(evidence); err != nil {
		return oscar.InjectionResult{}, err
	}
	return api.Inject(ctx, alert)
}

func (api *checkpointAPI) Inject(ctx context.Context, alert compiler.AlertPlan) (oscar.InjectionResult, error) {
	api.sent = append(api.sent, alert)
	api.sentAt = append(api.sentAt, api.Now())
	return api.Model.Inject(ctx, alert)
}
func (api *checkpointAPI) FindHistory(ctx context.Context, q oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	api.historyReadCounts = append(api.historyReadCounts, len(api.sent))
	return api.Model.FindHistory(ctx, q)
}
func (api *checkpointAPI) CorrelationAudit(ctx context.Context, key string) ([]oscar.AuditRecord, error) {
	if api.auditErrorAfter > 0 && len(api.sent) >= api.auditErrorAfter {
		return nil, fmt.Errorf("audit unavailable")
	}
	rows, err := api.Model.CorrelationAudit(ctx, key)
	if api.lateAudit && len(api.sent) == 4 && api.Now().Sub(api.sentAt[3]) >= time.Second && (!api.transientAudit || api.Now().Sub(api.sentAt[3]) < 2*time.Second) {
		for i := range rows {
			rows[i].Outcome = "parent_emitted"
		}
	}
	return rows, err
}
func stagedFlood() scenario.Scenario {
	doc := scenario.Builtin("flood")
	item := &doc.Cases[0]
	item.Role = ""
	item.Repeat = 0
	for i, delay := range []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second, 6 * time.Second} {
		item.Events = append(item.Events, scenario.Event{Role: "interface_down", Source: fmt.Sprintf("child_%d", i), Status: "firing", Delay: delay})
	}
	item.Checkpoints = []scenario.Checkpoint{{Name: "before-threshold", AfterEvent: 4, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 0}}}, {Name: "threshold-reached", AfterEvent: 5, Window: time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 1}}}}
	return doc
}
func TestCheckpointsObserveOnlySentPrefixAndPreserveAbsoluteSchedule(t *testing.T) {
	db := openDatabase(t)
	run := newRun()
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, stagedFlood(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	api := &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC))}
	if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond, Stabilization: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.GetRun(context.Background(), run.ID)
	var report struct {
		Checkpoints []struct {
			Name               string
			AfterEvent         int
			Verdict            string
			StartedAt, EndedAt time.Time
			SourceHistory      []oscar.HistoryRecord
		}
	}
	if err := json.Unmarshal(stored.CanonicalReportJSON, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Checkpoints) != 2 {
		t.Fatalf("checkpoint evidence missing: %s", stored.CanonicalReportJSON)
	}
	for i, point := range report.Checkpoints {
		if point.Verdict != "PASS" || len(point.SourceHistory) != 4+i {
			t.Fatalf("wrong checkpoint evidence: %+v", point)
		}
	}
	if got := report.Checkpoints[0].EndedAt.Sub(report.Checkpoints[0].StartedAt); got != 2*time.Second {
		t.Fatalf("negative observation shortened: %s", got)
	}
	if got := api.sentAt[4].Sub(api.sentAt[0]); got != 6*time.Second {
		t.Fatalf("checkpoint shifted absolute schedule: %s", got)
	}
}
func TestCheckpointFailureStopsLaterInputsAndRetainsEvidence(t *testing.T) {
	for _, mode := range []string{"late-negative", "transient-negative", "skipped", "later-read-error"} {
		t.Run(mode, func(t *testing.T) {
			db := openDatabase(t)
			run := newRun()
			if err := db.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			doc := stagedFlood()
			api := &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC))}
			wantVerdict := domain.VerdictFail
			wantSent := 4
			switch mode {
			case "late-negative":
				api.lateAudit = true
			case "transient-negative":
				api.lateAudit, api.transientAudit = true, true
			case "skipped":
				doc.Cases[0].Checkpoints[0].Assertions = append(doc.Cases[0].Checkpoints[0].Assertions, scenario.Assertion{Kind: "synthetic-alert-count", Equals: 0})
				wantVerdict = domain.VerdictInconclusive
			case "later-read-error":
				api.auditErrorAfter = 5
				wantVerdict = domain.VerdictError
				wantSent = 5
			}
			plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second, ObservationWindow: time.Millisecond}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if (err != nil) != (mode == "later-read-error") {
				t.Fatalf("execution error=%v", err)
			}
			stored, _ := db.GetRun(context.Background(), run.ID)
			if stored.Verdict != wantVerdict || len(api.sent) != wantSent || stored.CleanupStatus != domain.CleanupClean {
				t.Fatalf("verdict=%s sent=%d cleanup=%s", stored.Verdict, len(api.sent), stored.CleanupStatus)
			}
			var report struct {
				Checkpoints []struct{ Name, Verdict string }
			}
			json.Unmarshal(stored.CanonicalReportJSON, &report)
			if len(report.Checkpoints) == 0 || report.Checkpoints[0].Name != "before-threshold" {
				t.Fatalf("lost checkpoint evidence: %s", stored.CanonicalReportJSON)
			}
		})
	}
}

func TestCheckpointCancellationRetainsIncompleteEvidenceAndCleans(t *testing.T) {
	db := openDatabase(t)
	run := newRun()
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, stagedFlood(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	api := &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC))}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleep := func(ctx context.Context, delay time.Duration) error {
		if len(api.sent) == 4 {
			cancel()
			return ctx.Err()
		}
		return api.Sleep(ctx, delay)
	}
	err = runner.New(db, api, runner.Options{Now: api.Now, Sleep: sleep, PollInterval: time.Second}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
	if err == nil {
		t.Fatal("cancellation was accepted")
	}
	stored, _ := db.GetRun(context.Background(), run.ID)
	var report struct {
		Checkpoints []struct {
			Verdict             string
			ObservationComplete bool
		}
	}
	json.Unmarshal(stored.CanonicalReportJSON, &report)
	if stored.Verdict != domain.VerdictError || stored.CleanupStatus != domain.CleanupClean || len(api.sent) != 4 || len(report.Checkpoints) != 1 || report.Checkpoints[0].ObservationComplete {
		t.Fatalf("unsafe cancellation: verdict=%s cleanup=%s sent=%d evidence=%+v", stored.Verdict, stored.CleanupStatus, len(api.sent), report)
	}
}

type missingCheckpointHistory struct {
	*checkpointAPI
}

func (api *missingCheckpointHistory) FindHistory(context.Context, oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	// No source visibility is available during observation or owned cleanup.
	return nil, nil
}

func TestCheckpointMissingHistoryCannotPassZeroCount(t *testing.T) {
	db := openDatabase(t)
	run := newRun()
	if err := db.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(run, stagedFlood(), compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	api := &missingCheckpointHistory{checkpointAPI: &checkpointAPI{Model: testoscar.NewModel(time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC))}}
	if err := runner.New(db, api, runner.Options{Now: api.Now, Sleep: api.Sleep, PollInterval: time.Second}).Execute(context.Background(), run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.GetRun(context.Background(), run.ID)
	if stored.Verdict != domain.VerdictInconclusive || stored.CleanupStatus != domain.CleanupDirty || len(api.sent) != 4 {
		t.Fatalf("missing evidence passed: verdict=%s cleanup=%s sent=%d", stored.Verdict, stored.CleanupStatus, len(api.sent))
	}
}
