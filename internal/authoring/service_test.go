package authoring

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestServiceBuildReturnsCanonicalTargetFreePreview(t *testing.T) {
	page, err := New("test-version").Build(DefaultSelection())
	if err != nil {
		t.Fatal(err)
	}
	if page.Selection != DefaultSelection() {
		t.Fatalf("selection=%+v", page.Selection)
	}
	if page.Inspection.Plan.RunID != PreviewRunID || page.Inspection.Plan.ShortToken != PreviewShortToken {
		t.Fatalf("plan identity=%q/%q", page.Inspection.Plan.RunID, page.Inspection.Plan.ShortToken)
	}
	if got := len(page.Inspection.Plan.Cases); got != 2 || page.Inspection.Plan.Cases[0].Code != "P01" || page.Inspection.Plan.Cases[1].Code != "N01" {
		t.Fatalf("plan cases=%+v", page.Inspection.Plan.Cases)
	}
	if len(page.Inspection.Operations) == 0 || page.Inspection.Operations[0].Stage != "preflight.validate_rule" {
		t.Fatalf("operations=%+v", page.Inspection.Operations)
	}
	wantSource, err := scenario.Encode(page.Example.Scenario)
	if err != nil {
		t.Fatal(err)
	}
	if page.Inspection.Source != string(wantSource) {
		t.Fatalf("source=%q want=%q", page.Inspection.Source, wantSource)
	}
	if !strings.Contains(page.Catalog.Lessons[0].Fragment, "apiVersion") {
		t.Fatalf("quickstart fragment=%q", page.Catalog.Lessons[0].Fragment)
	}
}

func TestServiceBuildDefaultsOnlyBlankSelectionValues(t *testing.T) {
	service := New("test-version")
	selections := []Selection{{Section: "patterns", Step: "stimuli", Pattern: "cross_source", Level: "advanced", View: "api"}}
	for _, section := range []string{"quickstart", "schema", "patterns", "assertions", "validation"} {
		selections = append(selections, Selection{Section: section})
	}
	for _, step := range []string{"identity", "cases", "stimuli", "assertions", "validate"} {
		selections = append(selections, Selection{Step: step})
	}
	for _, pattern := range scenario.SupportedPatterns() {
		selections = append(selections, Selection{Pattern: pattern})
	}
	for _, level := range []string{"basic", "advanced"} {
		selections = append(selections, Selection{Level: level})
	}
	for _, view := range []string{"yaml", "contract", "api", "lifecycle"} {
		selections = append(selections, Selection{View: view})
	}
	for _, selection := range selections {
		page, err := service.Build(selection)
		if err != nil {
			t.Fatalf("selection=%+v: %v", selection, err)
		}
		if selection.Section != "" && page.Selection.Section != selection.Section ||
			selection.Step != "" && page.Selection.Step != selection.Step ||
			selection.Pattern != "" && page.Selection.Pattern != selection.Pattern ||
			selection.Level != "" && page.Selection.Level != selection.Level ||
			selection.View != "" && page.Selection.View != selection.View {
			t.Fatalf("selection=%+v normalized=%+v", selection, page.Selection)
		}
	}
}

func TestServiceBuildRejectsEveryNonblankInvalidSelectionValue(t *testing.T) {
	for _, selection := range []Selection{
		{Section: "unknown"}, {Step: "unknown"}, {Pattern: "unknown"}, {Level: "unknown"}, {View: "unknown"},
		{Section: " "}, {Step: " "}, {Pattern: " "}, {Level: " "}, {View: " "},
	} {
		if _, err := New("test-version").Build(selection); err == nil {
			t.Fatalf("selection accepted: %+v", selection)
		}
	}
}

func TestServiceInspectIsStrictAndDeterministic(t *testing.T) {
	source, err := scenario.BuiltinSource("flood")
	if err != nil {
		t.Fatal(err)
	}
	service := New("test-version")
	first, err := service.Inspect(context.Background(), source, "phase_b_dispatch")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Inspect(context.Background(), source, "phase_b_dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("inspection was not deterministic")
	}
	if _, err := service.Inspect(context.Background(), []byte("pattern: flood\n"), "phase_b_dispatch"); err == nil {
		t.Fatal("invalid source accepted")
	}
	if _, err := service.Inspect(context.Background(), source, "unknown"); err == nil {
		t.Fatal("invalid pipeline mode accepted")
	}
}
