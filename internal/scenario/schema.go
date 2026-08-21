package scenario

import (
	"encoding/json"
	"fmt"
)

type jsonSchema struct {
	Schema               string                 `json:"$schema"`
	ID                   string                 `json:"$id"`
	Title                string                 `json:"title"`
	Type                 string                 `json:"type"`
	AdditionalProperties *bool                  `json:"additionalProperties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Properties           map[string]schemaValue `json:"properties,omitempty"`
	Defs                 map[string]schemaValue `json:"$defs,omitempty"`
	AllOf                []schemaValue          `json:"allOf,omitempty"`
}

type schemaValue struct {
	Ref                  string                 `json:"$ref,omitempty"`
	Type                 string                 `json:"type,omitempty"`
	Const                any                    `json:"const,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
	Title                string                 `json:"title,omitempty"`
	Description          string                 `json:"description,omitempty"`
	MinLength            *int                   `json:"minLength,omitempty"`
	MaxLength            *int                   `json:"maxLength,omitempty"`
	Minimum              *int                   `json:"minimum,omitempty"`
	Maximum              *int                   `json:"maximum,omitempty"`
	MinItems             *int                   `json:"minItems,omitempty"`
	MaxItems             *int                   `json:"maxItems,omitempty"`
	MinProperties        *int                   `json:"minProperties,omitempty"`
	MaxProperties        *int                   `json:"maxProperties,omitempty"`
	UniqueItems          bool                   `json:"uniqueItems,omitempty"`
	Pattern              string                 `json:"pattern,omitempty"`
	Format               string                 `json:"x-corrtest-format,omitempty"`
	MinimumDescription   string                 `json:"x-corrtest-minimum,omitempty"`
	MaximumDescription   string                 `json:"x-corrtest-maximum,omitempty"`
	AdditionalProperties any                    `json:"additionalProperties,omitempty"`
	PropertyNames        *schemaValue           `json:"propertyNames,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Properties           map[string]schemaValue `json:"properties,omitempty"`
	Items                *schemaValue           `json:"items,omitempty"`
	Contains             *schemaValue           `json:"contains,omitempty"`
	MinContains          *int                   `json:"minContains,omitempty"`
	MaxContains          *int                   `json:"maxContains,omitempty"`
	AllOf                []schemaValue          `json:"allOf,omitempty"`
	AnyOf                []schemaValue          `json:"anyOf,omitempty"`
	OneOf                []schemaValue          `json:"oneOf,omitempty"`
	Not                  *schemaValue           `json:"not,omitempty"`
	If                   *schemaValue           `json:"if,omitempty"`
	Then                 *schemaValue           `json:"then,omitempty"`
}

func integer(value int) *int { return &value }
func closed() *bool          { value := false; return &value }

// GenerateJSONSchema deterministically produces the distributable scenario JSON Schema.
func GenerateJSONSchema() ([]byte, error) {
	schema := buildSchema(PublicContract())
	raw, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal scenario schema: %w", err)
	}
	return append(raw, '\n'), nil
}

func buildSchema(contract Contract) jsonSchema {
	patterns := make([]string, 0, len(contract.Patterns))
	for _, pattern := range contract.Patterns {
		patterns = append(patterns, pattern.ID)
	}
	duration := func(minimum, maximum string) schemaValue {
		return schemaValue{Type: "string", MinLength: integer(1), Format: "go-duration", MinimumDescription: minimum, MaximumDescription: maximum}
	}
	labelNames := schemaValue{Type: "string", MinLength: integer(1), MaxLength: integer(MaxLabelNameLength), Pattern: "^[A-Za-z_][A-Za-z0-9_]*$"}
	labels := schemaValue{Type: "object", MaxProperties: integer(MaxLabels), PropertyNames: &labelNames, AdditionalProperties: schemaValue{Type: "string", MaxLength: integer(MaxLabelValueLength), Pattern: "^[^\\r\\n\\u0000]*$"}}
	casePositive := schemaValue{AllOf: []schemaValue{
		{Properties: map[string]schemaValue{"code": {Const: "P01"}}, Required: []string{"code"}},
		{Properties: map[string]schemaValue{"polarity": {Const: "positive"}}, Required: []string{"polarity"}},
	}}
	caseNegative := schemaValue{AllOf: []schemaValue{
		{Properties: map[string]schemaValue{"code": {Const: "N01"}}, Required: []string{"code"}},
		{Properties: map[string]schemaValue{"polarity": {Const: "negative"}}, Required: []string{"polarity"}},
	}}
	return jsonSchema{
		Schema:               "https://json-schema.org/draft/2020-12/schema",
		ID:                   "https://github.com/cmetech/oscar-corrtest/docs/schema/correlation-scenario.schema.json",
		Title:                "OSCAR Correlation Scenario",
		Type:                 "object",
		AdditionalProperties: closed(),
		Required:             []string{"apiVersion", "kind", "name", "suite", "pattern", "maxDuration", "cases"},
		Properties: map[string]schemaValue{
			"apiVersion":  {Const: contract.APIVersion},
			"kind":        {Const: contract.Kind},
			"name":        {Type: "string", MinLength: integer(1), MaxLength: integer(MaxScenarioNameLength), Pattern: "^\\S(?:.*\\S)?$"},
			"suite":       {Type: "string", MinLength: integer(1), MaxLength: integer(MaxSuiteLength), Pattern: "^\\S(?:.*\\S)?$"},
			"pattern":     {Enum: patterns},
			"maxDuration": duration("> 0", MaxScenarioDuration.String()),
			"cases": {Type: "array", MinItems: integer(RequiredCaseCount), MaxItems: integer(RequiredCaseCount), Items: &schemaValue{Ref: "#/$defs/case"}, AllOf: []schemaValue{
				{Contains: &casePositive, MinContains: integer(1), MaxContains: integer(1)},
				{Contains: &caseNegative, MinContains: integer(1), MaxContains: integer(1)},
			}},
		},
		Defs: map[string]schemaValue{
			"case": {
				Type: "object", AdditionalProperties: false, Required: []string{"name", "code", "polarity", "window", "assertions"},
				Properties: map[string]schemaValue{
					"name":                 {Type: "string", MinLength: integer(1), MaxLength: integer(MaxCaseNameLength)},
					"code":                 {Enum: []string{"P01", "N01"}},
					"polarity":             {Enum: []string{"positive", "negative"}},
					"role":                 {Type: "string", MinLength: integer(1), MaxLength: integer(MaxRoleLength)},
					"repeat":               {Type: "integer", Minimum: integer(1), Maximum: integer(MaxEvents)},
					"window":               duration("> 0", MaxCaseWindow.String()),
					"groupBy":              {Type: "array", MaxItems: integer(MaxGroupByLabels), UniqueItems: true, Items: &labelNames},
					"labels":               labels,
					"suppressForNotifiers": notifierSchema(),
					"tagForNotifiers":      notifierSchema(),
					"assertions":           {Type: "array", MinItems: integer(1), MaxItems: integer(MaxAssertions), Items: &schemaValue{Ref: "#/$defs/assertion"}},
					"events":               {Type: "array", MinItems: integer(1), MaxItems: integer(MaxEvents), Items: &schemaValue{Ref: "#/$defs/event"}},
				},
				OneOf: []schemaValue{
					{Required: []string{"role", "repeat"}, Not: &schemaValue{Required: []string{"events"}}},
					{Required: []string{"events"}, Not: &schemaValue{AnyOf: []schemaValue{{Required: []string{"role"}}, {Required: []string{"repeat"}}}}},
				},
			},
			"event": {
				Type: "object", AdditionalProperties: false, Required: []string{"role", "status"},
				Properties: map[string]schemaValue{
					"role":   {Type: "string", MinLength: integer(1), MaxLength: integer(MaxRoleLength)},
					"status": {Enum: []string{"firing", "resolved"}},
					"labels": labels,
					"delay":  duration(">= 0", "scenario and observation budgets"),
				},
			},
			"assertion": {
				Type: "object", AdditionalProperties: false, Required: []string{"kind", "equals"},
				Properties: map[string]schemaValue{
					"kind":    {Enum: []string{"synthetic-alert-count", "audit-count", "parent-link-count"}},
					"outcome": {Type: "string", MinLength: integer(1), MaxLength: integer(MaxOutcomeLength), Pattern: "^\\S(?:.*\\S)?$"},
					"equals":  {Type: "integer", Minimum: integer(0), Maximum: integer(MaxExpectedCount)},
				},
				AllOf: []schemaValue{
					{If: &schemaValue{Properties: map[string]schemaValue{"kind": {Const: "synthetic-alert-count"}}, Required: []string{"kind"}}, Then: &schemaValue{Not: &schemaValue{Required: []string{"outcome"}}}},
					{If: &schemaValue{Properties: map[string]schemaValue{"kind": {Enum: []string{"audit-count", "parent-link-count"}}}, Required: []string{"kind"}}, Then: &schemaValue{Required: []string{"outcome"}}},
				},
			},
		},
	}
}

func notifierSchema() schemaValue {
	return schemaValue{Type: "array", MaxItems: integer(MaxNotifierNames), UniqueItems: true, Items: &schemaValue{Type: "string", MinLength: integer(1), MaxLength: integer(MaxNotifierNameLength), Pattern: "^\\S(?:.*\\S)?$"}}
}
