package testoscar

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

func TestSemanticModelFloodUsesRuleThresholdAndDistinctLabelFingerprints(t *testing.T) {
	model := NewModel(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	rule := compiler.RulePlan{Name: "rule", Pattern: "flood", MatchCriteria: map[string]any{"match": map[string]string{"alertname": "SOURCE"}, "min_count": 5}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT", "oscar_test_run_id": "crt_test"}}
	if _, err := model.CreateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 4; index++ {
		alert := compiler.AlertPlan{Name: "SOURCE", Status: "firing", Labels: map[string]string{"alertname": "SOURCE", "event": fmt.Sprint(index), "oscar_test_rule_name": "rule", "oscar_test_run_id": "crt_test"}}
		if _, err := model.Inject(context.Background(), alert); err != nil {
			t.Fatal(err)
		}
	}
	parents, err := model.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "PARENT"})
	if err != nil || len(parents) != 0 {
		t.Fatalf("below-threshold parents=%+v err=%v", parents, err)
	}
	if _, err := model.Inject(context.Background(), compiler.AlertPlan{Name: "SOURCE", Status: "firing", Labels: map[string]string{"alertname": "SOURCE", "event": "5", "oscar_test_rule_name": "rule", "oscar_test_run_id": "crt_test"}}); err != nil {
		t.Fatal(err)
	}
	parents, err = model.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "PARENT"})
	if err != nil || len(parents) != 1 {
		t.Fatalf("at-threshold parents=%+v err=%v", parents, err)
	}
}
