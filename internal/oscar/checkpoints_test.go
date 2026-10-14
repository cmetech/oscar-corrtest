package oscar_test

import (
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"testing"
	"time"
)

func TestPreviewOrdersCheckpointBeforeNextStimulus(t *testing.T) {
	doc := scenario.Builtin("flood")
	doc.Cases[0].Checkpoints = []scenario.Checkpoint{{Name: "after-fifth", AfterEvent: 5, Window: time.Second, Assertions: []scenario.Assertion{{Kind: "audit-count", Outcome: "parent_emitted", Equals: 1}}}}
	plan, err := compiler.Compile(domain.Run{ID: "crt_checkpoint", ShortToken: "CHECK123"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	operations, err := oscar.BuildOperationPreview(plan, "test")
	if err != nil {
		t.Fatal(err)
	}
	point, following := -1, -1
	for i, op := range operations {
		if op.Stage == "checkpoint.observe" {
			point = i
		}
		if op.Stage == "stimulus.inject_alert" && op.CaseCode == "N01" && following < 0 {
			following = i
		}
	}
	if point < 0 || following <= point || operations[point].Method != "LOCAL" {
		t.Fatalf("checkpoint preview omitted or reordered: point=%d next=%d", point, following)
	}
}
