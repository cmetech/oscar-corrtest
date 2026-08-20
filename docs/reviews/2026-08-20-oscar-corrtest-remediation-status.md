# Adversarial review remediation status

Date: 2026-08-20  
Frozen remediation implementation: `ce319b9218fbba038c4d38591d10893ba5ad2b48`

## Outcome

Both independent reviews were directionally consistent: the frozen `e8ab6d0`
implementation was not trustworthy for live OSCAR qualification. The stronger
`BLOCK LIVE QUALIFICATION` verdict was accepted. Every confirmed BLOCKER and
HIGH finding from both reports has now received an implementation and a
regression gate.

This is not a claim that a real OSCAR deployment has passed. The implementation
passed independent adversarial re-review with the verdict `READY FOR CONTROLLED
LIVE QUALIFICATION`. Live qualification remains `UNPROVEN` until an operator
runs the explicitly isolated all-eight-pattern gate against a disposable,
verified Phase-B target.

## Closure ledger

| Review concern | Status | Closure evidence |
|---|---|---|
| Wrong bearer authentication | Closed | Public-v1 sends `X-API-Key`; recorded auth/body fixtures reject contract drift. |
| Flood cannot reach cardinality | Closed | Stable per-event label identity creates five distinct server fingerprints while preserving group labels; firing/resolved pairs share identity. |
| Early positive read / stale negative read | Closed | Absolute case deadlines, repeated read-back, stabilization, and mandatory final zero-assertion snapshot. |
| Scenario assertions ignored | Closed | Explicit typed assertions are the only behavioral verdict source. |
| Normalized tables remain `PLANNED` | Closed | One SQLite finalization transaction updates cases, assertions, attempts, lifecycle, verdict, cleanup, report, timestamps, and terminal event. |
| Raw OSCAR evidence discarded | Closed | Production runner writes an immutable normalized evidence artifact, registers its hash/size, includes it in exports, and forces `ERROR` if publication fails. |
| Lost-create rule cannot be recovered | Closed | Exact ownership-proven `UNKNOWN -> CREATED` adoption is allowed and cleanup remains exact-ID. |
| Terminal crash window / unsafe deletion | Closed | `CLEANING_UP -> COMPLETED` and terminal proof are atomic; legacy null-verdict completion is recovered; deletion requires a valid verdict. |
| One history row per alertname assumption | Closed | Exact-run/event filtering and server-fingerprint deduplication support multiple identities and notifier duplicates. |
| Absence timing/control invalid | Closed | 55-second deadline and sustained 8-second heartbeat negative control. |
| Test alerts remain firing | Closed | Rules are deleted first, then exact run-owned source/synthetic history records are resolved using authoritative server fingerprints; classifications are retained. Error/cancel cleanup also discovers attempted event identities and resolves them, or records `DIRTY` if authoritative history cannot be recovered. |
| Case-code-scripted fake | Closed | Manual-clock semantic model evaluates rule criteria, label fingerprints, timers, audit outcomes, and all eight built-ins without reading polarity/assertion expectations. |
| 2xx dropped/queued bodies misclassified | Closed | Current OSCAR response fixtures classify accepted, rejected, queued, partial, and indeterminate outcomes. |
| Publish lanes skip release gates | Closed | GitHub and GitLab publish-capable lanes run `make clean release-gate`; GitLab packaging consumes verified artifacts. |
| Empty timer gate | Closed | Exact named tests cover scenario, compiler, and runner; `[no tests to run]` fails the contract script. |
| Broken scratch container defaults | Closed | Owned writable state directory; default command is non-networking `help`; safe launch forms documented. |
| Loopback DNS rebinding / immortal sessions | Closed | Actual-listener literal-loopback Host guard; signed issued-at expiry and logout replay revocation. |
| Missing live closure gate | Closed as a gate, not as a live result | Explicit Phase-B/disposable acknowledgements, all-eight serial runs, CLEAN cleanup, verified artifacts/bundles; excluded from offline CI. |

## Verification performed

`make clean release-gate` passed at implementation commit `65740ab`; after the
web hardening commit, focused web/runner/runtime/evidence/persistence tests also
passed. The re-review must rerun the complete gate against the frozen commit
and must not treat this status document as evidence by itself.

The independent re-review reran `make clean release-gate`, the 20-times shuffled
package suite, and `go test -race ./...` at frozen commit `ce319b9`; all passed.
The three residual MEDIUM findings were then closed at `dbe4864` with focused
red/green regressions and `go test ./... -count=1`.

## Post-re-review release fixes

The re-review at
`docs/reviews/2026-08-20-oscar-corrtest-adversarial-remediation-re-review.md`
(SHA-256 `e2bcad169e1f76d2a98230b2a6d79b9b9798349547b9e81f53693d4d12d14ad9`)
confirmed the original trust remediation and identified three MEDIUM release
gaps. All three are closed in `dbe4864`:

| Re-review concern | Status | Enforceable closure |
|---|---|---|
| RC-1: error/cancel path could report clean with firing residue | Closed | `TestRunnerResolvesInjectedAlertsWhenObservationFails`, `TestRunnerMarksCleanupDirtyWhenInjectedHistoryCannotBeRecovered`, and the strengthened cancellation test cover recoverable and unrecoverable residue. |
| MED-FIX: recorded public-v1 fixtures were inert | Closed | `TestRecordedPublicV1FixturesPinAdapterContracts` loads and exercises all six committed fixtures through the real OSCAR client. |
| NF-1: history route/filter was not pinned | Closed | The same adapter contract test asserts the exact method, path, page size, page, sort, RFC3339 timestamps, and `alertname`/`equals` filter object. |

## Explicit residuals

- Dispatch mode remains operator-declared because OSCAR has no public discovery
  endpoint. The live gate requires an explicit Phase-B acknowledgement.
- Bundle SHA-256 proves integrity relative to the bundle manifest, not publisher
  identity. Retain hashes externally; signing remains future work.
- Retention remains bounded to 500 candidates per invocation.
- Full UI parity for every CLI maintenance action, notifier discovery, remote
  login rate limiting, and generalized custom rule criteria remain follow-on
  enhancements, not hidden release claims.
- GitHub/GitLab account permissions and a real OSCAR result cannot be proven by
  local tests.
