package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/runner"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/testoscar"
)

func TestStagedCrossSourceTestLabelsThroughRealHTTPClient(t *testing.T) {
	for _, test := range []struct {
		mode           string
		verdict        domain.Verdict
		inputs, points int
	}{
		{"valid", domain.VerdictPass, 6, 5},
		{"crosssource same-tag trigger", domain.VerdictFail, 6, 5},
		{"missing startsAt", domain.VerdictInconclusive, 3, 3},
		{"crosssource ignored recovery", domain.VerdictFail, 3, 3},
	} {
		t.Run(test.mode, func(t *testing.T) {
			raw, err := os.ReadFile("../../scenarios/reliability/cross-source-lifecycle-downstream.yaml")
			if err != nil {
				t.Fatal(err)
			}
			doc, err := scenario.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
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
			for _, item := range plan.Cases {
				required := item.Rule.MatchCriteria["required_sources"].([]any)
				if len(required) != 2 {
					t.Fatal("lost required source pairs")
				}
				for index, tag := range []string{"snmp", "api"} {
					entry := required[index].(map[string]any)
					if entry["source"] != tag || entry["match"].(map[string]string)["alertname"] != item.Alerts[0].Name {
						t.Fatal("source rule definition changed")
					}
				}
				first, second := item.Alerts[0], item.Alerts[1]
				if first.Name != second.Name || first.Labels["oscar_test_event_id"] == second.Labels["oscar_test_event_id"] {
					t.Fatal("named source identities not distinct within one alertname")
				}
				if first.Labels["oscar_source"] != "snmp" {
					t.Fatal("first source tag changed")
				}
				wantSecond := "api"
				if item.Code == "N01" {
					wantSecond = "snmp"
				}
				if second.Labels["oscar_source"] != wantSecond {
					t.Fatal("source-mix control changed")
				}
			}
			fixture := newCorrelationWireFixture(test.mode)
			server := testoscar.NewWithResponder(t, fixture.respond)
			client, err := oscar.New(domain.Target{BaseURL: server.URL(), APIProfile: "public-v1"}, oscar.Options{Getenv: func(string) string { return "local-test-key" }})
			if err != nil {
				t.Fatal(err)
			}
			err = runner.New(db, client, runner.Options{Now: fixture.model.Now, Sleep: fixture.model.Sleep, PollInterval: time.Second, Stabilization: time.Millisecond}).Execute(ctx, run, plan, runner.CapabilitySnapshot{Ready: true, LabelsSurvived: true, PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			saved, err := db.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Checkpoints []struct {
					Verdict    string
					Assertions []struct {
						Outcome  string
						Observed int
					}
				}
				Coverage map[string]string
			}
			if err := json.Unmarshal(saved.CanonicalReportJSON, &report); err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if saved.Verdict != test.verdict || len(fixture.stimuli) != test.inputs || len(report.Checkpoints) != test.points {
				t.Fatalf("verdict=%s inputs=%d points=%d report=%s", saved.Verdict, len(fixture.stimuli), len(report.Checkpoints), saved.CanonicalReportJSON)
			}
			if report.Checkpoints[0].Assertions[1].Observed != 0 || report.Checkpoints[1].Assertions[1].Observed != 1 {
				t.Fatal("staged trigger evidence lost")
			}
			if test.mode == "valid" && report.Checkpoints[4].Assertions[1].Observed != 0 {
				t.Fatal("same-tag control triggered")
			}
			if report.Coverage["receiverAdmission"] != "NOT_TESTED" || report.Coverage["nativeAlertmanager"] != "BYPASSED" || report.Coverage["physicalClear"] != "NOT_PROVEN" {
				t.Fatal("local wire proof overclaims scope")
			}
		})
	}
}
