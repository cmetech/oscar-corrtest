package scenario_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
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

func TestGeneratedJSONSchemaRejectsReservedLabelsAndPatternRestrictedNotifiers(t *testing.T) {
	document := generatedSchemaDocument(t)
	definitions := document["$defs"].(map[string]any)
	for _, definitionName := range []string{"case", "event"} {
		properties := definitions[definitionName].(map[string]any)["properties"].(map[string]any)
		propertyNames := properties["labels"].(map[string]any)["propertyNames"].(map[string]any)
		not, ok := propertyNames["not"].(map[string]any)
		if !ok {
			t.Fatalf("%s label property names do not reject reserved keys: %#v", definitionName, propertyNames)
		}
		got := stringSlice(not["enum"])
		want := scenario.ReservedLabels()
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s reserved label rejection=%v want=%v", definitionName, got, want)
		}
	}

	conditionals, ok := document["allOf"].([]any)
	if !ok || len(conditionals) == 0 {
		t.Fatal("root schema has no pattern-aware conditional")
	}
	restriction := conditionals[0].(map[string]any)
	ifPattern := restriction["if"].(map[string]any)["properties"].(map[string]any)["pattern"].(map[string]any)
	gotPatterns := stringSlice(ifPattern["enum"])
	wantPatterns := []string{}
	for _, pattern := range scenario.SupportedPatterns() {
		if pattern != "parent_child" {
			wantPatterns = append(wantPatterns, pattern)
		}
	}
	sort.Strings(gotPatterns)
	sort.Strings(wantPatterns)
	if !reflect.DeepEqual(gotPatterns, wantPatterns) {
		t.Fatalf("notifier rejection patterns=%v want=%v", gotPatterns, wantPatterns)
	}
	caseItems := restriction["then"].(map[string]any)["properties"].(map[string]any)["cases"].(map[string]any)["items"].(map[string]any)
	forbidden := caseItems["not"].(map[string]any)["anyOf"].([]any)
	gotFields := []string{}
	for _, branch := range forbidden {
		gotFields = append(gotFields, stringSlice(branch.(map[string]any)["required"])...)
	}
	sort.Strings(gotFields)
	if !reflect.DeepEqual(gotFields, []string{"suppressForNotifiers", "tagForNotifiers"}) {
		t.Fatalf("non-parent-child notifier rejection=%v", gotFields)
	}
}

func TestGeneratedJSONSchemaRejectsLabelsOnResolvedEventsAndDocumentsSemanticLimits(t *testing.T) {
	document := generatedSchemaDocument(t)
	description, _ := document["description"].(string)
	for _, phrase := range []string{"reserved label exclusion", "pattern-restricted notifier fields", "cross-array notifier disjointness", "event ordering", "strict decoder"} {
		if !strings.Contains(strings.ToLower(description), phrase) {
			t.Errorf("schema description missing %q: %q", phrase, description)
		}
	}
	event := document["$defs"].(map[string]any)["event"].(map[string]any)
	conditionals, ok := event["allOf"].([]any)
	if !ok || len(conditionals) == 0 {
		t.Fatal("event schema has no status-aware conditional")
	}
	conditional := conditionals[0].(map[string]any)
	status := conditional["if"].(map[string]any)["properties"].(map[string]any)["status"].(map[string]any)["const"]
	if status != "resolved" {
		t.Fatalf("event conditional status=%v", status)
	}
	forbidden := stringSlice(conditional["then"].(map[string]any)["not"].(map[string]any)["required"])
	if !reflect.DeepEqual(forbidden, []string{"labels"}) {
		t.Fatalf("resolved event forbidden fields=%v", forbidden)
	}
}

func generatedSchemaDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := scenario.GenerateJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.(string))
	}
	return result
}
