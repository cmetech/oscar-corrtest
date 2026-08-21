package scenario_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

func TestPublicContractCoversClosedWireModel(t *testing.T) {
	want := []string{
		"scenario.apiVersion", "scenario.kind", "scenario.name", "scenario.suite",
		"scenario.pattern", "scenario.maxDuration", "scenario.cases",
		"case.name", "case.code", "case.polarity", "case.window", "case.groupBy",
		"case.labels", "case.role", "case.repeat", "case.events",
		"case.suppressForNotifiers", "case.tagForNotifiers", "case.assertions",
		"event.role", "event.status", "event.labels", "event.delay",
		"assertion.kind", "assertion.outcome", "assertion.equals",
	}
	got := map[string]int{}
	for _, field := range scenario.PublicContract().Fields {
		got[field.ID]++
	}
	for _, id := range want {
		if got[id] != 1 {
			t.Errorf("field %s count = %d, want 1", id, got[id])
		}
	}
}

func TestCommittedJSONSchemaMatchesGenerator(t *testing.T) {
	want, err := scenario.GenerateJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/schema/correlation-scenario.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("committed scenario schema differs from generated schema")
	}
}

func TestGeneratedJSONSchemaEncodesClosedObjectsAndCaseStimuli(t *testing.T) {
	raw, err := scenario.GenerateJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("generated schema is invalid JSON")
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	definitions := document["$defs"].(map[string]any)
	caseDefinition := definitions["case"].(map[string]any)
	if got := len(caseDefinition["oneOf"].([]any)); got != 2 {
		t.Errorf("case exclusive choices = %d, want 2", got)
	}
	for _, level := range []map[string]any{document, caseDefinition, definitions["event"].(map[string]any), definitions["assertion"].(map[string]any)} {
		if got, ok := level["additionalProperties"].(bool); !ok || got {
			t.Errorf("object level is not closed: %#v", level["additionalProperties"])
		}
	}
}

func TestReservedLabelsAndDurationBoundsComeFromThePublicContract(t *testing.T) {
	labels := scenario.ReservedLabels()
	for _, label := range labels {
		if !scenario.IsReservedLabel(label) {
			t.Errorf("listed reserved label %q is not recognized", label)
		}
	}
	first := labels[0]
	labels[0] = "operator_owned"
	if got := scenario.ReservedLabels()[0]; got != first {
		t.Errorf("ReservedLabels leaked mutable canonical storage: got %q, want %q", got, first)
	}
	if scenario.IsReservedLabel("operator_owned") {
		t.Fatal("unlisted operator label is reserved")
	}

	raw, err := scenario.GenerateJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	properties := document["properties"].(map[string]any)
	maxDuration := properties["maxDuration"].(map[string]any)
	if got, want := maxDuration["x-corrtest-maximum"], scenario.MaxScenarioDuration.String(); got != want {
		t.Errorf("maxDuration upper bound = %q, want %q", got, want)
	}
	definitions := document["$defs"].(map[string]any)
	caseProperties := definitions["case"].(map[string]any)["properties"].(map[string]any)
	window := caseProperties["window"].(map[string]any)
	if got, want := window["x-corrtest-maximum"], scenario.MaxCaseWindow.String(); got != want {
		t.Errorf("window upper bound = %q, want %q", got, want)
	}
	if scenario.MaxScenarioDuration != 5*time.Minute || scenario.MaxCaseWindow != 2*time.Minute {
		t.Fatal("approved duration budgets changed")
	}
}
