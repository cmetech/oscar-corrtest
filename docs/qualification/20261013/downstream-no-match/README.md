# Downstream no-match control qualification

This opt-in CorrTest increment adds Flood N01 `control: downstream-unmatched` with `downstream-no-match-count`. The control uses explicit unique named single-firing sources and an alertname disjoint from all compiled exact Flood fixture predicates. Five control sources are used, so the test cannot pass merely because its input count stayed below the Flood threshold. Existing scenarios and ordinary negative cases are unchanged.

Compiler and execution preflight reject unproven or overlapping fixture criteria, unsupported patterns, repeated source/event identity and missing named input identity before mutation. Receiver-admission requests remain rejected. There is no general predicate evaluator or persisted-rule snapshot engine.

The runtime requires exact named episode History still firing, plus at least one fresh positive audit for each authoritative source fingerprint: outcome `released_no_trigger`, reason `no_matching_rules`, explicitly null rule ID/name/pattern, positive audit ID, no synthetic parent or suppressed notifiers. Missing/zero rule fields are not explicit nulls. Duplicate valid receipts count each source once; an audit ID reused for different fingerprints is inconclusive. Missing evidence, index-unavailable or missing-alertname reasons, bound rules, stale/future/missing audit timestamps and stale History are inconclusive. API failure is ERROR. Candidate audit rows and source episode evidence are retained in canonical reports.

The fixture adds a two-second checkpoint and retains bounded terminal observation/owned cleanup. CLI/UI coverage text explicitly excludes receiver health/admission, selector revision/fleet coverage, selected-row isolation and notification delivery. The cases are sequential, not a 90/10 interleaved load test. History visibility and a downstream decision do not prove delivery, complete selection, physical clear or uninterrupted runtime health.

## Actual backend contract inspected (read-only)

- `oscar-correlator/src/app/core/consume.py:1103–1126`: unmatched input emits the null-rule `released_no_trigger` / `no_matching_rules` row. The preceding branch uses a distinct `no_rule_index` reason. No-match rows are always buffered; audit absence cannot establish success.
- `oscar-correlator/src/app/core/window_matching.py`: exact alertname MatchClause predicates; the fixture exclusion check only handles its known single-clause Flood shape.
- Existing public `/api/v1/correlation_rules/audit` and `/api/v1/alerts/history` adapters. No new endpoint, migration, activation or backend mutation.

Audit rows do not include source episode identity. Consequently the control is restricted to unique single-firing sources and binds freshness to the exact current History episode and audit creation time. Refire/recovery is rejected for this control. Deployed unrelated rules may match these inputs and correctly prevent the required witness. No saved or runtime fleet rule revision is claimed.

## Retained tests and failures

All tests ran locally in the absolute feature worktree with Go 1.27.0 on darwin/arm64. No containers or application services were started or changed.

| Evidence | Result |
|---|---|
| `red-contract.log.gz` | The old closed decoder rejected the new control field. |
| `green-contract.log.gz` | Five controls compile with alertnames disjoint from both fixture rules. |
| `red-null-identity.log.gz` | The old audit adapter lost the distinction between null, missing and zero rule identity. |
| `red-runner.log.gz` | The runner rejected the new witness assertion kind. |
| `green-focused-initial.log.gz` | Positive witness and absent/index-unavailable/stale/error counterexamples passed. |
| `red-exclusion.log.gz` | A supplied plan with a duplicate control event identity bypassed exclusion validation. |
| `green-exclusion.log.gz` | Unique event identity, predicate overlap/unknown-shape rejection and existing witness checks passed. |
| `green-focused.log.gz` | Adjacent compiler, API/preview, runner and scenario packages passed. |
| `suite-initial.jsonl.gz` | 679 passing test/subtest events across 27 packages. |
| `red-identity-conflict.log.gz` | Duplicate supplied source identity and audit ID reuse across different fingerprints could pass. |
| `green-identity-conflict.log.gz` | Both conflicts rejected; adjacent packages passed. |
| `suite-final.jsonl.gz` | **681 passing test/subtest events across 27 packages; no failures.** |
| `green-fixture.log.gz` | Additional committed YAML fixture decode/compile/checkpoint smoke check passed. |
| `red-resolved-history.log.gz` | A resolved History row could incorrectly witness this firing-only control. |
| `green-resolved-history.log.gz` | Resolved current History now stays INCONCLUSIVE. |
| `suite-final-status.jsonl.gz` | **683 passing test/subtest events across 27 packages; no failures.** |

Commands: focused `go test` selections recorded by test names in the logs, then `go test ./... -count=1 -json`. `git diff --check` passed. Logs are deterministic gzip (mtime=0).

Runner tests use the existing independent correlation model plus explicit History/audit test adapters to exercise witness branches. These are harness contract tests, not live backend qualification. No 39-case UAT, production capacity, native receiver flow, delivery or selected-row isolation acceptance is made. Enhanced admission/dispatch/suppression/physical clear remain disabled; tickets are excluded, live SNMP deferred, and absence/collector-health unsupported.

Parent whole-cut review found no additional actionable issue and endorsed the firing-only tightening. Backend CorrelationAudit timestamps use database write time at millisecond precision; clock skew or delayed audit remains bounded/inconclusive and does not prove selector health. Shared evidence contracts remain necessary for actual ordinary delivery/isolation and receiver selected E2E runs; see [reliability testing](../../../reliability-e2e.md).
