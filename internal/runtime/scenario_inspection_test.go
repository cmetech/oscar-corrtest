package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/config"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestInspectScenarioCompilesBothPolaritiesWithoutTarget(t *testing.T) {
	source, err := scenario.BuiltinSource("flood")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	inspection, err := runtime.InspectScenario(context.Background(), source, "phase_b_dispatch")
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Plan.Cases) != 2 || inspection.Plan.Cases[0].Code != "P01" || inspection.Plan.Cases[1].Code != "N01" {
		t.Fatalf("inspection=%+v", inspection)
	}
	if inspection.Plan.RunID != "crt_PREV1EW1000000000000000000" || inspection.Plan.ShortToken != "PREV1EW1" {
		t.Fatalf("inspection identity=%q/%q", inspection.Plan.RunID, inspection.Plan.ShortToken)
	}
	if len(inspection.Operations) == 0 || inspection.Operations[0].Stage != "preflight.validate_rule" {
		t.Fatalf("inspection operations=%+v", inspection.Operations)
	}
	for _, item := range inspection.Plan.Cases {
		if item.Rule.Name == "" || len(item.Alerts) == 0 || len(item.Assertions) == 0 {
			t.Fatalf("incomplete case=%+v", item)
		}
		for _, alert := range item.Alerts {
			for _, key := range []string{"alertname", "category", "oscar_test_run_id", "oscar_test_pattern", "oscar_test_case_code"} {
				if alert.Labels[key] == "" {
					t.Fatalf("alert missing %s: %+v", key, alert)
				}
			}
		}
	}
	if !strings.Contains(inspection.Source, "pattern: flood") {
		t.Fatalf("source=%q", inspection.Source)
	}
	runs, err := runtime.ListRuns(context.Background(), domain.RunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("inspection persisted runs=%+v err=%v", runs, err)
	}
}

func TestInspectScenarioRejectsInvalidPipelineMode(t *testing.T) {
	source, _ := scenario.BuiltinSource("flood")
	runtime, err := Open(context.Background(), config.Settings{DataDir: t.TempDir(), ListenAddress: "127.0.0.1:8787"}, version.Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.InspectScenario(context.Background(), source, "unknown"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
