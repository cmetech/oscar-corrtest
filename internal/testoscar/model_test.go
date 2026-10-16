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

// These are exact-set model scope checks, not Redis HyperLogLog accuracy tests.
func TestThresholdExactSetApproximationPartitionsEligibleValues(t *testing.T) {
	type input struct {
		device, site, status string
		second               int
	}
	for _, test := range []struct {
		name   string
		inputs []input
		want   bool
	}{
		{"same value repeated", []input{{"a", "x", "firing", 0}, {"a", "x", "firing", 1}, {"a", "x", "firing", 2}}, false},
		{"different groups", []input{{"a", "x", "firing", 0}, {"b", "x", "firing", 1}, {"c", "y", "firing", 2}}, false},
		{"fixed epoch splits close events", []input{{"a", "x", "firing", 29}, {"b", "x", "firing", 30}, {"c", "x", "firing", 31}}, false},
		{"resolved value ignored", []input{{"a", "x", "firing", 0}, {"b", "x", "firing", 1}, {"c", "x", "resolved", 2}}, false},
		{"empty value skipped by Lua", []input{{"a", "x", "firing", 0}, {"b", "x", "firing", 1}, {"", "x", "firing", 2}}, false},
		{"missing value ignored", []input{{"a", "x", "firing", 0}, {"b", "x", "firing", 1}, {"<missing>", "x", "firing", 2}}, false},
		{"three values idealized only", []input{{"a", "x", "firing", 0}, {"b", "x", "firing", 1}, {"c", "x", "firing", 2}}, true},
		{"zero string is present", []input{{"a", "x", "firing", 0}, {"b", "x", "firing", 1}, {"0", "x", "firing", 2}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			m := NewModel(base)
			rule := compiler.RulePlan{Name: "rule", Pattern: "threshold", WindowSeconds: 30, GroupBy: []string{"site"}, MatchCriteria: map[string]any{"match": map[string]string{"alertname": "A"}, "distinct_label": "device", "min_distinct_count": 3}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT"}}
			if _, err := m.CreateRule(context.Background(), rule); err != nil {
				t.Fatal(err)
			}
			for i, event := range test.inputs {
				at := base.Add(time.Duration(event.second) * time.Second)
				if at.After(m.Now()) {
					if err := m.Sleep(context.Background(), at.Sub(m.Now())); err != nil {
						t.Fatal(err)
					}
				}
				labels := map[string]string{"alertname": "A", "event": fmt.Sprint(i), "site": event.site, "oscar_test_rule_name": "rule"}
				if event.device != "<missing>" {
					labels["device"] = event.device
				}
				if _, err := m.Inject(context.Background(), compiler.AlertPlan{Name: "A", Status: event.status, StartsAt: at, Labels: labels}); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := m.FindHistory(context.Background(), oscar.HistoryQuery{AlertName: "PARENT"})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(rows) == 1; got != test.want {
				t.Fatalf("idealized trigger=%v want=%v", got, test.want)
			}
		})
	}
}

// First-timer semantics only: no scheduler, Redis, WAL or physical recovery proof.
func TestSemanticModelPersistenceUsesGroupTimerCancellation(t *testing.T) {
	type input struct {
		status, source, site string
		second               int
	}
	for _, test := range []struct {
		name    string
		inputs  []input
		observe int
		want    int
	}{
		{"not before deadline", []input{{"firing", "A", "x", 0}}, 9, -1},
		{"unresolved at deadline", []input{{"firing", "A", "x", 0}}, 10, 0},
		{"different fingerprint same group cancels", []input{{"firing", "A", "x", 0}, {"resolved", "B", "x", 5}}, 20, -1},
		{"other group cannot cancel", []input{{"firing", "A", "x", 0}, {"resolved", "A", "y", 5}}, 10, 0},
		{"repeat cannot postpone first timer", []input{{"firing", "A", "x", 0}, {"firing", "B", "x", 8}}, 10, 0},
		{"ideal timer wins exact deadline tie", []input{{"firing", "A", "x", 0}, {"resolved", "A", "x", 10}}, 10, 0},
		{"late clear retains elapsed trigger", []input{{"firing", "A", "x", 0}, {"resolved", "A", "x", 11}}, 12, 0},
		{"cancel then fresh firing has new deadline", []input{{"firing", "A", "x", 0}, {"resolved", "B", "x", 5}, {"firing", "C", "x", 6}}, 15, -1},
		{"fresh firing after cancel can trigger", []input{{"firing", "A", "x", 0}, {"resolved", "B", "x", 5}, {"firing", "C", "x", 6}}, 16, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			m := NewModel(base)
			ctx := context.Background()
			rule := compiler.RulePlan{Name: "rule", Pattern: "persistence", GroupBy: []string{"site"}, MatchCriteria: map[string]any{"match": map[string]string{"alertname": "SOURCE"}, "unresolved_for_seconds": 10}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT"}}
			if _, err := m.CreateRule(ctx, rule); err != nil {
				t.Fatal(err)
			}
			for _, event := range test.inputs {
				if err := m.Sleep(ctx, base.Add(time.Duration(event.second)*time.Second).Sub(m.Now())); err != nil {
					t.Fatal(err)
				}
				if _, err := m.Inject(ctx, compiler.AlertPlan{Name: "SOURCE", Status: event.status, Labels: map[string]string{"alertname": "SOURCE", "source": event.source, "site": event.site, "oscar_test_rule_name": "rule"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Sleep(ctx, base.Add(time.Duration(test.observe)*time.Second).Sub(m.Now())); err != nil {
				t.Fatal(err)
			}
			if got := m.triggerIndex(rule); got != test.want {
				t.Fatalf("trigger index=%d want=%d", got, test.want)
			}
			rows, err := m.FindHistory(ctx, oscar.HistoryQuery{AlertName: "PARENT"})
			if err != nil {
				t.Fatal(err)
			}
			if (len(rows) == 1) != (test.want >= 0) {
				t.Fatalf("model parent=%v", rows)
			}
		})
	}
}

func TestSemanticModelPersistenceDeadlineUsesOriginalStart(t *testing.T) {
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	m := NewModel(base)
	ctx := context.Background()
	rule := compiler.RulePlan{Name: "rule", Pattern: "persistence", GroupBy: []string{"site"}, MatchCriteria: map[string]any{"match": map[string]string{"alertname": "SOURCE"}, "unresolved_for_seconds": 10}, EmitAlertName: "PARENT", EmitLabels: map[string]string{"alertname": "PARENT"}}
	if _, err := m.CreateRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Inject(ctx, compiler.AlertPlan{Name: "SOURCE", Status: "firing", StartsAt: base.Add(-5 * time.Second), Labels: map[string]string{"alertname": "SOURCE", "site": "x", "oscar_test_rule_name": "rule"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Sleep(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := m.triggerIndex(rule); got != 0 {
		t.Fatalf("event-time deadline was replaced by arrival time: %d", got)
	}
}
