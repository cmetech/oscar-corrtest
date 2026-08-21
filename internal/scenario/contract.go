package scenario

import "time"

const (
	APIVersion            = "corrtest.oscar/v1alpha1"
	Kind                  = "CorrelationScenario"
	MaxDocumentBytes      = 1 << 20
	MaxScenarioNameLength = 100
	MaxSuiteLength        = 100
	RequiredCaseCount     = 2
	MaxCaseNameLength     = 120
	MaxRoleLength         = 100
	MaxLabelNameLength    = 100
	MaxLabelValueLength   = 500
	MaxOutcomeLength      = 100
	MaxNotifierNameLength = 100
	MaxScenarioDuration   = 5 * time.Minute
	MaxCaseWindow         = 2 * time.Minute
	MaxGroupByLabels      = 16
	MaxLabels             = 64
	MaxEvents             = 100
	MaxAssertions         = 32
	MaxNotifierNames      = 16
	MaxExpectedCount      = 100
)

type Requirement string

const (
	Required    Requirement = "required"
	Optional    Requirement = "optional"
	Conditional Requirement = "conditional"
)

type FieldDefinition struct {
	ID                 string      `json:"id"`
	Group              string      `json:"group"`
	YAMLName           string      `json:"yamlName"`
	ValueType          string      `json:"valueType"`
	Requirement        Requirement `json:"requirement"`
	AllowedValues      []string    `json:"allowedValues,omitempty"`
	Limits             string      `json:"limits,omitempty"`
	OmittedBehavior    string      `json:"omittedBehavior,omitempty"`
	PatternRestriction string      `json:"patternRestriction,omitempty"`
	Effect             string      `json:"effect"`
	Example            string      `json:"example"`
	CommonError        string      `json:"commonError"`
}

type PatternDefinition struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	FixedSemantics []string `json:"fixedSemantics"`
	RequiredInputs []string `json:"requiredInputs"`
}

type Contract struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Fields     []FieldDefinition   `json:"fields"`
	Patterns   []PatternDefinition `json:"patterns"`
}

var supportedPatterns = []string{"flood", "co_occurrence", "sequence", "persistence", "absence", "parent_child", "cross_source", "threshold"}

var reservedLabelSet = map[string]struct{}{
	"alertname": {}, "category": {}, "oscar_test": {}, "oscar_test_harness": {}, "oscar_test_schema_version": {},
	"oscar_test_run_id": {}, "oscar_test_run_short": {}, "oscar_test_suite": {}, "oscar_test_scenario": {},
	"oscar_test_pattern": {}, "oscar_test_case": {}, "oscar_test_case_code": {}, "oscar_test_polarity": {},
	"oscar_test_alert_class": {}, "oscar_test_alert_role": {}, "oscar_test_rule_name": {}, "oscar_test_event_id": {},
	"oscar_test_event_index": {}, "oscar_fingerprint": {}, "am_fingerprint": {},
	"severity": {},
}

var reservedLabels = []string{
	"alertname", "am_fingerprint", "category", "oscar_fingerprint", "oscar_test", "oscar_test_alert_class",
	"oscar_test_alert_role", "oscar_test_case", "oscar_test_case_code", "oscar_test_event_id", "oscar_test_event_index",
	"oscar_test_harness", "oscar_test_pattern", "oscar_test_polarity", "oscar_test_rule_name", "oscar_test_run_id",
	"oscar_test_run_short", "oscar_test_scenario", "oscar_test_schema_version", "oscar_test_suite", "severity",
}

// SupportedPatterns returns the canonical cookbook order without exposing internal storage.
func SupportedPatterns() []string { return append([]string(nil), supportedPatterns...) }

// ReservedLabels returns labels owned by CorrTest or the OSCAR alert transport.
func ReservedLabels() []string { return append([]string(nil), reservedLabels...) }

// IsReservedLabel reports whether a scenario cannot provide a label name.
func IsReservedLabel(label string) bool {
	_, found := reservedLabelSet[label]
	return found
}

// PublicContract describes every closed scenario wire field and supported compiler pattern.
func PublicContract() Contract {
	return Contract{APIVersion: APIVersion, Kind: Kind, Fields: []FieldDefinition{
		field("scenario.apiVersion", "Scenario", "apiVersion", "string", Required, []string{APIVersion}, "exact value", "", "", "Selects the CorrTest wire contract.", "apiVersion: corrtest.oscar/v1alpha1", "Using another API version is unsupported."),
		field("scenario.kind", "Scenario", "kind", "string", Required, []string{Kind}, "exact value", "", "", "Identifies this document as a correlation scenario.", "kind: CorrelationScenario", "A different kind is rejected."),
		field("scenario.name", "Scenario", "name", "string", Required, nil, "trimmed, 1–100 characters", "", "", "Names run-owned resources and evidence.", "name: checkout-flood", "Leading or trailing whitespace is invalid."),
		field("scenario.suite", "Scenario", "suite", "string", Required, nil, "trimmed, 1–100 characters", "", "", "Scopes the scenario in emitted ownership labels.", "suite: operator-custom", "An empty suite is invalid."),
		field("scenario.pattern", "Scenario", "pattern", "string", Required, SupportedPatterns(), "one supported pattern", "", "", "Selects fixed OSCAR compiler criteria.", "pattern: flood", "Unsupported patterns are rejected."),
		field("scenario.maxDuration", "Scenario", "maxDuration", "Go duration", Required, nil, "positive; at most 5 minutes", "", "", "Bounds all case observation and injection work.", "maxDuration: 90s", "A zero or over-five-minute duration is invalid."),
		field("scenario.cases", "Scenario", "cases", "array", Required, nil, "exactly two unique cases: P01 and N01", "", "", "Defines positive proof and nearby negative control.", "cases: [...]", "Missing P01 or N01 is invalid."),

		field("case.name", "Case", "name", "string", Required, nil, "unique, 1–120 characters", "", "", "Labels case-owned rules, alerts, and evidence.", "name: emits-parent", "Duplicate case names are invalid."),
		field("case.code", "Case", "code", "string", Required, []string{"P01", "N01"}, "exactly once each", "", "", "Pairs the case with its required polarity.", "code: P01", "Two P01 cases are invalid."),
		field("case.polarity", "Case", "polarity", "string", Required, []string{"positive", "negative"}, "P01=positive; N01=negative", "", "", "Documents expected proof or control behavior.", "polarity: positive", "P01 with negative polarity is invalid."),
		field("case.window", "Case", "window", "Go duration", Required, nil, "positive; at most 2 minutes", "", "", "Sets the compiled correlation window.", "window: 30s", "A zero or over-two-minute window is invalid."),
		field("case.groupBy", "Case", "groupBy", "array of label names", Optional, nil, "up to 16 unique safe label names", "No grouping labels are used.", "", "Sets compiled grouping labels.", "groupBy: [site]", "Duplicate or unsafe group labels are invalid."),
		field("case.labels", "Case", "labels", "object of strings", Optional, nil, "up to 64 safe, non-reserved labels", "No case labels are added.", "", "Applied to firing source stimuli.", "labels: {site: lab-a}", "Reserved labels cannot be overridden."),
		field("case.role", "Case", "role", "string", Conditional, nil, "non-empty; at most 100 characters", "", "Required with repeat; forbidden with events.", "Defines the repeated logical source role.", "role: interface_down", "A role cannot be combined with explicit events."),
		field("case.repeat", "Case", "repeat", "integer", Conditional, nil, "1–100 with role", "Zero or omitted with events.", "Required with role; forbidden with events.", "Expands to deterministic firing events.", "repeat: 5", "Repeat requires a role."),
		field("case.events", "Case", "events", "array", Conditional, nil, "1–100 events", "", "Mutually exclusive with role and repeat.", "Supplies deterministic firing and resolution events.", "events: [{role: parent, status: firing}]", "Events cannot be mixed with role/repeat."),
		field("case.suppressForNotifiers", "Case", "suppressForNotifiers", "array of strings", Conditional, nil, "up to 16 unique notifier names", "No notifier suppression is configured.", "parent_child only; disjoint from tagForNotifiers.", "Compiles child suppression notifier handling.", "suppressForNotifiers: [email]", "Notifiers are only valid for parent_child."),
		field("case.tagForNotifiers", "Case", "tagForNotifiers", "array of strings", Conditional, nil, "up to 16 unique notifier names", "No notifier tags are configured.", "parent_child only; disjoint from suppressForNotifiers.", "Compiles child tagging notifier handling.", "tagForNotifiers: [pagerduty]", "Notifiers are only valid for parent_child."),
		field("case.assertions", "Case", "assertions", "array", Required, nil, "1–32 typed exact-count assertions", "", "", "Defines evidence checks after the observation window.", "assertions: [{kind: synthetic-alert-count, equals: 1}]", "A case without assertions is invalid."),

		field("event.role", "Event", "role", "string", Required, nil, "non-empty; at most 100 characters", "", "", "Chooses the logical source alert role.", "role: parent", "An empty event role is invalid."),
		field("event.status", "Event", "status", "string", Required, []string{"firing", "resolved"}, "exact value", "", "", "Fires or resolves an authoritative source identity.", "status: firing", "A resolved event must follow a firing event."),
		field("event.labels", "Event", "labels", "object of strings", Optional, nil, "up to 64 safe, non-reserved labels", "No event-specific labels are added.", "Resolved events cannot change identity labels.", "Overrides case labels for a firing identity.", "labels: {device: edge-1}", "Resolution labels are forbidden."),
		field("event.delay", "Event", "delay", "Go duration", Optional, nil, "non-negative; within scenario and observation budgets", "Immediate injection.", "", "Schedules deterministic injection order.", "delay: 1m30s", "A delay beyond the case budget is invalid."),

		field("assertion.kind", "Assertion", "kind", "string", Required, []string{"synthetic-alert-count", "audit-count", "parent-link-count"}, "exact value", "", "", "Selects the evidence source to count.", "kind: audit-count", "Unsupported assertion kinds are invalid."),
		field("assertion.outcome", "Assertion", "outcome", "string", Conditional, nil, "non-blank; at most 100 characters", "Omitted for synthetic-alert-count.", "Required for audit-count and parent-link-count; forbidden for synthetic-alert-count.", "Matches the exact OSCAR audit outcome.", "outcome: parent_emitted", "Audit assertions require an outcome."),
		field("assertion.equals", "Assertion", "equals", "integer", Required, nil, "0–100", "", "", "Requires exactly this evidence count.", "equals: 1", "Negative or over-100 expected counts are invalid."),
	}, Patterns: []PatternDefinition{
		pattern("flood", "Flood", "Tests a threshold of repeated source occurrences.", []string{"min_count=5", "occurrences require distinct fingerprints"}, []string{"P01: at least five firing occurrences in one grouping key", "N01: no grouping key reaches five occurrences"}),
		pattern("co_occurrence", "Co-occurrence", "Tests required alert names within one grouping window.", []string{"all compiled required alert names must occur in one grouping window"}, []string{"P01: every required role fires in one group", "N01: a required role is absent from every group"}),
		pattern("sequence", "Sequence", "Tests ordered login and privilege signals.", []string{"login_failure then privileged_command"}, []string{"P01: required ordered pair in one group", "N01: no group has that valid ordered pair"}),
		pattern("persistence", "Persistence", "Tests an unresolved source alert.", []string{"one matching alert unresolved for 30 seconds"}, []string{"P01: firing remains unresolved for 30 seconds", "N01: same identity resolves before 30 seconds"}),
		pattern("absence", "Absence", "Tests for a missing expected heartbeat.", []string{"expected every 10 seconds", "absent for 30 seconds", "55-second observation"}, []string{"P01: a heartbeat gap reaches 30 seconds", "N01: heartbeats prevent a completed 30-second gap"}),
		pattern("parent_child", "Parent-child", "Tests parent-linked child handling.", []string{"roles parent and child", "no synthetic emit rule"}, []string{"P01: parent fires before child in one group", "N01: child fires without earlier active parent"}),
		pattern("cross_source", "Cross-source", "Tests matching source data from two source systems.", []string{"required sources snmp and api for one semantic alert"}, []string{"P01: one role fires from snmp and api in one group", "N01: every group lacks a required source"}),
		pattern("threshold", "Threshold", "Tests distinct device values within one group.", []string{"distinct label device", "minimum distinct count 3"}, []string{"P01: at least three device values in one group", "N01: no group reaches three device values"}),
	}}
}

func field(id, group, yamlName, valueType string, requirement Requirement, allowed []string, limits, omitted, restriction, effect, example, commonError string) FieldDefinition {
	return FieldDefinition{ID: id, Group: group, YAMLName: yamlName, ValueType: valueType, Requirement: requirement, AllowedValues: append([]string(nil), allowed...), Limits: limits, OmittedBehavior: omitted, PatternRestriction: restriction, Effect: effect, Example: example, CommonError: commonError}
}

func pattern(id, title, summary string, fixed, inputs []string) PatternDefinition {
	return PatternDefinition{ID: id, Title: title, Summary: summary, FixedSemantics: append([]string(nil), fixed...), RequiredInputs: append([]string(nil), inputs...)}
}
