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
		"unknown field": strings.Replace(validCustom, "pattern: flood", "pattern: flood\nunknown: true", 1),
		"duplicate key": strings.Replace(validCustom, "pattern: flood", "pattern: flood\npattern: threshold", 1),
		"multiple docs": validCustom + "\n---\n" + validCustom,
		"alias":         strings.Replace(validCustom, "labels: {site: lab-a}", "labels: &labels {site: lab-a}\n    events:\n      - {role: interface_down, status: firing, labels: *labels}", 1),
		"too large":     strings.Repeat("x", (1<<20)+1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := scenario.Decode(strings.NewReader(input)); err == nil {
				t.Fatal("unsafe document accepted")
			}
		})
	}
}
