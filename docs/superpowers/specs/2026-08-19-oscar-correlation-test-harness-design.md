# OSCAR Correlation Test Harness Design

**Status:** Approved — revision 2, adversarial findings incorporated
**Date:** 2026-08-19
**Proposed repository:** `oscar-corrtest`
**Primary readers:** OSCAR maintainers, test engineers, and operators
**Post-read action:** run the focused remediation confirmation, execute the repository-foundation plan, then plan the SQLite-ledger slice
**Review record:** `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md`

**Listener/distribution amendment (2026-08-20):**
`2026-08-20-oscar-corrtest-distribution-and-open-listener-design.md`
supersedes this document's original Linux-only distribution scope and its
loopback-only/no-unauthenticated-remote clauses in §§17, 19.2, 23.1, and 24.

**Operator-experience/service amendment (2026-08-20):**
`2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`
extends this document with a managed global API key, explicit user-level
service lifecycle, embedded page reference, scenario workbench, and live logs.

## 1. Decision summary

Build a standalone Go application named **OSCAR Correlation Test Harness**. It
ships as one self-contained executable per supported Linux, macOS, and Windows
platform, with an embedded web UI and a complete CLI. The harness creates
temporary correlation rules through OSCAR's public API, sends deterministic
synthetic alerts, observes OSCAR's public evidence surfaces, evaluates explicit
assertions, cleans up every resource it created, and preserves a durable report.

The application uses:

- Go 1.27 or newer, with CI initially pinned to Go 1.27.0.
- `net/http`, `html/template`, embedded assets, and small vanilla JavaScript modules for the UI.
- SQLite through `database/sql` and a pinned CGO-free driver.
- Local evidence directories for potentially large request, response, and export artifacts.
- Server-Sent Events for the live run timeline.
- Public OSCAR HTTP APIs only. Direct MySQL, Redis, NATS, or container access is not part of the supported product path.

The first supported suite covers positive and negative cases for all eight OSCAR correlation patterns:

1. `co_occurrence`
2. `flood`
3. `sequence`
4. `persistence`
5. `absence`
6. `parent_child`
7. `cross_source`
8. `threshold`

SQLite is the durable index and recovery ledger. Raw evidence remains in run-scoped files so the database stays small, exports remain portable, and a damaged or missing artifact can be reported precisely.

## 2. Problem statement

OSCAR has several alert-sending scripts and correlation UAT scripts, but no operator-facing tool proves the complete correlation outcome. Existing tools can inject alerts; some setup scripts can create rules or inspect internal state; none own the whole test lifecycle as a durable, repeatable transaction.

A useful correlation test must prove more than “the POST returned 2xx.” Depending on the pattern, success can mean:

- exactly one synthetic parent was emitted;
- no synthetic parent was emitted before a threshold;
- a child alert was linked to an existing parent;
- a child was suppressed for specific notifier types but not others;
- a sequence fired only in the required order;
- a timer-driven pattern fired inside a bounded observation window;
- unrelated alerts remained unaffected;
- every temporary rule was removed after the run.

The harness therefore needs an execution engine, an evidence model, an outcome oracle, durable recovery state, and human-readable reporting—not merely a richer alert sender.

## 3. Existing-tool assessment

The current scripts are useful source material but should not become runtime dependencies of the new repository.

| Existing tool | Useful ideas to retain | Why it is not the harness foundation |
|---|---|---|
| `send_alert.py` | Broad protocol and payload examples; interactive alert exploration | Very large, menu-oriented, dependency-heavy, and combines unrelated SNMP, Prometheus, webhook, NATS, template, and spreadsheet concerns |
| `send_custom_alert.py` | Spreadsheet/YAML ingestion examples and label/annotation mapping | Uses random data and environment-specific filesystem assumptions; lacks deterministic scenarios, an oracle, resource cleanup, and preserved reports |
| `send_test_alert.py` | Middleware and Alertmanager payload shapes, CLI knobs, timing, batches, and label construction | Injection-only; does not create rules, discover capabilities, capture correlation evidence, or assert outcomes |
| `send_alert_performance.py` | Run identifiers, rate control, bounded concurrency, and streaming metrics | Designed for load and performance behavior rather than correlation semantics and audit proof |
| Correlator setup shell scripts | Pattern-specific rule fixtures, wait budgets, audit expectations, and cleanup knowledge | Some use direct datastore access and ad hoc shell orchestration; not a stable external contract or durable application |

The new harness may reproduce validated payload semantics in native Go, backed by compatibility tests and golden fixtures. It must not shell out to Python scripts during normal operation.

The 2026-05-02 `oscar-testkit` proposal is superseded for correlation testing by this design. Its useful requirements—multi-target configuration, JUnit output, retained reports, capability snapshots, and CI execution—are retained here. Broader alert-flow/load tracks remain outside this harness, and recurring execution is delegated to GitHub/GitLab schedules or an operator scheduler rather than an in-process cron subsystem.

### 3.1 Considered approaches

| Approach | Strengths | Costs and risks | Decision |
|---|---|---|---|
| Add orchestration and reports around the existing Python scripts | Fastest proof of concept; reuses working send paths | Preserves environment-specific assumptions, multiple dependency stacks, nondeterministic generators, subprocess failure modes, and weak lifecycle ownership | Reject as the product architecture; use only as behavioral reference |
| Go API/runner plus a separate React single-page application | Rich component ecosystem and familiar frontend development | Requires Node tooling, coordinated frontend/backend releases, a larger supply chain, and more packaging work than the operational UI needs | Reject for version 1 |
| Single Go modular monolith with embedded server-rendered UI | One artifact, one lifecycle, shared CLI/UI services, simple Linux operation, OTTO-proven presentation pattern | Requires disciplined Go template and vanilla-JavaScript design; not intended for highly interactive visual editing | **Selected** |
| CLI-only Go application | Smallest initial surface and straightforward CI use | Makes evidence exploration, historical report review, target diagnostics, and cleanup repair less approachable for operators | Reject as the complete product; retain a first-class CLI alongside the UI |

For persistence, flat JSON directories were considered but rejected because filtering, lifecycle transitions, uniqueness, migrations, and crash-safe cleanup would have to be rebuilt poorly. A client/server database was rejected because the expected single-process workload does not justify another service. SQLite plus hashed artifact files provides the best operational tradeoff.

## 4. Goals

### 4.1 Product goals

- Install and run as one self-contained Linux executable.
- Provide equivalent CLI and web UI paths for configuring targets, running tests, monitoring progress, reviewing history, retrying cleanup, and exporting evidence.
- Create and delete test correlation rules using OSCAR's supported APIs.
- Generate deterministic alert sequences that exercise every correlation pattern.
- Give every generated rule and alert a human-readable name and a reserved label set that identifies its harness, pattern, scenario, case, role, and exact run.
- Include at least one positive and one negative case per pattern.
- Distinguish product failure, harness error, missing evidence, timeout, and cleanup failure.
- Preserve historical reports across process restarts and host reboots.
- Export self-contained run evidence without requiring the harness to view it.
- Resume or repair cleanup after a crash.
- Avoid exposing OSCAR credentials in reports, logs, SQLite, HTML, or exported artifacts.

### 4.2 Engineering goals

- Keep the runtime dependency surface small and CGO-free.
- Make scenario compilation and assertion evaluation deterministic and unit-testable.
- Isolate OSCAR API details behind a versioned adapter.
- Isolate SQLite behind repositories so the storage engine can be changed without touching the domain engine.
- Keep UI rendering useful without JavaScript; JavaScript adds live updates and interaction polish.
- Be safe to run repeatedly and safe to interrupt.

## 5. Non-goals for version 1

- General OSCAR load, soak, or chaos testing.
- Direct testing of SNMP trap decoding, SMTP ingestion, or every alert transport.
- Replacing OSCAR's correlation-rule editor or rule documentation.
- Editing production rules not created by the harness.
- Direct queries to OSCAR MySQL, Redis, NATS, or internal containers.
- Multi-user tenancy, organization-level authorization, or a shared cloud service.
- Distributed workers or more than one harness process writing the same data directory.
- Browser-authored arbitrary code, arbitrary shell commands, or custom JavaScript assertions.
- Pixel-identical reuse of the OTTO Gateway UI or its product branding.

## 6. Users and primary workflows

### 6.1 Operator smoke test

1. Start the binary and open the local web UI.
2. Add an OSCAR target using a base URL and a credential reference.
3. Run connection diagnostics and capability discovery.
4. Select “All built-in patterns” or one pattern.
5. Review the compiled test plan, resources to be created, alert count, and maximum duration.
6. Start the run.
7. Watch setup, alert injection, observation, assertions, and cleanup in real time.
8. Review the final verdict and drill into expected versus observed evidence.
9. Export a portable evidence bundle.

### 6.2 CLI validation in CI

1. Supply target configuration through flags and environment-variable references.
2. Run a built-in or file-based scenario non-interactively.
3. Receive structured console output and stable exit codes.
4. Write JUnit XML, canonical JSON, and an evidence ZIP.
5. Leave the historical record in SQLite unless an ephemeral data directory was requested.

### 6.3 Post-failure investigation

1. Open the Runs page after the original process has restarted.
2. Filter by target, suite, pattern, verdict, cleanup status, or date.
3. Inspect the immutable compiled plan, lifecycle timeline, individual assertions, and raw redacted evidence.
4. Retry cleanup if the original run left owned resources behind.
5. Export the report for a ticket or release record.

### 6.4 Custom rule validation

1. Import or author a declarative scenario.
2. Validate it locally without modifying OSCAR.
3. Compile it against the selected OSCAR target and review target-specific warnings.
4. Execute it using the same safety, evidence, cleanup, and reporting path as built-in scenarios.

## 7. Architectural shape

The application is a modular monolith. The CLI and UI invoke the same application services; neither contains correlation behavior.

```text
CLI commands             Embedded web UI
     |                         |
     +--------- application services ---------+
                          |
                 scenario compiler
                          |
                    run coordinator
          +---------------+---------------+
          |               |               |
      OSCAR API        assertion       evidence
       adapter           engine         collector
          |               |               |
          +---------------+---------------+
                          |
               persistence repositories
                 |                 |
              SQLite       run artifact files
```

The key internal boundaries are:

- **Domain:** scenarios, cases, steps, assertions, verdicts, resources, and reports.
- **Compiler:** validates scenario input and expands it into an immutable executable plan.
- **Runner:** owns the lifecycle state machine, deadlines, cancellation, stabilization, and cleanup.
- **OSCAR adapter:** authentication, capabilities, rules, alert injection, alert history, correlation audit, notification audit, and API error normalization.
- **Oracle:** turns collected evidence into assertion results without making network calls.
- **Persistence:** transactional metadata and append-only run history in SQLite.
- **Artifacts:** atomic, hashed, redacted files in a run directory.
- **Presentation:** CLI, HTML handlers/templates, SSE stream, report generation, and export.

The dependency direction points inward. OSCAR and SQLite types do not leak into the domain model.

## 8. Run lifecycle and state machine

Each run behaves as a recoverable transaction with an explicit resource ledger.

```text
QUEUED
  -> PREFLIGHT
  -> SETTING_UP
  -> INJECTING
  -> OBSERVING
  -> ASSERTING
  -> CLEANING_UP
  -> COMPLETED

Any active state -> CANCELLING -> CLEANING_UP -> COMPLETED
Any active state -> INTERRUPTED on process loss
INTERRUPTED -> RECOVERING -> CLEANING_UP -> COMPLETED
```

The coordinator persists a state transition before exposing it to the UI. Resource ownership is recorded before or in the same logical operation that makes the resource usable. When OSCAR returns a new resource identifier, the harness records it before moving to the next step.

At startup, the harness finds runs left in active states and marks them `INTERRUPTED`. It does not silently resume alert injection. It automatically attempts safe cleanup for resources whose ownership can be proven. If cleanup needs an unavailable target or credential, the run remains visible as cleanup-dirty with a retry action.

Only one run executes by default. A configurable queue may hold additional runs. Version 1 does not execute correlation runs concurrently because their windows, audit polling, and target load can interfere with one another.

## 9. Verdict model

Overall run status and test verdict are separate concepts.

### 9.1 Case verdicts

- `PASS`: all required assertions were satisfied within their observation windows.
- `FAIL`: sufficient evidence proves at least one product behavior differed from the expected result.
- `INCONCLUSIVE`: the expected behavior could not be determined because required evidence was absent, ambiguous, stale, or unsupported by the target.
- `ERROR`: the harness could not execute the case correctly because of invalid input, a transport failure, a persistence failure, or an internal invariant violation.
- `SKIPPED`: capability discovery or an explicit selection made the case inapplicable before it modified the target.

### 9.2 Cleanup status

- `CLEAN`: every resource owned by the run was deleted or conclusively absent.
- `DIRTY`: one or more owned resources may remain.
- `NOT_REQUIRED`: the run created no external resources.
- `UNKNOWN`: cleanup proof could not be obtained.

A run can be `PASS` and cleanup-dirty. The UI and exported report must display both prominently. Cleanup failure never converts a behavioral `PASS` into `FAIL`; it raises the final command exit code and a separate operator action.

### 9.3 CLI exit codes

| Code | Meaning |
|---:|---|
| 0 | All selected cases passed and cleanup is clean or not required |
| 1 | At least one selected case failed |
| 2 | At least one selected case was inconclusive and none failed |
| 3 | Harness or configuration error |
| 4 | Behavioral cases passed or skipped, but cleanup is dirty or unknown |
| 130 | Run cancelled by the user or process signal |

## 10. Scenario and executable-plan model

A scenario is declarative input. It is not executed directly. The compiler validates the document, applies defaults, resolves variables, checks target capabilities, and produces an immutable compiled plan stored with the run.

### 10.1 Scenario characteristics

- Versioned with a required `apiVersion` and `kind`.
- Supports built-in scenarios and imported YAML or JSON.
- Contains no executable expressions or shell interpolation.
- Uses typed variables, labels, annotations, timestamps, delays, and bounded repeat counts.
- Declares the expected rule, alerts, observation sources, assertions, and cleanup behavior.
- Allows secrets only by reference; scenario values cannot contain credentials.
- Has a canonical SHA-256 digest after normalization.

### 10.2 Illustrative scenario shape

```yaml
apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
metadata:
  name: flood-basic
spec:
  pattern: flood
  maxDuration: 90s
  rule:
    pattern: flood
    windowSeconds: 30
    groupBy: [site]
    matchCriteria:
      alertRef: interface-down
      minCount: 5
    emit:
      alertRole: synthetic
  cases:
    - name: emits-one-parent-at-threshold
      alerts:
        - template: interface-down
          role: interface-down
          repeat: 5
          labels:
            site: "${case_group}"
      assertions:
        - kind: audit-count
          outcome: parent_emitted
          equals: 1
        - kind: synthetic-alert-count
          equals: 1
    - name: does-not-fire-below-threshold
      alerts:
        - template: interface-down
          role: interface-down
          repeat: 4
          labels:
            site: "${case_group}"
      assertions:
        - kind: audit-count
          outcome: parent_emitted
          equals: 0
```

The scenario uses logical alert references. During compilation, `alertRef: interface-down` becomes the exact run-scoped physical alert name in OSCAR's `match_criteria`, while `alertRole: synthetic` becomes the corresponding run-scoped `emit_spec.alertname`. The final field spelling must mirror OSCAR's public rule schema rather than this illustrative casing. Imported scenarios are rejected if they contain unknown fields.

### 10.3 Identity and isolation

Every run receives a cryptographically random identifier such as `crt_7Q9...`. Every test rule, alert, group key, and annotation includes an ownership token derived from that run identifier.

The harness requires OSCAR to preserve a dedicated label such as `oscar_test_run_id` end-to-end. Until OSCAR guarantees that contract, the adapter also embeds the run token in alert names, rule names, grouping labels, and annotations. Assertions use the strongest available identifier and reject ambiguous evidence rather than guessing.

### 10.4 Human-readable alert and rule identity

The Alert Performance Framework established a useful precedent: `APF_<PROFILE>_<ACTION>` makes the alert's purpose recognizable before an operator opens its labels, while `category=apftest` and `apf_run_id` provide broader and exact filters. Correlation tests adopt the same layered approach with more explicit pattern and case identity.

Every harness-owned physical alert name follows this grammar:

```text
CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>
```

The components are:

- `CORRTEST`: a reserved, unmistakable prefix for this product.
- `PATTERN_CODE`: a stable uppercase code from the table below.
- `CASE_CODE`: `P01`, `P02`, and so on for positive cases; `N01`, `N02`, and so on for negative cases; `C01`, `C02`, and so on for neutral/custom control cases.
- `ROLE`: an uppercase semantic role of at most 24 ASCII characters, such as `INTERFACEDOWN`, `LOGINFAIL`, `PRIVCOMMAND`, `HEARTBEAT`, `PARENT`, `CHILD`, or `SYNTHETIC`.
- `RUN_SHORT`: an eight-character uppercase Crockford Base32 display token derived from the random run identifier.

| Pattern | Pattern code |
|---|---|
| `co_occurrence` | `COOCCURRENCE` |
| `flood` | `FLOOD` |
| `sequence` | `SEQUENCE` |
| `persistence` | `PERSISTENCE` |
| `absence` | `ABSENCE` |
| `parent_child` | `PARENTCHILD` |
| `cross_source` | `CROSSSOURCE` |
| `threshold` | `THRESHOLD` |

Examples:

```text
CORRTEST_FLOOD_P01_INTERFACEDOWN_7Q9K2M4A
CORRTEST_FLOOD_P01_SYNTHETIC_7Q9K2M4A
CORRTEST_SEQUENCE_N01_LOGINFAIL_7Q9K2M4A
CORRTEST_SEQUENCE_N01_PRIVCOMMAND_7Q9K2M4A
CORRTEST_PARENTCHILD_P01_PARENT_7Q9K2M4A
CORRTEST_PARENTCHILD_P01_CHILD_7Q9K2M4A
```

The run-short token makes alert names unique and easy to search visually, but it is not an authority boundary. Assertions, ownership, and cleanup always use the full run identifier plus returned OSCAR resource identifiers. The harness rejects a newly generated short token if it collides with retained harness history or an existing owned rule name on the target.

Temporary rule names use the parallel lowercase grammar:

```text
corrtest-<pattern>-<case-code>-<run-short>
```

For example, `corrtest-flood-p01-7q9k2m4a`. The rule description includes the full run ID, scenario slug, case slug, harness version, and an explicit temporary-resource warning. `created_by`, where supported, is `oscar-corrtest/<version>`.

### 10.5 Required label contract

Every source alert carries the following labels. Every synthetic parent carries them through its rule `emit_spec.labels`. Parent-child cases, which do not emit a synthetic parent, preserve them on the existing parent and child alerts.

| Label | Example | Purpose |
|---|---|---|
| `category` | `corrtest_flood` | Familiar OSCAR category filter that identifies both the harness and pattern |
| `oscar_test` | `true` | Broad filter for all OSCAR-generated test traffic |
| `oscar_test_harness` | `corrtest` | Distinguishes this harness from other test producers |
| `oscar_test_schema_version` | `v1` | Version of this naming and label contract |
| `oscar_test_run_id` | `crt_01K2...` | Exact, globally unique run filter and ownership key |
| `oscar_test_run_short` | `7Q9K2M4A` | Human-readable value also present in alert and rule names |
| `oscar_test_suite` | `builtin-all` | Suite-level filtering |
| `oscar_test_scenario` | `flood-basic` | Scenario-level filtering |
| `oscar_test_pattern` | `flood` | Canonical correlation pattern filter |
| `oscar_test_case` | `emits-one-parent-at-threshold` | Stable case slug |
| `oscar_test_case_code` | `P01` | Compact case identifier present in the alert name |
| `oscar_test_polarity` | `positive` | `positive`, `negative`, or `control` |
| `oscar_test_alert_class` | `source` | Broad `source` or `synthetic` filter |
| `oscar_test_alert_role` | `interface_down` | Semantic purpose such as `interface_down`, `login_failure`, `heartbeat`, `parent`, `child`, `resolution`, or `synthetic_parent` |
| `oscar_test_rule_name` | `corrtest-flood-p01-7q9k2m4a` | Direct visual link to the temporary correlation rule |

Pattern-required labels such as grouping keys, threshold distinct values, and cross-source identifiers are added alongside the reserved labels. Existing OSCAR correlation labels remain authoritative for correlation results and are never overwritten by the harness.

The compiler owns `alertname`, `category`, and every `oscar_test_*` label. A custom scenario cannot override them. It supplies a semantic role/name, and the compiler produces the physical alert name and rewrites the temporary rule's alert-name matches to the same value.

### 10.6 Fingerprint-safe annotations

Labels participate in alert identity and deduplication. Run, scenario, case, pattern, and role labels remain stable for every occurrence that is intended to share a fingerprint. Per-send information does not become a label unless the pattern explicitly requires that label to vary.

The following values are annotations:

- `oscar_test_event_id`: unique identifier for one planned send attempt;
- `oscar_test_event_index`: ordinal inside the case;
- `oscar_test_purpose`: plain-language reason this alert exists;
- `oscar_test_expected`: plain-language expected outcome;
- cycle, phase, delay, and other occurrence-specific details;
- a harness run URL when the deployed UI has a stable externally reachable base URL.

The ordinary summary follows this visual convention:

```text
[CORRTEST][FLOOD][P01][7Q9K2M4A] source alert 3 of 5
```

This deliberately mirrors the performance framework's rule that flapping-cycle details belong in annotations so they do not change the alert fingerprint. A pattern may add a varying label only when distinct label values are the behavior under test, as with threshold cardinality or cross-source identity.

### 10.7 Manual inspection contract

For every case, the compiler produces a stored inspection manifest containing:

- the exact full run ID and short token;
- the alert-name prefix and all concrete alert names;
- the category, pattern, scenario, case, polarity, and role filters;
- the temporary rule name and returned OSCAR rule ID;
- source and expected-synthetic fingerprints as they become available;
- the run's UTC observation interval;
- target-specific OSCAR UI links when the target profile can generate stable deep links.

The run detail UI exposes this as an **Inspect in OSCAR** panel. It provides copy buttons for individual values and ready-to-copy filter recipes:

```text
Exact run:       oscar_test_run_id = <full-run-id>
Pattern traffic: category = corrtest_flood
Case traffic:    oscar_test_run_id = <full-run-id> AND oscar_test_case_code = P01
Name fallback:   alertname contains CORRTEST_FLOOD_P01
Synthetic only:  oscar_test_run_id = <full-run-id> AND oscar_test_alert_class = synthetic
```

If OSCAR supports filter-bearing UI URLs, the panel opens those views directly. If it does not, it opens the relevant OSCAR page and presents the exact values to paste into its filters. Reports and exported bundles contain the same inspection manifest, so manual investigation remains possible after the harness process stops.

## 11. Built-in suite contract

Each pattern ships with at least one positive and one negative test. A suite manifest records the expected observation sources and maximum runtime.

| Pattern | Positive proof | Required negative proof |
|---|---|---|
| `co_occurrence` | Required alert types in one group/window emit one parent | Missing one required type emits no parent |
| `flood` | Minimum count in one group/window emits one parent | Count below threshold emits no parent |
| `sequence` | Required ordered alerts emit one parent | Same alerts in invalid order emit no parent |
| `persistence` | Unresolved alert persists long enough to emit one parent | Resolution before the duration emits no parent |
| `absence` | Missing heartbeat after the expected interval emits one parent | Continued heartbeat prevents a parent during the bounded observation window |
| `parent_child` | Existing parent links child and applies configured per-notifier suppression/tagging | Child without a matching parent is released without correlation suppression |
| `cross_source` | Required source-specific alerts emit one parent | Same alert names with an invalid source mix emit no parent |
| `threshold` | Required distinct values reach threshold and emit one parent | Repeated values below distinct threshold emit no parent |

Every positive synthetic-parent case also asserts that exactly one parent is emitted during a stabilization period. Every negative case observes for the full decision window plus bounded transport and audit lag. A momentary zero count is never accepted as negative proof.

## 12. Evidence collection and oracle rules

### 12.1 Evidence hierarchy

The harness prefers evidence in this order:

1. Correlation audit rows tied to the run, rule, alert fingerprint, and time window.
2. Alert history showing the synthetic parent or correlated child labels.
3. Notification audit proving delivery, suppression, or per-notifier behavior.
4. Rule read-back and deletion read-back.
5. Injection HTTP request and response records.

Transport acceptance is supporting evidence, not final proof of correlation.

For current OSCAR compatibility, the harness resolves each server-assigned alert fingerprint by polling alert history with the exact run-unique physical `alertname` and bounded creation-time range, then reads the fingerprint from that history row before querying correlation audit. Client-side fingerprint calculation is permitted only as a diagnostic cross-check; it is never an assertion key or cleanup authority. Zero or multiple candidate history rows after the bounded resolution window is `INCONCLUSIVE` or `ERROR`, never negative proof.

Negative cases require a positive eligibility anchor. The harness must first observe the injected child in alert history and, where the target exposes it, a non-triggering correlation-audit outcome such as `released_no_trigger`, `pass_through`, or the pattern-specific cancellation outcome. Only then may the absence of a forbidden synthetic parent or dispatch result be evaluated across the full window. Absence of all evidence cannot produce `PASS`.

### 12.2 Observation windows

Each assertion has:

- an earliest observation time;
- a hard deadline;
- a poll interval with bounded jitter;
- a stabilization duration after first satisfaction;
- a maximum accepted evidence age;
- a declared terminal condition.

Positive assertions can finish after they remain satisfied through stabilization. Negative assertions must run through their complete bounded window. Polling uses monotonic process time for deadlines and UTC wall-clock timestamps for evidence correlation.

Current correlation-audit writes may be buffered for approximately five seconds or until the batch threshold is reached, can lose oldest buffered rows under extreme backpressure, and are subject to target retention (currently 90 days by default). The capability snapshot records the target's observed/declared audit-lag and retention limits; assertion windows include bounded audit lag, and missing rows caused by an unavailable or demonstrably lossy evidence surface yield `INCONCLUSIVE`, not `PASS`.

### 12.3 Terminal evidence

The final assertion record stores:

- the typed expectation;
- the normalized observed value;
- the exact evidence identifiers used;
- the evaluated time range;
- whether target capability gaps affected confidence;
- a human explanation;
- the final verdict.

Assertions never depend on UI text, log scraping, or undocumented database rows.

### 12.4 Redaction

The OSCAR adapter redacts headers and bodies before they reach persistence or the live event stream. At minimum it removes authorization headers, API keys, cookies, passwords, client secrets, private keys, and configured custom field names. Raw unredacted HTTP data is not written to disk.

## 13. OSCAR API contract

### 13.1 Current usable surfaces

The harness adapter is designed around these public capabilities:

- correlation rule list, validate, create, read, update, delete, import, and export for operator interoperability; harness-owned temporary rules use create/read/delete only and never use the name-upserting import route;
- per-alert correlation audit and child evidence;
- notification audit list and export;
- label-safe alert injection through middleware `POST /api/v1/alerts`; the mapping-enabled `/alerts/webhook` route and upstream Alertmanager `/api/v2/alerts` are not version-1 harness injection paths;
- alert history or equivalent alert-query surface;
- service health and readiness.

The exact base prefix is configured per target and discovered or validated at preflight.

The adapter never supplies an `oscar_fingerprint` or `am_fingerprint` label. OSCAR's current public-v1 `AlertGroup` schema requires the Alertmanager transport field `fingerPrint`; the compatibility adapter derives that field deterministically from stable labels solely to satisfy the request envelope and rate-limiter semantics. It is never an assertion key, evidence identity, or cleanup authority. The server-assigned OSCAR fingerprint resolved from alert history remains authoritative. For each API profile the adapter parses the complete injection response body and distinguishes accepted-and-scheduled from ACL-filtered, per-fingerprint-rate-limited, circuit-breaker-queued, partially accepted, and unknown responses. A 2xx with a drop/queue/unknown status is not injection proof. Every target preflight performs a label-survival probe through the selected injection route and reads the alert back from history; failure to round-trip every reserved label blocks mutation runs on that target. Mutation budgets also respect the target's global request limiter.

### 13.2 Required OSCAR improvements

Reliable black-box automation should add or guarantee:

1. A discoverable pipeline-mode signal covering both Alertmanager-to-correlator publication (`CORRELATOR_NATS_PUBLISH_ENABLED`) and correlator side effects (`CORRELATOR_DISPATCH_ENABLED`), including fail-open/breaker state where applicable.
2. End-to-end preservation of `oscar_test_run_id` on source alerts, synthetic parents, correlation audit rows, and notification audit rows.
3. End-to-end preservation of the full reserved test-label contract on alert history, synthetic parents, and notification evidence where labels are represented.
4. Correlation-audit filtering by test run, pattern, rule identifier, outcome, and created-at range—not only by an alert fingerprint.
5. Alert-history filtering by exact label key/value pairs, alert-name prefix, and a bounded created-at range.
6. Injection responses that return each server-assigned `oscar_fingerprint`; until available, the harness uses exact-alertname history read-back as specified in §12.1.
7. Idempotency keys for rule creation and alert injection, or documented duplicate behavior.
8. A capabilities/version endpoint describing supported patterns, rule schema version, pipeline mode, evidence filters, guardrail limits, supported OSCAR UI deep links, and maximum accepted timing values.
9. Externally reachable correlator readiness, or correlator/NATS readiness folded into the capabilities response.
10. Stable machine-readable error codes in addition to human detail strings, plus public API documentation for the supported correlation, alert-history, injection, and notification-audit surfaces.

The harness can ship a constrained compatibility mode before all improvements exist, but it must classify weak or ambiguous proof as `INCONCLUSIVE`. It must not query OSCAR's internal databases to compensate. Until pipeline mode is discoverable, it is an operator-declared target property that must be recorded in the capability snapshot and verified during non-production qualification:

- `publication_disabled`: correlator evaluation/audit evidence is unavailable; correlation scenarios are unsupported and cannot pass;
- `phase_a_audit_only`: audit decisions may exist, but synthetic-parent history and notifier side effects are unavailable; assertions requiring those surfaces are `SKIPPED` or `INCONCLUSIVE`, including negative absence assertions;
- `phase_b_dispatch`: audit, synthetic-parent, and notifier evidence may be asserted subject to readiness and label-survival checks;
- `unknown`: no scenario whose proof depends on pipeline state may report `PASS`.

### 13.3 Adapter compatibility

Target discovery produces a capability snapshot stored with each run. It includes API profile, declared/discovered pipeline mode, readiness confidence, evidence filters, guardrail configuration, rate limits, audit lag/retention assumptions, and label-survival probe results. The adapter selects a supported API profile from that snapshot. Unknown future profiles, unknown pipeline state for a required assertion, failed label survival, or unverified required readiness fail preflight with an actionable compatibility message instead of attempting best-effort mutations.

## 14. Persistence design

### 14.1 Why SQLite

The expected workload is one local application process, low write volume, frequent historical reads, and strong value from transactional state. SQLite provides durable local storage without a second service. WAL mode keeps the web UI responsive while the run coordinator appends events.

The selected driver must:

- implement `database/sql`;
- require no CGO;
- support Linux AMD64 and ARM64 cross-compilation;
- be pinned with its coupled dependencies;
- embed SQLite 3.51.3 or newer because earlier current WAL releases contain a fixed corruption defect.

The initial recommendation is `modernc.org/sqlite`. It is isolated behind a storage package so a later driver change does not affect application services.

### 14.2 Database settings

On every connection, the application enables:

- `journal_mode=WAL`;
- `foreign_keys=ON`;
- `busy_timeout=5000` milliseconds;
- `synchronous=FULL`;
- a bounded connection pool suitable for one writer and several readers.

Writes are short transactions. Network calls never occur inside a SQLite transaction. The run coordinator serializes lifecycle writes through repositories. The database must reside on a local filesystem, never NFS or another network filesystem.

### 14.3 What SQLite stores

SQLite stores durable queryable facts:

- target metadata and credential references;
- scenario source and content digest;
- immutable compiled plans;
- run and case state;
- append-only lifecycle events;
- assertion expectations and normalized observations;
- owned-resource cleanup ledger;
- artifact manifests and hashes;
- canonical final report JSON;
- schema migration history.

SQLite does not store large raw payloads, generated HTML assets, or ZIP exports as BLOBs.

### 14.4 Logical schema

| Table | Purpose and important fields |
|---|---|
| `schema_migrations` | Applied migration version and timestamp |
| `targets` | ID, display name, base URL, API profile, TLS policy, CA path, credential reference, timestamps |
| `scenarios` | ID, name, API version, source document, SHA-256, built-in flag, timestamps |
| `runs` | ID, target/scenario references, status, verdict, cleanup status, harness version, capability snapshot, compiled plan, canonical report, start/end times, terminal error |
| `run_cases` | Run reference, pattern, name, status, verdict, start/end times |
| `run_events` | Monotonic per-run sequence, case reference, type, level, UTC time, summary, structured detail |
| `alert_attempts` | Run/case reference, event ID/index, physical alert name, semantic role, identity-label JSON, fingerprint, send state, request/response artifact references, timestamps |
| `assertions` | Case reference, stable assertion key, kind, expected JSON, observed JSON, verdict, explanation, observation bounds |
| `resources` | Run reference, kind, external ID/name, ownership token, lifecycle state, creation/deletion times, cleanup error |
| `artifacts` | Run/case reference, kind, relative path, MIME type, SHA-256, byte size, redaction state, creation time |

All JSON columns contain canonical versioned application structures. Foreign keys use restrictive deletion by default. Deleting a run is an explicit service operation that removes artifact files and database rows together; partial deletion is reported and recoverable.

### 14.5 Filesystem layout

```text
data-dir/
  corrtest.db
  corrtest.db-wal
  corrtest.db-shm
  runs/
    <run-id>/
      scenario.yaml
      compiled-plan.json
      report.json
      report.html
      junit.xml
      evidence/
        <sequence>-<kind>.json
      manifest.json
  exports/
```

Files are first written to a temporary sibling and atomically renamed. Every artifact manifest row records its size and SHA-256 digest. Paths are stored relative to the data directory and validated against traversal.

### 14.6 Backup, retention, and repair

- The UI and CLI provide an online SQLite backup operation. They do not copy a live database file without coordinating WAL state.
- A run export queries the required rows and artifacts into a new ZIP; it does not include the entire database.
- Version 1 keeps runs until an operator deletes them. No implicit age-based purge is enabled.
- An optional disk warning threshold blocks new runs before storage exhaustion but never deletes evidence automatically.
- Startup performs integrity and migration checks before accepting runs. Migration failure leaves the service in read-only diagnostic mode.
- Missing or hash-mismatched artifact files remain visible as report integrity warnings; the database record is not silently removed.

## 15. Report and export contract

The canonical report is versioned JSON generated from stored domain data. HTML and JUnit are projections of that JSON.

### 15.1 Report contents

- harness version and build identifier;
- target display name, sanitized URL, OSCAR capability/version snapshot;
- scenario identity and digest;
- immutable compiled plan;
- run and cleanup status;
- case verdicts and durations;
- expected-versus-observed assertion details;
- the inspection manifest with alert names, labels, rule identity, fingerprints, time bounds, and optional OSCAR deep links;
- resource creation and cleanup ledger;
- redacted HTTP and audit evidence references;
- artifact integrity hashes;
- warnings, capability limitations, and terminal errors.

### 15.2 Portable evidence bundle

The ZIP export contains:

- original scenario;
- compiled plan;
- canonical report JSON;
- self-contained HTML report;
- JUnit XML;
- redacted raw evidence;
- a manifest with path, size, MIME type, and SHA-256 for every file.

The report viewer does not require network access or JavaScript. If the source harness database is later removed, the exported bundle remains reviewable and verifiable.

## 16. Web UI design inspired by OTTO Gateway

The OTTO Gateway UI demonstrates the right operational shape: server-rendered templates, embedded assets, a compact always-dark header, token-driven light/dark themes, summary cards, dense tables, semantic status pills, and SSE-backed live logs. The harness adopts those principles without copying OTTO branding or creating a coupled UI package.

### 16.1 UI architecture

- Parse one shared base template and page templates once at startup.
- Render into a buffer before writing the HTTP response so template failures return a clean error page.
- Embed templates, CSS, icons, and JavaScript with the Go binary.
- Serve static assets as GET/HEAD only with explicit content types and cache policy.
- Use semantic HTML and ordinary links/forms as the baseline.
- Use small vanilla JavaScript modules for theme control, form enhancements, API requests, and SSE updates.
- Put page configuration in a serialized data island; use `data-*` attributes as stable DOM hooks.
- Avoid a Node build, frontend framework, runtime CDN, and generated asset hashes in version 1.

Unlike OTTO's current monolithic CSS and JavaScript files, the harness splits assets by responsibility:

```text
static/
  css/tokens.css
  css/base.css
  css/components.css
  css/pages.css
  js/theme.js
  js/run-stream.js
  js/forms.js
```

### 16.2 Theme contract

The document supports `light` and `dark`. Initial selection is:

1. a stored explicit user choice;
2. otherwise the operating-system `prefers-color-scheme` value;
3. otherwise dark.

A tiny inline script sets `data-theme` before the stylesheet loads to avoid a flash of the wrong theme. The toggle persists the explicit choice in local storage. `color-scheme` changes with the selected theme so native controls render correctly.

The toggle uses the stable accessible name “Light theme” and communicates whether that state is active with `aria-pressed`; a visible icon or non-accessible title may describe the next action. It is usable by keyboard. Theme is a presentation preference only; reports and evidence never depend on it.

### 16.3 Visual tokens

The initial palette deliberately follows OTTO's high-contrast operational visual language:

| Token | Dark | Light |
|---|---|---|
| Page background | `#1A1D24` | `#F7F8FA` |
| Card surface | `#242832` | `#FFFFFF` |
| Hover surface | `#2C3140` | `#F1F3F7` |
| Header | `#1E2128` | `#1E2128` |
| Border | `#2D3340` | `#E5E7EB` |
| Primary text | `#FAFAFA` | `#1A1D24` |
| Muted text | `#9CA3AF` | `#4B5563` |
| Accent | `#FAD22D` | `#8A6800` for text, `#FAD22D` for filled controls |
| Pass | `#0FC373` | `#087A49` |
| Warning/inconclusive | `#FF8C0A` | `#A34D00` |
| Fail/error | `#FF3232` | `#B42318` |
| Live activity | `#AF78D2` | `#704099` |

The neutral surfaces, compact density, and yellow accent establish family resemblance. The product name and iconography remain OSCAR Correlation Test Harness. Semantic colors are never the only status cue.

Spacing uses a 4/8/12/16/24/32 pixel scale. Cards use a 6-pixel radius, inputs 4 pixels, and status pills a full radius. The font stack is native system UI with a native monospace stack for identifiers and evidence.

### 16.4 Information architecture

The header contains product identity, target health, current target, version/build, and theme toggle. Primary navigation contains:

- **Dashboard:** target readiness, recent run summary, dirty cleanup count, and quick-start actions.
- **Run test:** suite/scenario selection, target, plan preview, and execution controls.
- **Runs:** filterable durable history with verdict and cleanup status.
- **Scenarios:** built-ins, imports, validation, and read-only compiled preview.
- **Targets:** target connection, credential reference, TLS policy, capability diagnostics.
- **Settings:** bind address, data directory, retention controls when introduced, redaction, and diagnostics.

The Run detail page is the operational center:

```text
+---------------------------------------------------------------+
| PASS   Cleanup: CLEAN   Target: lab-a   08:14–08:16   Export |
+---------------------------------------------------------------+
| Summary cards: 16 cases | 16 passed | 0 failed | 84 artifacts |
+---------------------------------------------------------------+
| Cases / Timeline / Assertions / Evidence / Resources          |
|                                                               |
| 12:14:02  rule created          flood-positive               |
| 12:14:03  5 alerts accepted     fingerprints ...             |
| 12:14:08  parent observed       audit row ...                 |
| 12:14:13  stabilized            exactly one parent           |
| 12:14:14  rule deleted          cleanup clean                |
+---------------------------------------------------------------+
```

Every case detail includes the **Inspect in OSCAR** panel defined by the identity contract. The panel groups filters by run, pattern, case, source/synthetic role, and fingerprint instead of making the operator reconstruct values from raw JSON.

### 16.5 Live behavior and accessibility

- One SSE endpoint streams persisted run events after a caller-provided sequence number.
- On reconnect, the browser requests missed events before returning to live mode.
- The timeline exposes connected, reconnecting, paused, and terminal states.
- Auto-scroll pauses when the user scrolls away and can be resumed explicitly.
- Only important state changes enter polite live regions; high-frequency raw evidence does not spam assistive technology.
- Tables have headers, captions or accessible names, responsive overflow, and a card fallback where narrow tables become unreadable.
- Every form control has a visible label and inline validation linked with `aria-describedby`.
- Focus-visible outlines are at least 3 pixels and are never removed.
- Animation respects `prefers-reduced-motion`.
- Theme contrast and shared accessibility hooks are contract-tested.

## 17. CLI contract

The CLI uses subcommands implemented with the standard library unless the final command tree proves a dedicated parser necessary.

```text
oscar-corrtest serve
oscar-corrtest doctor --target lab-a
oscar-corrtest target list
oscar-corrtest target add --name lab-a --url https://oscar.example
oscar-corrtest scenario list
oscar-corrtest scenario validate ./scenario.yaml
oscar-corrtest plan ./scenario.yaml --target lab-a
oscar-corrtest run builtin:all --target lab-a
oscar-corrtest run ./scenario.yaml --target lab-a
oscar-corrtest runs list --verdict fail
oscar-corrtest runs show <run-id>
oscar-corrtest cleanup retry <run-id>
oscar-corrtest export <run-id> --format bundle
oscar-corrtest backup --output ./corrtest-backup.db
```

Human output defaults to concise tables and progress lines. `--output json` produces versioned machine-readable envelopes. `--no-color` and the `NO_COLOR` convention are supported. Destructive commands require an exact run or resource identifier and an explicit confirmation unless `--yes` is supplied.

Superseded by the 2026-08-20 amendment: `serve` binds to `0.0.0.0:8787` by
default and prints an unauthenticated-network warning. Explicit loopback,
bearer/TLS, and trusted-proxy modes remain available.

## 18. Configuration and credentials

Configuration precedence is:

1. command flags;
2. environment variables;
3. configuration file;
4. documented defaults.

Interactive installation defaults to XDG paths. A system service normally uses `/etc/oscar-corrtest` for configuration and `/var/lib/oscar-corrtest` for state, provided explicitly by flags or the service unit.

Target credentials are never stored as plaintext in SQLite. A target stores a credential reference such as an environment-variable name, mounted-file path, or systemd credential name. The application resolves the reference only when making a request. The UI can confirm whether a credential is available but cannot display its value.

TLS verification defaults to enabled. A custom CA file is supported. An insecure mode requires an explicit per-target choice, is displayed as a persistent warning, and is recorded in sanitized report metadata.

## 19. Safety and security

### 19.1 External mutation safety

- Every created rule carries a reserved ownership prefix and run token.
- Harness-owned temporary rules are created only through the create endpoint; the import endpoint is never used because current OSCAR import semantics upsert by name.
- Every harness-owned alert and synthetic-parent specification carries the reserved naming and label contract; scenario input attempting to override reserved identity fields is rejected before target mutation.
- Cleanup deletes by returned external identifier and verifies ownership before deletion.
- Name-based cleanup is allowed only as a recovery fallback when the exact reserved prefix and run token match.
- The harness never updates or deletes a rule it did not create.
- A timeout or 5xx from rule creation is an unknown outcome. Before retrying, the harness reads back the unique proposed name, verifies the full ownership description/token, and either adopts the returned identifier or stops with cleanup status unknown; it never blindly re-POSTs or deletes a lookalike.
- Scenario compilation enforces maximum alert count, rate, window, duration, and active-resource limits.
- A run plan shows its mutation budget before execution.
- Cancellation always transitions to cleanup.

### 19.2 Web safety

- State changes use POST/PUT/DELETE, not GET.
- Same-origin checks and CSRF tokens protect browser mutations.
- Security headers include a restrictive Content Security Policy, frame denial, MIME sniffing prevention, and a conservative referrer policy.
- Templates auto-escape dynamic content. Evidence is rendered as text, never injected HTML.
- Download filenames and artifact paths are server-generated and traversal-safe.
- The intentional wildcard default warns that every reachable peer can use the
  harness's configured mutation authority; operators supply the lab-network or
  firewall boundary. This supersedes the original loopback-only threat-model
  invariant.

### 19.3 Local data safety

- Configuration and data directories use restrictive permissions.
- Reports contain sanitized target metadata, not credentials.
- SQLite and artifact backups use atomic destinations and never overwrite an existing file unless explicitly requested.
- Export creation validates every artifact hash and reports omissions.

## 20. Failure handling

| Failure | Required behavior |
|---|---|
| OSCAR unreachable during preflight | No target mutations; run ends `ERROR`, cleanup not required |
| Rule creation times out or returns 5xx with unknown outcome | Reconcile by idempotency key or unique ownership-name read-back; adopt only after full token/description verification, never use import/upsert, otherwise mark cleanup unknown |
| Alert POST returns ACL-filtered, per-fingerprint-rate-limited, circuit-breaker-queued, partially accepted, or an unknown 2xx body | Preserve the full redacted response, classify injection as failed or indeterminate, and continue only if server-side history proves the scenario's minimum accepted stimuli |
| Audit endpoint unavailable | Try other declared evidence sources; if required proof remains unavailable, verdict is `INCONCLUSIVE` |
| Process receives SIGINT/SIGTERM | Stop scheduling alerts, persist cancellation, attempt bounded cleanup, then exit |
| Host or process crashes | Mark active run interrupted at next startup; do not resume injection; retry provable cleanup |
| SQLite busy beyond timeout | Stop run progression, preserve error, and enter cleanup; do not continue with unpersisted lifecycle state |
| Artifact write fails | Record integrity error; if evidence required for an assertion cannot be retained, do not report `PASS` |
| Cleanup API fails | Preserve resource row and error; final cleanup status is dirty or unknown; allow retry |
| Browser disconnects | Run continues server-side; reconnect replays persisted events by sequence |

## 21. Test strategy

### 21.1 Unit tests

- scenario schema and strict unknown-field rejection;
- duration, repeat, rate, and mutation-budget validation;
- deterministic variable expansion and plan hashing;
- alert/rule name normalization, pattern-code mapping, short-token collision handling, reserved-label rejection, and exact inspection-manifest generation;
- fingerprint stability across repeat/flap occurrences and intentional variation for pattern-required distinct labels;
- every assertion kind across pass, fail, inconclusive, and malformed evidence;
- deadline and stabilization behavior with a fake clock;
- report and JUnit projection;
- redaction and artifact-path validation;
- state-machine transition invariants;
- resource ownership and cleanup eligibility;
- SQLite repository behavior and migrations.

### 21.2 Contract tests

- recorded OSCAR request/response fixtures from all supported API profiles;
- alert payload compatibility with the useful semantics from existing Python senders;
- injection-response fixtures for accepted, ACL-filtered, fingerprint-rate-limited, circuit-breaker-queued, partial, and unknown 2xx bodies;
- label-survival preflight through the exact `POST /api/v1/alerts` route;
- history read-back of the server-assigned fingerprint, with a deliberately wrong client-side calculation that cannot affect the verdict;
- Phase-A and unknown-pipeline profiles proving synthetic/notifier positive and negative assertions cannot report `PASS`;
- preservation of every reserved test label across source-alert history, synthetic-parent history, and available notification evidence;
- rule validation/create/read/delete round trips against an OSCAR-compatible fake server;
- duplicate-name and lost-create-response reconciliation proving import/upsert is never used and pre-existing rules are never deleted;
- pagination, filtering, time-boundary, retry, and machine-error behavior;
- capability negotiation and unsupported-profile rejection.

### 21.3 Integration tests

- full runner against a deterministic in-process fake OSCAR server;
- crash after rule creation followed by startup recovery and cleanup;
- SSE reconnect with event replay and no duplicate sequence rendering;
- SQLite WAL reads while a run appends events;
- online backup and restore;
- export integrity and offline HTML rendering;
- signal cancellation and bounded shutdown.

### 21.4 UI tests

- shared template structure, navigation, form labels, and status text;
- pre-paint theme selection and persisted override;
- light/dark contrast checks for tokens and status combinations;
- keyboard navigation and focus visibility;
- run timeline live/reconnect/pause states;
- browser-level smoke tests for creating a target, running a fake case, viewing history, and exporting a report.

### 21.5 OSCAR end-to-end qualification

A dedicated non-production OSCAR environment runs all sixteen minimum cases. The qualification suite verifies:

- the declared pipeline mode, including both NATS publication and correlator dispatch state, before trusting any verdict;
- externally visible correlator readiness or an explicit recorded compatibility limitation;
- reserved-label survival through the configured target's operator mappings and ACLs;
- all positive and negative pattern contracts;
- synthetic-parent cardinality and stabilization;
- per-notifier parent-child behavior;
- cleanup after success, failure, cancellation, and process interruption;
- evidence filtering by test run;
- repeated suite execution without cross-run interference.

## 22. Distribution and operation

### 22.1 Standalone repository boundary

The source repository is named `oscar-corrtest`. During development it is checked out at `oscar_app/oscar-corrtest`, beside the existing `oscar` checkout rather than inside it. `oscar-corrtest` has its own `.git` directory, Go module, dependency lock data, Makefile, CI definitions, release artifacts, version tags, issue history, and release cadence.

No build, test, lint, package, or release command may read `../oscar`, import an OSCAR source package, copy an OSCAR-generated file, or depend on an OSCAR container. OSCAR integration is only through versioned HTTP contracts and committed sanitized fixtures. A clean checkout of `oscar-corrtest` on an otherwise empty Linux runner must pass the complete build.

### 22.2 Build and CI parity

The Makefile is the single build interface used by developers, GitHub Actions, and GitLab CI. At minimum it exposes `fmt-check`, `vet`, `test`, `test-race`, `build`, `cross`, `package`, `checksums`, and `ci`. The `ci` target composes every required merge gate; CI YAML files orchestrate caching and artifacts but do not reimplement test logic.

GitHub and GitLab run equivalent stages:

1. formatting, vetting, static analysis, and module-integrity checks;
2. unit and integration tests;
3. race-detector tests on Linux AMD64;
4. CGO-disabled Linux AMD64 and ARM64 builds;
5. archive, checksum, and clean-checkout independence verification;
6. tag-triggered release publication, using each platform's native release mechanism.

Third-party GitHub Actions are pinned by immutable commit SHA with a version comment. GitLab jobs use digest-pinned container images where the registry supports multi-architecture manifests. Both systems upload already-packaged `.tar.gz` files rather than raw executables so executable permissions survive artifact transport.

Linux archives are byte-reproducible: the package target uses GNU tar with sorted members, normalized owner/group and commit-derived modification time, then `gzip -n`. Linux CI is the canonical packaging environment; macOS packaging requires GNU tar as `gtar`.

Release artifacts include:

- static Linux AMD64 executable;
- static Linux ARM64 executable;
- SHA-256 checksum file;
- optional OCI image containing only the binary and required certificates;
- example systemd unit and environment/config templates;
- scenario JSON Schema and built-in scenario documentation.

The binary exposes:

- `/healthz` for process health;
- `/readyz` for database readiness and successful migrations;
- a diagnostics view and `doctor` command for target/API readiness;
- structured application logs with run and case identifiers, with secrets redacted.

The first release is single-node. Operators back up both the application database through the supported backup command and any desired run evidence directories. Individual evidence bundles remain the primary long-term portable record.

## 23. Delivery slices

This section defines sequencing intent, not an implementation task plan.

### Slice 1: Executable skeleton and durable run ledger

Deliver configuration, SQLite migrations, target metadata, run history, artifact storage, report JSON, CLI shell, embedded UI shell, light/dark theme, and fake-server test infrastructure. No real OSCAR mutation yet.

### Slice 2: Flood vertical slice

Deliver target diagnostics, correlation-rule API adapter, middleware alert injection, audit/history observation, the flood positive and negative cases, cleanup recovery, live SSE timeline, and portable report export. This is the first end-to-end usable release.

### Slice 3: Window and order patterns

Add `co_occurrence`, `sequence`, `cross_source`, and `threshold`, including their negative proofs and shared cardinality assertions.

### Slice 4: Timer-driven patterns

Add `persistence` and `absence`, fake-clock coverage, long-poll UI states, cancellation, and reboot recovery qualification.

### Slice 5: Relationship and notifier proof

Add `parent_child`, child-link evidence, per-notifier suppression/tagging assertions, and notification-audit integration.

### Slice 6: Custom scenarios and operational hardening

Add imported YAML/JSON scenarios, scenario UI validation, online backup, manual retention/delete controls, systemd/OCI packaging, full browser tests, and all-pattern OSCAR qualification.

OSCAR API enhancements needed for strong test isolation should be delivered before or alongside Slice 2. If they are unavailable, Slice 2 is explicitly labeled compatibility mode and cannot treat ambiguous evidence as pass.

### 23.1 Implementation-plan mapping and mandatory gates

The six product slices are delivered by seven implementation plans because Slice 1 is split into two independently reviewable foundations:

| Plan | Product-slice ownership | Mandatory gate before completion |
|---|---|---|
| Plan 1 | Slice 1A: repository, executable/HTTP shell, theme, builds, packages, dual CI | Historical foundation gate; its loopback-only clause is superseded by the 2026-08-20 listener/distribution amendment |
| Plan 2 | Slice 1B: configuration, SQLite ledger, artifact/report history | Migration/WAL/recovery/backup tests and durable interrupted-run evidence |
| Plan 3 | Slice 2: first live flood vertical slice | Pipeline-mode/readiness snapshot, label-survival probe, history fingerprint read-back, safe create-only rule lifecycle, Phase-A false-pass tests |
| Plan 4 | Slice 3: window/order patterns | Per-pattern positive/negative fixtures matching current OSCAR ordering, source, and distinct-value semantics |
| Plan 5 | Slice 4: timer patterns | Fake-clock coverage plus live persistence/absence timing qualification |
| Plan 6 | Slice 5: parent-child/notifier evidence | Phase-B qualification and notification-evidence contract verification |
| Plan 7 | Slice 6: custom scenarios and operational hardening | Historical remote-serving clause superseded by the 2026-08-20 amendment; full browser/security and all-pattern qualification remains required |

## 24. Acceptance criteria

The version 1 design is complete when all of the following are true:

- One executable starts the CLI and embedded UI on each supported release
  platform without Python, Node, CGO, or an external database.
- The default unauthenticated wildcard listener is directly reachable and
  warns about its mutation authority; explicit loopback and authenticated
  modes retain their respective Host/authentication protections.
- A user can configure an OSCAR target without storing its secret value in SQLite.
- The harness can validate, create, read, and delete a uniquely owned correlation rule through public APIs.
- Temporary rule setup uses create/read/delete only, never import/upsert, and unknown create outcomes are reconciled without modifying a pre-existing same-name rule.
- Every generated source alert, synthetic parent, and temporary rule follows the documented human-readable naming convention.
- A user can isolate traffic by full run, pattern, scenario, case, polarity, role, alert-name prefix, or temporary rule name using values shown in the harness UI and exported report.
- Repeat occurrences preserve fingerprints unless label variation is explicitly required by the pattern under test.
- Built-in positive and negative cases exist for all eight correlation patterns.
- Each case produces terminal evidence and one of the documented verdicts.
- Negative cases observe through their full decision windows.
- A passed synthetic-parent assertion also proves exactly-one cardinality through stabilization.
- Interrupted runs are visible after restart and owned resources can be cleaned up safely.
- Run history, assertions, cleanup state, and canonical reports survive restart.
- The Runs UI can filter and reopen historical reports.
- A portable evidence ZIP contains valid manifest hashes, report JSON, standalone HTML, JUnit, plan, scenario, and redacted evidence.
- Light and dark modes both meet the contrast, keyboard, focus, and reduced-motion contracts.
- CLI JSON envelopes and exit codes are stable and documented.
- All target mutation, artifact integrity, report projection, recovery, backup, and UI contract tests pass.
- The full suite can run twice consecutively on a non-production OSCAR target without leftover rules or cross-run evidence contamination.

## 25. Decisions held for implementation planning

These are implementation-level choices to confirm in the owning follow-on plan:

- exact YAML decoder and schema-validation dependency;
- whether the standard-library flag parser remains ergonomic enough after final command enumeration;
- the initial OSCAR API profile name and exact endpoint mappings;
- concrete built-in rule and alert fixtures copied from the deployed OSCAR schema;
- maximum default timing and mutation budgets for each pattern;
- release-signing mechanism beyond the pinned dual-CI checksum artifacts.

They do not change the product boundary: one Go binary, embedded operational UI, public OSCAR APIs, deterministic scenarios, SQLite-backed history and recovery, filesystem evidence, and explicit outcome proof.
