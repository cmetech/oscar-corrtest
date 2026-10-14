# Named source episode History qualification

This CorrTest-only increment adds `source-episode-state-count`, with exact `firing` or `resolved` outcomes, to terminal assertions and staged checkpoints. Every event in the case must explicitly name its source. It binds the latest sent firing episode to millisecond `startsAt`, expected input labels, authoritative History fingerprint and unambiguous status. It counts sources once, not historical rows. Raw candidate rows and expected timestamps are retained in canonical `sourceEpisodes` evidence; CLI and timeline retain checkpoint verdicts.

Missing/stale-only/future/changed-label/unknown-status/conflicting evidence stays INCONCLUSIVE. Old rows alongside an unambiguous current row and identical current duplicates are supported independently of API row order. A zero-count source check retains any observed missing or colliding-identity gap as INCONCLUSIVE, even when later evidence is complete. Observed source zero-count violations remain sticky failures in both checkpoints and terminal observation; legacy terminal count behavior is preserved. Required labels must be present even when their expected value is empty. Source-only assertions query History without borrowing an old correlation audit or requiring a parent/notification query. Mixed assertions retain existing audit requirements and cumulative audit semantics.

The sample Flood scenario observes audit threshold behavior, then partial recovery, refire and recovery of the refired episode. This is downstream History visibility only. A matching status does not establish correlator application, accepted intent, delivery, producer completeness or safe physical clear. The negative case is below-threshold, not an ordinary no-match control. Live timing, native/receiver integration and UAT acceptance remain unverified; no 39-case acceptance or capacity claim is made. Enhanced admission/dispatch/suppression/clear remain disabled. Tickets are excluded; live SNMP is deferred and absence/collector health unsupported.

## Retained validation

All commands ran in the absolute feature worktree with local Go 1.27.0 on darwin/arm64. No containers, deployments, application mutations, new APIs or dependencies were used.

| Evidence | Result |
|---|---|
| `red-contract.log.gz` | The original runner/compiler rejected the new assertion kind. |
| `green-initial.log.gz` | Current firing/recovery/refire counts plus missing, stale, future, changed-label, unknown-status and conflicting evidence passed. |
| `red-negative-gap.log.gz` | Reproduced a false PASS when initial missing episode timestamps were hidden by a complete final read. |
| `green-negative-gap.log.gz` | The gap now remains INCONCLUSIVE; both row orders, current duplicates, old competing rows and stale refire boundaries passed. |
| `green-focused.log.gz` | Scenario, compiler, API/preview and runner packages passed, including strict wire round-trip and source-only query preview. |
| `suite-initial.jsonl.gz` | 632 passing test/subtest events, plus one documentation exact-text test failure after changing the outcome table wording. The old contract sentence was restored and the new outcome contract appended. |
| `red-identity-gap.log.gz` | Reproduced the analogous false PASS when multiple sources briefly shared one returned History fingerprint. |
| `green-final-focused.log.gz` | Source episode identity gaps now remain INCONCLUSIVE; focused lifecycle and row-order checks passed. |
| `suite-final.jsonl.gz` | Before review: 635 passing test/subtest events across 27 packages; no failures. |
| `red-review.log.gz` | Review counterexamples reproduced missing-empty-label acceptance and a transient terminal source zero-count violation being erased into PASS. |
| `green-review.log.gz` | Both review regressions passed; present-empty label controls and both API row orders passed. |
| `suite-reviewed.jsonl.gz` | **640 passing test/subtest events across 27 packages; no failures.** |

Focused commands used `go test ./internal/runner -run 'TestNamedSourceEpisodeHistory|TestSourceEpisode' -count=1` and the listed adjacent packages. Final verification was `go test ./... -count=1 -json`; `git diff --check` passed. Logs use deterministic gzip (mtime=0).

Unit runner tests use the existing independent correlation model with a test History adapter that supplies current episode rows; these prove harness contract behavior, not the live backend's lifecycle semantics. The sample's one-second checkpoints may require longer intervals and corresponding event delays in a lab. Polls cannot rule out unobserved transitions between reads. A cleanup request being accepted still does not prove terminal recovery.

Parent whole-cut review identified the two reproduced/fixed regressions above; final review response is retained in the session. See [the runtime contract and remaining integration work](../../../reliability-e2e.md).
