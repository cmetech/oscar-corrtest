# Durable CorrTest input attempt and honest cleanup retention — 2026-10-13

Scope: local CorrTest feature branch only. Existing SQLite RunEvent table, existing rule cleanup and existing lifecycle states; no new backend API/table, deployment, container, dependency or push.

## Evidence

- `red.log`: before-send tests failed because POST lacked a committed attempt; the rule-only retry test reproduced false CLEAN with unresolved input responsibility; startup with deleted/no live rules reproduced false NOT_REQUIRED.
- `green-focused.log`: runner, runtime and SQLite suites passed after the change.
- `full-suite.jsonl`: initial **729 tests/subtests passed across 27 packages**, zero failures.
- `red-legacy-report.log`: self-review reproduced NOT_REQUIRED for a legacy completed-without-verdict row whose only surviving input evidence was in the canonical report. Startup now supplies that stored report to the same retention guard.
- `final-suite.jsonl`: **730 tests/subtests passed across 27 packages**, zero failures, using `go test ./... -count=1 -json`.
- Additional qualification includes a child process killed after committing episode attempt evidence and marking its test rule deleted; reopening SQLite retained the timestamp/index and startup marked cleanup UNKNOWN. Guard cases cover rule-only runs, legacy injection/observation, old prepared events, interruption, malformed transition/report, and canonical attempts.

The existing process-loss test expected NOT_REQUIRED for an OBSERVING run with no rule rows. That expectation intentionally changed to UNKNOWN: absence of a test rule is not evidence that accepted source alerts were recovered. The existing successful rule-only retry fixture now follows SETTING_UP → CANCELLING → CLEANING_UP; it previously simulated injection despite having no inputs. Its owned-rule deletion remains verified.

## Behavior

`alert.attempting` is committed through the existing transaction-backed AppendRunEvent before calling Inject. It contains the possible attempt, case/event index and prepared named startsAt. Persistence failure stops before POST. It does not prove transport send or OSCAR acceptance. Normal in-process execution still updates the final attempt result, and no automatic duplicate injection is introduced.

Startup and rule-only RetryCleanup conservatively retain input responsibility independently of the rule ledger. The former marks possible input work UNKNOWN; the latter can delete owned rules but returns DIRTY with a source-reconciliation explanation. This prevents retention/deletion from discarding unresolved source evidence. There is one additional local harness write per stimulus; normal OSCAR ingest has no changes or new writes.

## Still open

This slice deliberately does not implement source reconciliation after restart, original-request replay, durable cleanup completion receipts, backend admission evidence or physical recovery. A before-send event can remain after a crash before the actual POST; that uncertainty must be reconciled rather than inferred away. Rule-only retry cannot discharge it. Local kill/reopen evidence is not a full power-loss/fsync, host-failure or distributed-recovery proof. CLEAN retains its existing request-acceptance meaning and does not establish terminal clear. No capacity gate or any of the 39 UAT cases is accepted here. Unsupported absence/collector-health and ticket-provider tests remain excluded.

Failures and passes are retained in deterministic gzip artifacts. Parent and fresh independent whole-cut review found no actionable defect. Independent review checked ordering, persistence failure, restart retention and rule-only guard; it inspected the archived 730/27 results without rerunning or contacting live services.
