# Adversarial remediation re-review resolution

Date: 2026-08-20  
Reviewed report: `2026-08-20-oscar-corrtest-adversarial-remediation-re-review.md`  
Report SHA-256: `e2bcad169e1f76d2a98230b2a6d79b9b9798349547b9e81f53693d4d12d14ad9`  
Fix commit: `dbe48649d1e17c6a2c3aa3e3bc346503b46f4855`

## Decision

The re-review verdict is accepted: the harness is ready for controlled live
qualification, but the report's three MEDIUM findings were real release gaps.
Each was independently confirmed against the implementation before changes.
They are now remediated and protected by tests that exercise the production
runner or real HTTP adapter boundary.

This resolution does not claim that OSCAR has passed live qualification. A v1
release still requires the isolated live gate against a disposable, explicitly
acknowledged Phase-B target.

## Resolution ledger

| Finding | Disposition | Resolution |
|---|---|---|
| RC-1 | Confirmed and fixed | Failure/cancel cleanup now deletes exact-ID owned rules, discovers source history for every sent event identity plus any run-owned synthetic history, and resolves records by authoritative server fingerprint. Missing or unreadable expected history forces `CleanupDirty` and is retained in the terminal error. |
| MED-FIX | Confirmed and fixed | All six `internal/oscar/testdata/public-v1/*.json` files are loaded by a client contract test and drive injection, history, annotation, audit, and classifier behavior. |
| NF-1 | Confirmed and fixed | The fixture-backed contract test asserts `GET /api/v1/alerts/history`, `perPage=100`, first page, ascending `createdAt`, exact time bounds, and the JSON filter item `{field: alertname, operator: equals, value: <name>}`. |

## Regression evidence

The cleanup tests were observed failing before the production change:

- cancellation and observation-error paths resolved zero alerts;
- an observation error with unavailable history ended as `ERROR + CLEAN`.

After the fix:

- recoverable run-owned history is resolved and may end `ERROR + CLEAN`;
- unavailable expected history ends `ERROR + DIRTY`;
- rules are deleted before failure-history discovery and alert resolution;
- cleanup errors are included in durable terminal evidence.

The adapter contract test was also observed failing to compile until the
fixture loader was added. It now reads every recorded fixture and independently
asserts the history request contract, so changing the route or `alertname`
filter field kills the test.

## Remaining boundary

No BLOCKER, HIGH, or MEDIUM item from the re-review remains open. The report's
LOW and explicitly deferred items remain bounded as documented; none authorizes
a live PASS without the disposable Phase-B qualification gate.
