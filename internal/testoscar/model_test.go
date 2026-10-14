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

func TestSemanticModelCoOccurrenceCountsWindowedDistinctNames(t *testing.T) {
	type input struct {
		name, status, source, site string
		second                     int
	}
	for _, test := range []struct {
		name     string
		required []string
		inputs   []input
		want     bool
	}{
		{"distinct roles", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "x", 3}}, true},
		{"same role twice", []string{"A", "B"}, []input{{"A", "firing", "a1", "x", 0}, {"A", "firing", "a2", "x", 3}}, false},
		{"duplicate clauses not distinct names", []string{"A", "A"}, []input{{"A", "firing", "a", "x", 0}}, false},
		{"different groups", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "y", 3}}, false},
		{"expired first", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"B", "firing", "b", "x", 31}}, false},
		{"resolution cannot advance", []string{"A", "B"}, []input{{"A", "resolved", "a", "x", 0}, {"B", "firing", "b", "x", 3}}, false},
		{"repeat refreshes name", []string{"A", "B"}, []input{{"A", "firing", "a1", "x", 0}, {"A", "firing", "a2", "x", 20}, {"B", "firing", "b", "x", 40}}, true},
		{"last arrival timestamp wins", []string{"A", "B"}, []input{{"A", "firing", "a1", "x", 20}, {"A", "firing", "a2", "x", 0}, {"B", "firing", "b", "x", 40}}, false},
		{"unmatched name not counted", []string{"A", "B"}, []input{{"A", "firing", "a", "x", 0}, {"X", "firing", "noise", "x", 3}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			m := NewModel(base)
			required := []any{}
			for _, name := range test.required {
				required = append(required, map[string]string{"alertname": name})
			}
			rule := compiler.RulePlan{Name: "rule", Pattern: "co_occurrence", WindowSeconds: 30, GroupBy: []string{"site"}, MatchCriteria: map[string]any{"required_matches": required, "min_matches": 2}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT", "oscar_test_run_id": "test"}}
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
				_, err := m.Inject(context.Background(), compiler.AlertPlan{Name: event.name, Status: event.status, StartsAt: eventTime, Labels: map[string]string{"alertname": event.name, "source": event.source, "site": event.site, "oscar_test_rule_name": "rule", "oscar_test_run_id": "test"}})
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

func TestSemanticModelCrossSourceUsesRequiredSourcePairs(t *testing.T) {
	type input struct {
		name, status, tag, fallback, site string
		second                            int
	}
	for _, test := range []struct {
		name   string
		inputs []input
		want   bool
	}{
		{"required tags", []input{{"A", "firing", "snmp", "", "x", 0}, {"B", "firing", "api", "", "x", 3}}, true},
		{"same tag twice", []input{{"A", "firing", "snmp", "", "x", 0}, {"A", "firing", "snmp", "", "x", 3}}, false},
		{"source fallback", []input{{"A", "firing", "", "snmp", "x", 0}, {"B", "firing", "", "api", "x", 3}}, true},
		{"primary tag wins", []input{{"A", "firing", "snmp", "api", "x", 0}, {"B", "firing", "api", "snmp", "x", 3}}, true},
		{"wrong source name pair", []input{{"B", "firing", "snmp", "", "x", 0}, {"A", "firing", "api", "", "x", 3}}, false},
		{"different groups", []input{{"A", "firing", "snmp", "", "x", 0}, {"B", "firing", "api", "", "y", 3}}, false},
		{"expired first", []input{{"A", "firing", "snmp", "", "x", 0}, {"B", "firing", "api", "", "x", 31}}, false},
		{"resolution ignored", []input{{"A", "resolved", "snmp", "", "x", 0}, {"B", "firing", "api", "", "x", 3}}, false},
		{"repeat refresh", []input{{"A", "firing", "snmp", "", "x", 0}, {"A", "firing", "snmp", "", "x", 20}, {"B", "firing", "api", "", "x", 40}}, true},
		{"last arriving timestamp wins", []input{{"A", "firing", "snmp", "", "x", 20}, {"A", "firing", "snmp", "", "x", 0}, {"B", "firing", "api", "", "x", 40}}, false},
		{"unknown tag ignored", []input{{"A", "firing", "snmp", "", "x", 0}, {"B", "firing", "other", "", "x", 3}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			m := NewModel(base)
			rule := compiler.RulePlan{Name: "rule", Pattern: "cross_source", WindowSeconds: 30, GroupBy: []string{"site"}, MatchCriteria: map[string]any{"required_sources": []any{map[string]any{"source": "snmp", "match": map[string]string{"alertname": "A"}}, map[string]any{"source": "api", "match": map[string]string{"alertname": "B"}}}}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT", "oscar_test_run_id": "test"}}
			if _, err := m.CreateRule(context.Background(), rule); err != nil {
				t.Fatal(err)
			}
			for index, event := range test.inputs {
				eventTime := base.Add(time.Duration(event.second) * time.Second)
				if eventTime.After(m.Now()) {
					if err := m.Sleep(context.Background(), eventTime.Sub(m.Now())); err != nil {
						t.Fatal(err)
					}
				}
				_, err := m.Inject(context.Background(), compiler.AlertPlan{Name: event.name, Status: event.status, StartsAt: eventTime, Labels: map[string]string{"alertname": event.name, "event": fmt.Sprint(index), "oscar_source": event.tag, "source": event.fallback, "site": event.site, "oscar_test_rule_name": "rule", "oscar_test_run_id": "test"}})
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
