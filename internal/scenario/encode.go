package scenario

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Encode returns stable, strict public-v1 YAML for a validated scenario.
func Encode(document Scenario) ([]byte, error) {
	if err := validate(document); err != nil {
		return nil, err
	}
	wire := wireScenario{
		APIVersion:  document.APIVersion,
		Kind:        document.Kind,
		Name:        document.Name,
		Suite:       document.Suite,
		Pattern:     document.Pattern,
		MaxDuration: document.MaxDuration.String(),
	}
	for _, item := range document.Cases {
		converted := wireCase{
			Name: item.Name, Code: item.Code, Polarity: item.Polarity,
			Role: item.Role, Repeat: item.Repeat, Window: item.Window.String(),
			GroupBy: append([]string(nil), item.GroupBy...), Labels: cloneStringMap(item.Labels),
			SuppressForNotifiers: append([]string(nil), item.SuppressForNotifiers...),
			TagForNotifiers:      append([]string(nil), item.TagForNotifiers...),
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
			converted.Events = append(converted.Events, wireEvent{Role: event.Role, Status: event.Status, Labels: cloneStringMap(event.Labels), Delay: delay})
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
