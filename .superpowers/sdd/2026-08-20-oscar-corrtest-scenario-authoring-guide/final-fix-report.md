# Scenario Authoring Guide final fix round

Date: 2026-08-20

Base: `e9a4147`

Status: complete; all five confirmed Important findings fixed and verified

## 1. Equal-delay semantic ordering

Commit: `dad581a fix: preserve equal-delay semantic order`

Changed files:

- `internal/scenario/semantics.go`
- `internal/scenario/semantics_test.go`

RED evidence:

```text
$ go test ./internal/scenario -run TestPatternSemanticValidationUsesDeclarationOrderForEqualDelayPrecedence -count=1
--- FAIL: TestPatternSemanticValidationUsesDeclarationOrderForEqualDelayPrecedence
    --- FAIL: .../sequence_P01_ordered
        P01 sequence must order login_failure before privileged_command in one group
    --- FAIL: .../parent_child_P01_ordered
        P01 parent_child requires an active parent before a child in one group
FAIL
```

GREEN evidence:

```text
$ go test ./internal/scenario -run 'TestPatternSemanticValidationUsesDeclarationOrderForEqualDelayPrecedence|TestPatternSemanticValidationKeepsEqualAndIncreasingDelayOrder|TestParentChild' -count=1
ok github.com/cmetech/oscar-corrtest/internal/scenario
$ go test ./internal/scenario ./internal/compiler -count=1
ok github.com/cmetech/oscar-corrtest/internal/scenario
ok github.com/cmetech/oscar-corrtest/internal/compiler
```

Result: semantic observations retain their original ordinal and precedence uses stable `(delay, ordinal)` ordering. Equal-delay P01 declaration order triggers for sequence and parent/child, while equal-delay reversed N01 declaration order remains non-triggering.

## 2. Pipeline-mode fidelity in Scenarios inspection

Commit: `f91086f fix: preserve scenario inspection pipeline mode`

Changed files:

- `internal/authoring/service.go`
- `internal/authoring/service_test.go`
- `internal/web/server.go`
- `internal/web/server_test.go`
- `internal/web/templates/authoring_preview.html.tmpl`
- `internal/web/templates/scenarios.html.tmpl`

RED evidence:

```text
$ go test ./internal/authoring -run TestServiceInspectRetainsValidatedPipelineMode -count=1
internal/authoring/service_test.go: inspection.PipelineMode undefined
internal/authoring/service_test.go: undefined: PipelineModePhaseAAuditOnly
FAIL

$ go test ./internal/web -run TestScenarioInspectionPreservesPipelineModeAcrossGETPOSTAndValidationFailure -count=1
--- FAIL: TestScenarioInspectionPreservesPipelineModeAcrossGETPOSTAndValidationFailure
    GET did not render the default Phase B selection and inspection
FAIL
```

GREEN evidence:

```text
$ go test ./internal/web -run TestScenarioInspectionPreservesPipelineModeAcrossGETPOSTAndValidationFailure -count=1
ok github.com/cmetech/oscar-corrtest/internal/web
$ go test ./internal/authoring ./internal/runtime ./internal/web -count=1
ok github.com/cmetech/oscar-corrtest/internal/authoring
ok github.com/cmetech/oscar-corrtest/internal/runtime
ok github.com/cmetech/oscar-corrtest/internal/web
```

Result: `authoring.Inspection` retains a closed, validated `PipelineMode`. GET/default Authoring examples remain Phase B. Scenarios POST retains the submitted mode and source on success and validation failure, renders the actual mode, and prominently labels Phase A as audit-only and unable to prove dispatched synthetic-parent/notifier evidence. Phase A uses the same target-free structural request preview; no alternate OSCAR requests are fabricated.

## 3. Lifecycle order

Commit: `41c661e fix: align preview lifecycle with final persistence`

Changed files:

- `internal/oscar/preview.go`
- `internal/oscar/preview_test.go`
- `internal/web/server_test.go`
- `internal/web/templates/authoring_preview.html.tmpl`
- `docs/scenario-authoring.md`

RED evidence:

```text
$ go test ./internal/oscar -run 'TestBuildOperationPreviewShowsOrderedCredentialFreeLifecycle|TestPreviewEvaluatesAssertionsBeforeCleanupAndPersistsTerminalEvidenceLast' -count=1
--- FAIL: TestBuildOperationPreviewShowsOrderedCredentialFreeLifecycle
    last operation={Stage:cleanup.resolve_alert ...}
--- FAIL: TestPreviewEvaluatesAssertionsBeforeCleanupAndPersistsTerminalEvidenceLast
    evaluate=31 first cleanup=33 ... persist=32 operations=46
FAIL
```

GREEN evidence:

```text
$ go test ./internal/oscar -run 'TestBuildOperationPreviewShowsOrderedCredentialFreeLifecycle|TestPreviewEvaluatesAssertionsBeforeCleanupAndPersistsTerminalEvidenceLast' -count=1
ok github.com/cmetech/oscar-corrtest/internal/oscar
$ go test ./internal/oscar ./internal/web ./internal/docs -count=1
ok github.com/cmetech/oscar-corrtest/internal/oscar
ok github.com/cmetech/oscar-corrtest/internal/web
ok github.com/cmetech/oscar-corrtest/internal/docs
```

Result: preview and documentation now match the live runner: evaluate assertions, delete rules and resolve alerts, then persist normalized assertions, cleanup outcome, terminal facts, and evidence. `evidence.evaluate_assertions` remains before final persistence.

## 4. Stable deep links that land

Commit: `dcc4cbe fix: anchor authoring deep links`

Changed files:

- `internal/web/templates/authoring.html.tmpl`
- `internal/web/templates/authoring_schema.html.tmpl`
- `internal/web/templates/authoring_pattern.html.tmpl`
- `internal/web/templates/authoring_preview.html.tmpl`
- `internal/web/templates/scenarios.html.tmpl`
- `internal/web/help.go`
- `internal/web/server_test.go`
- `docs/builtins.md`
- `internal/docs/operator_docs_test.go`

RED evidence:

```text
$ go test ./internal/web -run TestAuthoringQueryLinksLandOnUniqueServerRenderedFragmentTargets -count=1
--- FAIL: TestAuthoringQueryLinksLandOnUniqueServerRenderedFragmentTargets
    Authoring content link has no fragment target: /authoring?section=quickstart
    Authoring content link has no fragment target: /authoring?section=schema
    Authoring content link has no fragment target: /authoring?section=patterns&pattern=sequence
    ...
FAIL
```

GREEN evidence:

```text
$ go test ./internal/web -run TestAuthoringQueryLinksLandOnUniqueServerRenderedFragmentTargets -count=1
ok github.com/cmetech/oscar-corrtest/internal/web
$ go test ./internal/web ./internal/docs -count=1
ok github.com/cmetech/oscar-corrtest/internal/web
ok github.com/cmetech/oscar-corrtest/internal/docs
```

Result: sections, lessons, selected pattern chapters, and inspection views expose unique server-rendered fragment targets. Outline, lesson, cookbook, depth/view, Scenarios, Reference/help, and packaged documentation links append matching fragments. The navigation test follows discovered links without JavaScript and requires exactly one rendered target. Nonselected fixed-order sections and lessons remain rendered, and invalid query selections still return not found.

## 5. Public JSON Schema fidelity

Commit: `a8a319a fix: tighten public scenario schema`

Changed files:

- `internal/scenario/schema.go`
- `internal/scenario/schema_contract_test.go`
- `internal/authoring/catalog.go`
- `docs/scenario-authoring.md`
- `docs/schema/correlation-scenario.schema.json`

RED evidence:

```text
$ go test ./internal/scenario -run 'TestGeneratedJSONSchemaRejectsReservedLabelsAndPatternRestrictedNotifiers|TestGeneratedJSONSchemaRejectsLabelsOnResolvedEventsAndDocumentsSemanticLimits' -count=1
--- FAIL: TestGeneratedJSONSchemaRejectsReservedLabelsAndPatternRestrictedNotifiers
    case label property names do not reject reserved keys
--- FAIL: TestGeneratedJSONSchemaRejectsLabelsOnResolvedEventsAndDocumentsSemanticLimits
    schema description missing "cross-array notifier disjointness"
    event schema has no status-aware conditional
FAIL
```

GREEN evidence:

```text
$ go test ./internal/scenario -run 'TestGeneratedJSONSchemaRejectsReservedLabelsAndPatternRestrictedNotifiers|TestGeneratedJSONSchemaRejectsLabelsOnResolvedEventsAndDocumentsSemanticLimits' -count=1
ok github.com/cmetech/oscar-corrtest/internal/scenario
$ go test ./internal/scenario ./internal/compiler ./internal/authoring ./internal/web ./internal/docs -count=1
ok github.com/cmetech/oscar-corrtest/internal/scenario
ok github.com/cmetech/oscar-corrtest/internal/compiler
ok github.com/cmetech/oscar-corrtest/internal/authoring
ok github.com/cmetech/oscar-corrtest/internal/web
ok github.com/cmetech/oscar-corrtest/internal/docs
```

Schema drift evidence:

```text
$ shasum -a 256 docs/schema/correlation-scenario.schema.json
7ea997a1d7324491cddfaed693fa38482916474e969b7fc647a020f7f87e910b
$ go run ./cmd/generate-scenario-schema
$ shasum -a 256 docs/schema/correlation-scenario.schema.json
7ea997a1d7324491cddfaed693fa38482916474e969b7fc647a020f7f87e910b
```

Result: the schema rejects all compiler/transport-reserved case and event label keys, rejects notifier fields for every non-`parent_child` root pattern, and rejects labels on resolved events. Existing closed-field, P01/N01 pairing, stimulus-choice, and conditional assertion behavior remains. Schema and guide descriptions explicitly identify cross-array notifier disjointness, prior-firing/event ordering, cross-field timing, case-name uniqueness, YAML-only protections, and pattern semantics as strict-decoder checks that standard draft 2020-12 cannot accurately express.

## Full verification

```text
$ go test ./internal/scenario ./internal/compiler ./internal/oscar ./internal/authoring ./internal/runtime ./internal/web ./internal/docs -count=1
PASS: all seven packages

$ go test -race ./...
PASS: all packages; no race reports

$ make clean release-gate
PASS
- gofmt/module/vet gates passed
- gosec: 68 files, 13,778 lines, 0 issues
- govulncheck: No vulnerabilities found
- full unit/integration and race suites passed
- standalone smoke passed
- Linux, macOS, and Windows package builds passed
- package-content, container, POSIX installer, release-script, and reproducibility checks passed

$ git diff --check e9a4147..HEAD
PASS
```

## Self-review

- Reviewed the full `e9a4147..a8a319a` change set for scope, stale hard-coded Phase B output, lifecycle-order contradictions, unanchored internal Authoring content links, duplicate IDs, generated-schema drift, and whitespace errors.
- Preserved target-free inspection and default Phase B Authoring examples.
- Did not alter OSCAR request bodies for Phase A; only the retained mode and truthful limitations differ.
- Kept assertion evaluation before cleanup/final persistence and placed terminal persistence after all cleanup previews.
- Kept all fixed-order Authoring sections and lessons visible without JavaScript.
- Documented rather than approximated JSON Schema constraints that require comparisons or ordered semantic state.
- Left the final review's UX-only minor and previously deferred minors unchanged.
- No unresolved blocker or known concern remains in this fix round.
