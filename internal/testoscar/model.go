package testoscar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/compiler"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
)

// Model is an independent, manual-clock OSCAR correlation model for runner
// contract tests. It evaluates rule criteria and labels; it never reads case
// codes, polarity, physical naming conventions, or expected assertions.
type Model struct {
	mu      sync.Mutex
	now     time.Time
	nextID  int
	rules   map[int]compiler.RulePlan
	alerts  []modelAlert
	deleted map[int]bool
}

type modelAlert struct {
	plan        compiler.AlertPlan
	fingerprint string
	at          time.Time
}

func NewModel(now time.Time) *Model {
	return &Model{now: now, nextID: 70, rules: map[int]compiler.RulePlan{}, deleted: map[int]bool{}}
}

func (m *Model) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *Model) Sleep(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.now = m.now.Add(delay)
	m.mu.Unlock()
	return nil
}

func (m *Model) ValidateRule(_ context.Context, rule compiler.RulePlan) error {
	if rule.Name == "" || rule.Pattern == "" || len(rule.MatchCriteria) == 0 {
		return fmt.Errorf("invalid rule")
	}
	return nil
}

func (m *Model) CreateRule(_ context.Context, rule compiler.RulePlan) (oscar.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	m.rules[m.nextID] = rule
	return oscar.Rule{ID: m.nextID, Name: rule.Name, Pattern: rule.Pattern, Description: rule.Description}, nil
}

func (m *Model) GetRule(_ context.Context, id int) (oscar.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.rules[id]
	if !ok || m.deleted[id] {
		return oscar.Rule{}, &oscar.MachineError{Operation: "get rule", StatusCode: 404, Detail: "not found"}
	}
	return oscar.Rule{ID: id, Name: rule.Name, Pattern: rule.Pattern, Description: rule.Description}, nil
}

func (m *Model) FindRules(_ context.Context, name string) ([]oscar.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []oscar.Rule
	for id, rule := range m.rules {
		if !m.deleted[id] && rule.Name == name {
			result = append(result, oscar.Rule{ID: id, Name: rule.Name, Pattern: rule.Pattern, Description: rule.Description})
		}
	}
	return result, nil
}

func (m *Model) DeleteRule(_ context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rules[id]; !ok {
		return nil
	}
	m.deleted[id] = true
	return nil
}

func (m *Model) Inject(_ context.Context, alert compiler.AlertPlan) (oscar.InjectionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fingerprint := labelFingerprint(alert.Labels)
	m.alerts = append(m.alerts, modelAlert{plan: cloneAlert(alert), fingerprint: fingerprint, at: m.now})
	return oscar.InjectionResult{Class: oscar.InjectionAccepted, StatusCode: 200}, nil
}

func (m *Model) ResolveHistory(_ context.Context, record oscar.HistoryRecord) (oscar.InjectionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if record.Fingerprint == "" || record.Labels["oscar_test_run_id"] == "" {
		return oscar.InjectionResult{}, fmt.Errorf("unsafe cleanup identity")
	}
	return oscar.InjectionResult{Class: oscar.InjectionAccepted, StatusCode: 200}, nil
}

func (m *Model) FindHistory(_ context.Context, query oscar.HistoryQuery) ([]oscar.HistoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []oscar.HistoryRecord
	for index, item := range m.alerts {
		if item.plan.Name == query.AlertName {
			result = append(result, oscar.HistoryRecord{ID: fmt.Sprintf("history-%d", index+1), AlertName: item.plan.Name, Fingerprint: item.fingerprint,
				Status: item.plan.Status, CreatedAt: item.at, Labels: cloneMap(item.plan.Labels), Annotations: cloneMap(item.plan.Annotations)})
		}
	}
	if len(result) != 0 {
		return result, nil
	}
	for id, rule := range m.rules {
		if rule.EmitAlertName != query.AlertName || rule.EmitAlertName == "" {
			continue
		}
		trigger := m.triggerIndex(rule)
		if trigger < 0 {
			continue
		}
		labels := cloneMap(rule.EmitLabels)
		labels["alertname"] = rule.EmitAlertName
		result = append(result, oscar.HistoryRecord{ID: fmt.Sprintf("parent-%d", id), AlertName: rule.EmitAlertName,
			Fingerprint: labelFingerprint(labels), Status: "firing", CreatedAt: m.now, Labels: labels, Annotations: map[string]string{"summary": "semantic model parent"}})
	}
	return result, nil
}

func (m *Model) CorrelationAudit(_ context.Context, fingerprint string) ([]oscar.AuditRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.alertIndex(fingerprint)
	if index < 0 {
		return nil, nil
	}
	event := m.alerts[index]
	ruleName := event.plan.Labels["oscar_test_rule_name"]
	id, rule, ok := m.ruleByName(ruleName)
	if !ok {
		return nil, nil
	}
	outcome := "no_trigger"
	parentFingerprint := ""
	if rule.Pattern == "parent_child" {
		if m.matchesAny(event, childMatches(rule)) {
			if m.hasPriorParent(rule, index) {
				outcome = "suppressed_per_notifier"
				parentFingerprint = "semantic-parent-" + fmt.Sprint(id)
			} else {
				outcome = "released_no_trigger"
			}
		}
	} else if m.triggerIndex(rule) == index {
		outcome = "parent_emitted"
		labels := cloneMap(rule.EmitLabels)
		labels["alertname"] = rule.EmitAlertName
		parentFingerprint = labelFingerprint(labels)
	}
	return []oscar.AuditRecord{{ID: id*1000 + index + 1, CreatedAt: m.now, AlertFingerprint: fingerprint, RuleID: id,
		RuleName: rule.Name, Pattern: rule.Pattern, Outcome: outcome, ParentFingerprint: parentFingerprint}}, nil
}

func (m *Model) NotificationAudit(_ context.Context, fingerprint string, _, _ time.Time) ([]oscar.NotificationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.alertIndex(fingerprint)
	if index < 0 {
		return nil, nil
	}
	event := m.alerts[index]
	_, rule, ok := m.ruleByName(event.plan.Labels["oscar_test_rule_name"])
	if !ok || rule.Pattern != "parent_child" || !m.matchesAny(event, childMatches(rule)) || !m.hasPriorParent(rule, index) {
		return nil, nil
	}
	values, _ := rule.MatchCriteria["suppress_children_for_notifiers"].([]string)
	if len(values) == 0 {
		if raw, ok := rule.MatchCriteria["suppress_children_for_notifiers"].([]any); ok {
			for _, value := range raw {
				values = append(values, fmt.Sprint(value))
			}
		}
	}
	result := make([]oscar.NotificationRecord, 0, len(values))
	for index, notifier := range values {
		result = append(result, oscar.NotificationRecord{ID: fmt.Sprintf("notification-%d", index+1), AlertFingerprint: fingerprint,
			NotifierType: notifier, Status: "suppressed", CreatedAt: m.now, Labels: cloneMap(event.plan.Labels)})
	}
	return result, nil
}

func (m *Model) triggerIndex(rule compiler.RulePlan) int {
	events := m.ruleEvents(rule.Name)
	if len(events) == 0 {
		return -1
	}
	switch rule.Pattern {
	case "flood":
		match := stringMap(rule.MatchCriteria["match"])
		minimum := asInt(rule.MatchCriteria["min_count"])
		seen := map[string]bool{}
		for _, index := range events {
			if m.matches(m.alerts[index], match) && m.alerts[index].plan.Status != "resolved" {
				seen[m.alerts[index].fingerprint] = true
				if len(seen) >= minimum {
					return index
				}
			}
		}
	case "co_occurrence":
		required := matchList(rule.MatchCriteria["required_matches"])
		seen := map[int]bool{}
		for _, index := range events {
			for requiredIndex, match := range required {
				if m.matches(m.alerts[index], match) {
					seen[requiredIndex] = true
				}
			}
			if len(seen) >= asInt(rule.MatchCriteria["min_matches"]) {
				return index
			}
		}
	case "sequence":
		sequence := matchList(rule.MatchCriteria["sequence"])
		position := 0
		for _, index := range events {
			if position < len(sequence) && m.matches(m.alerts[index], sequence[position]) {
				position++
				if position == len(sequence) {
					return index
				}
			}
		}
	case "cross_source":
		required, _ := rule.MatchCriteria["required_sources"].([]any)
		seen := map[string]bool{}
		for _, index := range events {
			for _, raw := range required {
				item, _ := raw.(map[string]any)
				source := fmt.Sprint(item["source"])
				if m.alerts[index].plan.Labels["oscar_source"] == source && m.matches(m.alerts[index], stringMap(item["match"])) {
					seen[source] = true
				}
			}
			if len(seen) == len(required) {
				return index
			}
		}
	case "threshold":
		match, distinct := stringMap(rule.MatchCriteria["match"]), fmt.Sprint(rule.MatchCriteria["distinct_label"])
		seen := map[string]bool{}
		for _, index := range events {
			if m.matches(m.alerts[index], match) {
				seen[m.alerts[index].plan.Labels[distinct]] = true
				if len(seen) >= asInt(rule.MatchCriteria["min_distinct_count"]) {
					return index
				}
			}
		}
	case "persistence":
		match, seconds := stringMap(rule.MatchCriteria["match"]), asInt(rule.MatchCriteria["unresolved_for_seconds"])
		for _, index := range events {
			item := m.alerts[index]
			if item.plan.Status == "resolved" || !m.matches(item, match) {
				continue
			}
			resolved := false
			for _, later := range events {
				if later > index && m.alerts[later].fingerprint == item.fingerprint && m.alerts[later].plan.Status == "resolved" {
					resolved = true
				}
			}
			if !resolved && !m.now.Before(item.at.Add(time.Duration(seconds)*time.Second)) {
				return index
			}
		}
	case "absence":
		match, seconds := stringMap(rule.MatchCriteria["expected_match"]), asInt(rule.MatchCriteria["absent_for_seconds"])
		last, lastIndex := time.Time{}, -1
		for _, index := range events {
			if m.alerts[index].plan.Status != "resolved" && m.matches(m.alerts[index], match) && m.alerts[index].at.After(last) {
				last, lastIndex = m.alerts[index].at, index
			}
		}
		if lastIndex >= 0 && !m.now.Before(last.Add(time.Duration(seconds)*time.Second)) {
			return lastIndex
		}
	}
	return -1
}

func (m *Model) ruleEvents(name string) []int {
	var result []int
	for index, item := range m.alerts {
		if item.plan.Labels["oscar_test_rule_name"] == name {
			result = append(result, index)
		}
	}
	return result
}

func (m *Model) alertIndex(fingerprint string) int {
	for index := len(m.alerts) - 1; index >= 0; index-- {
		if m.alerts[index].fingerprint == fingerprint {
			return index
		}
	}
	return -1
}

func (m *Model) ruleByName(name string) (int, compiler.RulePlan, bool) {
	for id, rule := range m.rules {
		if rule.Name == name {
			return id, rule, true
		}
	}
	return 0, compiler.RulePlan{}, false
}

func (m *Model) hasPriorParent(rule compiler.RulePlan, childIndex int) bool {
	parent := stringMap(rule.MatchCriteria["parent_match"])
	for _, index := range m.ruleEvents(rule.Name) {
		if index < childIndex && m.matches(m.alerts[index], parent) && m.alerts[index].plan.Status != "resolved" {
			return true
		}
	}
	return false
}

func childMatches(rule compiler.RulePlan) []map[string]string {
	return matchList(rule.MatchCriteria["child_matches"])
}
func (m *Model) matchesAny(event modelAlert, matches []map[string]string) bool {
	for _, match := range matches {
		if m.matches(event, match) {
			return true
		}
	}
	return false
}
func (m *Model) matches(event modelAlert, match map[string]string) bool {
	for key, value := range match {
		if event.plan.Labels[key] != value {
			return false
		}
	}
	return true
}

func labelFingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key == "oscar_fingerprint" || key == "am_fingerprint" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		_, _ = io.WriteString(hash, key)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, labels[key])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:12]
}

func cloneAlert(input compiler.AlertPlan) compiler.AlertPlan {
	input.Labels, input.Annotations = cloneMap(input.Labels), cloneMap(input.Annotations)
	return input
}
func cloneMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}
func stringMap(value any) map[string]string {
	if result, ok := value.(map[string]string); ok {
		return result
	}
	result := map[string]string{}
	if raw, ok := value.(map[string]any); ok {
		for key, item := range raw {
			result[key] = fmt.Sprint(item)
		}
	}
	return result
}
func matchList(value any) []map[string]string {
	raw, _ := value.([]any)
	result := make([]map[string]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, stringMap(item))
	}
	return result
}
