# Threshold duplicate-value control — 2026-10-13

This is a local runner/HTTP control and test-model correction, **not positive Threshold trigger qualification or a sixth complete reliability scenario**. The existing five full fixtures remain unchanged. No backend, schema, endpoint, dependency, container, activation or deployment change.

## Backend contract and deliberate scope

Read OSCAR `patterns/threshold.py`, `schemas/match_criteria/threshold.py`, `core/lua/add_threshold_and_check.lua`, and CorrTest's compiler/scenario semantics. Ordinary Threshold groups by configured labels and triggers on approximate Redis `PFCOUNT`, not raw alert count. HLL keys use fixed event-time buckets: `floor(event_timestamp_ms / window_ms)`. Close events on opposite sides of a bucket boundary do not accumulate together. Resolutions do not advance state. Missing distinct labels are rejected by Python; present empty strings reach Lua but are not added to HLL. A string `"0"` is a real value.

CorrTest's old local model used a global exact set across groups, time and status. The corrected helper explicitly remains an **exact-set approximation**, not an HLL implementation: it partitions eligible nonempty values by group and fixed event-time bucket and models the first idealized trigger only. Its multi-value positives are compatibility/test convenience; no HLL accuracy, error distribution, cardinality-boundary, TTL, cooldown, rearm, clock-clamp or concurrency proof is claimed.

The public scenario contract requires both P01 and N01, and Threshold P01 requires three distinct devices. A History-only positive case would misleadingly appear to qualify Threshold. Rather than widen the schema, `TestThresholdDuplicateValueControlThroughRealHTTPClient` compiles a normal valid scenario, then executes only its customized N01 as an isolated runner contract test. The actual HTTP responder explicitly refuses multi-value or empty-value Threshold inputs; it cannot serve as a positive HLL oracle.

## Staged control

The single N01 uses `cpu_high` with three different named sources A/B/C and the same `device=same-device` in one site. Each identity has its own fingerprint. Inputs occur at 0/3/6 seconds; A/B/C resolve at 9/12/15 seconds. Four full two-second checkpoints observe:

1. One enriched audit, no parent audit, one exact firing source episode.
2. Three enriched audits, no parent audit, three exact firing source episodes.
3. One resolved and two firing source episodes after A resolves.
4. Three resolved source episodes after all three resolve.

The test clock deliberately straddles a 30-second epoch boundary. Every bucket still contains at most one distinct value; three raw fingerprints cannot satisfy a three-device threshold. This is a matched duplicate-value control, not no-match. It avoids a timing-dependent positive HLL oracle. Recovery observations concern source History only, and audit is queried only for admitted nonempty firing inputs before recovery.

For a future full fixture, the repeated-value stimulus fragment is:

```yaml
# Case fragment only; not a standalone executable scenario.
events:
  - {role: cpu_high, source: A, status: firing, labels: {device: same-device}}
  - {role: cpu_high, source: B, status: firing, delay: 3s, labels: {device: same-device}}
  - {role: cpu_high, source: C, status: firing, delay: 6s, labels: {device: same-device}}
```

## Evidence

- `red-model.log`: five reproduced defects: group mixing, fixed-epoch mixing, resolved value, empty value and missing value counted.
- `green-model.log`: testoscar passed; eight new unit cases distinguish exact-set partition/eligibility behavior from HLL qualification.
- `red-http.log`: all four initial staged cases failed because the wire responder did not support Threshold.
- `green-focused.log`: runner/testoscar passed after narrow support.
- `green-control.log`: six HTTP cases passed: valid singleton control, false raw-count trigger → FAIL, missing startsAt → INCONCLUSIVE, ignored History recovery → FAIL, unsupported multi-value oracle → ERROR, and unsupported empty-value oracle → ERROR. Failed/inconclusive checkpoints stop later stimuli; owned cleanup remains exercised.
- `full-suite.jsonl`: **795 tests/subtests across 27 packages passed**, zero failures, with `go test ./... -count=1 -json`.

All coverage stays downstream: direct Alert Engine injection, Native Alertmanager bypassed, receiver admission untested, parent physical clear and notification delivery unproved. No source/barrier/selector identity, capacity or any of 39 UAT acceptances is inferred. Existing synthetic/legacy tests are not positive Threshold reliability qualification. Absence/collector-health remains unsupported, ticket tests excluded and live SNMP deferred.

Next positive qualification needs actual Redis/Lua observations for chosen device values, fixed event-time bucket identity and trigger audit evidence; arbitrary HLL counts must not be replaced by exact-set expectations. No local `redis-server` executable was found during this cut; no dependency or container was added. Complete retained-episode Threshold behavior also needs the external contracts listed in the handoff inventory.

## Review disposition

Fresh independent whole-cut read-only review found no blocking or concrete correctness defect. It compared the model, six HTTP cases, eight unit cases, responder and documentation against the actual Threshold Python/schema/Lua. Singleton no-trigger checks do not rely on multi-value HLL accuracy; multi/empty-value oracle refusal remains explicit. The reviewer did not rerun tests or contact live services. Actual Redis/HLL positives, TTL/cooldown/rearm and receiver/native/physical-clear evidence remain unqualified.

Failures and passes are retained below in deterministic gzip artifacts; filenames above omit the `.gz` suffix for readability.
