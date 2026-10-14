package compiler_test

import (
	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"os"
	"strings"
	"testing"
)

func TestOrdinaryWebhookFixtureCompilesWithExplicitRoute(t *testing.T) {
	raw, err := os.ReadFile("../../scenarios/reliability/flood-ordinary-webhook.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scenario.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "crt_00000000000000000000000000", ShortToken: "00000000"}
	plan, err := compiler.Compile(run, doc, compiler.Capabilities{PipelineMode: "phase_a_audit_only"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InputPath != "ordinary-webhook" || len(plan.Cases[0].Alerts) != 5 || len(plan.Cases[1].Alerts) != 4 {
		t.Fatal("ordinary route lost")
	}
	for _, mode := range []string{"refire", "unnamed", "resolved", "selected"} {
		t.Run(mode, func(t *testing.T) {
			changed := doc
			changed.Cases = append([]scenario.Case(nil), doc.Cases...)
			changed.Cases[0].Events = append([]scenario.Event(nil), doc.Cases[0].Events...)
			switch mode {
			case "refire":
				changed.Cases[0].Events[1].Source = "A"
			case "unnamed":
				changed.Cases[0].Events[0].Source = ""
			case "resolved":
				changed.Cases[0].Events[1].Source = "A"
				changed.Cases[0].Events[1].Status = "resolved"
			case "selected":
				changed.InputPath = "receiver-admission"
			}
			if _, err := compiler.Compile(run, changed, compiler.Capabilities{PipelineMode: "phase_a_audit_only"}); err == nil {
				t.Fatal("unsupported source flow admitted")
			}
		})
	}
}
