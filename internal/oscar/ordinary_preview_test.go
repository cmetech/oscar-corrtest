package oscar_test

import (
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"os"
	"strings"
	"testing"
)

func TestOrdinaryPreviewDescribesActualReceiverAndSeparatePublicOperations(t *testing.T) {
	raw, err := os.ReadFile("../../scenarios/reliability/flood-ordinary-webhook.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compiler.Compile(domain.Run{ID: "crt_00000000000000000000000000", ShortToken: "00000000"}, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := oscar.BuildOperationPreview(plan, "test")
	if err != nil {
		t.Fatal(err)
	}
	receiver, probeClear := 0, 0
	for _, op := range ops {
		if op.Stage == "stimulus.inject_alert" || op.Stage == "preflight.inject_label_probe" || op.Stage == "preflight.clear_label_probe" {
			if op.Path != domain.OrdinaryWebhookPath || strings.Contains(op.Body, `"alerts"`) || !strings.Contains(op.Body, `"endsAt"`) {
				t.Fatalf("wrong receiver preview %+v", op)
			}
			receiver++
		}
		if op.Stage == "preflight.clear_label_probe" {
			probeClear++
		}
		if op.Stage == "setup.create_rule" && op.Path != "/api/v1/correlation_rules" {
			t.Fatal("rule routed to receiver")
		}
	}
	if receiver != 11 || probeClear != 1 {
		t.Fatalf("receiver=%d probeClear=%d", receiver, probeClear)
	}
}
