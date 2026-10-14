# Ordinary webhook route qualification — 2026-10-13

Scope: one fixed profile ordinary receiver route and basic Flood five-versus-four fixture, preserving direct scenarios. Product plan: [ordinary input](../../../superpowers/plans/2026-10-13-ordinary-webhook-input.md). Operator setup and exact limits: [ordinary webhook input](../../../ordinary-webhook-input.md).

## Retained evidence

- `red-target.log.gz`: receiver binding was discarded and invalid configurations accepted before target support. `green-target.log.gz`: domain, persistence and history pass. New migration is additive nullable target JSON; existing direct targets remain valid.
- `red-cli.log.gz`: new receiver flags unsupported. `green-config-ui.log.gz`: command/UI regression pass.
- `red-client.log.gz`: requested receiver input went to public injection and returned the direct accepted classification. `green-client.log.gz`: separate exact request/client route and original-episode clear pass.
- `red-profile-ack.log.gz`: old/mismatched acknowledgment could qualify as forwarded before the strict acknowledgment guard.
- `red-scenario.log.gz`, `red-runner.log.gz`, `red-probe.log.gz`, `red-preview.log.gz`: absent route/compiler/runner/recorded-probe/API-preview support; corresponding `green-route-contract.log.gz`, `green-runner.log.gz`, `green-integrated.log.gz`, `green-durable-probe.log.gz` retain the successful increments.
- `red-cli-route-summary.log.gz`: execution output still claimed native bypass for an ordinary run. `green-cli-route-summary.log.gz` qualifies actual route text while keeping direct text unchanged.
- `green-request-boundaries.log.gz`: recorder failure prevents HTTP; redirects are not followed; caller map mutation cannot change the recorded body or retained cleanup identity; missing/wrong clear acknowledgments retain unresolved responsibility and cannot be retried.
- Setup/compatibility failures are retained separately: `initial-migration-expectations.log.gz` (two old hard-coded migration counts), `initial-runtime-import-failure.log.gz` (missing import), `initial-runtime-probe-test-setup.log.gz` (test used nonexistent runtime Options.Getenv). These were test/build setup failures, not successful behavioral reproductions.
- `full-suite.jsonl.gz`: 858 test/subtest passes, 27 passing packages, zero failures. Two package skip events are the no-test-file commands `cmd/generate-scenario-schema` and `cmd/package-zip`; no individual test skip occurred.

## What was actually exercised

`TestOrdinaryWebhookRecordsActualRouteAndClearsOriginalNativeEpisode`, `TestOrdinaryWebhookUncertainForwardCannotBeOvertakenByClear`, `TestOrdinaryWebhookRecorderAndRedirectBoundary`, `TestOrdinaryProbeRequiresRecordedRouteAndNativeCleanup`, `TestOrdinaryWebhookFloodThroughSeparateReceiverAndPublicAPI`, and `TestOrdinaryRuntimeProbeIsRecordedAndUnknownStaysDirty` use local HTTP servers. The runner uses real client wire encoding/decoding and existing independent first-trigger model, not an actual deployed receiver/native stack. SQLite runtime tests observe the before-send event from inside the receiving HTTP handler. The valid ordinary runtime probe and invalid/unknown preflight paths are tested; a full successful live runtime run is not claimed.

The fixed Mapping artifact is copied byte-for-byte from OSCAR qualification `corrtest-ordinary-webhook/corrtest_ordinary_v1.json`. Backend route/compiler verification and W16 packaging are independently owned by the parent workstream; their results are not counted as CorrTest local passes here.

## Limits

No app containers, live deployment, profile installation or production requests were performed by this cut. Native grouping/coalesced callback behavior, real rule reload timing, Redis/Lua/MySQL integration and asynchronous resolved callback delivery remain external qualification. Known-forwarded cleanup is not physical recovery. Selected admission/source ordering/accepted intent/producer completeness/parent recovery/notifier delivery remain unproved. Lost acknowledgment blocks automatic clear; interrupted possible work stays UNKNOWN and cannot be erased by rule-only cleanup. Explicit input validity limits lifetime without granting recovery evidence. No UAT cases are accepted; absence/collector-health unsupported and ticket-provider checks excluded.

## Review and final verification

The [fresh review](review.md) reported two P2 issues: source relabeling could choose direct cleanup, and preflight interruption lost route metadata. Both were reproduced in `review-red.log.gz` and fixed in one bounded pass; `review-green.log.gz` passes the same checks plus adjacent packages. Retained source identity now precedes synthetic fallback, and the ordinary plan is durably saved before probing. Reopen retains ordinary coverage and UNKNOWN responsibility. Final full-suite counts are recorded after verification below.

Final `review-fixed-suite.jsonl.gz`: **859 tests/subtests passed across 27 passing packages; zero failures; two package-level no-test-file skips** (`cmd/generate-scenario-schema`, `cmd/package-zip`). No individual tests skipped. `git diff --check` passed. Logs are stored with deterministic gzip (mtime=0); both initial failures and successful runs are retained.
