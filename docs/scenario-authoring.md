# Scenario authoring guide

This guide is for an OSCAR operator who understands alerts and correlation but
is new to CorrTest scenario YAML. After following it, you can choose a built-in
example, inspect its exact compiled/API/lifecycle preview, adapt it into a
valid P01/N01 scenario, save it explicitly, and know what a live run creates
and removes.

## Quickstart and discovery

Start the local UI with `oscar-corrtest serve`, then open `/authoring`. The
Authoring page works without JavaScript, a target, or a credential. Its five
sections are deliberately ordered:

1. **Quickstart** builds one flood document in five steps.
2. **Schema** is the typed field reference and validation boundary.
3. **Patterns** is the eight-pattern cookbook.
4. **Assertions** explains which OSCAR evidence each exact count uses.
5. **Validation** explains strict YAML and pattern-aware checks.

In **Patterns**, choose a pattern, then choose **basic** or **advanced**. There
are 16 examples: one basic and one advanced example for each of the eight
patterns. Each example offers four views:

- **YAML** is the source to adapt.
- **Compiled contract** is the P01/N01 rule, stimulus, assertion, and
  inspection contract CorrTest will use.
- **OSCAR API** is the exact request-body preview.
- **Lifecycle** is the ordered preflight, mutation, evidence, and cleanup
  plan, including honest runtime placeholders such as `{returned-rule-id}` and
  `{server-fingerprint}`.

Choose **Open in Scenarios editor** to put the selected example in Scenarios as
an editable, unsaved draft. That inspection is target- and credential-free: it
does not contact OSCAR, prove target compatibility, persist a scenario, or
create a run. Use the editor's **Save custom scenario** only when you intend
to persist the validated source. Editing a saved version requires **Save as new
version**; earlier versions stay immutable.

## Scenario identity and cases

A document has one identity, one selected pattern, and exactly two cases:
P01 is the positive proof and N01 is the nearby negative control. The codes
and polarities are paired: P01 is `positive`; N01 is `negative`. A document
with duplicate, missing, or mismatched cases is invalid.

```yaml
apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: checkout-flood
suite: operator-custom
pattern: flood
maxDuration: 90s
cases:
  - name: threshold-emits
    code: P01
    polarity: positive
    window: 30s
    groupBy: [site]
    labels: {site: lab-a}
    role: interface_down
    repeat: 5
    assertions:
      - {kind: audit-count, outcome: parent_emitted, equals: 1}
      - {kind: synthetic-alert-count, equals: 1}
  - name: below-threshold
    code: N01
    polarity: negative
    window: 30s
    groupBy: [site]
    labels: {site: lab-a}
    role: interface_down
    repeat: 4
    assertions:
      - {kind: audit-count, outcome: parent_emitted, equals: 0}
      - {kind: synthetic-alert-count, equals: 0}
```

The scenario fields are closed: spelling a new field does not add a feature; it
causes validation to reject the document. The generated JSON Schema is the
machine-readable form of this same contract.

| YAML field | Type and requirement | Meaning and limits |
|---|---|---|
| `apiVersion` | required string | Exactly `corrtest.oscar/v1alpha1`. |
| `kind` | required string | Exactly `CorrelationScenario`. |
| `name` | required string | Trimmed 1–100 characters; names run-owned resources and evidence. |
| `suite` | required string | Trimmed 1–100 characters; recorded in ownership labels. |
| `pattern` | required string | One of the eight cookbook patterns. |
| `maxDuration` | required Go duration | Positive and at most 5 minutes; caps the full scenario. |
| `cases` | required array | Exactly one P01 and one N01. |
| `cases[].name` | required string | Unique, 1–120 characters. |
| `cases[].code` | required string | `P01` or `N01`, exactly once each. |
| `cases[].polarity` | required string | `positive` for P01; `negative` for N01. |
| `cases[].window` | required Go duration | Positive and at most 2 minutes. |
| `cases[].groupBy` | optional label-name array | Up to 16 unique safe label names, each 1–100 characters. |
| `cases[].labels` | optional string map | Up to 64 safe, non-reserved source labels; keys are 1–100 characters and values are at most 500 characters. |
| `cases[].role` | conditional string | Use with `repeat`; do not combine with `events`. |
| `cases[].repeat` | conditional integer | 1–100; use with `role`; do not combine with `events`. |
| `cases[].events` | conditional event array | 1–100 explicit events; do not combine with `role` or `repeat`. |
| `cases[].suppressForNotifiers` | conditional string array | Up to 16 unique, nonblank names; each is at most 100 characters; `parent_child` only; disjoint from `tagForNotifiers`. |
| `cases[].tagForNotifiers` | conditional string array | Up to 16 unique, nonblank names; each is at most 100 characters; `parent_child` only; disjoint from `suppressForNotifiers`. |
| `cases[].assertions` | required assertion array | 1–32 exact-count assertions. |
| `events[].role` | required string | Logical source role, 1–100 characters. |
| `events[].status` | required string | `firing` or `resolved`. |
| `events[].labels` | optional string map | Firing-only overrides; a resolution cannot change identity labels. Label keys are 1–100 characters and values are at most 500 characters. |
| `events[].delay` | optional Go duration | Non-negative absolute offset, non-decreasing, and within the budgets. |
| `assertions[].kind` | required string | `synthetic-alert-count`, `audit-count`, or `parent-link-count`. |
| `assertions[].outcome` | conditional string | Required, nonblank, and at most 100 characters for `audit-count` and `parent-link-count`; forbidden for `synthetic-alert-count`. |
| `assertions[].equals` | required integer | Exact expected count from 0 through 100. |

CorrTest supplies reserved labels such as `alertname`, `category`, and
`oscar_test_run_id`; a scenario cannot override them. It also rejects unknown
or duplicate keys, aliases, multiple YAML documents, unsafe durations,
unsafe labels, and inputs over 1 MiB.

## Stimulus forms

Choose one stimulus form in each case:

- **Repeated role** is concise when identical firing occurrences are enough:
  `role: interface_down` plus `repeat: 5`.
- **Explicit events** express several roles, timing, resolution, or event-level
  labels. A `resolved` event must refer to a source identity that fired first.

```yaml
events:
  - {role: login_failure, status: firing}
  - {role: privileged_command, status: firing, delay: 10s}
```

The compiler turns logical roles into run-unique physical alert names in the
form `CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>`. Every injection
has CorrTest ownership labels, including `category=corrtest_<pattern>` and
`oscar_test_run_id`. Use the compiled contract's exact inspection filters when
checking an OSCAR run manually; do not guess an ID from the display name.

## Assertions and evidence

Assertions are proof obligations, not a request to stop early. CorrTest waits
for the complete applicable observation window and checks exact counts:

- `synthetic-alert-count` reads matching synthetic parents from OSCAR history;
  it has no `outcome`.
- `audit-count` reads correlation audit rows with the stated exact `outcome`.
- `parent-link-count` reads parent-child audit rows with the stated exact
  `outcome`.

An `equals: 0` assertion needs a final read at or after the absolute case
deadline. An early empty response, or the absence of a synthetic alert alone,
is not negative proof. The P01 and N01 pair, completed window, authoritative
history/audit evidence, and cleanup record together make the result useful.

## Eight-pattern cookbook

The **basic** example shows the smallest valid configuration; **advanced**
shows the same fixed pattern with richer grouping, labels, timing, or notifier
settings. Open either in `/authoring`, compare its P01/N01 matrix and all four
preview views, then open it as an unsaved draft before changing it.

| Pattern | P01 positive proof | N01 negative control | Fixed behavior to preserve |
|---|---|---|---|
| `flood` | Five distinct occurrences in one group emit one parent. | Four occurrences stay below threshold. | `min_count=5`; fingerprints must be distinct. |
| `co_occurrence` | Every required role arrives in one group/window. | A required role is missing. | All compiled role names are required. |
| `sequence` | `login_failure` precedes `privileged_command` in one group. | The order is reversed or not completed. | The ordered pair is fixed. |
| `persistence` | A firing source remains unresolved for 30 seconds. | That identity resolves before 30 seconds. | One unresolved matching identity is required. |
| `absence` | A heartbeat gap reaches the absence deadline. | Heartbeats prevent a completed gap. | Expected every 10s, absent for 30s, with a 55s observation. |
| `parent_child` | An active parent precedes the child and notifier policy is evidenced. | An unmatched child is released. | Parent/child roles; no synthetic emit rule. |
| `cross_source` | Matching semantics arrive from both `snmp` and `api`. | At least one required source is absent per group. | Both sources are required in one group. |
| `threshold` | Three distinct `device` values in one group emit. | Repeated/split values stay below cardinality. | `device` is distinct; minimum count is 3. |

Do not change a selected pattern's fixed semantics to make a scenario easier
to pass. Adapt only the documented configurable inputs shown in its Authoring
view. In particular, parent-child does not create a synthetic parent; use
parent-link evidence and exact notifier names instead.

## Validate, preview, save, and run

Use this order for a custom document:

1. Start from the matching basic or advanced example in `/authoring`.
2. Open it in Scenarios, make the smallest necessary change, and inspect YAML,
   compiled contract, OSCAR API JSON, and lifecycle again.
3. Validate strict structure and pattern semantics:

   ```sh
   oscar-corrtest scenario validate ./scenario.yaml
   ```

4. Save deliberately in the UI with **Save custom scenario**, or import the
   already validated file with `oscar-corrtest scenario import ./scenario.yaml`.
   Saving stores the exact source and digest; it does not run it.
5. Configure a target and run a live preflight before mutation:

   ```sh
   oscar-corrtest doctor --target <target-id> --pipeline-mode phase_b_dispatch
   oscar-corrtest plan ./scenario.yaml --target <target-id> --pipeline-mode phase_b_dispatch
   oscar-corrtest run ./scenario.yaml --target <target-id> --pipeline-mode phase_b_dispatch
   ```

`plan` and Authoring previews show intended work. Neither proves that an OSCAR
target accepts the request, preserves labels, publishes a correlation, or
dispatches a notifier. Only a live doctor/run against that target can do so.

## What a live run creates and cleans up

For a valid, compatible target, one scenario run follows this order:

1. **Preflight:** validate a real correlation-rule payload, inject a diagnostic
   alert, resolve its server identity from authoritative history, and verify
   CorrTest's reserved labels survived. If this fails, no temporary scenario
   rule is created.
2. **Mutation:** create exactly two temporary correlation rules—one for P01 and
   one for N01. CorrTest does not create ordinary OSCAR alert rules. It injects
   source alerts through `POST /api/v1/alerts`.
3. **Observation and assertions:** wait through the case window, read
   authoritative history and audit evidence, and evaluate the declared counts.
   Returned IDs and server fingerprints are runtime-dependent; previews never
   fabricate them.
4. **Cleanup:** resolve CorrTest's injected alerts using authoritative-history
   identities and delete only the exact returned IDs for the two temporary
   correlation rules. Cleanup never broad-prefix deletes and does not delete
   operator-owned rules.
5. **Persist terminal evidence:** after cleanup completes, record normalized
   assertions, cleanup status, terminal facts, and evidence in the final
   transaction.

Behavioral verdict and cleanup status are independent. A failed or interrupted
run remains visible so an operator can inspect it and use the exact cleanup
retry workflow when appropriate.

## Phase A, Phase B, and preview limits

Pipeline mode is an operator declaration captured with the run because OSCAR
does not currently expose it publicly. **Phase A is audit-only**: it can supply
audit-oriented evidence but cannot prove a dispatched synthetic parent.
**Phase B is required** when the assertion needs synthetic-parent or notifier
evidence. Treat either mode as a target-specific live compatibility question,
not something proven by the credential-free Authoring preview.

## Generated schema and drift check

The distributable schema is
`docs/schema/correlation-scenario.schema.json`. Regenerate it from the typed
scenario contract with:

```sh
go run ./cmd/generate-scenario-schema
git diff --exit-code docs/schema/correlation-scenario.schema.json
```

The second command is the byte-drift check: it must produce no diff after a
committed schema update. Release archives carry this schema together with this
guide, the built-in catalog, and the operator guide.
