# CorrTest compact observation qualification — 2026-10-20

This qualification connects `test-pack observe-compact` to the public `compact-evidence-v1` reader. It is an observation collector, not a scenario runner. See [operator instructions](../../../installed-test-pack.md#read-only-compact-scope-observation).

## Scope and result

The collector verifies the original pinned kit, ownership, installation and stored HTTPS target. It captures six installed-configuration readbacks, derives a currently authenticated compact scope, performs one evidence GET, and repeats the six configuration reads. The first runtime load timestamps are pinned through postflight. A completed restart before observation is allowed; changed timestamps, rule, mapping, scope or consumer manifest during observation make the result unavailable. Original installed-run behavior remains unchanged.

Exact HTTP bodies are retained as base64 in an exclusive 0600 artifact. Existing output refuses before credential lookup or network access. Cancellation joins artifact publication before runtime shutdown returns. Bounded failure artifacts are retained when possible; terminal output contains no raw server errors. Non-200 compact bodies are discarded. A complete zero inventory is distinct from HTTP 503; neither establishes that all source inputs were processed.

Final command outcomes and test-event counts are in `results.json`; full machine-readable outputs are compressed under `logs/`. The source manifest binds these final runs to all 21 changed implementation, test, fixture and operator-document files. No source hash changed during the final tests. Package/test counts are test runner events, including nested subtests, not independent UAT cases.

## Commands and replay

From the feature CorrTest checkout:

```sh
go test -json ./... -count=1
go test -race -json ./internal/command ./internal/runtime ./internal/oscar ./internal/testpack -run 'TestCompact|TestCanonicalJSONInt64|TestInstalledPack' -count=1
```

Replay an original collected artifact through exported `runtime.ValidateCompactObservation(raw, kit, installation)`, with separately verified original kit and installation objects. Preserve output bytes exactly: the outer artifact deliberately rejects reformatting, normalization, duplicate keys and case-fold aliases. The compact payload digest uses independently checked Python-compatible canonical JSON, including Unicode, HTML characters and signed int64 values. A digest is consistency metadata, not a signature.

To reproduce the independent overlay counterexamples, decompress `logs/client/runtime-review/replay_probe_test.go.gz` into a temporary file; create a Go overlay JSON mapping the absolute checkout path `internal/runtime/review_compact_replay_probe_test.go` to that temporary file; then run:

```sh
go test -overlay /tmp/compact-review-overlay.json ./internal/runtime -run 'TestReviewCompactReplay|TestCompactObservation' -count=1
```

The permanent suite also retains the corresponding counterexamples. `manifest.json` records original and compressed SHA256 values and lengths. Decompress each entry and verify its original hash; archived logs do not depend on private runtime credentials. Earlier-stage source manifests describe their stages, while `source-hashes.json` is the final combined state.

## Independent review and failures

Two independently reproduced replay findings were fixed before final tests:

- Duplicate outer outcome keys could turn a failure claim into success during replay. Exact immutable writer serialization now rejects duplicate keys, aliases and reformatted envelopes.
- Replay accepted configuration bodies larger than capture allowed. One shared check now enforces six nonempty bodies, 1 MiB each and 4 MiB aggregate for both capture and replay.

The independent overlay was RED before both fixes and GREEN afterward. Its original review and logs are retained. The client review checked HTTPS and target binding, no redirect/retry, size/deadline bounds, unavailable versus healthy-empty evidence, canonical digest compatibility, dense generation inventory, and consistent pending/physical stages. Final current-runtime review is recorded separately.

Retained author failures include missing APIs before implementation, five contradictory client state cases, missing CLI-result success validation, the normal-restart collector refusal, and the two replay findings. Setup failures are kept separately: CLI tests initially lacked HOME, and the race detector found an HTTP test-counter race fixed with atomic counters. These are not represented as production acceptance failures or silently erased. An initial TLS fixture compilation typo was corrected before its test log was captured; no original log is claimed for that setup error.

## Limits

This is local authenticated HTTPS fixture, pure replay and Go regression evidence. No command was run against Beast, no source was injected, no scenario PASS was assigned, and no notification delivery, recovery, moderate-load or production capacity claim follows. The collector has no processed-source frontier and cannot prove completeness of source ingestion. Backend physical-history claims depend on authenticated backend projection; private ancestry is intentionally absent from the client response.

Only the installed fixed compact Flood scope is supported. Other patterns and the 39 use cases are not accepted by this record. Enhanced admission and ordinary Correlator dispatch remain disabled. Ticket-provider acceptance is excluded by user direction. The Go tests create no Docker images, containers or volumes. Public standalone observations are not legacy fifteen-input audit runs and are not `verify-bundle` ZIP files. The client retains its stricter 4 MiB compact response cap despite the server allowing 8 MiB; overflow is unavailable. The read-only collector does not provision its runtime, SQL reader or API authorization prerequisites.
