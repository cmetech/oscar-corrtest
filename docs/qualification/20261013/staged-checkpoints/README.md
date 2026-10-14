# CorrTest staged checkpoints

This increment extends the existing downstream CorrTest runner. It does not add a receiver adapter, backend endpoint, activation flag, dependency or migration.

## Behavior

An optional case checkpoint names a one-based accepted-input prefix, a bounded observation interval and existing count assertions. The runner waits the entire interval, polls only that prefix, and proceeds only when every required assertion is scored and passes. A missing source/audit proof or shadow-skipped assertion is INCONCLUSIVE. An observed zero-count violation stays failed even if a later read no longer sees it. Read failure/cancellation is ERROR. Later stimuli stop in every non-PASS case, while bounded owned cleanup runs.

The schema/decoder/compiler enforce at most 16 checkpoints, unique names, increasing ordinals, bounded windows, explicit expected counts, and a reserved interval before the next absolute event delay. Existing valid scenarios without checkpoints retain their previous execution behavior. Previously accepted malformed assertions without `equals` are now rejected rather than silently becoming negative checks.

Canonical reports and the visible timeline retain the snapshots, assertions and start/end times of completed checkpoints, including earlier results when a later checkpoint fails. Preview lists the checkpoint pause and the existing History/audit API reads. CLI `run` and `runs show` list checkpoint verdicts. The Flood fixture now checks zero parent-emitted audits after four inputs and one after five, before its later recovery/refire stimuli.

## Validation and retained failures

All commands ran locally with Go 1.27.0 on darwin/arm64, in the absolute feature worktree. No containers were started or application resources mutated.

| Evidence | Result |
|---|---|
| `red-contract.log.gz` | New checkpoint field rejected by the original closed decoder. |
| `green-contract.log.gz` | Strict decode/encode/compile and unsafe-boundary checks passed. |
| `red-runner.log.gz` | Runner ignored checkpoints, sent all nine inputs, and could PASS an intermediate violation or skipped assertion. |
| `green-runner.log.gz` | Prefix-only observations, absolute scheduling, stop-on-failure and retained checkpoint reports passed. |
| `red-transient.log.gz` | A forbidden result appearing only at an intermediate poll was incorrectly allowed to disappear into PASS. |
| `green-focused.log.gz` | Sticky negative violations plus adjacent compiler/scenario/runner suites passed. |
| `red-preview-required.log.gz` | Preview omitted checkpoint ordering; missing `equals` silently became zero. |
| `green-preview.log.gz` | Compiler/scenario/request-preview/runner packages passed after correction. |
| `red-cli.log.gz` | Test-only missing `json` import caused build failure. |
| `red-cli-corrected.log.gz` | Corrected test reproduced absent checkpoint text through the existing `runs show` command before its call was restored. |
| `suite-initial.jsonl.gz` | Initial full suite recorded that same test-only build failure. |
| `suite-corrected.jsonl.gz` | 594 test/subtest results passed across 27 packages. |
| `green-boundaries.log.gz` | Additional cancellation, missing-History/dirty-cleanup, order/name/count/window bounds passed. |
| `suite-final.jsonl.gz` | **602 passing test/subtest events across 27 packages; zero failures.** |

Commands: focused `go test` package/test selectors as identified by each log, followed by `go test ./... -count=1 -json`. `git diff --check` passed. Evidence is deterministic gzip (mtime=0).

## Limits and next work

Counts are cumulative evidence for the sent prefix and rule, not deltas since a preceding checkpoint. The current assertion set does not attest that a particular named recovery/refire episode was applied. Polling cannot establish absence of an unobserved transient between reads. Declared intervals are bounded minimum observation periods; actual API completion time is recorded, and the overall run deadline bounds late operations. A cleanup request being accepted is not proof of terminal recovery.

The below-threshold case still matches an installed rule and is not an ordinary no-match control. Ordinary delivery/selected-state isolation requires its own evidence contract. Receiver admission, native grouping, accepted-intent ownership, producer barriers, delivery receipts and safe physical clear remain outside this adapter. No live OSCAR execution or UAT/capacity acceptance was performed. Admission/dispatch/suppression/physical clear stay disabled; tickets are excluded, live SNMP deferred, and absence/collector-health unsupported.

See [the shared CorrTest/backend boundary](../../../reliability-e2e.md) for required receiver/evidence and staged lifecycle work. No API is invented to fill those gaps.
