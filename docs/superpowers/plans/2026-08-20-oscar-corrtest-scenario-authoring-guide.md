# OSCAR CorrTest Scenario Authoring Guide Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a technical, tutorial-first Authoring workspace that teaches the complete CorrTest YAML language, supplies executable basic and advanced examples for all eight patterns, and previews the exact OSCAR requests and lifecycle generated from validated scenarios.

**Architecture:** Make `internal/scenario` the single source of truth for structural field metadata, budgets, supported values, executable examples, semantic validation, and generated JSON Schema. Extract typed request construction from `internal/oscar` so the live client and read-only previews serialize the same values, then compose these contracts in a credential-free `internal/authoring` service used by both `/authoring` and the existing Scenarios workbench. Keep all tutorial navigation server-rendered; JavaScript only enhances filtering, copying, and view switching.

**Tech Stack:** Go 1.27, `net/http`, `html/template`, embedded HTML/CSS/JavaScript, `gopkg.in/yaml.v3`, generated JSON Schema, existing CorrTest compiler/runtime/OSCAR client, Go tests and shell package-contract tests.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-scenario-authoring-guide-design.md`

## Global Constraints

- The scenario language remains closed at `apiVersion: corrtest.oscar/v1alpha1` and `kind: CorrelationScenario`; do not expose arbitrary OSCAR match criteria in YAML.
- Support exactly `flood`, `co_occurrence`, `sequence`, `persistence`, `absence`, `parent_child`, `cross_source`, and `threshold`.
- A valid document contains exactly one P01/positive case and one N01/negative case, is at most 1 MiB, has a maximum duration of five minutes, and rejects aliases, duplicate keys, extra YAML documents, and unknown fields.
- Preserve current case, event, label, assertion, notifier, and duration safety limits; export them as named constants instead of duplicating numeric literals.
- CorrTest creates exactly two temporary correlation rules, P01 and N01. It creates no ordinary alert rules; source alerts use `POST /api/v1/alerts`.
- OSCAR previews are target-free and credential-free. Never render a target URL, API key, authorization header, or fabricated server-returned ID/fingerprint.
- The compatibility preflight precedes mutation: validate P01, inject a label-survival probe, and read it back from history. Runtime then validates and creates P01 and N01, injects alerts, observes evidence, deletes returned-ID rules, and resolves injected alerts.
- Preview JSON and live-client JSON must be built from the same typed request values and compared canonically in regression tests.
- Basic examples are the current built-ins. Advanced examples are typed `scenario.Scenario` values, encoded by `scenario.Encode`; all sixteen examples must decode and compile in tests.
- Opening an example in Scenarios is a GET-only, unsaved draft operation. It may read the existing local catalog needed by the page, but must not write SQLite, resolve credentials, contact OSCAR, create a run, or mutate a target.
- All content is usable without JavaScript and preserves current dense-console styling, dark/light themes, CSP, keyboard access, focus visibility, and reduced-motion behavior.
- `parent_child` has no emitted synthetic rule. Phase B is required for synthetic-parent assertions; Phase A is audit-only.
- Do not add a frontend framework, client-side persistence, a second YAML editor, or a new runtime dependency.

## File map

| Responsibility | Files |
|---|---|
| Public scenario contract, limits, generated schema | `internal/scenario/contract.go`, `internal/scenario/schema.go`, `cmd/generate-scenario-schema/main.go`, `docs/schema/correlation-scenario.schema.json` |
| Pattern-aware validation and executable examples | `internal/scenario/semantics.go`, `internal/scenario/examples.go`, `internal/scenario/examples_test.go`, `internal/scenario/semantics_test.go` |
| OSCAR request truth and lifecycle preview | `internal/oscar/request.go`, `internal/oscar/preview.go`, `internal/oscar/request_test.go`, `internal/oscar/preview_test.go`, `internal/oscar/client.go` |
| Credential-free authoring composition | `internal/authoring/catalog.go`, `internal/authoring/service.go`, corresponding tests, `internal/runtime/scenario_inspection.go` |
| Authoring UI | `internal/web/templates/authoring*.html.tmpl`, `internal/web/static/css/pages.css`, `internal/web/static/css/components.css`, `internal/web/static/js/authoring.js`, `internal/web/server.go` |
| Scenarios/reference integration | `internal/web/templates/scenarios.html.tmpl`, `internal/web/templates/reference.html.tmpl`, `internal/web/templates/help_drawer.html.tmpl`, `internal/web/help.go`, `internal/web/static/js/scenarios.js` |
| Operator docs and packaging | `README.md`, `docs/scenario-authoring.md`, `docs/builtins.md`, `docs/operator.md`, `internal/docs/operator_docs_test.go`, `scripts/package_contract_test.go` |

---

### Task 1: Make the Scenario Contract Typed and Generate JSON Schema

**Files:**
- Create: `internal/scenario/contract.go`
- Create: `internal/scenario/schema.go`
- Create: `cmd/generate-scenario-schema/main.go`
- Modify: `internal/scenario/decode.go`
- Modify: `internal/scenario/schema_contract_test.go`
- Modify: `internal/compiler/compiler.go`
- Test: `internal/compiler/compiler_test.go`
- Generate: `docs/schema/correlation-scenario.schema.json`

**Interfaces:**
- Consumes: existing `Scenario`, `Case`, `Event`, and `Assertion` types from `internal/scenario/model.go`.
- Produces: `scenario.PublicContract() Contract`, `scenario.GenerateJSONSchema() ([]byte, error)`, and exported safety constants used by validation, examples, and the UI.

- [ ] **Step 1: Add failing contract and schema-drift tests**

Replace the current string-presence-only schema test with behavioral tests that require every YAML field exactly once in the public contract, require stable enum and budget values, and compare the generated bytes with the committed file:

```go
func TestPublicContractCoversClosedWireModel(t *testing.T) {
	want := []string{
		"scenario.apiVersion", "scenario.kind", "scenario.name", "scenario.suite",
		"scenario.pattern", "scenario.maxDuration", "scenario.cases",
		"case.name", "case.code", "case.polarity", "case.window", "case.groupBy",
		"case.labels", "case.role", "case.repeat", "case.events",
		"case.suppressForNotifiers", "case.tagForNotifiers", "case.assertions",
		"event.role", "event.status", "event.labels", "event.delay",
		"assertion.kind", "assertion.outcome", "assertion.equals",
	}
	got := map[string]int{}
	for _, field := range scenario.PublicContract().Fields { got[field.ID]++ }
	for _, id := range want {
		if got[id] != 1 { t.Errorf("field %s count = %d, want 1", id, got[id]) }
	}
}

func TestCommittedJSONSchemaMatchesGenerator(t *testing.T) {
	want, err := scenario.GenerateJSONSchema()
	if err != nil { t.Fatal(err) }
	got, err := os.ReadFile("../../docs/schema/correlation-scenario.schema.json")
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(want, got) {
		t.Fatal("committed scenario schema differs from generated schema")
	}
}
```

Also assert the schema encodes the repeat/events exclusivity and `additionalProperties: false` at all four object levels.

- [ ] **Step 2: Run the focused tests and confirm the contract does not exist**

Run: `go test ./internal/scenario -run 'Test(PublicContract|CommittedJSONSchema)' -count=1`

Expected: FAIL because `PublicContract` and `GenerateJSONSchema` are undefined.

- [ ] **Step 3: Define named budgets and the public field contract**

Create these stable types and values in `contract.go`:

```go
const (
	APIVersion = "corrtest.oscar/v1alpha1"
	Kind = "CorrelationScenario"
	MaxDocumentBytes = 1 << 20
	MaxScenarioNameLength = 100
	MaxSuiteLength = 100
	RequiredCaseCount = 2
	MaxCaseNameLength = 120
	MaxRoleLength = 100
	MaxLabelNameLength = 100
	MaxLabelValueLength = 500
	MaxOutcomeLength = 100
	MaxNotifierNameLength = 100
	MaxScenarioDuration = 5 * time.Minute
	MaxCaseWindow = 2 * time.Minute
	MaxGroupByLabels = 16
	MaxLabels = 64
	MaxEvents = 100
	MaxAssertions = 32
	MaxNotifierNames = 16
	MaxExpectedCount = 100
)

type Requirement string
const (
	Required Requirement = "required"
	Optional Requirement = "optional"
	Conditional Requirement = "conditional"
)

type FieldDefinition struct {
	ID string `json:"id"`
	Group string `json:"group"`
	YAMLName string `json:"yamlName"`
	ValueType string `json:"valueType"`
	Requirement Requirement `json:"requirement"`
	AllowedValues []string `json:"allowedValues,omitempty"`
	Limits string `json:"limits,omitempty"`
	OmittedBehavior string `json:"omittedBehavior,omitempty"`
	PatternRestriction string `json:"patternRestriction,omitempty"`
	Effect string `json:"effect"`
	Example string `json:"example"`
	CommonError string `json:"commonError"`
}

type PatternDefinition struct {
	ID string `json:"id"`
	Title string `json:"title"`
	Summary string `json:"summary"`
	FixedSemantics []string `json:"fixedSemantics"`
	RequiredInputs []string `json:"requiredInputs"`
}

type Contract struct {
	APIVersion string `json:"apiVersion"`
	Kind string `json:"kind"`
	Fields []FieldDefinition `json:"fields"`
	Patterns []PatternDefinition `json:"patterns"`
}

func PublicContract() Contract
```

Populate all 26 field definitions from design sections 7.1–7.4, including required/optional/conditional state, accepted values, omitted behavior, restrictions, compiler effect, a minimal snippet, and a concrete common error. Populate the eight pattern definitions from design section 6.3. Expose `SupportedPatterns() []string` as a defensive copy in canonical cookbook order and replace the decoder’s private pattern map and duplicated literal bounds with these constants. Expose `ReservedLabels() []string` and `IsReservedLabel(string) bool` from one private set shared by metadata, decoder validation, and `compiler.Compile`; include every compiler-owned identity label (`oscar_test_event_id` and `oscar_test_event_index` included) plus transport-reserved `oscar_fingerprint` and `am_fingerprint`, so strict decode cannot accept a label that compile or injection later rejects.

- [ ] **Step 4: Generate the JSON Schema from the typed contract**

In `schema.go`, define private JSON-Schema structs and implement:

```go
func GenerateJSONSchema() ([]byte, error) {
	schema := buildSchema(PublicContract())
	raw, err := json.MarshalIndent(schema, "", "  ")
	if err != nil { return nil, fmt.Errorf("marshal scenario schema: %w", err) }
	return append(raw, '\n'), nil
}
```

`buildSchema` must emit draft 2020-12, the exact API/kind constants, the eight-pattern enum, duration strings, field bounds, four object-level `additionalProperties: false` declarations, P01/positive and N01/negative pairing constraints, and this case-level exclusive choice. Represent duration fields as non-empty strings with `x-corrtest-format: go-duration` and documented min/max bounds; do not retain the current narrow regex, which rejects valid `time.ParseDuration` values such as `1m30s`.

```json
"oneOf": [
  {"required": ["role", "repeat"], "not": {"required": ["events"]}},
  {"required": ["events"], "not": {"anyOf": [{"required": ["role"]}, {"required": ["repeat"]}]}}
]
```

The assertion schema must require `outcome` for `audit-count` and `parent-link-count`, and must reject `outcome` for `synthetic-alert-count`, matching the typed conditional contract.

Create `cmd/generate-scenario-schema/main.go` as a write-on-success command:

```go
func main() {
	raw, err := scenario.GenerateJSONSchema()
	if err != nil { log.Fatal(err) }
	if err := os.WriteFile("docs/schema/correlation-scenario.schema.json", raw, 0o644); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 5: Generate the file and run the scenario suite**

Run:

```bash
go run ./cmd/generate-scenario-schema
gofmt -w internal/scenario/contract.go internal/scenario/schema.go cmd/generate-scenario-schema/main.go internal/scenario/decode.go internal/scenario/schema_contract_test.go internal/compiler/compiler.go internal/compiler/compiler_test.go
go test ./internal/scenario ./internal/compiler -count=1
git diff --check
```

Expected: all tests PASS and a second `go run ./cmd/generate-scenario-schema` leaves the schema unchanged.

- [ ] **Step 6: Commit the contract**

```bash
git add internal/scenario/contract.go internal/scenario/schema.go internal/scenario/decode.go internal/scenario/schema_contract_test.go internal/compiler/compiler.go internal/compiler/compiler_test.go cmd/generate-scenario-schema/main.go docs/schema/correlation-scenario.schema.json
git commit -m "feat: define scenario authoring contract"
```

---

### Task 2: Add Pattern-Aware Validation and Sixteen Executable Examples

**Files:**
- Create: `internal/scenario/semantics.go`
- Create: `internal/scenario/semantics_test.go`
- Create: `internal/scenario/examples.go`
- Create: `internal/scenario/examples_test.go`
- Modify: `internal/scenario/decode.go`
- Modify: `internal/scenario/builtin.go`
- Modify: `internal/compiler/compiler.go`
- Test: `internal/compiler/patterns_test.go`

**Interfaces:**
- Consumes: `PublicContract() Contract`, exported budgets, `Builtin(pattern string) Scenario`, `Encode`, `Decode`, and `compiler.Compile`.
- Produces: polarity-aware `validatePatternSemantics(Scenario) error`, `scenario.AllExamples() []ExampleDefinition`, and `scenario.LookupExample(pattern, level string) (ExampleDefinition, bool)`.

- [ ] **Step 1: Write table-driven failing semantic tests**

Add one valid P01/N01 document and one impossible polarity mutation for each pattern. The mutations must prove these checks rather than merely repeat structural validation:

```go
var semanticMutations = []struct{
	name string
	pattern string
	mutate func(*Scenario)
	want string
}{
	{"flood positive below five", "flood", setP01Repeat(4), "P01 flood must reach five"},
	{"flood negative reaches five", "flood", setN01Repeat(5), "N01 flood must remain below five"},
	{"co occurrence missing P01 member", "co_occurrence", removeP01Role("disk_full"), "P01 co_occurrence"},
	{"sequence valid in N01", "sequence", orderN01("login_failure", "privileged_command"), "N01 sequence"},
	{"persistence resolves late in N01", "persistence", setN01ResolveDelay(30*time.Second), "N01 persistence must resolve before 30s"},
	{"absence gap missing in P01", "absence", sustainP01Heartbeats(10*time.Second), "P01 absence"},
	{"parent child parent missing in P01", "parent_child", removeP01Role("parent"), "P01 parent_child"},
	{"cross source P01 missing api", "cross_source", removeP01Source("api"), "P01 cross_source"},
	{"threshold N01 reaches three devices", "threshold", addN01Devices("edge-1", "edge-2", "edge-3"), "N01 threshold"},
}
```

Define each named mutator in the same test file as `func(*Scenario)` and use this shared public-boundary assertion rather than calling private validators:

```go
func requireSemanticDecodeError(t *testing.T, pattern, contains string, mutate func(*Scenario)) {
	t.Helper()
	document := Builtin(pattern)
	mutate(&document)
	raw := encodeWireWithoutValidation(t, document)
	_, err := Decode(bytes.NewReader(raw))
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error=%v, want substring %q", err, contains)
	}
}
```

`encodeWireWithoutValidation` is a test-only YAML marshal of the existing `wireScenario` conversion; it must not use `Encode`, because `Encode` will correctly fail before the bytes reach `Decode`. Keep the nine mutations literal: edit the referenced P01/N01 case's `Repeat`, `Events`, roles, delays, `oscar_source`, `device`, or grouping labels directly.

- [ ] **Step 2: Run the semantic tests and confirm impossible cases are accepted**

Run: `go test ./internal/scenario -run TestPatternSemanticValidation -count=1`

Expected: FAIL because at least one impossible mutation decodes successfully.

- [ ] **Step 3: Implement the closed semantic evaluator**

Create normalized internal observations that apply case labels plus event overrides and retain role, status, absolute scheduled delay, grouping-key values, and alert identity. Event `delay` is an offset from case start because the runner sleeps `alert.Delay-elapsed`; do not sum adjacent delays:

```go
type semanticEvent struct {
	Role string
	Status string
	Labels map[string]string
	At time.Duration
	GroupKey string
	Identity string
}

type semanticContract struct {
	Pattern string
	RequiredRoles []string
}

func validatePatternSemantics(document Scenario) error {
	contract := buildSemanticContract(document)
	for _, testCase := range document.Cases {
		events := expandSemanticEvents(testCase)
		if err := validateCaseSemantics(contract, testCase, events); err != nil {
			return fmt.Errorf("case %s: %w", testCase.Code, err)
		}
	}
	return nil
}
```

Dispatch to eight focused functions and implement the exact polarity contracts from design section 6.5: flood distinct occurrences at 5; all co-occurrence roles; ordered sequence; persistence unresolved for 30 seconds; absence 30-second gap within its 55-second observation; active parent before child; `snmp` plus `api`; three distinct `device` values. For single-source patterns, reject extra firing roles that would change the compiled selector. Call this function at the end of `validate` in `decode.go`.

For `co_occurrence`, compute the logical required-role vocabulary once from both scenario cases plus the fixed `disk_full` and `cpu_high` roles, then make `compiler.Compile` materialize every vocabulary role into each case rule. This is required for an advanced N01 to omit an additional role while its rule still requires that role; deriving each rule solely from events present in that case would make the negative control vacuously pass against a weaker rule. Add a compiler regression asserting an extra P01 role appears in both P01 and N01 `required_matches`, while only P01 injects it.

- [ ] **Step 4: Run validation tests**

Run: `go test ./internal/scenario -run 'Test(PatternSemanticValidation|Decode)' -count=1`

Expected: PASS, including every P01 and N01 mutation.

- [ ] **Step 5: Write failing executable-example tests**

Define the public example shape expected by the UI:

```go
type ExampleDefinition struct {
	ID string
	Pattern string
	Level string
	Title string
	Summary string
	Scenario Scenario
}
```

Test all 16 IDs, strict encode/decode, compile, and canonical lookup:

```go
func TestAllExamplesDecodeAndCompile(t *testing.T) {
	examples := AllExamples()
	if len(examples) != 16 { t.Fatalf("got %d examples, want 16", len(examples)) }
	seen := map[string]bool{}
	for _, example := range examples {
		if seen[example.ID] { t.Fatalf("duplicate %s", example.ID) }
		seen[example.ID] = true
		raw, err := Encode(example.Scenario)
		if err != nil { t.Fatal(err) }
		document, err := Decode(bytes.NewReader(raw))
		if err != nil { t.Fatalf("%s: %v", example.ID, err) }
		_, err = compiler.Compile(domain.Run{ID: "crt_example", ShortToken: "EXAMPLE1"}, document, compiler.Capabilities{PipelineMode: "phase_b_dispatch"})
		if err != nil { t.Fatalf("compile %s: %v", example.ID, err) }
	}
}
```

- [ ] **Step 6: Implement the basic/advanced catalog**

Implement `AllExamples` in supported-pattern order with IDs `<pattern>:basic` and `<pattern>:advanced`. Basic values are defensive copies of `Builtin(pattern)`. Advanced typed values must demonstrate exactly:

| Pattern | Advanced P01 | Advanced N01 |
|---|---|---|
| flood | five distinct identities in one `site`/`service` group | five events split so no group reaches five |
| co_occurrence | three required roles in one group | one required role absent |
| sequence | login then privileged command in one group | reversed order in that group |
| persistence | firing identity remains active for 30 seconds | same identity resolves at 29 seconds |
| absence | heartbeat followed by a 30-second gap | heartbeats every 10 seconds through the window |
| parent_child | active parent followed by child with multiple disjoint suppress/tag notifiers | unmatched child with the same notifier policy |
| cross_source | same logical alert from `snmp` and `api` in one group | sources split across groups |
| threshold | three distinct `device` values in one group | three values split across groups |

`LookupExample` accepts only exact pattern and level values and returns a defensive copy so handlers cannot mutate catalog state.

- [ ] **Step 7: Run example, compiler, and full scenario tests**

Run:

```bash
gofmt -w internal/scenario/semantics.go internal/scenario/semantics_test.go internal/scenario/examples.go internal/scenario/examples_test.go internal/scenario/decode.go internal/scenario/builtin.go internal/compiler/compiler.go internal/compiler/patterns_test.go
go test ./internal/scenario ./internal/compiler -count=1
```

Expected: PASS with exactly 16 executable examples.

- [ ] **Step 8: Commit semantic validation and examples**

```bash
git add internal/scenario internal/compiler/patterns_test.go
git commit -m "feat: add executable scenario cookbook"
```

---

### Task 3: Share Exact OSCAR Request Builders with Live Execution and Preview

**Files:**
- Create: `internal/oscar/request.go`
- Create: `internal/oscar/request_test.go`
- Create: `internal/oscar/preview.go`
- Create: `internal/oscar/preview_test.go`
- Modify: `internal/oscar/client.go`
- Modify: `internal/oscar/client_test.go`

**Interfaces:**
- Consumes: `compiler.RulePlan`, `compiler.AlertPlan`, and `compiler.Plan`.
- Produces: `BuildRuleRequest`, `BuildAlertRequest`, `BuildResolutionRequest`, `BuildLabelProbeAlert`, `CanonicalJSON`, `BuildOperationPreview`, and public credential-free request/preview types.

- [ ] **Step 1: Write failing request-equivalence tests**

Record bodies received by an `httptest.Server` from `ValidateRule`, `CreateRule`, and `Inject`. Compare each decoded body with the exported builder result:

```go
func TestLiveClientUsesPublicRequestBuilders(t *testing.T) {
	rule := compiler.RulePlan{Name: "corrtest-flood-p01-preview1", Pattern: "flood", WindowSeconds: 30, GroupBy: []string{"site"}, MatchCriteria: map[string]any{"min_count": 5}}
	wantRule := BuildRuleRequest(rule, "test-version")
	// Invoke ValidateRule and CreateRule against the recorder; require both JSON values equal wantRule.

	alert := compiler.AlertPlan{Name: "CORRTEST_FLOOD_P01_SOURCE_PREVIEW1", Status: "firing", Labels: map[string]string{"alertname": "CORRTEST_FLOOD_P01_SOURCE_PREVIEW1"}, Annotations: map[string]string{"summary": "test"}}
	wantAlert, err := BuildAlertRequest(alert)
	if err != nil { t.Fatal(err) }
	// Invoke Inject; require its JSON value equal wantAlert.
}
```

- [ ] **Step 2: Run the request test and confirm the builders are undefined**

Run: `go test ./internal/oscar -run TestLiveClientUsesPublicRequestBuilders -count=1`

Expected: FAIL at compile time for undefined builder functions.

- [ ] **Step 3: Create typed request values and switch the client to them**

Use explicit JSON tags matching OSCAR public-v1:

```go
type RuleRequest struct {
	Name string `json:"name"`
	Pattern string `json:"pattern"`
	WindowSeconds int `json:"window_seconds"`
	GroupByLabels []string `json:"group_by_labels"`
	MatchCriteria map[string]any `json:"match_criteria"`
	Priority int `json:"priority"`
	MaxSyntheticPerMinute int `json:"max_synthetic_per_minute"`
	Enabled bool `json:"enabled"`
	Description string `json:"description"`
	CreatedBy string `json:"created_by"`
	EmitSpec *EmitSpecRequest `json:"emit_spec,omitempty"`
}

type EmitSpecRequest struct {
	AlertName string `json:"alertname"`
	Labels map[string]string `json:"labels"`
}

type AlertGroupRequest struct {
	Receiver string `json:"receiver"`
	Status string `json:"status"`
	GroupKey string `json:"groupKey"`
	GroupLabels map[string]string `json:"groupLabels"`
	CommonLabels map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	Alerts []AlertRequest `json:"alerts"`
}
type AlertRequest struct {
	Fingerprint string `json:"fingerPrint"`
	Status string `json:"status"`
	Labels map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func BuildRuleRequest(rule compiler.RulePlan, harnessVersion string) RuleRequest
func BuildAlertRequest(alert compiler.AlertPlan) (AlertGroupRequest, error)
func BuildResolutionRequest(record HistoryRecord) (AlertGroupRequest, error)
func BuildLabelProbeAlert(runID, shortToken string) (compiler.AlertPlan, error)
func CanonicalJSON(value any) ([]byte, error)
```

`BuildAlertRequest` must reject a blank status/name, clone its maps, force the compiler plan’s name into `labels.alertname`, and calculate the existing Alertmanager-compatible transport fingerprint. It must preserve the current receiver, run/name group key, group labels, common labels, common annotations, and one-alert envelope. `BuildResolutionRequest` must preserve `ResolveHistory`'s exact authoritative-history ownership checks, `oscar_fingerprint` label, cleanup annotation, and deterministic cleanup transport fingerprint. `BuildLabelProbeAlert` must own the current probe name, reserved labels, severity, and annotation; `ProbeLabelSurvival` and preview both call it. Replace private `rulePayload`, the inline injection map, the inline resolution map, and the inline probe plan in `client.go` with these functions. Do not change paths, auth, classification, or response parsing.

- [ ] **Step 4: Run OSCAR client tests**

Run: `gofmt -w internal/oscar/request.go internal/oscar/request_test.go internal/oscar/client.go internal/oscar/client_test.go && go test ./internal/oscar -count=1`

Expected: PASS, with live body equality tested for validate, create, and inject.

- [ ] **Step 5: Write failing ordered-lifecycle preview tests**

Define and test the preview interface:

```go
type OperationPreview struct {
	Stage string `json:"stage"`
	CaseCode string `json:"caseCode,omitempty"`
	Method string `json:"method"`
	Path string `json:"path"`
	Summary string `json:"summary"`
	Body string `json:"body,omitempty"`
	Attempt int `json:"attempt,omitempty"`
	ScheduledDelay time.Duration `json:"scheduledDelay,omitempty"`
	RuntimeFields []string `json:"runtimeFields,omitempty"`
}

func BuildOperationPreview(plan compiler.Plan, harnessVersion string) ([]OperationPreview, error)
```

Require the preview to start with `preflight.validate_rule`, `preflight.inject_label_probe`, `preflight.read_history`; compare the probe body with `BuildAlertRequest(BuildLabelProbeAlert(...))`; contain P01 and N01 validate/create operations whose bodies equal `CanonicalJSON(BuildRuleRequest(...))`; contain every alert injection using `BuildAlertRequest`; then evidence history/audit/notification operations; and finish with returned-ID deletes plus alert-resolution templates derived from `BuildResolutionRequest` using explicit `{server-fingerprint}`/ownership placeholders. Record `FindHistory`, `CorrelationAudit`, and `NotificationAudit` requests from the client and assert the preview uses the same route and query-field names after runtime values are normalized. Assert no operation string contains a target URL, `X-API-Key`, `Authorization`, or a fake numeric rule ID/fingerprint.

- [ ] **Step 6: Implement the lifecycle preview**

Build the ordered slice without network calls. Preserve each compiled alert's attempt order and scheduled delay in `Attempt` and `ScheduledDelay`. Use the real public-v1 paths:

```go
const (
	validateRulePath = "/api/v1/correlation_rules/validate"
	createRulePath = "/api/v1/correlation_rules"
	alertsPath = "/api/v1/alerts"
	historyPath = "/api/v1/alerts/history"
	correlationAuditPath = "/api/v1/correlation_rules/audit"
	notificationAuditPath = "/api/v1/notification-audit/"
)
```

For runtime-dependent paths/bodies, use readable markers such as `{returned-rule-id}` and `{server-fingerprint}` and list those exact names in `RuntimeFields`; do not serialize invented values. Evidence preview paths must show the same query contract used by the client: alertname/filter/time pagination on history, `fingerprint={server-fingerprint}` on correlation-rule audit, and `alert_fingerprint={server-fingerprint}` plus date bounds on notification audit. Include a no-body lifecycle note for the runtime’s final evidence transaction, but do not misrepresent SQLite persistence as an OSCAR call.

- [ ] **Step 7: Run preview and package tests**

Run:

```bash
gofmt -w internal/oscar/preview.go internal/oscar/preview_test.go
go test ./internal/oscar ./internal/runner ./internal/runtime -count=1
```

Expected: PASS and existing runner/client behavior unchanged.

- [ ] **Step 8: Commit shared requests and preview**

```bash
git add internal/oscar
git commit -m "feat: expose exact OSCAR request previews"
```

---

### Task 4: Compose a Credential-Free Authoring Service

**Files:**
- Create: `internal/authoring/catalog.go`
- Create: `internal/authoring/catalog_test.go`
- Create: `internal/authoring/service.go`
- Create: `internal/authoring/service_test.go`
- Modify: `internal/runtime/scenario_inspection.go`
- Modify: `internal/runtime/scenario_inspection_test.go`

**Interfaces:**
- Consumes: `scenario.PublicContract`, `scenario.LookupExample`, `scenario.Encode/Decode`, `compiler.Compile`, and `oscar.BuildOperationPreview`.
- Produces: `authoring.Catalog`, `authoring.Selection`, `authoring.Page`, `authoring.Inspection`, `authoring.New(harnessVersion string) Service`, `Service.Build(Selection) (Page, error)`, and `Service.Inspect(context.Context, []byte, string) (Inspection, error)`.

- [ ] **Step 1: Write failing catalog completeness tests**

Require five sections, five quickstart lessons, eight pattern chapters, the required views, all field/pattern references to resolve, and all examples to exist:

```go
func TestCatalogIsCompleteAndInternallyLinked(t *testing.T) {
	catalog := DefaultCatalog()
	if got := len(catalog.Lessons); got != 5 { t.Fatalf("lessons=%d", got) }
	if got := len(catalog.Patterns); got != 8 { t.Fatalf("patterns=%d", got) }
	if err := catalog.Validate(scenario.PublicContract(), scenario.AllExamples()); err != nil { t.Fatal(err) }
}
```

The narrative model must use stable IDs rather than template conditionals:

```go
type Lesson struct { ID, Title, Concept, Effect, Fragment, CommonMistake string; FieldIDs []string }
type PatternGuide struct { ID, Title, Behavior, ExpectedEvidence string; FixedSemantics, Configurable, Mistakes []string }
type Catalog struct { Sections []Section; Lessons []Lesson; Patterns []PatternGuide; AssertionNotes, ValidationNotes []Note }
```

- [ ] **Step 2: Implement and validate the narrative catalog**

Populate lessons `identity`, `cases`, `stimuli`, `assertions`, and `validate` from design section 6.1. Populate all pattern behavior, fixed semantics, configurable inputs, expected evidence, and false-positive/false-negative mistakes from sections 6.3–6.5. Populate assertion notes for the three supported kinds, zero-count final-window semantics, Phase A/B limitations, and outcomes `parent_emitted`, `suppressed_per_notifier`, and `released_no_trigger`. `Catalog.Validate` must reject unknown field IDs, unknown pattern IDs, missing basic/advanced examples, empty fragments, and duplicate section/lesson IDs.

- [ ] **Step 3: Write failing service purity and selection tests**

Define exact selection defaults and invalid values:

```go
type Selection struct { Section, Step, Pattern, Level, View string }

func DefaultSelection() Selection {
	return Selection{Section: "quickstart", Step: "identity", Pattern: "flood", Level: "basic", View: "yaml"}
}
```

Test `Service.Build(DefaultSelection())` returns canonical YAML, a compiled P01/N01 plan, exact operations, and the assembled quickstart YAML. Test every legal query value and reject an invalid section/step/pattern/level/view. Use an OSCAR HTTP recorder and a SQLite fake that panic if touched; none must be invoked.

- [ ] **Step 4: Implement deterministic read-only inspection**

Use a clearly non-live identity:

```go
const PreviewRunID = "crt_authoring_preview"
const PreviewShortToken = "PREVIEW1"

type Inspection struct {
	Document scenario.Scenario `json:"document"`
	Source string `json:"source"`
	Plan compiler.Plan `json:"plan"`
	Operations []oscar.OperationPreview `json:"operations"`
}

type Page struct {
	Selection Selection
	Catalog Catalog
	Contract scenario.Contract
	Example scenario.ExampleDefinition
	Inspection Inspection
}

type Service struct { harnessVersion string }
func New(harnessVersion string) Service
func (s Service) Build(selection Selection) (Page, error)
func (s Service) Inspect(ctx context.Context, source []byte, pipelineMode string) (Inspection, error)
```

`Build` normalizes only blank fields to defaults and rejects nonblank invalid fields. It looks up and encodes the server-known example, then calls `Inspect` with `phase_b_dispatch`. `Inspect` checks context, strictly decodes, compiles with the fixed preview identity, and builds OSCAR operations. It performs no random generation, persistence, credential resolution, or HTTP.

- [ ] **Step 5: Make runtime inspection delegate to the authoring service**

Replace the runtime-owned duplicate implementation with:

```go
type ScenarioInspection = authoring.Inspection

func (r *Runtime) InspectScenario(ctx context.Context, source []byte, pipelineMode string) (ScenarioInspection, error) {
	return authoring.New(r.Version()).Inspect(ctx, source, pipelineMode)
}
```

If the runtime version is not currently exported, add the narrow `Version() string` accessor beside other immutable runtime metadata; do not expose credentials or targets.

- [ ] **Step 6: Run authoring and runtime tests**

Run:

```bash
gofmt -w internal/authoring internal/runtime/scenario_inspection.go internal/runtime/scenario_inspection_test.go
go test ./internal/authoring ./internal/runtime ./internal/scenario ./internal/oscar -count=1
```

Expected: PASS with deterministic inspection output across repeated calls.

- [ ] **Step 7: Commit the authoring service**

```bash
git add internal/authoring internal/runtime/scenario_inspection.go internal/runtime/scenario_inspection_test.go
git commit -m "feat: compose target-free authoring previews"
```

---

### Task 5: Add the Authoring Route, Navigation, Quickstart, and Schema Reference

**Files:**
- Create: `internal/web/templates/authoring.html.tmpl`
- Create: `internal/web/templates/authoring_quickstart.html.tmpl`
- Create: `internal/web/templates/authoring_schema.html.tmpl`
- Create: `internal/web/static/js/authoring.js`
- Modify: `internal/web/server.go`
- Modify: `internal/web/templates/base.html.tmpl`
- Modify: `internal/web/static/css/pages.css`
- Modify: `internal/web/static/css/components.css`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/theme_contract_test.go`

**Interfaces:**
- Consumes: `authoring.New(info.Version).Build(authoring.Selection)`, `authoring.Page`, and current template/static embedding.
- Produces: server-rendered `GET /authoring` and primary Authoring navigation.

- [ ] **Step 1: Write failing route and no-JavaScript content tests**

Add tests for default content, every valid query dimension, invalid 404 behavior, nav selection, and target-free operation:

```go
func TestAuthoringRouteRendersCompleteDefaultWithoutDataSource(t *testing.T) {
	h := NewHandler(version.Info{Version: "test"})
	r := httptest.NewRequest(http.MethodGet, "/authoring", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK { t.Fatalf("status=%d", w.Code) }
	for _, text := range []string{"Scenario Authoring", "Document identity", "Assembled YAML", "apiVersion", "Open in Scenarios editor"} {
		if !strings.Contains(w.Body.String(), text) { t.Errorf("missing %q", text) }
	}
}
```

Request invalid structural values such as `pattern=typo`, `level=expert`, and `view=secret`; require 404, not a fallback selection.

- [ ] **Step 2: Run web tests and confirm `/authoring` is missing**

Run: `go test ./internal/web -run 'TestAuthoring|TestNavigation' -count=1`

Expected: FAIL with 404 or missing expected content.

- [ ] **Step 3: Add typed page data and query parsing**

Add `Authoring *authoring.Page` to `pageData` and a closed parser:

```go
func authoringSelection(values url.Values) authoring.Selection {
	return authoring.Selection{
		Section: values.Get("section"), Step: values.Get("step"),
		Pattern: values.Get("pattern"), Level: values.Get("level"), View: values.Get("view"),
	}
}
```

Register `GET /authoring` before the fallback routes. Call `authoring.New(info.Version).Build`; render 404 for selection errors and 500 for catalog/generation failures. This route must work in `NewHandler` with `data == nil`.

- [ ] **Step 4: Build the server-rendered workspace**

Add Authoring to primary nav immediately after Scenarios. Render:

- a persistent left outline for Quickstart, Schema, Patterns, Assertions, and Validation;
- five ordinary step links with `aria-current="step"`;
- the lesson concept, effect, introduced YAML, requirement chips, and common mistake;
- a sticky assembled-YAML panel with copy and `/scenarios?selected=example:<pattern>:<level>` links;
- a grouped field table with all contract columns and a native GET filter fallback.

All links must preserve the current legal selection values. Every interactive icon has visible text or an accessible name. Do not place JSON/YAML in an HTML attribute.

- [ ] **Step 5: Add progressive enhancement and dense-console styling**

Create `authoring.js` with only these enhancements:

```js
function copyBlock(button) {
  const source = document.getElementById(button.dataset.copyTarget);
  return navigator.clipboard.writeText(source.textContent).then(() => {
    button.textContent = "Copied";
  });
}

function filterRows(input) {
  const query = input.value.trim().toLowerCase();
  document.querySelectorAll("[data-schema-row]").forEach((row) => {
    row.hidden = !row.dataset.searchValue.includes(query);
  });
}
```

Bind with `addEventListener`, tolerate missing Clipboard API by leaving selection usable, and avoid inline handlers. Add responsive two-column/one-column layouts, sticky panels only at desktop widths, existing token-based colors, visible focus, wrapped code blocks, and reduced-motion compliance.

- [ ] **Step 6: Run route, theme, and full web tests**

Run:

```bash
gofmt -w internal/web/server.go internal/web/server_test.go
go test ./internal/web -count=1
git diff --check
```

Expected: PASS; default and deep-linked content is present in raw HTML without JavaScript.

- [ ] **Step 7: Commit the authoring shell**

```bash
git add internal/web
git commit -m "feat: add scenario authoring workspace"
```

---

### Task 6: Render the Eight-Pattern Cookbook and Exact API/Lifecycle Views

**Files:**
- Create: `internal/web/templates/authoring_pattern.html.tmpl`
- Create: `internal/web/templates/authoring_preview.html.tmpl`
- Modify: `internal/web/templates/authoring.html.tmpl`
- Modify: `internal/web/templates/authoring_quickstart.html.tmpl`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/static/css/pages.css`
- Modify: `internal/web/static/js/authoring.js`

**Interfaces:**
- Consumes: `authoring.Page.Example`, `.Inspection.Plan`, `.Inspection.Operations`, and the catalog’s pattern/assertion/validation narratives.
- Produces: linkable pattern/level/view content for all 16 examples and four preview modes.

- [ ] **Step 1: Write failing 16-example and API-preview rendering tests**

Loop over all patterns and both levels. Require the YAML, compiled P01/N01 rules, exact rule request keys, lifecycle stages, and server-known editor link. Add negative credential tests:

```go
for _, pattern := range scenario.SupportedPatterns() {
	for _, level := range []string{"basic", "advanced"} {
		path := fmt.Sprintf("/authoring?section=patterns&pattern=%s&level=%s&view=api", pattern, level)
		body := serve(t, handler, path)
		for _, want := range []string{"POST", "/api/v1/correlation_rules/validate", "window_seconds", "match_criteria", "Open in Scenarios editor"} {
			if !strings.Contains(body, want) { t.Errorf("%s missing %q", path, want) }
		}
		for _, forbidden := range []string{"X-API-Key", "Authorization: Bearer", "api_key"} {
			if strings.Contains(body, forbidden) { t.Errorf("%s leaked %q", path, forbidden) }
		}
	}
}
```

For `parent_child`, assert the copy explicitly says there is no synthetic `emit_spec`; do not require an emitted rule field that the compiler omits.

- [ ] **Step 2: Run the focused rendering tests**

Run: `go test ./internal/web -run 'TestAuthoring(Pattern|APIPreview|Lifecycle)' -count=1`

Expected: FAIL because cookbook/API/lifecycle partials are not rendered.

- [ ] **Step 3: Render cookbook navigation and teaching content**

Render the eight pattern links, basic/advanced level switch, behavior, fixed semantics, configurable fields, expected evidence, mistakes, and canonical P01/N01 YAML. Clearly label current fixed compiler values: flood 5, persistence 30 seconds, absence expected 10/absent 30/observe 55, cross-source `snmp` plus `api`, and threshold distinct `device` count 3.

Render Assertions and Validation sections from catalog data, including exact-zero final-snapshot semantics, Phase A/B limitations, strict YAML constraints, protected labels, budgets, and the polarity-aware P01/N01 matrix.

- [ ] **Step 4: Render compiled contract, exact API JSON, and lifecycle**

Provide ordinary links for `view=yaml|contract|api|lifecycle` and server-render every view:

- `yaml`: canonical encoded source;
- `contract`: compiled case/rule/alert names, pattern, window, group labels, match criteria, emit spec, assertions, and observation budgets;
- `api`: each operation’s method/path and escaped pretty JSON body from `OperationPreview.Body`;
- `lifecycle`: ordered preflight, mutation, observation, verdict persistence, and cleanup stages.

Display `{returned-rule-id}` and `{server-fingerprint}` as runtime-dependent values with explanatory badges. Add this exact clarification near the lifecycle:

> CorrTest creates two temporary correlation rules (P01 and N01), injects source alerts directly through the public alert API, observes OSCAR evidence, deletes only the returned rule IDs, and resolves its injected alerts. It does not create ordinary OSCAR alert rules.

- [ ] **Step 5: Enhance view switching without hiding link fallbacks**

In `authoring.js`, intercept a view link only when its target panel already exists in the rendered document; otherwise allow normal navigation. Update `aria-selected`, `hidden`, and the browser query string with `history.replaceState`. No preview content may be generated in JavaScript.

- [ ] **Step 6: Run web and contract tests**

Run:

```bash
go test ./internal/web ./internal/authoring ./internal/oscar ./internal/scenario -count=1
git diff --check
```

Expected: PASS for every pattern/level/view combination.

- [ ] **Step 7: Commit cookbook and previews**

```bash
git add internal/web
git commit -m "feat: teach patterns and OSCAR request lifecycle"
```

---

### Task 7: Integrate Authoring with Scenarios, Reference, and Context Help

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/templates/scenarios.html.tmpl`
- Modify: `internal/web/templates/reference.html.tmpl`
- Modify: `internal/web/templates/help_drawer.html.tmpl`
- Modify: `internal/web/help.go`
- Modify: `internal/web/help_test.go`
- Modify: `internal/web/static/js/scenarios.js`
- Modify: `internal/web/static/css/pages.css`

**Interfaces:**
- Consumes: `scenario.LookupExample`, `runtime.ScenarioInspection` (now an alias of `authoring.Inspection`), and stable Authoring query URLs.
- Produces: `example:<pattern>:<level>` Scenarios selections, API/lifecycle tabs for validated drafts, and deep links from Reference/help.

- [ ] **Step 1: Write failing GET-only example handoff tests**

Use a data source spy whose scenario listing is allowed but whose import, target, credential, run, and network methods panic. Request `/scenarios?selected=example:flood:advanced` and require:

```go
	for _, want := range []string{
	"flood-advanced", "unsaved example", "corrtest-flood-p01-preview1",
	"OSCAR API JSON", "Lifecycle", "/authoring?section=patterns&amp;pattern=flood&amp;level=advanced",
} {
	if !strings.Contains(body, want) { t.Errorf("missing %q", want) }
}
```

Require invalid example IDs to return 404 and require repeated GETs to leave the imported-scenario count unchanged.

- [ ] **Step 2: Run the scenario integration tests**

Run: `go test ./internal/web -run 'TestScenarioExample|TestScenarioInspectionViews' -count=1`

Expected: FAIL because `example:` selection and request views are absent.

- [ ] **Step 3: Extend scenario selection without persistence**

Extend `scenarioWorkbench` with an exact three-part `example:<pattern>:<level>` parser. Use `scenario.LookupExample`, canonical `scenario.Encode`, and `scenarioInspector.InspectScenario`; mark it as a draft-like, non-deletable, non-persisted selection. Add fields to `pageData` only as needed:

```go
	ScenarioInspection *appruntime.ScenarioInspection
	SelectedExample bool
	SelectedLevel string
```

Retain `ScenarioPlan` temporarily only if another template still consumes it; otherwise replace it with `ScenarioInspection.Plan` in the same commit. Update both `POST /scenarios` branches (`action=preview` and the post-import inspection) to store the complete successful inspection, not only its `Plan`, so validated custom source immediately exposes API and lifecycle views. On inspection error, store neither the plan nor request operations.

- [ ] **Step 4: Add Scenarios authoring and inspection controls**

Place a visible **Scenario Authoring Guide** link next to editor guidance and a pattern-specific tutorial link when a document has validated. After successful inspection, render tabs for Compiled contract, OSCAR API JSON, and Lifecycle using the same `OperationPreview` data and shared preview partial; never reconstruct request maps in the Scenarios template or JavaScript.

For example selections, label the source **Unsaved example** and keep the existing import/save workflow explicit. Do not automatically import when the page opens.

- [ ] **Step 5: Add stable Reference and Page Reference links**

Update Reference with concise links to:

```text
/authoring?section=quickstart
/authoring?section=schema
/authoring?section=assertions
/authoring?section=validation
/authoring?section=patterns&pattern=<each-supported-pattern>
```

Populate the existing `HelpTopic.Links []HelpLink` contract for Scenarios and the new Authoring topic, then render those links in the help drawer while retaining the full Reference fallback. Add catalog validation/tests that reject blank labels, unknown local routes, and off-site help URLs.

- [ ] **Step 6: Run integration and accessibility contract tests**

Run:

```bash
gofmt -w internal/web/server.go internal/web/server_test.go internal/web/help.go internal/web/help_test.go
go test ./internal/web -count=1
```

Expected: PASS; HTML contains ordinary fallback links, accessible tab semantics, and no inline event handlers.

- [ ] **Step 7: Commit cross-page integration**

```bash
git add internal/web
git commit -m "feat: connect authoring guidance to scenario workflows"
```

---

### Task 8: Update Operator Documentation, Packaging, and Release Gates

**Files:**
- Modify: `README.md`
- Modify: `docs/scenario-authoring.md`
- Modify: `docs/builtins.md`
- Modify: `docs/operator.md`
- Modify: `internal/docs/operator_docs_test.go`
- Modify: `scripts/package_contract_test.go`

**Interfaces:**
- Consumes: final routes, supported field/pattern contract, exact runtime lifecycle, and generated schema command.
- Produces: installable documentation and a verified release archive containing the guide and schema.

- [ ] **Step 1: Write failing documentation contract tests**

Require operator docs to name the Authoring route, all eight patterns, both example levels, the exact two-rule/no-ordinary-rule lifecycle, and schema generation command:

```go
required := []string{
	"/authoring", "basic", "advanced", "P01", "N01",
	"two temporary correlation rules", "does not create ordinary OSCAR alert rules",
	"go run ./cmd/generate-scenario-schema",
}
```

Extend package tests to require `docs/scenario-authoring.md`, `docs/builtins.md`, `docs/operator.md`, and `docs/schema/correlation-scenario.schema.json` in every archive. Assert the packaged schema byte-for-byte matches `scenario.GenerateJSONSchema()`.

- [ ] **Step 2: Run docs/package tests and observe missing contract copy**

Run: `go test ./internal/docs ./scripts -count=1`

Expected: FAIL until the documentation and package assertions are updated.

- [ ] **Step 3: Update the user and operator documentation**

Document:

- how to open Authoring and navigate Quickstart, Schema, Patterns, Assertions, and Validation;
- how basic and advanced examples open as unsaved Scenarios drafts;
- all supported YAML fields and where the generated schema lives;
- P01 positive proof versus N01 negative control;
- the exact preflight/live/cleanup lifecycle and runtime-dependent IDs;
- that previews are credential-free and do not prove target compatibility;
- Phase A audit-only versus Phase B synthetic-parent evidence;
- the command `go run ./cmd/generate-scenario-schema` and drift test.

Keep README concise and link to the full packaged guide. Update built-in descriptions with pattern tutorial deep links.

- [ ] **Step 4: Run focused docs and package verification**

Run:

```bash
go test ./internal/docs ./scripts -count=1
go run ./cmd/generate-scenario-schema
git diff --exit-code docs/schema/correlation-scenario.schema.json
./scripts/check-package.sh
```

Expected: PASS and schema generation produces no diff.

- [ ] **Step 5: Run full correctness and release gates**

Run:

```bash
go test -race ./...
make clean release-gate
git diff --check
git status --short
```

Expected: all commands exit 0; only the intentional implementation/docs changes are present before commit; release archives include the authoring docs and generated schema.

- [ ] **Step 6: Perform browser acceptance on both themes**

Run `go run ./cmd/oscar-corrtest serve`, then verify at desktop and narrow widths:

1. `/authoring` renders all five lessons without JavaScript.
2. All eight pattern chapters and both levels produce YAML, contract, API, and lifecycle views.
3. Copy buttons work; keyboard focus remains visible; code scrolls without moving the page.
4. `/scenarios?selected=example:flood:advanced` is an unsaved editable draft and shows the same API/lifecycle values.
5. Reference and Page Reference links land on the selected section.
6. No page shows a target URL, API key, authorization header, fabricated rule ID, or fabricated fingerprint.
7. Dark and light themes retain readable tables, chips, code blocks, and sticky panels.

- [ ] **Step 7: Commit documentation and final verification changes**

```bash
git add README.md docs internal/docs scripts
git commit -m "docs: publish scenario authoring tutorial"
```

## Final acceptance checklist

- [ ] `GET /authoring` is available from primary navigation and works without a data source or JavaScript.
- [ ] Every accepted YAML property has one typed reference entry and one generated-schema definition.
- [ ] All sixteen examples strictly decode, pass pattern-aware validation, and compile.
- [ ] The live OSCAR client and preview use the same typed rule and alert request builders.
- [ ] API/lifecycle previews expose exact request bodies and honest runtime placeholders without credentials.
- [ ] Scenarios opens server-known examples as unsaved drafts and shows compiled/API/lifecycle views after validation.
- [ ] Reference and Page Reference link to stable Authoring sections and pattern chapters.
- [ ] `go test -race ./...`, `make clean release-gate`, schema drift, package checks, and browser acceptance all pass.
