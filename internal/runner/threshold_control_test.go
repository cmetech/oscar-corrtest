package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

// This intentionally executes only compiled N01. The public scenario format
// requires P01/N01; an exact-set mock cannot qualify a positive HLL trigger.
// No standalone runnable scenario or complete Threshold acceptance is implied.
func TestThresholdDuplicateValueControlThroughRealHTTPClient(t *testing.T) {
	for _, test := range []struct {
		mode           string
		verdict        domain.Verdict
		inputs, points int
	}{
		{"valid", domain.VerdictPass, 6, 4},
		{"threshold raw-count trigger", domain.VerdictFail, 3, 2},
		{"missing startsAt", domain.VerdictInconclusive, 1, 1},
		{"threshold ignored recovery", domain.VerdictFail, 4, 3},
		{"unsupported multi-value", domain.VerdictError, 1, 1},
		{"unsupported empty-value", domain.VerdictError, 1, 1},
	} {
		t.Run(test.mode, func(t *testing.T) {
			doc := scenario.Builtin("threshold")
			doc.Name = "threshold-duplicate-value-control-only"
			item := &doc.Cases[1]
			item.Name = "three-identities-one-device-no-trigger"
			item.Events = []scenario.Event{
				{Role: "cpu_high", Source: "A", Status: "firing", Labels: map[string]string{"device": "same-device"}},
				{Role: "cpu_high", Source: "B", Status: "firing", Delay: 3 * time.Second, Labels: map[string]string{"device": "same-device"}},
				{Role: "cpu_high", Source: "C", Status: "firing", Delay: 6 * time.Second, Labels: map[string]string{"device": "same-device"}},
				{Role: "cpu_high", Source: "A", Status: "resolved", Delay: 9 * time.Second},
				{Role: "cpu_high", Source: "B", Status: "resolved", Delay: 12 * time.Second},
				{Role: "cpu_high", Source: "C", Status: "resolved", Delay: 15 * time.Second},
			}
			if test.mode == "unsupported multi-value" {
				item.Events[1].Labels["device"] = "second-device"
			}
			if test.mode == "unsupported empty-value" {
				item.Events[1].Labels["device"] = ""
			}
			item.Checkpoints = []scenario.Checkpoint{
				{Name: "one-value-advances-without-trigger", AfterEvent: 1, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "enriched", Equals: 1}, {Kind: "audit-count", Outcome: "parent_emitted", Equals: 0}, {Kind: "source-episode-state-count", Outcome: "firing", Equals: 1}}},
				{Name: "three-identities-still-one-value", AfterEvent: 3, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "enriched", Equals: 3}, {Kind: "audit-count", Outcome: "parent_emitted", Equals: 0}, {Kind: "source-episode-state-count", Outcome: "firing", Equals: 3}}},
				{Name: "partial-source-history-recovery-only", AfterEvent: 4, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: 1}, {Kind: "source-episode-state-count", Outcome: "firing", Equals: 2}}},
				{Name: "all-source-history-recovery-only", AfterEvent: 6, Window: 2 * time.Second, Assertions: []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: 3}}},
			}
			item.Assertions = []scenario.Assertion{{Kind: "source-episode-state-count", Outcome: "resolved", Equals: 3}}
			ctx := context.Background()
			db := openDatabase(t)
			run := newRun()
			if err := db.CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			plan.Cases = plan.Cases[1:]
			plan.MutationBudget.Rules = 1
			plan.MutationBudget.Alerts = 6
			if plan.Cases[0].Code != "N01" {
				t.Fatal("control selection included positive case")
			}
			seen := map[string]bool{}
			for _, alert := range plan.Cases[0].Alerts[:3] {
				if !strings.HasPrefix(test.mode, "unsupported") && alert.Labels["device"] != "same-device" {
					t.Fatal("distinct-value control became multi-value")
				}
				seen[alert.Labels["oscar_test_event_id"]] = true
			}
			if len(seen) != 3 {
				t.Fatal("control lost distinct source identities")
			}
			fixture := newCorrelationWireFixture(test.mode)
			// Deliberately cross the fixed 30s bucket boundary. A singleton value stays
			// below threshold on either side; no clock alignment is necessary.
			fixture.model = testoscar.NewModel(time.Date(2026, 10, 13, 12, 0, 28, 123000000, time.UTC))
			server := testoscar.NewWithResponder(t, fixture.respond)
			client, err := oscar.New(domain.Target{BaseURL: server.URL(), APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "local-test-key" }})
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(db, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if strings.HasPrefix(test.mode, "unsupported") {
				if err == nil || !strings.Contains(err.Error(), "Threshold") {
					t.Fatalf("expected unsupported Threshold oracle error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			saved, err := db.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Checkpoints []struct{ Verdict string }
				Coverage    map[string]string
			}
			if err := json.Unmarshal(saved.CanonicalReportJSON, &report); err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if saved.Verdict != test.verdict || len(fixture.stimuli) != test.inputs || len(report.Checkpoints) != test.points {
				t.Fatalf("verdict=%s inputs=%d points=%d report=%s", saved.Verdict, len(fixture.stimuli), len(report.Checkpoints), saved.CanonicalReportJSON)
			}
			if report.Coverage["receiverAdmission"] != "NOT_TESTED" || report.Coverage["nativeAlertmanager"] != "BYPASSED" || report.Coverage["physicalClear"] != "NOT_PROVEN" {
				t.Fatal("local control overclaims scope")
			}
			if test.mode == "valid" {
				first, err := time.Parse(time.RFC3339Nano, fixture.stimuli[0].StartsAt)
				if err != nil {
					t.Fatal(err)
				}
				second, err := time.Parse(time.RFC3339Nano, fixture.stimuli[1].StartsAt)
				if err != nil {
					t.Fatal(err)
				}
				if first.UnixMilli()/30000 == second.UnixMilli()/30000 {
					t.Fatal("boundary control did not cross fixed epoch")
				}
				if len(fixture.cleanup) != 3 || len(fixture.deleted) != 1 {
					t.Fatal("owned cleanup incomplete")
				}
			}
		})
	}
}
