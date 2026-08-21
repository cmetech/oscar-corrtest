package scenario_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

const validCustom = `apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: custom-flood
suite: operator-custom
pattern: flood
maxDuration: 90s
cases:
  - name: positive
    code: P01
    polarity: positive
    role: interface_down
    repeat: 5
    window: 30s
    groupBy: [site]
    labels: {site: lab-a}
    assertions:
      - {kind: synthetic-alert-count, equals: 1}
  - name: negative
    code: N01
    polarity: negative
    role: interface_down
    repeat: 4
    window: 30s
    groupBy: [site]
    labels: {site: lab-b}
    assertions:
      - {kind: synthetic-alert-count, equals: 0}
`

func TestDecodeStrictCustomScenario(t *testing.T) {
	document, err := scenario.Decode(strings.NewReader(validCustom))
	if err != nil {
		t.Fatal(err)
	}
	if document.Name != "custom-flood" || document.MaxDuration != 90*time.Second || len(document.Cases) != 2 || document.Cases[0].Window != 30*time.Second {
		t.Fatalf("document=%+v", document)
	}
}

func TestDecodeRejectsUnsafeOrAmbiguousDocuments(t *testing.T) {
	tests := map[string]string{
		"unknown field":  strings.Replace(validCustom, "pattern: flood", "pattern: flood\nunknown: true", 1),
		"duplicate key":  strings.Replace(validCustom, "pattern: flood", "pattern: flood\npattern: threshold", 1),
		"multiple docs":  validCustom + "\n---\n" + validCustom,
		"alias":          strings.Replace(validCustom, "labels: {site: lab-a}", "labels: &labels {site: lab-a}\n    events:\n      - {role: interface_down, status: firing, labels: *labels}", 1),
		"too large":      strings.Repeat("x", (1<<20)+1),
		"empty cases":    strings.Replace(validCustom, strings.Split(validCustom, "cases:")[1], " []\n", 1),
		"reserved label": strings.Replace(validCustom, "labels: {site: lab-a}", "labels: {oscar_test_run_id: forged}", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := scenario.Decode(strings.NewReader(input)); err == nil {
				t.Fatal("unsafe document accepted")
			}
		})
	}
}

func TestDecodeEnforcesConditionalAssertionOutcomes(t *testing.T) {
	tests := map[string]string{
		"audit count needs outcome":             strings.Replace(validCustom, "{kind: synthetic-alert-count, equals: 1}", "{kind: audit-count, equals: 1}", 1),
		"audit count needs nonblank outcome":    strings.Replace(validCustom, "{kind: synthetic-alert-count, equals: 1}", "{kind: audit-count, outcome: ' ', equals: 1}", 1),
		"parent link count needs outcome":       strings.Replace(validCustom, "{kind: synthetic-alert-count, equals: 1}", "{kind: parent-link-count, equals: 1}", 1),
		"synthetic count forbids outcome":       strings.Replace(validCustom, "{kind: synthetic-alert-count, equals: 1}", "{kind: synthetic-alert-count, outcome: parent_emitted, equals: 1}", 1),
		"synthetic count forbids empty outcome": strings.Replace(validCustom, "{kind: synthetic-alert-count, equals: 1}", "{kind: synthetic-alert-count, outcome: '', equals: 1}", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := scenario.Decode(strings.NewReader(input)); err == nil {
				t.Fatal("invalid assertion outcome contract accepted")
			}
		})
	}
}

func TestDecodeRejectsExplicitRepeatOrRoleWithEventStimuli(t *testing.T) {
	eventBlock := "events:\n      - {role: interface_down, status: firing}\n      - {role: interface_down, status: firing}\n      - {role: interface_down, status: firing}\n      - {role: interface_down, status: firing}\n      - {role: interface_down, status: firing}"
	eventForm := strings.Replace(validCustom, "role: interface_down\n    repeat: 5", eventBlock, 1)
	for name, input := range map[string]string{
		"valid omitted event fields": eventForm,
		"events empty":               strings.Replace(eventForm, eventBlock, "events: []", 1),
		"events null":                strings.Replace(eventForm, eventBlock, "events: null", 1),
		"event role null":            strings.Replace(eventForm, "events:\n", "role: null\n    events:\n", 1),
		"event role empty":           strings.Replace(eventForm, "events:\n", "role: \"\"\n    events:\n", 1),
		"event repeat null":          strings.Replace(eventForm, "events:\n", "repeat: null\n    events:\n", 1),
		"event repeat zero":          strings.Replace(eventForm, "events:\n", "repeat: 0\n    events:\n", 1),
		"repeat role absent":         strings.Replace(validCustom, "role: interface_down\n    ", "", 1),
		"repeat role null":           strings.Replace(validCustom, "role: interface_down", "role: null", 1),
		"repeat role empty":          strings.Replace(validCustom, "role: interface_down", "role: \"\"", 1),
		"repeat absent":              strings.Replace(validCustom, "repeat: 5\n    ", "", 1),
		"repeat null":                strings.Replace(validCustom, "repeat: 5", "repeat: null", 1),
		"repeat zero":                strings.Replace(validCustom, "repeat: 5", "repeat: 0", 1),
		"case unknown field":         strings.Replace(validCustom, "code: P01", "code: P01\n    unknown: true", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := scenario.Decode(strings.NewReader(input))
			if name == "valid omitted event fields" {
				if err != nil {
					t.Fatalf("valid event form rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid stimulus field presence accepted")
			}
		})
	}
}
