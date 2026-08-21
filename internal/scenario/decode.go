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

var labelName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	Role                 *string           `yaml:"role,omitempty"`
	Repeat               *int              `yaml:"repeat,omitempty"`
	Window               string            `yaml:"window"`
	GroupBy              []string          `yaml:"groupBy"`
	Labels               map[string]string `yaml:"labels,omitempty"`
	SuppressForNotifiers []string          `yaml:"suppressForNotifiers,omitempty"`
	TagForNotifiers      []string          `yaml:"tagForNotifiers,omitempty"`
	Assertions           []wireAssertion   `yaml:"assertions"`
	Events               []wireEvent       `yaml:"events,omitempty"`
}

type wireEvent struct {
	Role   string            `yaml:"role"`
	Status string            `yaml:"status"`
	Labels map[string]string `yaml:"labels,omitempty"`
	Delay  string            `yaml:"delay,omitempty"`
}

type wireAssertion struct {
	Kind    string  `yaml:"kind"`
	Outcome *string `yaml:"outcome,omitempty"`
	Equals  int     `yaml:"equals"`
}

type caseFieldPresence struct {
	role   bool
	repeat bool
	events bool
}

func (assertion *wireAssertion) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("assertion must be a mapping")
	}
	seen := map[string]bool{}
	for index := 0; index < len(value.Content); index += 2 {
		key, item := value.Content[index].Value, value.Content[index+1]
		if seen[key] {
			return fmt.Errorf("mapping key %q already defined", key)
		}
		seen[key] = true
		switch key {
		case "kind":
			if err := item.Decode(&assertion.Kind); err != nil {
				return err
			}
		case "outcome":
			var outcome string
			if err := item.Decode(&outcome); err != nil {
				return err
			}
			assertion.Outcome = &outcome
		case "equals":
			if err := item.Decode(&assertion.Equals); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown assertion field %q", key)
		}
	}
	return nil
}

// Decode accepts exactly one strict YAML or JSON document with no aliases.
func Decode(reader io.Reader) (Scenario, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, MaxDocumentBytes+1))
	if err != nil {
		return Scenario{}, fmt.Errorf("read scenario: %w", err)
	}
	if len(raw) > MaxDocumentBytes {
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
	caseFields := findCaseFieldPresence(&node)
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
	for index, item := range wire.Cases {
		window, parseErr := time.ParseDuration(item.Window)
		if parseErr != nil {
			return Scenario{}, fmt.Errorf("case %q window: %w", item.Name, parseErr)
		}
		fields := caseFieldPresence{}
		if index < len(caseFields) {
			fields = caseFields[index]
		}
		if fields.events && (fields.role || fields.repeat) {
			return Scenario{}, fmt.Errorf("case %q mixes event stimuli with role or repeat", item.Name)
		}
		if fields.events && len(item.Events) == 0 {
			return Scenario{}, fmt.Errorf("case %q event stimuli must contain at least one event", item.Name)
		}
		if !fields.events && (!fields.role || !fields.repeat || item.Role == nil || item.Repeat == nil) {
			return Scenario{}, fmt.Errorf("case %q repeat stimuli require non-null role and repeat fields", item.Name)
		}
		role, repeat := "", 0
		if item.Role != nil {
			role = *item.Role
		}
		if item.Repeat != nil {
			repeat = *item.Repeat
		}
		converted := Case{Name: item.Name, Code: item.Code, Polarity: item.Polarity, Role: role, Repeat: repeat, Window: window, GroupBy: item.GroupBy, Labels: item.Labels,
			SuppressForNotifiers: item.SuppressForNotifiers, TagForNotifiers: item.TagForNotifiers}
		for _, assertion := range item.Assertions {
			if assertion.Kind == "synthetic-alert-count" && assertion.Outcome != nil {
				return Scenario{}, fmt.Errorf("case %q synthetic-alert-count assertion must not supply outcome", item.Name)
			}
			if (assertion.Kind == "audit-count" || assertion.Kind == "parent-link-count") && (assertion.Outcome == nil || strings.TrimSpace(*assertion.Outcome) == "") {
				return Scenario{}, fmt.Errorf("case %q %s assertion requires a nonblank outcome", item.Name, assertion.Kind)
			}
			outcome := ""
			if assertion.Outcome != nil {
				outcome = *assertion.Outcome
			}
			converted.Assertions = append(converted.Assertions, Assertion{Kind: assertion.Kind, Outcome: outcome, Equals: assertion.Equals})
		}
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
	if document.APIVersion != APIVersion || document.Kind != Kind || !isSupportedPattern(document.Pattern) {
		return fmt.Errorf("scenario apiVersion, kind, or pattern is unsupported")
	}
	if strings.TrimSpace(document.Name) != document.Name || document.Name == "" || len(document.Name) > MaxScenarioNameLength ||
		strings.TrimSpace(document.Suite) != document.Suite || document.Suite == "" || len(document.Suite) > MaxSuiteLength {
		return fmt.Errorf("scenario name and suite are required and bounded")
	}
	if document.MaxDuration <= 0 || document.MaxDuration > MaxScenarioDuration || len(document.Cases) != RequiredCaseCount {
		return fmt.Errorf("scenario must contain exactly two cases within a five-minute budget")
	}
	codes := map[string]bool{}
	names := map[string]bool{}
	for _, item := range document.Cases {
		if item.Name == "" || len(item.Name) > MaxCaseNameLength || names[item.Name] || (item.Code != "P01" && item.Code != "N01") || codes[item.Code] {
			return fmt.Errorf("scenario case identity is invalid or duplicated")
		}
		if (item.Code == "P01" && item.Polarity != "positive") || (item.Code == "N01" && item.Polarity != "negative") {
			return fmt.Errorf("case %q polarity does not match its code", item.Name)
		}
		names[item.Name], codes[item.Code] = true, true
		if item.Window <= 0 || item.Window > MaxCaseWindow || len(item.GroupBy) > MaxGroupByLabels || len(item.Labels) > MaxLabels {
			return fmt.Errorf("case %q exceeds timing, grouping, or label budgets", item.Name)
		}
		if err := validateNames(item.GroupBy, MaxLabelNameLength, "groupBy"); err != nil {
			return fmt.Errorf("case %q: %w", item.Name, err)
		}
		if err := validateLabels(item.Labels); err != nil {
			return fmt.Errorf("case %q: %w", item.Name, err)
		}
		if len(item.Assertions) < 1 || len(item.Assertions) > MaxAssertions {
			return fmt.Errorf("case %q must contain bounded assertions", item.Name)
		}
		for _, assertion := range item.Assertions {
			if assertion.Kind != "synthetic-alert-count" && assertion.Kind != "audit-count" && assertion.Kind != "parent-link-count" {
				return fmt.Errorf("case %q assertion kind is unsupported", item.Name)
			}
			if assertion.Equals < 0 || assertion.Equals > MaxExpectedCount || len(assertion.Outcome) > MaxOutcomeLength {
				return fmt.Errorf("case %q assertion is outside the safe budget", item.Name)
			}
			if (assertion.Kind == "audit-count" || assertion.Kind == "parent-link-count") && strings.TrimSpace(assertion.Outcome) == "" {
				return fmt.Errorf("case %q assertion outcome is required", item.Name)
			}
			if assertion.Kind == "synthetic-alert-count" && assertion.Outcome != "" {
				return fmt.Errorf("case %q synthetic assertion outcome is forbidden", item.Name)
			}
		}
		if document.Pattern != "parent_child" && (len(item.SuppressForNotifiers) != 0 || len(item.TagForNotifiers) != 0) {
			return fmt.Errorf("case %q supplies notifiers for a non-parent-child pattern", item.Name)
		}
		if len(item.SuppressForNotifiers) > MaxNotifierNames || len(item.TagForNotifiers) > MaxNotifierNames {
			return fmt.Errorf("case %q exceeds the notifier budget", item.Name)
		}
		if err := validateNames(append(append([]string{}, item.SuppressForNotifiers...), item.TagForNotifiers...), MaxNotifierNameLength, "notifier"); err != nil {
			return fmt.Errorf("case %q: %w", item.Name, err)
		}
		events := item.Events
		if len(events) == 0 {
			if item.Repeat < 1 || item.Repeat > MaxEvents || item.Role == "" || len(item.Role) > MaxRoleLength {
				return fmt.Errorf("case %q repeat stimulus is invalid", item.Name)
			}
			continue
		}
		if item.Repeat != 0 || item.Role != "" || len(events) > MaxEvents {
			return fmt.Errorf("case %q mixes or exceeds event stimulus forms", item.Name)
		}
		for _, event := range events {
			if event.Role == "" || len(event.Role) > MaxRoleLength || (event.Status != "firing" && event.Status != "resolved") || event.Delay < 0 || event.Delay > document.MaxDuration {
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
		if IsReservedLabel(key) {
			return fmt.Errorf("reserved label %q cannot be overridden", key)
		}
		if !labelName.MatchString(key) || len(key) > MaxLabelNameLength || len(value) > MaxLabelValueLength || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("label %q is unsafe", key)
		}
	}
	return nil
}

func isSupportedPattern(pattern string) bool {
	for _, supported := range supportedPatterns {
		if pattern == supported {
			return true
		}
	}
	return false
}

func findCaseFieldPresence(document *yaml.Node) []caseFieldPresence {
	if document == nil || len(document.Content) == 0 {
		return nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value != "cases" {
			continue
		}
		cases := root.Content[index+1]
		if cases.Kind != yaml.SequenceNode {
			return nil
		}
		result := make([]caseFieldPresence, len(cases.Content))
		for caseIndex, item := range cases.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			for fieldIndex := 0; fieldIndex+1 < len(item.Content); fieldIndex += 2 {
				switch item.Content[fieldIndex].Value {
				case "role":
					result[caseIndex].role = true
				case "repeat":
					result[caseIndex].repeat = true
				case "events":
					result[caseIndex].events = true
				}
			}
		}
		return result
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
