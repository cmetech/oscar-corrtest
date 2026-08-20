package compiler_test

import (
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestAllBuiltinsCompilePositiveAndNegativeCases(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	builtins := scenario.AllBuiltins()
	if len(builtins) != 8 {
		t.Fatalf("builtins=%d", len(builtins))
	}
	for _, input := range builtins {
		t.Run(input.Pattern, func(t *testing.T) {
			plan, err := compiler.Compile(run, input, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Cases) != 2 || plan.Cases[0].Code != "P01" || plan.Cases[1].Code != "N01" {
				t.Fatalf("cases=%+v", plan.Cases)
			}
			for _, item := range plan.Cases {
				for _, alert := range item.Alerts {
					if alert.Labels["oscar_test_pattern"] != input.Pattern || alert.Labels["category"] != "corrtest_"+input.Pattern {
						t.Fatalf("identity labels=%v", alert.Labels)
					}
				}
			}
		})
	}
}

func TestPatternStimuliEncodeCurrentOscarSemantics(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	compile := func(pattern string) compiler.Plan {
		t.Helper()
		plan, err := compiler.Compile(run, scenario.Builtin(pattern), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	sequence := compile("sequence")
	if sequence.Cases[0].Alerts[0].Name == sequence.Cases[0].Alerts[1].Name || sequence.Cases[0].Alerts[0].Delay >= sequence.Cases[0].Alerts[1].Delay {
		t.Fatalf("sequence stimuli=%+v", sequence.Cases[0].Alerts)
	}
	threshold := compile("threshold")
	if threshold.Cases[0].Alerts[0].Labels["device"] == threshold.Cases[0].Alerts[1].Labels["device"] {
		t.Fatalf("positive threshold values not distinct: %+v", threshold.Cases[0].Alerts)
	}
	if threshold.Cases[1].Alerts[0].Labels["device"] != threshold.Cases[1].Alerts[1].Labels["device"] {
		t.Fatalf("negative threshold values varied: %+v", threshold.Cases[1].Alerts)
	}
	crossSource := compile("cross_source")
	if crossSource.Cases[0].Alerts[0].Labels["oscar_source"] == crossSource.Cases[0].Alerts[1].Labels["oscar_source"] {
		t.Fatalf("cross-source stimuli=%+v", crossSource.Cases[0].Alerts)
	}
	persistence := compile("persistence")
	if len(persistence.Cases[1].Alerts) != 2 || persistence.Cases[1].Alerts[1].Status != "resolved" {
		t.Fatalf("persistence negative=%+v", persistence.Cases[1].Alerts)
	}
	parentChild := compile("parent_child")
	if parentChild.Cases[0].Rule.EmitAlertName != "" || len(parentChild.Cases[0].Alerts) != 2 {
		t.Fatalf("parent-child plan=%+v", parentChild.Cases[0])
	}
}
