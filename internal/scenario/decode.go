package scenario

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const maxDocumentBytes = 1 << 20

var labelName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var supportedPatterns = map[string]bool{"flood": true, "co_occurrence": true, "sequence": true, "persistence": true, "absence": true, "parent_child": true, "cross_source": true, "threshold": true}
var protectedLabels = map[string]bool{"alertname": true, "category": true, "oscar_test": true, "oscar_test_harness": true, "oscar_test_schema_version": true, "oscar_test_run_id": true, "oscar_test_run_short": true, "oscar_test_suite": true, "oscar_test_scenario": true, "oscar_test_pattern": true, "oscar_test_case": true, "oscar_test_case_code": true, "oscar_test_polarity": true, "oscar_test_alert_class": true, "oscar_test_alert_role": true, "oscar_test_rule_name": true}

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
	Name                 string            `yaml:"name"`
	Code                 string            `yaml:"code"`
	Polarity             string            `yaml:"polarity"`
	Role                 string            `yaml:"role,omitempty"`
	Repeat               int               `yaml:"repeat,omitempty"`
	Window               string            `yaml:"window"`
	GroupBy              []string          `yaml:"groupBy"`
	Labels               map[string]string `yaml:"labels,omitempty"`
	SuppressForNotifiers []string          `yaml:"suppressForNotifiers,omitempty"`
	TagForNotifiers      []string          `yaml:"tagForNotifiers,omitempty"`
	Assertions           []Assertion       `yaml:"assertions"`
	Events               []wireEvent       `yaml:"events,omitempty"`
}

type wireEvent struct {
	Role   string            `yaml:"role"`
	Status string            `yaml:"status"`
	Labels map[string]string `yaml:"labels,omitempty"`
	Delay  string            `yaml:"delay,omitempty"`
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
		converted := Case{Name: item.Name, Code: item.Code, Polarity: item.Polarity, Role: item.Role, Repeat: item.Repeat, Window: window, GroupBy: item.GroupBy, Labels: item.Labels,
			SuppressForNotifiers: item.SuppressForNotifiers, TagForNotifiers: item.TagForNotifiers, Assertions: item.Assertions}
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
	if err := validate(result); err != nil {
		return Scenario{}, err
	}
	return result, nil
}

func validate(document Scenario) error {
	if document.APIVersion != "corrtest.oscar/v1alpha1" || document.Kind != "CorrelationScenario" || !supportedPatterns[document.Pattern] {
		return fmt.Errorf("scenario apiVersion, kind, or pattern is unsupported")
	}
	if strings.TrimSpace(document.Name) != document.Name || document.Name == "" || len(document.Name) > 100 ||
		strings.TrimSpace(document.Suite) != document.Suite || document.Suite == "" || len(document.Suite) > 100 {
		return fmt.Errorf("scenario name and suite are required and bounded")
	}
	if document.MaxDuration <= 0 || document.MaxDuration > 5*time.Minute || len(document.Cases) != 2 {
		return fmt.Errorf("scenario must contain exactly two cases within a five-minute budget")
	}
	codes := map[string]bool{}
	names := map[string]bool{}
	for _, item := range document.Cases {
		if item.Name == "" || len(item.Name) > 120 || names[item.Name] || (item.Code != "P01" && item.Code != "N01") || codes[item.Code] {
			return fmt.Errorf("scenario case identity is invalid or duplicated")
		}
		if (item.Code == "P01" && item.Polarity != "positive") || (item.Code == "N01" && item.Polarity != "negative") {
			return fmt.Errorf("case %q polarity does not match its code", item.Name)
		}
		names[item.Name], codes[item.Code] = true, true
		if item.Window <= 0 || item.Window > 2*time.Minute || len(item.GroupBy) > 16 || len(item.Labels) > 64 {
			return fmt.Errorf("case %q exceeds timing, grouping, or label budgets", item.Name)
		}
		if err := validateNames(item.GroupBy, 100, "groupBy"); err != nil {
			return fmt.Errorf("case %q: %w", item.Name, err)
		}
		if err := validateLabels(item.Labels); err != nil {
			return fmt.Errorf("case %q: %w", item.Name, err)
		}
		if len(item.Assertions) < 1 || len(item.Assertions) > 32 {
			return fmt.Errorf("case %q must contain bounded assertions", item.Name)
		}
		for _, assertion := range item.Assertions {
			if assertion.Kind != "synthetic-alert-count" && assertion.Kind != "audit-count" && assertion.Kind != "parent-link-count" {
				return fmt.Errorf("case %q assertion kind is unsupported", item.Name)
			}
			if assertion.Equals < 0 || assertion.Equals > 100 || len(assertion.Outcome) > 100 {
				return fmt.Errorf("case %q assertion is outside the safe budget", item.Name)
			}
		}
		if document.Pattern != "parent_child" && (len(item.SuppressForNotifiers) != 0 || len(item.TagForNotifiers) != 0) {
			return fmt.Errorf("case %q supplies notifiers for a non-parent-child pattern", item.Name)
		}
		if len(item.SuppressForNotifiers) > 16 || len(item.TagForNotifiers) > 16 {
			return fmt.Errorf("case %q exceeds the notifier budget", item.Name)
		}
		if err := validateNames(append(append([]string{}, item.SuppressForNotifiers...), item.TagForNotifiers...), 100, "notifier"); err != nil {
			return fmt.Errorf("case %q: %w", item.Name, err)
		}
		events := item.Events
		if len(events) == 0 {
			if item.Repeat < 1 || item.Repeat > 100 || item.Role == "" || len(item.Role) > 100 {
				return fmt.Errorf("case %q repeat stimulus is invalid", item.Name)
			}
			continue
		}
		if item.Repeat != 0 || item.Role != "" || len(events) > 100 {
			return fmt.Errorf("case %q mixes or exceeds event stimulus forms", item.Name)
		}
		for _, event := range events {
			if event.Role == "" || len(event.Role) > 100 || (event.Status != "firing" && event.Status != "resolved") || event.Delay < 0 || event.Delay > document.MaxDuration {
				return fmt.Errorf("case %q event is invalid", item.Name)
			}
			if err := validateLabels(event.Labels); err != nil {
				return fmt.Errorf("case %q event: %w", item.Name, err)
			}
		}
	}
	if !codes["P01"] || !codes["N01"] {
		return fmt.Errorf("scenario requires P01 and N01 cases")
	}
	return nil
}

func validateNames(values []string, maximum int, field string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || len(value) > maximum || seen[value] {
			return fmt.Errorf("%s value is invalid or duplicated", field)
		}
		seen[value] = true
	}
	return nil
}

func validateLabels(labels map[string]string) error {
	for key, value := range labels {
		if protectedLabels[key] {
			return fmt.Errorf("reserved label %q cannot be overridden", key)
		}
		if !labelName.MatchString(key) || len(key) > 100 || len(value) > 500 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("label %q is unsafe", key)
		}
	}
	return nil
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
