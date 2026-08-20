package compiler_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestCompiledObservationWindowsCoverDecisionAndEvidenceLag(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	compile := func(pattern string) compiler.Plan {
		t.Helper()
		plan, err := compiler.Compile(run, scenario.Builtin(pattern), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	if got := compile("flood").Cases[1].ObservationWindow; got != 45*time.Second {
		t.Fatalf("flood observation=%s", got)
	}
	absence := compile("absence")
	if got := absence.Cases[0].ObservationWindow; got != 55*time.Second {
		t.Fatalf("absence observation=%s", got)
	}
	negative := absence.Cases[1].Alerts
	if len(negative) != 7 || negative[len(negative)-1].Delay != 48*time.Second {
		t.Fatalf("absence negative timeline=%+v", negative)
	}
}

func TestFloodEventsHaveFiveDistinctStableLabelIdentities(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	plan, err := compiler.Compile(run, scenario.Builtin("flood"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, alert := range plan.Cases[0].Alerts {
		id := alert.Labels["oscar_test_event_id"]
		index := alert.Labels["oscar_test_event_index"]
		if id == "" || index == "" || seen[id] {
			t.Fatalf("event identity id=%q index=%q seen=%v", id, index, seen)
		}
		if alert.Labels["site"] != "corrtest-p01" {
			t.Fatalf("group label changed: %v", alert.Labels)
		}
		seen[id] = true
	}
	if len(seen) != 5 {
		t.Fatalf("identities=%d", len(seen))
	}
}

func TestResolvedAttemptReusesFiringIdentity(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	plan, err := compiler.Compile(run, scenario.Builtin("persistence"), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	firing, resolved := plan.Cases[1].Alerts[0], plan.Cases[1].Alerts[1]
	if !reflect.DeepEqual(firing.Labels, resolved.Labels) {
		t.Fatalf("firing labels=%v resolved labels=%v", firing.Labels, resolved.Labels)
	}
	if firing.Annotations["oscar_test_attempt_index"] == resolved.Annotations["oscar_test_attempt_index"] {
		t.Fatalf("attempt indexes must remain distinct: %v %v", firing.Annotations, resolved.Annotations)
	}
}

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
	coOccurrence := compile("co_occurrence")
	negativeMatches, _ := coOccurrence.Cases[1].Rule.MatchCriteria["required_matches"].([]any)
	if len(negativeMatches) != 2 || coOccurrence.Cases[1].Rule.MatchCriteria["min_matches"] != 2 {
		t.Fatalf("co-occurrence negative changed the rule threshold instead of omitting a stimulus: %+v", coOccurrence.Cases[1].Rule.MatchCriteria)
	}
	persistence := compile("persistence")
	if len(persistence.Cases[1].Alerts) != 2 || persistence.Cases[1].Alerts[1].Status != "resolved" {
		t.Fatalf("persistence negative=%+v", persistence.Cases[1].Alerts)
	}
	parentChild := compile("parent_child")
	if parentChild.Cases[0].Rule.EmitAlertName != "" || len(parentChild.Cases[0].Alerts) != 2 {
		t.Fatalf("parent-child plan=%+v", parentChild.Cases[0])
	}
	negativeParentMatch, _ := parentChild.Cases[1].Rule.MatchCriteria["parent_match"].(map[string]string)
	if negativeParentMatch["alertname"] == "" {
		t.Fatalf("parent-child negative rule has no deterministic parent selector: %+v", parentChild.Cases[1].Rule.MatchCriteria)
	}
}

func TestCompiledMatchCriteriaMirrorOSCARPublicV1Schemas(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	tests := []struct {
		pattern string
		keys    []string
	}{
		{"flood", []string{"match", "min_count"}},
		{"co_occurrence", []string{"required_matches", "min_matches"}},
		{"sequence", []string{"sequence"}},
		{"persistence", []string{"match", "unresolved_for_seconds"}},
		{"absence", []string{"expected_match", "expected_every_seconds", "absent_for_seconds"}},
		{"parent_child", []string{"parent_match", "child_matches", "suppress_children_for_notifiers", "tag_children_for_notifiers"}},
		{"cross_source", []string{"required_sources"}},
		{"threshold", []string{"match", "distinct_label", "min_distinct_count"}},
	}
	for _, test := range tests {
		plan, err := compiler.Compile(run, scenario.Builtin(test.pattern), compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil {
			t.Fatalf("%s: %v", test.pattern, err)
		}
		for _, item := range plan.Cases {
			if len(item.Rule.MatchCriteria) != len(test.keys) {
				t.Errorf("%s keys=%v want=%v", test.pattern, item.Rule.MatchCriteria, test.keys)
			}
			for _, key := range test.keys {
				if _, ok := item.Rule.MatchCriteria[key]; !ok {
					t.Errorf("%s missing OSCAR schema key %q: %v", test.pattern, key, item.Rule.MatchCriteria)
				}
			}
		}
	}
}

func TestParentChildNotifierNamesComeFromScenarioInput(t *testing.T) {
	run := domain.Run{ID: "crt_0123456789ABCDEFGHJKMNPQRS", ShortToken: "7Q9K2M4A"}
	input := scenario.Builtin("parent_child")
	input.Cases[0].SuppressForNotifiers = []string{"pager-duty-lab"}
	input.Cases[0].TagForNotifiers = []string{"email-lab"}
	plan, err := compiler.Compile(run, input, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	suppress, ok := plan.Cases[0].Rule.MatchCriteria["suppress_children_for_notifiers"].([]string)
	if !ok || len(suppress) != 1 || suppress[0] != "pager-duty-lab" {
		t.Fatalf("suppression notifiers=%#v", plan.Cases[0].Rule.MatchCriteria["suppress_children_for_notifiers"])
	}
	tag, ok := plan.Cases[0].Rule.MatchCriteria["tag_children_for_notifiers"].([]string)
	if !ok || len(tag) != 1 || tag[0] != "email-lab" {
		t.Fatalf("tag notifiers=%#v", plan.Cases[0].Rule.MatchCriteria["tag_children_for_notifiers"])
	}
}
