# Scenario Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all built-in scenarios inspectable and clonable while giving technical users a three-pane workbench for source, compiled P01/N01 behavior, labels, assertions, and validation.

**Architecture:** Canonical YAML encoding is added to the existing strict scenario model. A target-free runtime inspection operation parses, validates, and compiles a document without mutation. The web layer uses one selected-scenario model for built-ins and imported scenarios, then renders the approved catalog/source/contract panes with progressive enhancement.

**Tech Stack:** Existing Go scenario compiler and YAML dependency, server-rendered templates, small embedded JavaScript, existing SQLite scenario records.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- Built-ins remain immutable and are identified as `builtin:<pattern-code>`.
- Imported/custom scenarios retain current SQLite persistence and are identified as `imported:<id>`.
- No database migration; existing `ScenarioRecord.SourceDocument` remains authoritative for imported source.
- Preview and validation are target-free and perform no OSCAR or database mutation.
- Built-in YAML must round-trip through the same strict decoder and compiler used for imported YAML.
- Cloning always creates a new imported scenario and never edits a built-in in place.

---

### Task 1: Produce canonical YAML for every built-in

**Files:**
- Create: `internal/scenario/encode.go`
- Create: `internal/scenario/encode_test.go`
- Modify: `internal/scenario/builtin_test.go`

**Interfaces:**
- Produces: `scenario.Encode(document scenario.Document) ([]byte, error)`.
- Produces: `scenario.BuiltinSource(patternCode string) ([]byte, error)`.
- Guarantees: stable field order, two-space indentation, LF line endings, and no YAML aliases.

- [ ] **Step 1: Write built-in round-trip tests**

```go
func TestEveryBuiltinCanonicalYAMLRoundTrips(t *testing.T) {
    for _, builtin := range scenario.Builtins() {
        source, err := scenario.BuiltinSource(builtin.PatternCode)
        decoded, err := scenario.DecodeStrict(source)
        // Compare the semantic document and compile both polarities.
    }
}
```

Require all eight patterns, both P01 and N01 cases, reserved naming/labels,
event identity, assertion values, cleanup policy, and stable repeated encoding.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/scenario -run 'Canonical|RoundTrip'`

Expected: FAIL because canonical encoding does not exist.

- [ ] **Step 3: Implement encoding through the existing wire model**

Reuse the decoder's explicit wire structs instead of marshaling runtime-only
types. Sort map keys at conversion boundaries and reject documents that cannot
be represented by the public v1 schema.

- [ ] **Step 4: Run scenario package tests**

Run: `go test ./internal/scenario -count=20`

Expected: PASS with byte-stable YAML.

- [ ] **Step 5: Commit**

```bash
git add internal/scenario
git commit -m "feat: encode canonical scenario YAML"
```

### Task 2: Add target-free scenario inspection

**Files:**
- Create: `internal/runtime/scenario_inspection.go`
- Create: `internal/runtime/scenario_inspection_test.go`
- Modify: `internal/runtime/runtime.go`

**Interfaces:**
- Produces: `ScenarioInspection{Document scenario.Document; Source string; Cases []CaseInspection; Diagnostics []Diagnostic}`.
- Produces: `CaseInspection{Code, Polarity, AlertName, Category string; Rule any; Alerts []domain.Alert; Assertions []scenario.Assertion}`.
- Produces: `Runtime.InspectScenario(ctx context.Context, source []byte, pipelineMode string) (ScenarioInspection, error)`.
- Guarantees: no repository write, OSCAR client construction, or secret lookup.

- [ ] **Step 1: Write inspection behavior tests**

```go
func TestInspectScenarioCompilesBothPolaritiesWithoutTarget(t *testing.T) {
    // Inspect canonical flood YAML and assert P01/N01 rules, distinct firing
    // fingerprints, resolved identity reuse, labels, names, and assertions.
}
```

Cover strict YAML errors with line/field diagnostics, unsupported pipeline
mode, invalid labels, unknown assertion operations, and deterministic output.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/runtime -run InspectScenario`

Expected: FAIL because the inspection operation is absent.

- [ ] **Step 3: Implement parse/validate/compile inspection**

Call the same parser, validator, naming, alert builder, rule compiler, and
assertion compiler used by execution. Convert errors into bounded diagnostics
without echoing full source lines or credential values.

- [ ] **Step 4: Add no-side-effect regression**

Inject repositories and OSCAR-client factories that panic when touched, run
inspection, and require success. Run: `go test -race ./internal/runtime -run InspectScenario`.

Expected: PASS without the panic fakes being called.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime
git commit -m "feat: add target-free scenario inspection"
```

### Task 3: Build the three-pane scenario catalog and clone flow

**Files:**
- Create: `internal/web/static/js/scenarios.js`
- Modify: `internal/web/templates/scenarios.html.tmpl`
- Modify: `internal/web/static/css/app.css`
- Modify: `internal/web/assets.go`
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/view.go`
- Modify: `internal/web/view_test.go`

**Interfaces:**
- Adds: `GET /scenarios?selected=builtin:<code>|imported:<id>`.
- Adds: `POST /scenarios/clone` with CSRF and form field `scenario_ref`.
- Preserves: existing scenario import endpoint and strict validation behavior.
- Adds: workbench view types for catalog rows, source, diagnostics, and compiled P01/N01 tabs.

- [ ] **Step 1: Write catalog, selection, and clone tests**

```go
func TestScenarioPagePreviewsBuiltinSourceAndContract(t *testing.T) {
    // Select builtin:flood; require canonical YAML, P01/N01, alertname,
    // category, oscar_test_run_id, pattern label, sequence identity,
    // assertions, and a CSRF-protected clone action.
}
```

Also test imported selection, unknown/stale references, HTML escaping, failed
clone rollback, clone name collision handling, and a no-JavaScript source view.

- [ ] **Step 2: Run web tests and verify failure**

Run: `go test ./internal/web -run Scenario`

Expected: FAIL because built-in preview and clone routes do not exist.

- [ ] **Step 3: Build the unified catalog model**

List built-ins first in pattern-code order and imported scenarios newest first.
Resolve one selection, obtain canonical/source YAML, call target-free inspection,
and preserve the selection in redirects. Limit preview source to the existing
scenario document size boundary.

- [ ] **Step 4: Implement cloning**

Accept only a built-in reference, fetch canonical YAML server-side, assign a
collision-free custom name, persist through the existing scenario repository,
then redirect to the imported selection. Never trust source submitted by the
clone form.

- [ ] **Step 5: Render the approved three-pane workbench**

Desktop panes are catalog, source/validation, and compiled contract. Small
screens use accessible pane tabs. Add copy buttons for source and names, P01/N01
tabs, rule/alert/assertion sections, and inline guidance. JavaScript enhances
copying and pane/tab switching only.

- [ ] **Step 6: Run the plan gate**

Run: `go test -race ./internal/scenario ./internal/runtime ./internal/web`

Expected: PASS, including source round-trip and clone regressions.

- [ ] **Step 7: Commit**

```bash
git add internal/scenario internal/runtime internal/web
git commit -m "feat: add scenario inspection workbench"
```
