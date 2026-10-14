# Co-occurrence real-client HTTP qualification — 2026-10-13

Scope: CorrTest feature worktree test model, wire helper and tests only. Reuses the existing staged co-occurrence YAML unchanged. No product runner/schema change, backend edit, API, dependency, deployment, live request or container.

## Contract

Read OSCAR `oscar-correlator/src/app/patterns/co_occurrence.py`, `schemas/match_criteria/co_occurrence.py`, and `core/lua/add_cooccur_and_check_trigger.lua`. Ordinary co-occurrence counts distinct alertname fields per group, evicts timestamps below the event window, overwrites a repeated name's timestamp with the latest arriving event timestamp, and emits enriched until the first parent_emitted trigger. Required MatchClauses filter eligible names before evaluation. Resolved inputs do not advance the window. Duplicate clauses are not distinct names.

## Reproductions and validation

- `red-model.log`: five reproduced mismatches in the earlier generic model—counting duplicate clauses, mixing groups, ignoring expiry, counting resolved inputs, and missing last-arrival timestamp overwrite semantics.
- `green-model.log`: model suite passed with nine focused co-occurrence cases. Controls include valid distinct roles, repeated same role, unmatched source and refreshing a name before expiry.
- `red-http.log`: four real-client cases failed before responder support existed for co_occurrence.
- `green-focused.log`: runner and testoscar suites passed after reusing exact required_matches wire classification and actual enriched encoding. Existing Flood and sequence HTTP cases remain covered.
- `full-suite.jsonl`: **762 tests/subtests passed across 27 packages**, zero failures (`go test ./... -count=1 -json`).

The four new HTTP cases are valid staged lifecycle plus matched missing-role N01, prematurely reported trigger, missing startsAt, and History ignoring recovery. They produce PASS, FAIL, INCONCLUSIVE and FAIL as expected. Failing intermediate checkpoints stop later stimuli and owned cleanup still runs. NOT_PROVEN/NOT_TESTED boundaries remain in canonical reports. The missing-role case is not no-match, and History resolution is not parent recovery.

## Limits

The independent model describes the first ordinary trigger, not Redis TTL expiry, cooldown rearming, concurrency, broker redelivery or enhanced retained-episode repeated-input reconciliation. Local model behavior is not an executable backend or live qualification. Audit trigger decisions do not prove parent creation or delivery; exact source History status does not prove physical clear. Receiver admission, selector revision, producer barriers, alert delivery/performance, all 39 UAT acceptances and the full capacity gate remain unproved. Absence/collector-health is unsupported; ticket-provider and live SNMP tests remain excluded.

Failures and passes are retained in deterministic gzip artifacts. Parent model review and fresh independent whole-cut review found no actionable defect. Review checked ordinary first-trigger semantics, HTTP verdict/error scope and the retained 762/27 results.
