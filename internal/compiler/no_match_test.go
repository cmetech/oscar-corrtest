package compiler_test

import (
	"bytes"
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"os"
	"strings"
	"testing"
)

const unmatchedYAML = `apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: downstream-no-match
suite: reliability
pattern: flood
maxDuration: 90s
cases:
- name: trigger
  code: P01
  polarity: positive
  role: interface_down
  repeat: 5
  window: 30s
  assertions: [{kind: audit-count, outcome: parent_emitted, equals: 1}]
- name: no-match
  code: N01
  polarity: negative
  control: downstream-unmatched
  window: 30s
  events:
  - {role: interface_down, source: A, status: firing}
  - {role: interface_down, source: B, status: firing}
  - {role: interface_down, source: C, status: firing}
  - {role: interface_down, source: D, status: firing}
  - {role: interface_down, source: E, status: firing}
  assertions: [{kind: downstream-no-match-count, equals: 5}]
`

func TestDownstreamNoMatchControlIsNotBelowThreshold(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(unmatchedYAML))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_control", ShortToken: "ABC12345"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range plan.Cases[1].Alerts {
		for _, item := range plan.Cases {
			match := item.Rule.MatchCriteria["match"].(map[string]string)
			if control.Name == match["alertname"] {
				t.Fatalf("control still matches fixture %s", item.Rule.Name)
			}
		}
	}
	if len(plan.Cases[1].Alerts) != 5 {
		t.Fatal("control relies on fewer than threshold")
	}
}

func TestDownstreamNoMatchRejectsUnsafeControlForms(t *testing.T) {
	for name, change := range map[string]func(*scenario.Scenario){
		"receiver boundary":   func(d *scenario.Scenario) { d.InputPath = "receiver-admission" },
		"unnamed":             func(d *scenario.Scenario) { d.Cases[1].Events[0].Source = "" },
		"resolved":            func(d *scenario.Scenario) { d.Cases[1].Events[4].Status = "resolved" },
		"reused source":       func(d *scenario.Scenario) { d.Cases[1].Events[4].Source = "A" },
		"ordinary default":    func(d *scenario.Scenario) { d.Cases[1].Control = "" },
		"unsupported pattern": func(d *scenario.Scenario) { d.Pattern = "threshold" },
		"mixed assertions": func(d *scenario.Scenario) {
			d.Cases[1].Assertions = append(d.Cases[1].Assertions, scenario.Assertion{Kind: "audit-count", Outcome: "parent_emitted", Equals: 0})
		},
		"zero proof": func(d *scenario.Scenario) { d.Cases[1].Assertions[0].Equals = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := scenario.Decode(strings.NewReader(unmatchedYAML))
			if err != nil {
				t.Fatal(err)
			}
			change(&doc)
			if _, err := compiler.Compile(domain.Run{ID: "crt_control", ShortToken: "ABC12345"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"}); err == nil {
				t.Fatal("unsafe control compiled")
			}
		})
	}
}
func TestDownstreamNoMatchRejectsUnprovedFixtureExclusion(t *testing.T) {
	for _, mode := range []string{"overlap", "unknown clause", "duplicate event", "duplicate source"} {
		t.Run(mode, func(t *testing.T) {
			doc, _ := scenario.Decode(strings.NewReader(unmatchedYAML))
			plan, err := compiler.Compile(domain.Run{ID: "crt_control", ShortToken: "ABC12345"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "overlap":
				plan.Cases[0].Rule.MatchCriteria["match"] = map[string]string{"alertname": plan.Cases[1].Alerts[0].Name}
			case "unknown clause":
				plan.Cases[0].Rule.MatchCriteria["all_of"] = map[string]any{}
			case "duplicate source":
				plan.Cases[1].Alerts[1].Source = plan.Cases[1].Alerts[0].Source
			case "duplicate event":
				plan.Cases[1].Alerts[1] = plan.Cases[1].Alerts[0]
			}
			if compiler.ValidateControlExclusion(plan) == nil {
				t.Fatal("unproved exclusion accepted")
			}
		})
	}
}

func TestDownstreamNoMatchWireRoundTrip(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(unmatchedYAML))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := scenario.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := scenario.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if actual.Cases[1].Control != "downstream-unmatched" || actual.Cases[1].Assertions[0].Kind != "downstream-no-match-count" {
		t.Fatal("lost control contract")
	}
	if _, err := scenario.Decode(strings.NewReader(strings.Replace(unmatchedYAML, "kind: downstream-no-match-count, equals: 5", "kind: downstream-no-match-count, outcome: no_matching_rules, equals: 5", 1))); err == nil {
		t.Fatal("unexpected outcome accepted")
	}
}

func TestCommittedDownstreamNoMatchFixture(t *testing.T) {
	raw, err := os.ReadFile("../../scenarios/reliability/flood-downstream-no-match.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_control", ShortToken: "ABC12345"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cases[1].Control != "downstream-unmatched" || len(plan.Cases[1].Alerts) != 5 || len(plan.Cases[1].Checkpoints) != 1 {
		t.Fatal("control fixture lost its witness checkpoint")
	}
}
