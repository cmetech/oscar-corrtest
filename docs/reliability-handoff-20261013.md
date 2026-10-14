# CorrTest reliability handoff — 2026-10-13

Five repeatable downstream fixtures are prepared for controlled lab qualification. They are locally tested harness scenarios, not accepted backend E2E or UAT cases. Product checkpoint: feature commit `be4f88d`.

## Prepared fixtures

| Fixture | What its staged evidence checks | Negative control |
| --- | --- | --- |
| [Flood lifecycle](../scenarios/reliability/flood-lifecycle-downstream.yaml) | Four-to-five trigger audit; partial source History recovery; same-label refire with a new exact episode; recovery of that episode | Four matching children remain below threshold |
| [Flood downstream no-match](../scenarios/reliability/flood-downstream-no-match.yaml) | Positive Flood case plus five fixture-disjoint named controls, each requiring current firing History and an explicit `no_matching_rules` audit with null rule identity | Genuine downstream no-match witness; absence of audit is insufficient |
| [Co-occurrence lifecycle](../scenarios/reliability/co-occurrence-lifecycle-downstream.yaml) | First distinct role without trigger, second required role with trigger, partial/all source History recovery | One matching role cannot supply the missing distinct role |
| [Sequence lifecycle](../scenarios/reliability/sequence-lifecycle-downstream.yaml) | A then B chronological-tail trigger, followed by partial/all source History recovery | B then A is matching but wrong-order input |
| [Cross-source test labels](../scenarios/reliability/cross-source-lifecycle-downstream.yaml) | Required `snmp` + `api` label mix, followed by partial/all source History recovery | Two distinct `snmp`-tagged inputs cannot supply `api` |

Cross-source tags are injected labels, **not trusted receiver provenance**. Below-threshold, missing-role, wrong-order and same-tag controls are not ordinary no-match controls.

## Exact qualification boundary

- Every fixture uses `POST /api/v1/alerts` directly into OSCAR Alert Engine. Receiver admission is `NOT_TESTED`; Native Alertmanager is `BYPASSED`. Requesting receiver-admission coverage refuses execution before mutation rather than silently substituting this adapter.
- Local HTTP tests exercise the real client, compiler, runner, response decoding, checkpoints, reporting and owned cleanup against a test server with an independent first-trigger model. They do not run deployed Redis/Lua, MySQL, broker workers or receivers.
- `parent_emitted` audit is a trigger decision, not proof of a created or delivered parent. Exact `startsAt`/labels/fingerprint History binding proves observed source status only. Lifecycle application and physical clear remain `NOT_PROVEN`; producer barriers, selector revision, selected-row isolation and notification delivery are not proved.
- Checkpoints observe their full bounded interval, retain observed zero-count violations and stop later stimuli on failure or unavailable evidence. Polling cannot exclude unobserved transients. Local short intervals and immediate rule activation require live timing qualification.
- No-match requires a positive downstream witness; it cannot prove receiver selector health or a deployed global rule snapshot. Clock skew, delayed audits or unrelated matching rules may prevent qualification.
- Models qualify first ordinary triggers only: no Redis TTL/cooldown rearming, distributed retry, concurrency or enhanced episode reconciliation. Cross-source HTTP audits are qualified only for admitted firing source/name pairs; rejected-tag/pair/recovery fallback audit outcomes are outside that fixture.
- Input attempts are committed to CorrTest's existing SQLite timeline before POST; failed persistence prevents send. Ambiguous POSTs are not retried. Missing or conflicting exact cleanup identity remains `DIRTY`; interrupted possible inputs remain `UNKNOWN`, and rule-only cleanup cannot discharge them. `CLEAN` means accepted cleanup requests, not terminal recovery. Process-kill/reopen evidence is not power-loss or distributed recovery proof.

At the five-fixture checkpoint, the archived full suite passed **779 tests/subtests across 27 packages**, with zero failures. [Cross-source qualification](qualification/20261013/cross-source-http/README.md) links the final cut's scope and retained RED/GREEN review evidence; `review-fixed-suite.jsonl.gz` contains the final `go test ./... -count=1 -json` result. Earlier cut evidence remains under [qualification/20261013](qualification/20261013).

Actual HTTP test entry points are `TestFloodFixturesThroughRealHTTPClient`, `TestStagedCoOccurrenceThroughRealHTTPClient`, `TestStagedSequenceThroughRealHTTPClient` and `TestStagedCrossSourceTestLabelsThroughRealHTTPClient` in `internal/runner`. The Flood entry point covers both Flood fixtures. This documentation-only handoff does not claim a new test run.

## External contracts still needed

1. A supported authenticated selected receiver adapter, including selection and recovery-owner lookup. The private ordinary-only native-array ingress is not that adapter.
2. Versioned read evidence connecting run-owned inputs to accepted intents, exact episodes, loaded selector/rule-bundle revisions, retained members, producer completeness/barriers, applied checkpoints, parent actions and delivery receipts. CorrTest must use concrete backend contracts rather than invent endpoints.
3. Actual namespace load/edit/import/reload and Native Alertmanager grouping/callback qualification, including activation readiness, history-first hand-off and audit freshness.
4. Evidence for one parent, partial recovery keeping it open, complete coverage permitting one safe clear, and refire rejecting stale recovery. Source History alone cannot authorize these assertions.
5. Interleaved ordinary controls proving delivery and no selected-source persistence, plus restart/retry/missing-evidence cases. Current sequential controls do not qualify a 90/10 load or capacity target.
6. Safe interrupted-source reconciliation: bind persisted attempts to the original plan and exact episode, and prove original in-flight work and correlation actions cannot arrive after cleanup. History alone and current cleanup facts cannot discharge that responsibility. New receiver/selector resources also need explicit ownership and cleanup contracts.

Since that five-fixture checkpoint, a [local Threshold duplicate-value control](qualification/20261013/threshold-control/README.md) was added as an isolated compiled N01 runner/HTTP test. It does not add a sixth full fixture or claim positive Threshold trigger qualification. Its exact-set model correction is explicitly not Redis HLL proof; a real positive oracle remains open. That cut's archived full suite passed 795 tests/subtests across 27 packages. Persistence still needs timer evidence; Parent–child needs relationship/suppression contracts. Absence/collector-health is unsupported, ticket-provider tests are excluded, and live SNMP is deferred. Enhanced admission, parent dispatch/suppression and automatic physical clear remain disabled. No capacity figure or any of the 39 UAT cases is accepted from these tests.

The subsequent [recorded-injection cut](qualification/20261013/recorded-injection/README.md) binds the existing before-send attempt event to exact HTTP body bytes/hash and a credential-free initial target. It sends those frozen bytes without redirect or automatic retry. Process-kill/reopen and export tests retain the evidence and UNKNOWN responsibility; no selected-source contract is implied. Oversized aggregate timeline export is refused at the existing 16 MiB entry cap, with SQLite evidence retained. Its final review-corrected full suite passed **816 tests/subtests across 27 packages**, zero failed/skipped tests; two package-level skips were commands with no test files (`cmd/generate-scenario-schema`, `cmd/package-zip`).

See [the detailed runner contract](reliability-e2e.md) for assertion semantics, observation limits and cleanup behavior.

A [design-only lab-source contract proposal](corrtest-lab-source-contract-proposal-20261013.md) maps the remaining identity/order/episode/fencing requirements onto existing storage. It is not implementation-ready or an activation grant; backend protocol review remains required.

## Ordinary receiver route increment

In addition to the five direct downstream fixtures, [ordinary webhook Flood](../scenarios/reliability/flood-ordinary-webhook.yaml) adds a bounded receiver profile route for five-versus-four named firing inputs. Its [setup and limits](ordinary-webhook-input.md) require a fixed profile hash/acknowledgment, separate credentials and explicit validity. Local model qualification does not prove deployed native grouping or selected admission. Source clears use the same receiver and original episode; ambiguous input remains DIRTY. Standalone doctor and the five earlier fixtures remain direct.

Ordinary route qualification: **859 tests/subtests, 27 passing packages, zero failures**, with two command packages reporting no test files. [Retained review and test evidence](qualification/20261013/ordinary-webhook/README.md) includes the two reproduced/fixed review findings. No live qualification is implied.
