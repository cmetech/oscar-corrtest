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

func TestSemanticModelSequenceUsesChronologicalTail(t *testing.T) {
	type input struct {
		name, status, source, site string
		second                     int
	}
	for _, test := range []struct {
		name     string
		sequence []string
		inputs   []input
		want     bool
	}{
		{"unmatched alert does not enter window", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"X", "firing", "noise", "x", 1}, {"B", "firing", "b", "x", 3}}, true},
		{"in order", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "x", 3}}, true},
		{"reversed", []string{"A", "B"}, []input{{"B", "firing", "b", "x", 0}, {"A", "firing", "a", "x", 3}}, false},
		{"tail not subsequence", []string{"A", "B", "C"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b1", "x", 1}, {"B", "firing", "b2", "x", 2}, {"C", "firing", "c", "x", 3}}, false},
		{"resolution cannot advance", []string{"A", "B"}, []input{{"A", "resolved", "a", "x", 0}, {"B", "firing", "b", "x", 3}}, false},
		{"arrival differs from event order", []string{"A", "B"}, []input{{"B", "firing", "b", "x", 3}, {"A", "firing", "a", "x", 0}}, true},
		{"event order invalid", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 3}, {"B", "firing", "b", "x", 0}}, false},
		{"expired first", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "x", 31}}, false},
		{"different groups", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "y", 3}}, false},
		{"dedup identical source", []string{"A", "A"}, []input{{"A", "firing", "a", "x", 0}, {"A", "firing", "a", "x", 1}}, false},
		{"same time arrival tie", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "x", 0}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			m := NewModel(base)
			clauses := []any{}
			for _, name := range test.sequence {
				clauses = append(clauses, map[string]string{"alertname": name})
			}
			rule := compiler.RulePlan{Name: "rule", Pattern: "sequence", WindowSeconds: 30, GroupBy: []string{"site"}, MatchCriteria: map[string]any{"sequence": clauses}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT", "oscar_test_run_id": "test"}}
			if _, err := m.CreateRule(context.Background(), rule); err != nil {
				t.Fatal(err)
			}
			for _, event := range test.inputs {
				eventTime := base.Add(time.Duration(event.second) * time.Second)
				if eventTime.After(m.Now()) {
					if err := m.Sleep(context.Background(), eventTime.Sub(m.Now())); err != nil {
						t.Fatal(err)
					}
				}
				_, err := m.Inject(context.Background(), compiler.AlertPlan{Name: event.name, Status: event.status, StartsAt: base.Add(time.Duration(event.second) * time.Second), Labels: map[string]string{"alertname": event.name, "source": event.source, "site": event.site, "oscar_test_rule_name": "rule", "oscar_test_run_id": "test"}})
				if err != nil {
					t.Fatal(err)
				}
			}
			rows, err := m.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "PARENT"})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(rows) == 1; got != test.want {
				t.Fatalf("trigger=%v want=%v", got, test.want)
			}
		})
	}
}
