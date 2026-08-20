package scenario

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

const maxDocumentBytes = 1 << 20

type wireScenario struct {
	APIVersion  string     `yaml:"apiVersion"`
	Kind        string     `yaml:"kind"`
	Name        string     `yaml:"name"`
	Suite       string     `yaml:"suite"`
	Pattern     string     `yaml:"pattern"`
	MaxDuration string     `yaml:"maxDuration"`
	Cases       []wireCase `yaml:"cases"`
}

type wireCase struct {
	Name       string            `yaml:"name"`
	Code       string            `yaml:"code"`
	Polarity   string            `yaml:"polarity"`
	Role       string            `yaml:"role"`
	Repeat     int               `yaml:"repeat"`
	Window     string            `yaml:"window"`
	GroupBy    []string          `yaml:"groupBy"`
	Labels     map[string]string `yaml:"labels"`
	Assertions []Assertion       `yaml:"assertions"`
	Events     []wireEvent       `yaml:"events"`
}

type wireEvent struct {
	Role   string            `yaml:"role"`
	Status string            `yaml:"status"`
	Labels map[string]string `yaml:"labels"`
	Delay  string            `yaml:"delay"`
}

// Decode accepts exactly one strict YAML or JSON document with no aliases.
func Decode(reader io.Reader) (Scenario, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxDocumentBytes+1))
	if err != nil {
		return Scenario{}, fmt.Errorf("read scenario: %w", err)
	}
	if len(raw) > maxDocumentBytes {
		return Scenario{}, fmt.Errorf("scenario exceeds 1 MiB")
	}
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&node); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario syntax: %w", err)
	}
	if len(node.Content) == 0 {
		return Scenario{}, fmt.Errorf("scenario document is empty")
	}
	if containsAlias(&node) {
		return Scenario{}, fmt.Errorf("YAML aliases are not permitted")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil && len(extra.Content) > 0 {
			return Scenario{}, fmt.Errorf("exactly one scenario document is required")
		}
		return Scenario{}, fmt.Errorf("decode trailing scenario document: %w", err)
	}
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	var wire wireScenario
	if err := strict.Decode(&wire); err != nil {
		return Scenario{}, fmt.Errorf("decode strict scenario: %w", err)
	}
	maxDuration, err := time.ParseDuration(wire.MaxDuration)
	if err != nil {
		return Scenario{}, fmt.Errorf("maxDuration: %w", err)
	}
	result := Scenario{APIVersion: wire.APIVersion, Kind: wire.Kind, Name: wire.Name, Suite: wire.Suite, Pattern: wire.Pattern, MaxDuration: maxDuration}
	for _, item := range wire.Cases {
		window, parseErr := time.ParseDuration(item.Window)
		if parseErr != nil {
			return Scenario{}, fmt.Errorf("case %q window: %w", item.Name, parseErr)
		}
		converted := Case{Name: item.Name, Code: item.Code, Polarity: item.Polarity, Role: item.Role, Repeat: item.Repeat, Window: window, GroupBy: item.GroupBy, Labels: item.Labels, Assertions: item.Assertions}
		for _, event := range item.Events {
			var delay time.Duration
			if event.Delay != "" {
				delay, parseErr = time.ParseDuration(event.Delay)
				if parseErr != nil {
					return Scenario{}, fmt.Errorf("case %q event delay: %w", item.Name, parseErr)
				}
			}
			converted.Events = append(converted.Events, Event{Role: event.Role, Status: event.Status, Labels: event.Labels, Delay: delay})
		}
		result.Cases = append(result.Cases, converted)
	}
	return result, nil
}

func containsAlias(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return true
	}
	for _, child := range node.Content {
		if containsAlias(child) {
			return true
		}
	}
	return false
}
