package compiler_test

import (
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"strings"
	"testing"
	"time"
)

func checkpointDocument() string {
	return strings.Replace(lifecycleScenario, "  assertions: [{kind: audit-count, outcome: parent_emitted, equals: 1}]", `  checkpoints:
  - name: threshold-reached
    afterEvent: 5
    window: 1s
    assertions: [{kind: audit-count, outcome: parent_emitted, equals: 1}]
  assertions: [{kind: audit-count, outcome: parent_emitted, equals: 1}]`, 1)
}

func TestCheckpointContractRoundTripsAndCompiles(t *testing.T) {
	doc, err := scenario.Decode(strings.NewReader(checkpointDocument()))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := scenario.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "threshold-reached") || !strings.Contains(string(encoded), "afterEvent: 5") {
		t.Fatal("checkpoint contract lost on round trip")
	}
	if _, err := compiler.Compile(domain.Run{ID: "crt_checkpoint", ShortToken: "CHECK123"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointRejectsUnsafeBoundaries(t *testing.T) {
	for name, change := range map[string][2]string{
		"before input":               {"afterEvent: 5", "afterEvent: 0"},
		"past input":                 {"afterEvent: 5", "afterEvent: 100"},
		"zero window":                {"window: 1s", "window: 0s"},
		"window overlaps next input": {"window: 1s", "window: 2s"},
		"unknown assertion":          {"kind: audit-count, outcome: parent_emitted, equals: 1}]\n  assertions:", "kind: source-clear-authority, equals: 1}]\n  assertions:"},
		"unknown field":              {"afterEvent: 5", "afterEvent: 5\n    allowSkipped: true"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scenario.Decode(strings.NewReader(strings.Replace(checkpointDocument(), change[0], change[1], 1))); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
}

func TestCheckpointRequiresExplicitExpectedCount(t *testing.T) {
	raw := strings.Replace(checkpointDocument(), "outcome: parent_emitted, equals: 1}]\n  assertions:", "outcome: parent_emitted}]\n  assertions:", 1)
	if _, err := scenario.Decode(strings.NewReader(raw)); err == nil {
		t.Fatal("missing equals silently became a negative assertion")
	}
}

func TestCheckpointNamesAndOrdinalsStayBoundedAndOrdered(t *testing.T) {
	for _, kind := range []string{"duplicate name", "duplicate ordinal", "too many", "empty assertions", "late window"} {
		t.Run(kind, func(t *testing.T) {
			doc, err := scenario.Decode(strings.NewReader(checkpointDocument()))
			if err != nil {
				t.Fatal(err)
			}
			point := doc.Cases[0].Checkpoints[0]
			switch kind {
			case "duplicate name":
				point.AfterEvent = 9
				doc.Cases[0].Checkpoints = append(doc.Cases[0].Checkpoints, point)
			case "duplicate ordinal":
				point.Name = "another"
				doc.Cases[0].Checkpoints = append(doc.Cases[0].Checkpoints, point)
			case "too many":
				for i := 0; i < scenario.MaxCheckpoints; i++ {
					doc.Cases[0].Checkpoints = append(doc.Cases[0].Checkpoints, point)
				}
			case "empty assertions":
				doc.Cases[0].Checkpoints[0].Assertions = nil
			case "late window":
				doc.Cases[0].Checkpoints[0].AfterEvent = 9
				doc.Cases[0].Checkpoints[0].Window = 2 * time.Minute
			}
			if err := scenario.Validate(doc); err == nil {
				t.Fatal("unsafe checkpoint accepted")
			}
		})
	}
}
