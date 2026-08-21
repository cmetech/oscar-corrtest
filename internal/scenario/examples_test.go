package scenario_test

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestAllExamplesDecodeAndCompile(t *testing.T) {
	examples := scenario.AllExamples()
	expectedIDs := []string{
		"flood:basic", "flood:advanced",
		"co_occurrence:basic", "co_occurrence:advanced",
		"sequence:basic", "sequence:advanced",
		"persistence:basic", "persistence:advanced",
		"absence:basic", "absence:advanced",
		"parent_child:basic", "parent_child:advanced",
		"cross_source:basic", "cross_source:advanced",
		"threshold:basic", "threshold:advanced",
	}
	if len(examples) != 16 {
		t.Fatalf("got %d examples, want 16", len(examples))
	}
	seen := map[string]bool{}
	for index, example := range examples {
		if example.ID != expectedIDs[index] {
			t.Fatalf("example %d ID=%q want %q", index, example.ID, expectedIDs[index])
		}
		if seen[example.ID] {
			t.Fatalf("duplicate %s", example.ID)
		}
		seen[example.ID] = true
		if example.Title == "" || example.Summary == "" {
			t.Fatalf("%s has empty display metadata", example.ID)
		}
		raw, err := scenario.Encode(example.Scenario)
		if err != nil {
			t.Fatalf("encode %s: %v", example.ID, err)
		}
		document, err := scenario.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", example.ID, err)
		}
		_, err = compiler.Compile(domain.Run{ID: "crt_example", ShortToken: "EXAMPLE1"}, document, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil {
			t.Fatalf("compile %s: %v", example.ID, err)
		}
		found, ok := scenario.LookupExample(example.Pattern, example.Level)
		if !ok || found.ID != example.ID {
			t.Fatalf("lookup %s/%s = (%+v, %v)", example.Pattern, example.Level, found, ok)
		}
	}
}

func TestLookupExampleIsExactAndDefensive(t *testing.T) {
	for _, query := range [][2]string{{"Flood", "basic"}, {"flood", "Basic"}, {"flood ", "basic"}, {"flood", "advanced "}, {"unknown", "basic"}} {
		if _, ok := scenario.LookupExample(query[0], query[1]); ok {
			t.Fatalf("non-canonical lookup accepted: %q/%q", query[0], query[1])
		}
	}
	first, ok := scenario.LookupExample("flood", "basic")
	if !ok {
		t.Fatal("canonical lookup failed")
	}
	first.Scenario.Cases[0].Labels["site"] = "mutated"
	first.Scenario.Cases[0].GroupBy[0] = "mutated"
	second, _ := scenario.LookupExample("flood", "basic")
	if second.Scenario.Cases[0].Labels["site"] == "mutated" || second.Scenario.Cases[0].GroupBy[0] == "mutated" {
		t.Fatal("lookup exposed mutable catalog storage")
	}
}

func TestAdvancedExamplesDemonstrateCookbookVariants(t *testing.T) {
	lookup := func(pattern string) scenario.Scenario {
		t.Helper()
		example, ok := scenario.LookupExample(pattern, "advanced")
		if !ok {
			t.Fatalf("missing advanced %s example", pattern)
		}
		return example.Scenario
	}

	flood := lookup("flood")
	if !reflect.DeepEqual(flood.Cases[0].GroupBy, []string{"site", "service"}) || len(flood.Cases[0].Events) != 5 || len(flood.Cases[1].Events) != 5 {
		t.Fatalf("advanced flood stimuli=%+v", flood.Cases)
	}
	coOccurrence := lookup("co_occurrence")
	if len(coOccurrence.Cases[0].Events) != 3 || len(coOccurrence.Cases[1].Events) != 2 {
		t.Fatalf("advanced co-occurrence stimuli=%+v", coOccurrence.Cases)
	}
	persistence := lookup("persistence")
	if persistence.Cases[1].Events[1].Delay != 29*time.Second {
		t.Fatalf("advanced persistence resolution=%s", persistence.Cases[1].Events[1].Delay)
	}
	absence := lookup("absence")
	if len(absence.Cases[1].Events) != 6 || absence.Cases[1].Events[5].Delay != 50*time.Second {
		t.Fatalf("advanced absence cadence=%+v", absence.Cases[1].Events)
	}
	parentChild := lookup("parent_child")
	if len(parentChild.Cases[0].SuppressForNotifiers) < 2 || len(parentChild.Cases[0].TagForNotifiers) < 2 {
		t.Fatalf("advanced parent-child notifier policy=%+v", parentChild.Cases[0])
	}
}
