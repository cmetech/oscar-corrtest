package compiler_test

import (
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestCompileFloodBuildsInspectableIsolatedCases(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pattern != "flood" || plan.Digest == "" || len(plan.Cases) != 2 {
		t.Fatalf("plan=%+v", plan)
	}
	positive := plan.Cases[0]
	if positive.Code != "P01" || positive.Rule.Name != "corrtest-flood-p01-7q9k2m4a" || len(positive.Alerts) != 5 {
		t.Fatalf("positive=%+v", positive)
	}
	alert := positive.Alerts[0]
	if alert.Name != "CORRTEST_FLOOD_P01_INTERFACEDOWN_7Q9K2M4A" {
		t.Fatalf("alert name=%q", alert.Name)
	}
	wantLabels := map[string]string{
		"category": "corrtest_flood", "oscar_test": "true", "oscar_test_harness": "corrtest",
		"oscar_test_schema_version": "v1", "oscar_test_run_id": run.ID, "oscar_test_run_short": run.ShortToken,
		"oscar_test_suite": "builtin-all", "oscar_test_scenario": "flood-basic", "oscar_test_pattern": "flood",
		"oscar_test_case": "emits-one-parent-at-threshold", "oscar_test_case_code": "P01", "oscar_test_polarity": "positive",
		"oscar_test_alert_class": "source", "oscar_test_alert_role": "interface_down", "oscar_test_rule_name": positive.Rule.Name,
	}
	for key, want := range wantLabels {
		if got := alert.Labels[key]; got != want {
			t.Fatalf("label %s=%q want %q", key, got, want)
		}
	}
	if positive.Rule.EmitLabels["oscar_test_alert_class"] != "synthetic" {
		t.Fatalf("emit labels=%v", positive.Rule.EmitLabels)
	}
	if plan.MutationBudget.Rules != 2 || plan.MutationBudget.Alerts != 9 || plan.MaxDuration != 90*time.Second {
		t.Fatalf("budget=%+v duration=%s", plan.MutationBudget, plan.MaxDuration)
	}
	if positive.Inspection.ExactRun != run.ID || positive.Inspection.AlertNamePrefix != "CORRTEST_FLOOD_P01" {
		t.Fatalf("inspection=%+v", positive.Inspection)
	}
}

func TestCompileRejectsUnsafeCapabilitiesAndReservedLabels(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	for _, mode := range []string{"", "unknown", "publication_disabled"} {
		_, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: mode})
		if err == nil {
			t.Fatalf("pipeline mode %q accepted", mode)
		}
	}
	custom := scenario.Builtin("flood")
	custom.Cases[0].Labels = map[string]string{"oscar_test_run_id": "forged"}
	if _, err := compiler.Compile(run, custom, compiler.Capabilities{PipelineMode: "phase_b_dispatch"}); err == nil {
		t.Fatal("reserved label override accepted")
	}
}

func TestCompileIsDeterministic(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	capabilities := compiler.Capabilities{PipelineMode: "phase_b_dispatch"}
	first, err := compiler.Compile(run, scenario.Builtin("flood"), capabilities)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiler.Compile(run, scenario.Builtin("flood"), capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("digests differ: %s %s", first.Digest, second.Digest)
	}
}
