// Package compiler turns declarative scenarios into immutable executable plans.
package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

var roleCleaner = regexp.MustCompile(`[^A-Z0-9]+`)
var patternCodes = map[string]string{"co_occurrence": "COOCCURRENCE", "flood": "FLOOD", "sequence": "SEQUENCE", "persistence": "PERSISTENCE", "absence": "ABSENCE", "parent_child": "PARENTCHILD", "cross_source": "CROSSSOURCE", "threshold": "THRESHOLD"}

type Capabilities struct {
	PipelineMode string `json:"pipelineMode"`
}
type MutationBudget struct{ Rules, Alerts int }
type Plan struct {
	APIVersion     string         `json:"apiVersion"`
	Scenario       string         `json:"scenario"`
	Suite          string         `json:"suite"`
	Pattern        string         `json:"pattern"`
	RunID          string         `json:"runId"`
	ShortToken     string         `json:"shortToken"`
	MaxDuration    time.Duration  `json:"maxDuration"`
	MutationBudget MutationBudget `json:"mutationBudget"`
	Cases          []CasePlan     `json:"cases"`
	Digest         string         `json:"digest"`
}
type CasePlan struct {
	Name       string               `json:"name"`
	Code       string               `json:"code"`
	Polarity   string               `json:"polarity"`
	Rule       RulePlan             `json:"rule"`
	Alerts     []AlertPlan          `json:"alerts"`
	Assertions []scenario.Assertion `json:"assertions"`
	Inspection Inspection           `json:"inspection"`
}
type RulePlan struct {
	Name          string            `json:"name"`
	Pattern       string            `json:"pattern"`
	WindowSeconds int               `json:"windowSeconds"`
	GroupBy       []string          `json:"groupByLabels"`
	MatchCriteria map[string]any    `json:"matchCriteria"`
	EmitAlertName string            `json:"emitAlertName"`
	EmitLabels    map[string]string `json:"emitLabels"`
	Description   string            `json:"description"`
}
type AlertPlan struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Delay       time.Duration     `json:"delay"`
}
type Inspection struct {
	ExactRun        string            `json:"exactRun"`
	AlertNamePrefix string            `json:"alertNamePrefix"`
	AlertNames      []string          `json:"alertNames"`
	RuleName        string            `json:"ruleName"`
	Filters         map[string]string `json:"filters"`
}

var reservedLabels = map[string]struct{}{"alertname": {}, "category": {}, "oscar_test": {}, "oscar_test_harness": {}, "oscar_test_schema_version": {}, "oscar_test_run_id": {}, "oscar_test_run_short": {}, "oscar_test_suite": {}, "oscar_test_scenario": {}, "oscar_test_pattern": {}, "oscar_test_case": {}, "oscar_test_case_code": {}, "oscar_test_polarity": {}, "oscar_test_alert_class": {}, "oscar_test_alert_role": {}, "oscar_test_rule_name": {}}

func Compile(run domain.Run, input scenario.Scenario, capabilities Capabilities) (Plan, error) {
	if run.ID == "" || len(run.ShortToken) != 8 {
		return Plan{}, fmt.Errorf("run identity is invalid")
	}
	if capabilities.PipelineMode != "phase_a_audit_only" && capabilities.PipelineMode != "phase_b_dispatch" {
		return Plan{}, fmt.Errorf("pipeline mode %q cannot support correlation execution", capabilities.PipelineMode)
	}
	patternCode, supported := patternCodes[input.Pattern]
	if input.APIVersion != "corrtest.oscar/v1alpha1" || input.Kind != "CorrelationScenario" || !supported || len(input.Cases) != 2 {
		return Plan{}, fmt.Errorf("unsupported or incomplete scenario")
	}
	if input.MaxDuration <= 0 || input.MaxDuration > 5*time.Minute {
		return Plan{}, fmt.Errorf("scenario maximum duration is outside the safe budget")
	}
	plan := Plan{APIVersion: input.APIVersion, Scenario: input.Name, Suite: input.Suite, Pattern: input.Pattern, RunID: run.ID, ShortToken: strings.ToUpper(run.ShortToken), MaxDuration: input.MaxDuration}
	for _, source := range input.Cases {
		events := source.Events
		if len(events) == 0 {
			for range source.Repeat {
				events = append(events, scenario.Event{Role: source.Role, Status: "firing"})
			}
		}
		if len(events) < 1 || len(events) > 100 || source.Window <= 0 || source.Window > 2*time.Minute {
			return Plan{}, fmt.Errorf("case %q exceeds the mutation or timing budget", source.Name)
		}
		for key := range source.Labels {
			if _, reserved := reservedLabels[key]; reserved {
				return Plan{}, fmt.Errorf("case %q overrides reserved label %q", source.Name, key)
			}
		}
		caseCode, short := strings.ToUpper(source.Code), strings.ToUpper(run.ShortToken)
		ruleName := fmt.Sprintf("corrtest-%s-%s-%s", input.Pattern, strings.ToLower(caseCode), strings.ToLower(short))
		names := map[string]string{}
		for _, event := range events {
			if _, exists := names[event.Role]; !exists {
				names[event.Role] = physicalName(patternCode, caseCode, event.Role, short)
			}
		}
		syntheticName := physicalName(patternCode, caseCode, "synthetic", short)
		if input.Pattern == "parent_child" {
			syntheticName = ""
		}
		item := CasePlan{Name: source.Name, Code: caseCode, Polarity: source.Polarity, Assertions: source.Assertions}
		item.Rule = RulePlan{Name: ruleName, Pattern: input.Pattern, WindowSeconds: int(source.Window / time.Second), GroupBy: source.GroupBy, MatchCriteria: matchCriteria(input.Pattern, names), EmitAlertName: syntheticName, EmitLabels: identityLabels(run, input, source, ruleName, "synthetic", "synthetic_parent"), Description: fmt.Sprintf("Temporary OSCAR correlation test rule; run=%s scenario=%s case=%s", run.ID, input.Name, source.Name)}
		for index, event := range events {
			labels := identityLabels(run, input, source, ruleName, "source", event.Role)
			for key, value := range source.Labels {
				labels[key] = value
			}
			for key, value := range event.Labels {
				if _, reserved := reservedLabels[key]; reserved {
					return Plan{}, fmt.Errorf("case %q event overrides reserved label %q", source.Name, key)
				}
				labels[key] = value
			}
			name := names[event.Role]
			item.Alerts = append(item.Alerts, AlertPlan{Name: name, Status: event.Status, Labels: labels, Delay: event.Delay, Annotations: map[string]string{"oscar_test_event_id": fmt.Sprintf("%s-%s-%03d", run.ID, caseCode, index+1), "oscar_test_event_index": fmt.Sprintf("%d", index+1), "summary": fmt.Sprintf("[CORRTEST][%s][%s][%s] source alert %d of %d", patternCode, caseCode, short, index+1, len(events))}})
		}
		alertNames := uniqueNames(names)
		if syntheticName != "" {
			alertNames = append(alertNames, syntheticName)
		}
		item.Inspection = Inspection{ExactRun: run.ID, AlertNamePrefix: "CORRTEST_" + patternCode + "_" + caseCode, AlertNames: alertNames, RuleName: ruleName, Filters: map[string]string{"exactRun": "oscar_test_run_id = " + run.ID, "pattern": "category = corrtest_" + input.Pattern, "case": "oscar_test_run_id = " + run.ID + " AND oscar_test_case_code = " + caseCode, "nameFallback": "alertname contains CORRTEST_" + patternCode + "_" + caseCode}}
		plan.Cases = append(plan.Cases, item)
		plan.MutationBudget.Rules++
		plan.MutationBudget.Alerts += len(events)
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize compiled plan: %w", err)
	}
	digest := sha256.Sum256(canonical)
	plan.Digest = hex.EncodeToString(digest[:])
	return plan, nil
}

func physicalName(patternCode, caseCode, role, short string) string {
	role = strings.Trim(roleCleaner.ReplaceAllString(strings.ToUpper(role), ""), "_")
	if role == "" {
		role = "SOURCE"
	}
	if len(role) > 24 {
		role = role[:24]
	}
	return fmt.Sprintf("CORRTEST_%s_%s_%s_%s", patternCode, caseCode, role, short)
}
func matchCriteria(pattern string, names map[string]string) map[string]any {
	first := func() string {
		values := uniqueNames(names)
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	switch pattern {
	case "flood":
		return map[string]any{"alertname": first(), "min_count": 5}
	case "co_occurrence":
		return map[string]any{"alertnames": uniqueNames(names)}
	case "sequence":
		return map[string]any{"sequence": []any{map[string]string{"alertname": names["login_failure"]}, map[string]string{"alertname": names["privileged_command"]}}}
	case "cross_source":
		return map[string]any{"alertnames": uniqueNames(names), "sources": []string{"snmp", "api"}}
	case "threshold":
		return map[string]any{"alertname": first(), "distinct_label": "device", "min_distinct": 3}
	case "persistence":
		return map[string]any{"alertname": first(), "unresolved_for_seconds": 30}
	case "absence":
		return map[string]any{"alertname": first(), "absent_for_seconds": 30}
	case "parent_child":
		return map[string]any{"parent": map[string]string{"alertname": names["parent"]}, "child": map[string]string{"alertname": names["child"]}, "suppress_for_notifiers": []string{"email"}}
	default:
		return nil
	}
}
func uniqueNames(names map[string]string) []string {
	result := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, value := range names {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func identityLabels(run domain.Run, input scenario.Scenario, item scenario.Case, ruleName, class, role string) map[string]string {
	return map[string]string{"category": "corrtest_" + input.Pattern, "oscar_test": "true", "oscar_test_harness": "corrtest", "oscar_test_schema_version": "v1", "oscar_test_run_id": run.ID, "oscar_test_run_short": strings.ToUpper(run.ShortToken), "oscar_test_suite": input.Suite, "oscar_test_scenario": input.Name, "oscar_test_pattern": input.Pattern, "oscar_test_case": item.Name, "oscar_test_case_code": strings.ToUpper(item.Code), "oscar_test_polarity": item.Polarity, "oscar_test_alert_class": class, "oscar_test_alert_role": role, "oscar_test_rule_name": ruleName}
}
