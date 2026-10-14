package scenario

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Encode returns stable, strict public-v1 YAML for a validated scenario.
func Encode(document Scenario) ([]byte, error) {
	if err := Validate(document); err != nil {
		return nil, err
	}
	wire := wireScenario{
		APIVersion:  document.APIVersion,
		Kind:        document.Kind,
		Name:        document.Name,
		Suite:       document.Suite,
		Pattern:     document.Pattern,
		InputPath:   document.InputPath,
		MaxDuration: document.MaxDuration.String(),
	}
	for _, item := range document.Cases {
		converted := wireCase{
			Name: item.Name, Code: item.Code, Polarity: item.Polarity,
			Window:  item.Window.String(),
			GroupBy: append([]string(nil), item.GroupBy...), Labels: cloneStringMap(item.Labels),
			SuppressForNotifiers: append([]string(nil), item.SuppressForNotifiers...),
			TagForNotifiers:      append([]string(nil), item.TagForNotifiers...),
		}
		if item.Role != "" {
			role := item.Role
			converted.Role = &role
		}
		if item.Repeat != 0 {
			repeat := item.Repeat
			converted.Repeat = &repeat
		}
		for _, assertion := range item.Assertions {
			encoded := wireAssertion{Kind: assertion.Kind, Equals: assertion.Equals}
			if assertion.Outcome != "" {
				outcome := assertion.Outcome
				encoded.Outcome = &outcome
			}
			converted.Assertions = append(converted.Assertions, encoded)
		}
		for _, event := range item.Events {
			delay := ""
			if event.Delay != 0 {
				delay = event.Delay.String()
			}
			var source *string
			if event.Source != "" {
				value := event.Source
				source = &value
			}
			converted.Events = append(converted.Events, wireEvent{Role: event.Role, Source: source, Status: event.Status, Labels: cloneStringMap(event.Labels), Delay: delay})
		}
		wire.Cases = append(wire.Cases, converted)
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(wire); err != nil {
		return nil, fmt.Errorf("encode scenario: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("finish scenario encoding: %w", err)
	}
	return output.Bytes(), nil
}

// BuiltinSource returns canonical YAML for one immutable built-in.
func BuiltinSource(pattern string) ([]byte, error) {
	document := Builtin(pattern)
	if len(document.Cases) != 2 {
		return nil, fmt.Errorf("unknown built-in pattern %q", pattern)
	}
	return Encode(document)
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
