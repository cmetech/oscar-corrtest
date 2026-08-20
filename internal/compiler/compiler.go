// Package compiler turns declarative scenarios into immutable executable plans.
package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/scenario"
)

var roleCleaner = regexp.MustCompile(`[^A-Z0-9]+`)

// Capabilities contains only compile-time target facts.
type Capabilities struct {
	PipelineMode string `json:"pipelineMode"`
}

type MutationBudget struct {
	Rules  int `json:"rules"`
	Alerts int `json:"alerts"`
}

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

var reservedLabels = map[string]struct{}{
	"alertname": {}, "category": {}, "oscar_test": {}, "oscar_test_harness": {}, "oscar_test_schema_version": {},
	"oscar_test_run_id": {}, "oscar_test_run_short": {}, "oscar_test_suite": {}, "oscar_test_scenario": {},
	"oscar_test_pattern": {}, "oscar_test_case": {}, "oscar_test_case_code": {}, "oscar_test_polarity": {},
	"oscar_test_alert_class": {}, "oscar_test_alert_role": {}, "oscar_test_rule_name": {},
}

// Compile validates safety boundaries and returns a canonical plan.
func Compile(run domain.Run, input scenario.Scenario, capabilities Capabilities) (Plan, error) {
	if run.ID == "" || len(run.ShortToken) != 8 {
		return Plan{}, fmt.Errorf("run identity is invalid")
	}
	if capabilities.PipelineMode != "phase_a_audit_only" && capabilities.PipelineMode != "phase_b_dispatch" {
		return Plan{}, fmt.Errorf("pipeline mode %q cannot support correlation execution", capabilities.PipelineMode)
	}
	if input.APIVersion != "corrtest.oscar/v1alpha1" || input.Kind != "CorrelationScenario" || input.Pattern != "flood" || len(input.Cases) != 2 {
		return Plan{}, fmt.Errorf("unsupported or incomplete scenario")
	}
	if input.MaxDuration <= 0 || input.MaxDuration > 5*time.Minute {
		return Plan{}, fmt.Errorf("scenario maximum duration is outside the safe budget")
	}
	plan := Plan{APIVersion: input.APIVersion, Scenario: input.Name, Suite: input.Suite, Pattern: input.Pattern,
		RunID: run.ID, ShortToken: strings.ToUpper(run.ShortToken), MaxDuration: input.MaxDuration}
	for _, source := range input.Cases {
		if source.Repeat < 1 || source.Repeat > 100 || source.Window <= 0 || source.Window > 2*time.Minute {
			return Plan{}, fmt.Errorf("case %q exceeds the mutation or timing budget", source.Name)
		}
		for key := range source.Labels {
			if _, reserved := reservedLabels[key]; reserved {
				return Plan{}, fmt.Errorf("case %q overrides reserved label %q", source.Name, key)
			}
		}
		caseCode := strings.ToUpper(source.Code)
		shortLower := strings.ToLower(run.ShortToken)
		ruleName := fmt.Sprintf("corrtest-%s-%s-%s", input.Pattern, strings.ToLower(caseCode), shortLower)
		roleUpper := strings.Trim(roleCleaner.ReplaceAllString(strings.ToUpper(source.Role), ""), "_")
		alertName := fmt.Sprintf("CORRTEST_FLOOD_%s_%s_%s", caseCode, roleUpper, strings.ToUpper(run.ShortToken))
		syntheticName := fmt.Sprintf("CORRTEST_FLOOD_%s_SYNTHETIC_%s", caseCode, strings.ToUpper(run.ShortToken))
		labels := identityLabels(run, input, source, ruleName, "source", source.Role)
		for key, value := range source.Labels {
			labels[key] = value
		}
		emitLabels := identityLabels(run, input, source, ruleName, "synthetic", "synthetic_parent")
		casePlan := CasePlan{Name: source.Name, Code: caseCode, Polarity: source.Polarity, Assertions: source.Assertions}
		casePlan.Rule = RulePlan{Name: ruleName, Pattern: input.Pattern, WindowSeconds: int(source.Window / time.Second), GroupBy: source.GroupBy,
			MatchCriteria: map[string]any{"alertname": alertName, "min_count": 5}, EmitAlertName: syntheticName,
			EmitLabels: emitLabels, Description: fmt.Sprintf("Temporary OSCAR correlation test rule; run=%s scenario=%s case=%s", run.ID, input.Name, source.Name)}
		for index := 1; index <= source.Repeat; index++ {
			copyLabels := clone(labels)
			casePlan.Alerts = append(casePlan.Alerts, AlertPlan{Name: alertName, Status: "firing", Labels: copyLabels,
				Annotations: map[string]string{"oscar_test_event_id": fmt.Sprintf("%s-%s-%03d", run.ID, caseCode, index),
					"oscar_test_event_index": fmt.Sprintf("%d", index), "summary": fmt.Sprintf("[CORRTEST][FLOOD][%s][%s] source alert %d of %d", caseCode, run.ShortToken, index, source.Repeat)}})
		}
		casePlan.Inspection = Inspection{ExactRun: run.ID, AlertNamePrefix: "CORRTEST_FLOOD_" + caseCode,
			AlertNames: []string{alertName, syntheticName}, RuleName: ruleName, Filters: map[string]string{
				"exactRun": "oscar_test_run_id = " + run.ID, "pattern": "category = corrtest_flood",
				"case":         "oscar_test_run_id = " + run.ID + " AND oscar_test_case_code = " + caseCode,
				"nameFallback": "alertname contains CORRTEST_FLOOD_" + caseCode}}
		plan.Cases = append(plan.Cases, casePlan)
		plan.MutationBudget.Rules++
		plan.MutationBudget.Alerts += source.Repeat
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize compiled plan: %w", err)
	}
	digest := sha256.Sum256(canonical)
	plan.Digest = hex.EncodeToString(digest[:])
	return plan, nil
}

func identityLabels(run domain.Run, input scenario.Scenario, item scenario.Case, ruleName, class, role string) map[string]string {
	return map[string]string{
		"category": "corrtest_" + input.Pattern, "oscar_test": "true", "oscar_test_harness": "corrtest", "oscar_test_schema_version": "v1",
		"oscar_test_run_id": run.ID, "oscar_test_run_short": strings.ToUpper(run.ShortToken), "oscar_test_suite": input.Suite,
		"oscar_test_scenario": input.Name, "oscar_test_pattern": input.Pattern, "oscar_test_case": item.Name,
		"oscar_test_case_code": strings.ToUpper(item.Code), "oscar_test_polarity": item.Polarity, "oscar_test_alert_class": class,
		"oscar_test_alert_role": role, "oscar_test_rule_name": ruleName,
	}
}

func clone(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
