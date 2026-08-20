# OSCAR CorrTest Scenario Authoring Guide Design

**Date:** 2026-08-20  
**Status:** Approved for implementation planning  
**Scope:** Interactive scenario tutorial, schema reference, pattern cookbook, and exact OSCAR API preview

## 1. Purpose

CorrTest currently exposes canonical built-in YAML and a compiled P01/N01 contract in the Scenarios workbench, while the release archive contains a short Markdown authoring guide and a JSON Schema. Those assets are not discoverable enough in the running application and do not teach an operator how to construct a valid scenario from first principles. The existing Reference page describes pattern intent but does not document every YAML property, conditional requirement, safe budget, pattern restriction, or OSCAR wire effect.

This feature adds a dedicated, technical Scenario Authoring workspace. It teaches the closed CorrTest scenario language progressively, provides basic and advanced examples for every supported pattern, and shows the exact JSON and HTTP lifecycle CorrTest will use against OSCAR. Examples can be copied or opened as unsaved drafts in the existing Scenarios editor.

The central correctness requirement is that documentation, examples, validation, compilation, and live OSCAR serialization cannot silently drift apart.

## 2. Goals

1. Let an operator with OSCAR knowledge but no CorrTest schema knowledge build a valid scenario.
2. Document every accepted YAML property as required, optional, or conditionally required.
3. Explain the semantic difference between P01 positive proof and N01 nearby negative control.
4. Teach both supported stimulus forms: `role` plus `repeat`, or explicit `events`.
5. Provide a basic and advanced executable example for all eight correlation patterns.
6. Explain which pattern semantics are fixed by the current compiler and which scenario properties are configurable.
7. Show the structurally exact OSCAR correlation-rule and alert-injection JSON produced from an example or validated custom draft.
8. Show preflight, mutation, evidence, and cleanup operations in their actual order.
9. Open examples in the Scenarios editor without persistence or OSCAR contact.
10. Preserve the existing dense engineering-console visual language, dark/light themes, keyboard access, and progressive enhancement.

## 3. Non-goals

- The Authoring page is not a second YAML editor. Editing remains in Scenarios.
- CorrTest does not become a general-purpose OSCAR correlation-rule authoring interface.
- This feature does not expose arbitrary OSCAR match-criteria JSON in scenario YAML.
- Tutorial progress is not stored in SQLite or user preferences.
- The API preview does not contact a target, validate credentials, or promise target compatibility.
- Preview output never includes a target URL, API key, or other credential material.
- This feature does not create ordinary OSCAR alert rules. Test source alerts continue to be injected through the public alert API.
- This feature does not change evidence verdict semantics or resource ownership rules.

## 4. Decisions and alternatives

### 4.1 Dedicated workspace

Add **Authoring** to primary navigation after Scenarios. This was selected over expanding the existing Reference page or placing the complete tutorial in a drawer.

The dedicated workspace gives the tutorial, schema, and sixteen examples enough space while keeping Scenarios focused on source editing and compiled inspection. Scenarios and Reference link into the relevant authoring section.

### 4.2 Typed contract plus executable examples

Use a typed authoring catalog backed by the scenario package rather than static template copy or JSON-Schema-only generation.

- Typed field metadata supplies the UI schema reference and packaged JSON Schema.
- Validator constants and schema metadata share enums and safe-budget constants.
- Basic examples are the canonical built-ins.
- Advanced examples are typed scenario values, encoded by `scenario.Encode`.
- Every example is strictly decoded and compiled in tests.
- Narrative teaching copy is maintained separately from structural metadata, but references stable field and pattern IDs verified by tests.

JSON Schema alone is insufficient because it cannot adequately teach P01/N01 intent, evidence semantics, fixed compiler behavior, or the compiled OSCAR lifecycle. Static templates alone are insufficient because they can drift from the decoder and client.

### 4.3 Shared OSCAR request builders

Extract typed, credential-free request builders from the OSCAR client. The live client and preview renderer consume the same request values. Templates never recreate OSCAR JSON.

Canonical pretty JSON is rendered from the shared values. Tests compare preview and live-client request values after canonical JSON formatting. Runtime-only identities and server-returned IDs are labeled as such.

## 5. Navigation and routes

### 5.1 Primary route

`GET /authoring` renders the dedicated workspace.

Stable query parameters make content linkable without persisting progress:

- `section=quickstart|schema|patterns|assertions|validation`
- `step=identity|cases|stimuli|assertions|validate`
- `pattern=co_occurrence|flood|sequence|persistence|absence|parent_child|cross_source|threshold`
- `level=basic|advanced`
- `view=yaml|contract|api|lifecycle`

Missing parameters select Quickstart, the first lesson, and flood basic. Invalid structural values render a not-found response rather than selecting an unintended example.

### 5.2 Scenarios integration

The Scenarios workbench adds:

- a visible **Scenario Authoring Guide** link near the source editor;
- a **Read the `<pattern>` tutorial** deep link for the selected source;
- a view switch for **Compiled contract**, **OSCAR API JSON**, and **Lifecycle** after successful validation;
- relevant authoring links beside validation diagnostics where a stable field or pattern mapping exists.

`GET /scenarios?selected=example:<pattern>:<level>` resolves only a server-known example ID and displays its canonical source as an unsaved editable draft. It does not persist a scenario, create a run, resolve a credential, or contact OSCAR. The URL never carries arbitrary YAML.

### 5.3 Reference integration

The existing Reference page retains a concise scenario and pattern index. It links to the dedicated Quickstart, schema groups, assertion guide, validation guide, and eight cookbook chapters rather than duplicating their complete content.

The contextual Page Reference drawer for Scenarios and Authoring includes the same stable links.

## 6. Authoring workspace

The approved page is tutorial-first with a persistent lesson/pattern outline and an assembled YAML preview.

### 6.1 Quickstart tutorial

Five lessons progressively build one valid flood scenario:

1. **Document identity:** `apiVersion`, `kind`, `name`, `suite`, `pattern`, and `maxDuration`.
2. **P01 and N01:** exactly two cases, code/polarity pairing, and why a nearby negative control is mandatory.
3. **Stimuli:** mutually exclusive repeat and explicit-event forms.
4. **Assertions:** expected evidence, exact counts, and complete-window semantics.
5. **Validate and run:** strict validation, target-free compiled preview, immutable save, Phase A/B selection, execution, and cleanup.

Each lesson contains:

- the concept and its OSCAR/compiler effect;
- only the YAML introduced at that step;
- required/conditional field indicators;
- a common mistake;
- the complete assembled scenario in a sticky panel;
- **Copy YAML** and **Open in Scenarios editor** actions.

The tutorial is navigable with ordinary links. JavaScript may update panels without a full load but cannot be required for access.

### 6.2 Schema reference

Provide a client-filterable, server-rendered reference grouped into Scenario, Case, Event, and Assertion fields. Each row shows:

- YAML property name;
- value type;
- required, optional, or conditional status;
- allowed values;
- limits and safe budgets;
- omitted behavior;
- pattern restrictions;
- compiler or OSCAR effect;
- minimal valid snippet;
- common validation error.

The schema reference explicitly describes strict unknown-field rejection, duplicate-key rejection, the one-document rule, alias prohibition, and the 1 MiB input limit.

### 6.3 Pattern cookbook

Every pattern chapter includes:

- behavior under test;
- fixed compiler semantics;
- author-configurable fields;
- required roles, labels, statuses, or notifier names;
- basic P01/N01 example;
- advanced P01/N01 example;
- compiled rule summary;
- exact OSCAR API preview;
- expected evidence;
- common false-positive and false-negative mistakes.

| Pattern | Fixed/current semantics to document | Advanced example focus |
|---|---|---|
| `flood` | `min_count=5`; occurrences require distinct fingerprints | Multi-label grouping and a negative case split across groups |
| `co_occurrence` | All compiled required alert names must occur in one grouping window | Additional required role and missing-member N01 |
| `sequence` | `login_failure` then `privileged_command` | Group isolation and explicit delays/order reversal |
| `persistence` | One matching alert unresolved for 30 seconds | Firing/resolved identity and near-boundary cancellation |
| `absence` | Expected every 10 seconds; absent for 30 seconds; 55-second observation | Sustained heartbeat cadence and grouping isolation |
| `parent_child` | Roles `parent` and `child`; no synthetic emit rule | Multiple suppress/tag notifiers and unmatched-child release |
| `cross_source` | Required sources `snmp` and `api` for the same semantic alert | Group isolation and repeated-single-source N01 |
| `threshold` | Distinct label `device`; minimum distinct count 3 | Three devices in one group versus values split across groups |

Examples must remain within the closed compiler language. The guide explicitly states that YAML does not currently configure flood count, persistence/absence timers, threshold label/count, sequence role names, or required cross-source names.

### 6.4 Assertions and evidence

Document:

- `synthetic-alert-count`: number of matching synthetic parents read from history;
- `audit-count`: number of correlation audit rows with the specified outcome;
- `parent-link-count`: parent-child audit rows with the specified outcome;
- `outcome`: exact OSCAR audit outcome matched by audit-based assertions;
- `equals`: required exact expected count from 0 through 100.

Explain known CorrTest outcomes used by built-ins, including `parent_emitted`, `suppressed_per_notifier`, and `released_no_trigger`. Explain that an assertion with `equals: 0` cannot decide early: it requires a mandatory final snapshot at or after the absolute case deadline. Absence alone is not proof.

Phase B is required for synthetic-parent assertions. Phase A is audit-only and must not be presented as capable of proving dispatched synthetic parents.

### 6.5 Validation guide

Consolidate syntax, structural, semantic, and budget rules:

- Go duration syntax and maximum durations;
- exactly one P01/positive and one N01/negative case;
- conditional stimulus exclusivity;
- safe label names/values and protected labels;
- event status, delay, and resolution identity rules;
- grouping/notifier uniqueness and limits;
- supported assertion kinds and count range;
- pattern-specific positive and negative stimulus requirements;
- maximum case, event, label, assertion, and notifier counts.

Pattern-aware validation must reject structurally accepted inputs that cannot exercise the compiler rule they select. Validation remains polarity-aware: negative controls intentionally omit, reverse, resolve, repeat, or split the condition being tested.

The semantic checker evaluates the following closed contracts after applying case labels, event-label overrides, grouping keys, event status, and delay order:

| Pattern | P01 positive contract | N01 negative-control contract |
|---|---|---|
| `flood` | At least five firing occurrences for the compiled source role share one grouping key | No grouping key reaches five firing occurrences |
| `co_occurrence` | Every compiled required role has a firing event in one grouping key/window | At least one compiled required role is absent from every grouping key/window |
| `sequence` | `login_failure` precedes `privileged_command` in one grouping key | No grouping key contains that valid ordered pair |
| `persistence` | One compiled source role fires and remains unresolved for at least 30 seconds | The same role/identity resolves before 30 seconds |
| `absence` | One compiled heartbeat role has a gap of at least 30 seconds | Heartbeats for each tested grouping key continue frequently enough that no 30-second gap completes before observation closes |
| `parent_child` | A `parent` firing precedes a `child` firing in the same grouping key | A `child` fires without an earlier active parent in its grouping key |
| `cross_source` | One compiled source role fires from both `snmp` and `api` in one grouping key | Every grouping key lacks at least one required source |
| `threshold` | One compiled source role reaches at least three distinct `device` values in one grouping key | No grouping key reaches three distinct `device` values |

For patterns whose match criteria select one source alert name (`flood`, `persistence`, `absence`, `cross_source`, and `threshold`), authored stimuli use one logical source role per case. Resolved events reuse that role and identity. Additional events are permitted only when they cannot change the compiled selector or invalidate the declared polarity contract.

## 7. Scenario field contract

### 7.1 Scenario fields

| Field | Status | Contract |
|---|---|---|
| `apiVersion` | Required | Exactly `corrtest.oscar/v1alpha1` |
| `kind` | Required | Exactly `CorrelationScenario` |
| `name` | Required | Trimmed, 1–100 characters |
| `suite` | Required | Trimmed, 1–100 characters |
| `pattern` | Required | One of the eight supported patterns |
| `maxDuration` | Required | Positive Go duration, at most five minutes |
| `cases` | Required | Exactly two unique cases containing P01 and N01 |

### 7.2 Case fields

| Field | Status | Contract |
|---|---|---|
| `name` | Required | Unique, 1–120 characters |
| `code` | Required | `P01` or `N01`, exactly once each |
| `polarity` | Required | `positive` for P01; `negative` for N01 |
| `window` | Required | Positive duration, at most two minutes |
| `groupBy` | Optional | Up to 16 unique safe label names; omission means no grouping labels |
| `labels` | Optional | Up to 64 safe non-reserved labels applied to firing stimuli |
| `role` | Conditional | Required with repeat form; forbidden with explicit events |
| `repeat` | Conditional | 1–100 with role; zero/omitted with explicit events |
| `events` | Conditional | 1–100; mutually exclusive with role/repeat |
| `assertions` | Required | 1–32 typed exact-count assertions |
| `suppressForNotifiers` | Parent-child only | Up to 16 exact notifier names |
| `tagForNotifiers` | Parent-child only | Up to 16 exact notifier names; notifier lists are disjoint |

### 7.3 Event fields

| Field | Status | Contract |
|---|---|---|
| `role` | Required | Non-empty logical role, at most 100 characters |
| `status` | Required | `firing` or `resolved` |
| `labels` | Optional | Safe non-reserved labels for this firing identity |
| `delay` | Optional | Non-negative offset within scenario and observation budgets |

A resolved event must follow a firing event for the same role and cannot change its identity labels. CorrTest reuses the authoritative firing identity for the resolution.

### 7.4 Assertion fields

| Field | Status | Contract |
|---|---|---|
| `kind` | Required | `synthetic-alert-count`, `audit-count`, or `parent-link-count` |
| `outcome` | Conditional | Omitted for synthetic count; required for audit-based assertions |
| `equals` | Required | Integer 0–100 |

## 8. OSCAR API preview

The API preview is available for every cookbook example and every successfully validated Scenarios draft. Views include YAML, compiled contract, OSCAR API JSON, and lifecycle.

### 8.1 Preview identity

Compilation uses a valid, clearly marked preview run ID and eight-character short token. The UI explains that real execution regenerates:

- run ID and short token;
- correlation-rule names;
- physical alert names;
- ownership descriptions and labels;
- alert transport fingerprints;
- server-returned correlation-rule IDs.

All other request field names, nesting, criteria, values, labels, annotations, and paths are the same values the shared OSCAR request builders produce.

### 8.2 Rule request views

Show P01 and N01 separately. For each, display:

- `POST /api/v1/correlation_rules/validate`;
- `POST /api/v1/correlation_rules`;
- the shared JSON body, including name, pattern, window, grouping, match criteria, priority, synthetic rate, enabled state, ownership description, creator, and optional `emit_spec`;
- the fact that validation and creation use the same body.

Parent-child rules correctly omit `emit_spec` because they link and annotate/suppress children rather than emit a synthetic parent.

### 8.3 Alert request views

Show every deterministic event and its `POST /api/v1/alerts` envelope, including:

- receiver, status, group key, and group labels;
- common labels and annotations;
- per-alert transport fingerprint;
- run/case/pattern/role/event identity labels;
- scheduled delay and attempt order.

The preview explains that the Alertmanager-compatible transport fingerprint is required by OSCAR's ingress envelope but is not used as oracle identity. OSCAR history read-back supplies the authoritative server fingerprint.

### 8.4 Actual live lifecycle

The lifecycle view distinguishes preflight from mutation.

**Compatibility preflight**

1. Validate the P01 rule body through `POST /api/v1/correlation_rules/validate`.
2. Inject one run-owned label-survival probe through `POST /api/v1/alerts`.
3. Poll `GET /api/v1/alerts/history` for exact read-back and verify every reserved label.
4. Stop before rule creation if rule validation, injection, unique read-back, fingerprint, or label survival is unproven.

**Run mutation**

1. Record proposed P01 ownership locally.
2. Validate and create the P01 correlation rule.
3. Record proposed N01 ownership locally.
4. Validate and create the N01 correlation rule.
5. Inject deterministic source alerts for both cases.
6. Query alert history by exact run-unique alert names.
7. Query correlation audit by authoritative fingerprint; query notification audit where required.
8. Evaluate declared assertions only after their evidence timing rules are satisfied.
9. Delete owned temporary correlation rules using OSCAR-returned IDs.
10. Resolve observed CorrTest alerts using authoritative history identities.
11. Persist cleanup and evidence independently from the behavioral verdict.

The page states prominently: **CorrTest creates two temporary correlation rules and no ordinary alert rules. Source alerts are injected directly through the public alert API.**

### 8.5 Unknown runtime values

Deletion operations use `<returned-p01-rule-id>` and `<returned-n01-rule-id>` placeholders. Response-dependent fingerprints and pagination are labeled as runtime values. The preview does not fabricate server responses.

## 9. Component boundaries

### 9.1 Scenario contract

The scenario package owns:

- shared supported-pattern, assertion-kind, duration, count, and label-budget constants;
- field metadata for the authoring schema;
- pattern semantic descriptors;
- strict validation;
- canonical YAML encoding;
- basic and advanced example values;
- deterministic JSON Schema generation.

The existing checked-in `docs/schema/correlation-scenario.schema.json` remains a release artifact but is generated and verified rather than manually maintained.

### 9.2 OSCAR request contract

The OSCAR package owns typed request values/builders for:

- correlation-rule validate/create bodies;
- alert injection envelopes;
- exact evidence query descriptions;
- cleanup alert resolution envelopes;
- deletion path descriptions.

The network client serializes those values. The authoring preview serializes the same values after sanitizing only secrets and identifying runtime placeholders. Request builders do no I/O.

### 9.3 Authoring catalog

A focused authoring catalog joins structural metadata with narrative lessons and pattern explanations. It references scenario fields and patterns by stable ID. Tests reject references to missing schema fields, patterns, examples, or sections.

### 9.4 Runtime inspection

Target-free inspection accepts a validated scenario and pipeline mode, generates a preview run identity, compiles the plan, builds request previews, and returns a read-only view model. It performs no target lookup, credential resolution, persistence, or network call.

### 9.5 Web layer

The web layer owns routing, link-state parsing, server-rendered templates, progressive-enhancement JavaScript, and presentation CSS. It receives escaped typed view models and never constructs schema rules or OSCAR request objects.

## 10. Error handling

- Invalid YAML renders safe syntax/field diagnostics and no compiled/API preview.
- Pattern-semantic failures identify the case and required role, label, status, grouping, timing, or polarity relationship.
- Unsupported section, pattern, level, or example IDs do not fall through to another document.
- Request-preview construction errors prevent the preview; the UI never displays approximate JSON.
- Runtime-only values are placeholders, never invented successes or response IDs.
- Copy failures render an inline status message without losing the page.
- Opening an example fails closed if its registered source cannot encode, decode, and compile.
- Phase A renders an explicit synthetic-dispatch limitation wherever a synthetic assertion or payload is shown.
- Authoring remains read-only with respect to SQLite and OSCAR under all error paths.

## 11. Accessibility and visual contract

- Reuse CorrTest tokens, density, typography, OC identity, panels, buttons, and dark/light themes.
- Primary navigation identifies Authoring with `aria-current`.
- Section, lesson, pattern, level, and payload navigation uses real links or correct tab semantics.
- All content remains accessible without JavaScript.
- Schema filtering has a visible label and a no-results state.
- Code blocks have descriptive headings and adjacent Copy actions with live status text.
- Required/optional status is conveyed by text, not color alone.
- Focus is visible and preserved across progressive tab/filter actions.
- Narrow screens stack outline, lesson, and assembled YAML without horizontal page scrolling; code blocks may scroll internally.
- Motion is nonessential and disabled under reduced-motion preference.

## 12. Testing strategy

### 12.1 Schema and validation

- Every wire YAML field appears exactly once in the typed schema catalog.
- Required, conditional, enum, limit, and pattern metadata match decoder/validator behavior.
- Generated JSON Schema matches the checked-in artifact byte-for-byte.
- Unknown fields, duplicates, aliases, multiple documents, oversized input, unsafe labels, reserved labels, invalid durations, and exceeded budgets remain rejected.
- Pattern-aware positive/negative fixtures kill semantically impossible scenarios without rejecting intentional N01 controls.

### 12.2 Examples

- Exactly eight basic and eight advanced examples exist.
- Every example canonical-encodes, strictly decodes, and recompiles.
- P01 and N01 identities and polarities are complete.
- Each compiled example contains exactly two temporary correlation rules and a bounded mutation budget.
- Expected fixed criteria, roles, sources, distinct values, timers, notifier behavior, grouping, and observation windows are asserted per pattern.

### 12.3 Exact OSCAR requests

- Preview and network-client correlation-rule request values are identical.
- Preview and network-client alert envelopes are identical for the same compiled event.
- Canonical formatted JSON is deterministic.
- Validate/create endpoints and shared bodies are shown for P01 and N01.
- Preflight validation, label probe, history read-back, evidence queries, cleanup resolutions, and returned-ID delete paths are present in stable order.
- No credential, resolved key, authorization header, or target secret can enter preview output.

### 12.4 Web behavior

- `/authoring` renders every section, lesson, pattern, and level through stable links.
- Schema search and code-copy progressive behavior has focused JavaScript contract tests where practical and server-rendered fallback tests.
- Every example deep link opens the exact canonical YAML as an unsaved Scenarios draft.
- Draft opening leaves scenario/run counts unchanged and uses no OSCAR client.
- A validated custom draft shows compiled, API JSON, and lifecycle views.
- Invalid source shows diagnostics and withholds mutation previews.
- Authoring, Scenarios, Reference, help links, primary navigation, CSP, CSRF boundaries, and accessible landmarks are regression-tested.

### 12.5 Release documentation

Update README, operator documentation, scenario authoring documentation, built-in catalog, packaged JSON Schema, contextual help, and Reference links. Documentation tests require all eight patterns, every schema group, all assertion kinds, the two-rule/no-normal-rule statement, and the preflight/mutation/cleanup lifecycle.

## 13. Acceptance criteria

1. A user can reach Scenario Authoring from primary navigation, Scenarios, Reference, and contextual help.
2. A user can complete the five-step tutorial and obtain one valid scenario without prior schema knowledge.
3. Every YAML property is documented with its type, requirement status, constraints, effect, and example.
4. Every pattern has one basic and one advanced example that passes strict decode and compilation.
5. The guide clearly distinguishes fixed compiler semantics from configurable scenario values.
6. Every example can be copied or opened as an unsaved Scenarios draft without persistence or OSCAR contact.
7. Successfully validated custom YAML exposes the same compiled/API/lifecycle views as cookbook examples.
8. P01 and N01 correlation-rule request JSON comes from the same builders used by the live OSCAR client.
9. Every source alert envelope can be inspected before execution.
10. The lifecycle displays compatibility preflight, two temporary correlation-rule creations, evidence reads, returned-ID rule deletion, and alert resolution in actual order.
11. The UI explicitly states that CorrTest creates no ordinary alert rules.
12. Preview-only and runtime-only values are unambiguous, and no credential material is rendered.
13. Generated JSON Schema and documentation cannot drift from the tested typed contract without failing CI.
14. Authoring works in dark/light themes, by keyboard, on narrow screens, and without JavaScript for core navigation/content.

## 14. Delivery boundary

This design is one implementation phase with vertical slices:

1. Typed schema contract, generated JSON Schema, and semantic validation.
2. Basic/advanced executable example catalog.
3. Shared OSCAR request builders and target-free request preview.
4. Dedicated Authoring route, navigation, tutorial, and schema reference.
5. Pattern cookbook, API/lifecycle views, and Scenarios editor handoff.
6. Reference/help/release documentation integration and complete verification.

Implementation planning must keep each slice independently testable and must preserve the no-network/no-persistence preview boundary throughout.
