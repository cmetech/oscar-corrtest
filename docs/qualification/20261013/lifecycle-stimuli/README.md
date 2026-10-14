# CorrTest named lifecycle stimuli and truthful input coverage

Feature branch: `feat/correlation-reliability-20260919`. Parent backend work proceeded independently. This increment changes CorrTest only; no OSCAR runtime flag, service, dependency, schema migration or deployment is changed.

## Implemented

- Optional `source` identity within a role/case allows five same-role Flood children to recover independently. Refire keeps labels/fingerprint and advances the firing episode.
- Named offsets require whole milliseconds, matching OSCAR History `TIMESTAMP(fsp=3)` / active `DATETIME(fsp=3)`. Case anchors are truncated to milliseconds; same-start and sub-millisecond refires are rejected. A 1 ms distinct refire is supported by the harness contract.
- Compile/encode/decode, public field contract, generated schema, request preview, actual alert payload, prepared-input timeline, and saved plan all retain the new identity/timing semantics.
- Cleanup propagates History startsAt. Every runner history reduction retains the latest known episode rather than trusting row order, including observation-failure discovery.
- `inputPath: receiver-admission` is rejected before OSCAR mutation. Existing input defaults to `alert-engine`; the runtime guard also rejects an unsupported path on a supplied compiled plan.
- Canonical reports, CLI summaries and the run page identify the downstream scope. No ordinary-alert fast-path or correlation-selected table isolation is inferred from the below-threshold negative case.

See [the contract and remaining work](../../../reliability-e2e.md) and [the fixture](../../../../scenarios/reliability/flood-lifecycle-downstream.yaml). Existing unnamed scenarios retain their previous semantics.

## Test evidence

All commands ran locally with `go version go1.27.0 darwin/arm64`; no containers were started.

| Evidence | Command/scope | Result |
|---|---|---|
| `red.log.gz` | `go test ./internal/compiler -run 'TestNamed\|TestReceiverAdmission' -count=1` | Missing strict `source` / `inputPath` fields rejected the new positive tests. |
| `red-runtime.log.gz` | Compiler/request/runner focused cases | Wire episode omission failed; runner test initially had an unused import. That test-only compile error was corrected. |
| `red-runner.log.gz` | `go test ./internal/runner -run TestRunnerBinds -count=1` | Runtime had no firing/recovery episode timestamps. |
| `green-focused.log.gz` | `go test ./internal/scenario ./internal/compiler ./internal/oscar ./internal/runner -count=1` | Four packages passed. |
| `red-cleanup-preview.log.gz` | New cleanup/preview tests | Preview omitted runtime timing. Cleanup fixture first used the wrong pagination field and exhausted its response queue. |
| `red-cleanup-corrected.log.gz` | Corrected History fixture (`records`) | Real cleanup startsAt omission reproduced. |
| `red-refire-time.log.gz` | Named refire / distinct Flood count | Same-start refire was accepted; refire did not incorrectly count as a fifth fingerprint. |
| `red-source-evidence.log.gz` | Named source saved plan | Compiled evidence omitted source A/B mapping. |
| `red-cleanup-order.log.gz` | Both row orders, normal and failed observation | Descending order selected the old episode in both paths. |
| `red-millisecond.log.gz` | Named offset + runner timestamp tests | Sub-ms input and nanosecond anchor were not constrained to History precision. |
| `suite-initial.log.gz`, `suite-final.jsonl.gz`, `suite-complete.jsonl.gz`, `suite-verified.jsonl.gz` | Intermediate full `go test ./... -count=1` runs | All passed at their respective checkpoints; retained, not substitutes for final verification. |
| `suite-release.jsonl.gz` | `go test ./... -count=1 -json` | **577 passing test/subtest events, 27 passing packages, zero failures.** |

`git diff --check` passed. Logs use deterministic gzip (mtime=0). These are harness tests, including in-process HTTP fixtures and SQLite, not live OSCAR E2E acceptance.

## Review corrections and operational mistake

Parent review requested latest-episode cleanup ordering and matching millisecond precision; both were reproduced before fixing and are included above. One edit command mistakenly used the main CorrTest checkout path. It changed only the two runner files before failing on a missing feature test file. The exact diff was inspected and only those introduced changes were reversed immediately. `git status --short` then returned empty; no main commit or runtime action occurred, and no changes remain there. Subsequent edits used the absolute feature worktree path. No test containers or application volumes were created or removed.

## Limits

The fixture exercises downstream stimuli plus terminal audit/count assertions. It does not prove intermediate source transitions, actual receiver admission, native grouping, accepted intent, producer barriers, per-destination delivery, or safe parent clear. A cleanup request being accepted is not terminal recovery proof; History timestamps absent on legacy records remain unknown. A 1 ms harness timestamp distinction is not a live target throughput/episode acceptance result.

The next increment needs the backend-supported receiver transport/evidence contract, then staged assertions and ordinary controls. No invented endpoints. Admission/dispatch/suppression/physical clear remain disabled; live SNMP is deferred, tickets excluded, absence/collector-health unsupported, and 0/39 UAT cases accepted.
