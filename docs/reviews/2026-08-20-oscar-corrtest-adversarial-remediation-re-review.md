# Adversarial remediation re-review — OSCAR Correlation Test Harness

Independent hostile re-review. The reviewer did not author the code. Correctness
was not inferred from plan/status documents or from a green suite; every
load-bearing closure was checked against current OSCAR source and, where the
claim was behavioral, reproduced in a throwaway clone. Seven independent
sub-reviewers re-attacked the twelve campaigns; the coordinator re-verified each
one's load-bearing claims first-hand against the frozen source.

---

## 1. Scope, digest, and worktree verification

- **Workspace root:** `/Users/coreyellis/code/github.com/cmetech/oscar_app`
- **Review repo:** `oscar-corrtest`; **OSCAR comparison:** `../oscar`
- **Frozen remediation implementation:** `ce319b9218fbba038c4d38591d10893ba5ad2b48`
- **Repository HEAD:** `5cb8c79a8b5667e0d19ebe7d86efa6112bc8a8db`. The only paths changed after the frozen commit are `docs/reviews/2026-08-20-...re-review-prompt.md` and `docs/reviews/2026-08-20-...remediation-status.md` — both docs. **No implementation/build/test/workflow file differs from the frozen commit.** Scope valid (not `SCOPE INVALID`).
- **Worktree:** clean.
- **Input digests (verified at `ce319b9`):** review `dcd9416c…193685` ✓, resolution `74d8348a…27b555` ✓, plan `183e2aa6…f72240` ✓ — all match the prompt.
- **Remediation size:** 54 files, +4373/−335 across `e8ab6d0..ce319b9`.

**Mandatory execution (throwaway clone at `ce319b9`, `GOWORK=off`, caches redirected outside both workspaces):**

| Command | Exit | Notes |
|---|---|---|
| `make clean release-gate` | **0** | tools, fmt, mod, vet, gosec, plan2–7 gates, `check-release-contract.sh`, container/package-content/reproducible checks, standalone, package, checksums |
| `go test -shuffle=on -count=20 ./internal/{compiler,oscar,runner,runtime,web,testoscar}` | **0** | no failures |
| `go test -race -count=1 ./...` | **0** | no data races |

No `FAIL`, no `[no tests to run]`, no panic, no `DATA RACE` anywhere in the log. Live qualification was **not** run against a real target; its fail-closed preconditions were tested with unset/sentinel env only.

---

## 2. Executive verdict

> **READY FOR CONTROLLED LIVE QUALIFICATION** — with required changes before a v1 release and before single-run use against non-disposable targets.

All three prior BLOCKERs and eight of nine HIGHs are genuinely and correctly
closed against current OSCAR source, each with a real regression and, for the
behavioral ones, an independently reproduced demonstration. The trust-core the
first review found hollow — a false-PASS/false-FAIL oracle, an un-exercisable
flood pattern, wrong auth, unmaterialized evidence, and a leaking cleanup — is
now materialized: absolute per-case deadlines with a mandatory final negative
read, declared-assertion-only verdicts, distinct flood fingerprints, `X-API-Key`,
a one-transaction finalize with normalized terminal facts and an immutable
recomputable evidence artifact, `UNKNOWN`-state re-adoption, exact-ID
server-fingerprint cleanup, and a genuinely semantic fake. The isolated
`make live-qualification` lane is fail-closed, disposable-target, ack-gated, and
non-PASS by default; **no surviving finding can produce a false live PASS, an
unsafe deletion of a non-owned resource, or a credential leak.**

The verdict is not `READY … RELEASE` because three MEDIUM residuals remain, one
of which (RC-1) is a real cleanup-invariant gap on the error/cancel path that the
status document slightly over-claims as fully closed. These do not corrupt the
isolated live-gate verdict (they surface only on non-PASS runs, which already stop
the gate), so controlled live qualification against a disposable target may
proceed; they must be closed before v1 release.

---

## 3. Prior-finding closure table

Legend: **CLOSED** = code + regression + (behavioral) reproduction verified; **PARTIAL** = materially closed with a bounded residual; verdicts independently re-verified by the coordinator.

### Original BLOCKERs
| ID | Prior defect | Verdict | Evidence (frozen `ce319b9`) |
|---|---|---|---|
| BLOCKER-1 | Bearer vs required `X-API-Key` | **CLOSED** | `client.go:491` sends `X-API-Key` only; `client_test.go:25,78` reject Bearer via a 400ing fake; `/ext/mw` added `operator.md:10`. Matches OSCAR `APIKeyHeader` (`middleware/main.py:205-213`). |
| BLOCKER-2 | Flood 5 repeats → 1 fingerprint | **CLOSED** | Reserved per-event **labels** `oscar_test_event_id/_index` via `eventSequence++` (`compiler.go:73,158,169-170`), outside OSCAR's fingerprint exclusions → 5 distinct fingerprints, `group_by=[site]` preserved; resolved events `cloneLabels(firing)` and cannot change identity (`compiler.go:147-156`). Gated by `TestFloodEventsHaveFiveDistinctStableLabelIdentities`. |
| BLOCKER-3 | Oracle reads audit once pre-decision; negatives from pre-window snapshot | **CLOSED** | Absolute `deadline=injectedAt+window` (`observation.go:32`); negatives (`requiresFullWindow`, any `Equals 0`) never early-exit and are decided from the final at/after-deadline snapshot (`observation.go:69-91`); fail-closed eligibility on server fingerprint + exact run/event/rule id. Reproduced: late in-window parent → FAIL; late audit flush → PASS; absent evidence → INCONCLUSIVE. |

### Original HIGHs
| ID | Prior defect | Verdict | Evidence |
|---|---|---|---|
| HIGH-1 | Declared assertions never evaluated | **CLOSED** | `assertions.go` `evaluateAssertions` is the sole verdict source; `assertionsPass` requires non-empty + all pass; unsupported kinds error. Assertion-value mutations MA1–MA4 all KILLED. |
| HIGH-2 | Normalized tables stay `PLANNED` | **CLOSED** | `FinalizeRun` (`execution_repository.go:13-105`) terminalizes cases/assertions/attempts + envelope in one txn with a no-PLANNED fallback. Real run → `RESIDUAL_PLANNED_OR_NULL=0`. |
| HIGH-3 | Raw evidence discarded; report/artifact dead | **CLOSED** | Runner writes an immutable hashed `normalized-oscar-evidence` artifact embedding raw history/audits; publish failure forces `VerdictError`. Verdict independently recomputed from the artifact alone. |
| HIGH-4 | `UNKNOWN` lost-create rule un-reconcilable | **CLOSED** | `AdoptResource` accepts `PROPOSED,UNKNOWN` (`resource_repository.go:37-38`); retry find→verify→adopt→read-back→exact-ID delete → CLEAN, reproduced with the owned rule behind a full page. |
| HIGH-5 | Non-atomic terminal write; deletable crash window | **CLOSED** | COMPLETED transition folded into `FinalizeRun`; recovery catches `COMPLETED AND verdict IS NULL`; `DeleteTerminalRun` requires a verdict. Reproduced: verdict-less COMPLETED is non-deletable and recovers to INTERRUPTED/UNKNOWN. |
| HIGH-6 | One-row-per-alertname vs episode store | **CLOSED** | `observeCase` matches expected event identities by server fingerprint + exact run/event id (`observation.go:95-120`); threshold/cross_source multi-fingerprint stimuli now satisfy the oracle. |
| HIGH-7 | Absence P01 impossible; N01 control invalid | **CLOSED** | 55s absence observation window (`compiler.go:137-139`) covers the 40s fire; N01 sustains heartbeats 0→48s at 8s cadence (`builtin.go:47-54`); compile guard `event.Delay ≤ window ≤ MaxDuration` (`compiler.go:178`). |
| HIGH-8 | Injected firing alerts never resolved | **PARTIAL** | Happy path deletes rules first, then resolves observed source+synthetic history by server fingerprint (`ResolveHistory`), non-Accepted → DIRTY. **Residual RC-1:** the error/cancel path passes `nil` histories (`runner.go:375`) and can report CLEAN with alerts still firing. |
| HIGH-9 | Case-code-scripted fake | **CLOSED** | `testoscar/model.go` is a manual-clock semantic model (no P01/N01/polarity/assertion branching); semantic-break mutations (flood min_count, sequence order, threshold distinct, absent_for) each fail the exact pattern end-to-end. |

### "Report two" items and accepted medium gates
| Item | Verdict | Evidence |
|---|---|---|
| CI publishes without `release-gate` | **CLOSED** | GitHub `ci.yml`/`release.yml` + GitLab verify all run `make clean release-gate`; `check-release-contract.sh` greps all three and fails otherwise. |
| No prefix-delete / operator-rule blocker | **CLOSED** | Cleanup is exact-ID only; `UNKNOWN` (HIGH-4) and crash-window (HIGH-5) leaks closed. No prefix/name-only deletion exists. |
| Non-loopback requires remote auth | **CLOSED (preserved)** | Bearer needs TLS+secure cookies; trusted-proxy needs peer CIDR + identity; loopback Host guard added on top. |
| `fingerPrint` always supplied | **NOT A DEFECT** | OSCAR `AlertGroup` requires it; derived from stable labels, never used as oracle identity. |
| Annotation decode `Annotation` not `Label` | **CLOSED** | `annotationWire{Annotation,Value}` (`client.go:551`). |
| Bounded pagination; truncated page ≠ absence | **CLOSED** | `listComplete`/`pagination_limit` (`client.go:390-402`); reproduced with owned rule on page 2. |
| Literal 2xx body classifiers | **CLOSED (fixtures inert — MED-FIX)** | Classifier matches verbatim OSCAR bodies (`alerts.py:544/665/717/848`); asserted by inline strings, not the committed fixtures. |
| `/ext/mw` in docs | **CLOSED** | `operator.md:10`. |
| `MaxDuration` bounds execution | **CLOSED** | `context.WithTimeout` (`runner.go:128-134`) + compile guard. |
| Transition-failure best-effort cleanup | **CLOSED (see RC-1)** | Detached cleanup budget retained; but error-path alert resolution missing (RC-1). |
| Loopback Host allowlist | **CLOSED** | `loopbackHostGuard` (`server.go:591-607`); ephemeral-listener test: foreign/localhost/wrong-port → 421, `127.0.0.1`/`[::1]` → 200. |
| Session expiry + logout revocation | **CLOSED** | Signed issued-at, 8h expiry + future-timestamp rejection (`auth.go:187-212`), nonce revocation map. |
| Timer gate fails on empty selection | **CLOSED** | Plan5 runs full packages + `check-release-contract.sh` `exit 1`s on `[no tests to run]`. |
| Release from a clean output dir | **CLOSED (bounded)** | `make clean` + exact tag-named artifacts (GitHub) / artifact-passing on ephemeral executors (GitLab); residual only on non-default self-hosted shell runners. |
| Container writable state + reachable invocation | **CLOSED** | Owned `/var/lib/oscar-corrtest`, `CMD ["help"]` non-networking default, documented `docker run`. |
| Recorded fixtures + isolated live gate | **PARTIAL** | Live gate fully closed; recorded fixtures inert (MED-FIX). |

---

## 4. New findings ordered by severity

No new BLOCKER or HIGH. Three MEDIUM, remainder LOW.

### MEDIUM
- **RC-1 — Error/cancel cleanup path leaves injected firing alerts and can report `CleanupClean` (CONFIRMED; invariant: cleanup-visibility / HIGH-8 closure).** `failAndCleanup` calls `r.cleanup(cleanupCtx, run.ID, resources, nil)` (`runner.go:375`) — `nil` histories — so a run that injected `firing` alerts and then FAILs/ERRORs/cancels deletes its rules but never resolves those alerts, and if rule deletion succeeds `cleanup` returns `CleanupClean` (`runner.go:345`). This is the exact residue class HIGH-8 targeted, surviving on the failure path, and it contradicts the status document's claim that HIGH-8 cleanup "failure must remain visible and make cleanup DIRTY." Bounded: the run is visibly `VerdictError`, the rule is removed, and alerts are run-ID-labeled; it **cannot** produce a false live PASS (the isolated gate requires PASS+CLEAN and stops on ERROR). *Reproduction:* verified by code path; the happy-path resolve is skipped whenever `failAndCleanup` runs. *Remediation:* resolve run-owned firing history on the failure path too, or force `CleanupDirty` whenever injection occurred without a completed resolution. **Required before v1 release / before single-run use against non-disposable targets.**
- **MED-FIX — Committed `testdata/public-v1/*.json` recorded fixtures are loaded by no test (CONFIRMED; invariant: HIGH-TRACE-4 enforceable-gate).** The six fixtures exist but grep shows no test reads them; the only `public-v1` references are the API-profile string. Contract-drift protection is real but comes from **inline** strings in `client_test.go`, so the on-disk "recorded fixtures" the remediation presents as the closure gate for the prior HIGH-TRACE-4 are inert false assurance. Found independently by RC-CONTRACT and RC-BUILD; coordinator-verified. *Remediation:* load each fixture in a test that asserts the parsed contract (≈15 lines), or drop the fixtures and document inline coverage as the gate.
- **NF-1 — The authoritative history read route and filter DSL field are pinned by no test (CONFIRMED; prior MED-TESTS-6, not in the resolution's closure list).** Mutations M02 (`/api/v1/alerts/history` → `/alertsx/history`) and M03 (filter field `alertname` → `alert_name`) still **survive** the suite: client tests assert parsed results from a scripted queue, never the outbound request. History read-back is the sole fingerprint-identity source (closing invariant #5), so a drift there is undetectable offline. *Remediation:* assert method/path/query (`filter`, `start_datetime`, `end_datetime`, pagination) on the recorded history request, as the rule-lifecycle test already does.

### LOW
- **RC-ORACLE-NEW-1 — Bounded negative-absence blind spot past the deadline (DEFERRED-WITH-GATE).** A wrongful emit landing *after* the per-case window escapes to PASS; realistically only absence N01 (a broken OSCAR emitting >55s late). Inherent to finite negative-window testing; recommend a one-line note in `live-qualification.md`.
- **RC-BUILD-2 — `Host: localhost:PORT` is rejected 421** because `net.ParseIP("localhost")` fails (`server.go:600`); mitigated because docs use `127.0.0.1`. Usability nit.
- **RC-2 — Only history-observed alerts are resolved;** an accepted-but-not-yet-in-history alert is skipped at cleanup. No false PASS.
- **RC-EVIDENCE-LOW — Evidence `redaction_state` is asserted, not computed;** low risk (credentials live in headers; history is run-ID filtered).
- **MED-4 residual (re-zip authenticity) — DEFERRED-WITH-GATE.** In-place tamper is caught (`hash_mismatch`, export refused); full re-zip is not authenticated because `bundle.Verify` is internal-consistency-only. Explicitly disclosed in the resolution and `live-qualification.md` pending bundle signing.
- **RC-RESIDUAL LOWs** — `email` over-declared as an un-closable prerequisite (OSCAR exposes `GET /notifiers`); doctor `compatible` probes label-survival, not dispatch mode (fail-closed at run time); four disclosed ungated operability residuals.
- **RC-MUTATE NF-2…5** — wrong-run/audit-rule guards lack regressions but have compensating run-ID-embedded controls; two compiled criteria not tightly bounded by built-ins (no verdict impact); some decode branches bypassed by built-ins.

---

## 5. Oracle / fingerprint / timing analysis

The oracle rewrite is the strongest part of the remediation. `observeCase`
(`observation.go:24-92`) computes an absolute `deadline = injectedAt + window`
and loops: positives may early-exit only when `SourceReady && AuditReady &&
assertionsPass`, followed by a stabilization re-read; negatives
(`requiresFullWindow`, triggered by any `Equals 0`) never early-exit and are
decided **only** from the final at/after-deadline snapshot — closing the prior
false-PASS. Eligibility is fail-closed: `SourceReady` requires every expected
event id present with a server fingerprint, `AuditReady` requires an audit row per
source fingerprint matched by `AlertFingerprint` + exact `RuleName`; unproven →
INCONCLUSIVE. Identity is OSCAR's server fingerprint throughout (closing invariant
#5). `MaxDuration` is a real `context.WithTimeout` (`runner.go:128-134`) with a
compile guard `event.Delay ≤ ObservationWindow ≤ MaxDuration`. Reproduced in a
clone: late in-window parent → FAIL; late audit flush → PASS; absent evidence →
INCONCLUSIVE; assertion `Equals=7` → FAIL. Fingerprints: flood P01 now yields five
distinct `oscar_fingerprint`s (independently recomputed), grouping preserved,
firing/resolved share identity. Closing invariants #1 and #2: **CLOSED.** The one
residual is RC-ORACLE-NEW-1 (post-deadline emit), inherent and bounded.

---

## 6. Durability / evidence / crash analysis

`FinalizeRun` (`execution_repository.go:13-105`) is a single transaction that
terminalizes `run_cases`, `assertions`, `alert_attempts`, and the `runs` envelope
(verdict, cleanup, report, timestamps, terminal event) together, with a fallback
pass so no row remains `PLANNED`. A real flood run followed by a read-only reopen
showed `RESIDUAL_PLANNED_OR_NULL=0` with per-case verdicts equal to the report.
The production runner writes an immutable, hashed, credential-free
`normalized-oscar-evidence` artifact embedding raw history/audit facts; publish
failure forces `VerdictError`; the verdict was **independently recomputed from the
artifact alone**. The crash window is closed: the COMPLETED transition is inside
`FinalizeRun`, recovery catches `COMPLETED AND verdict IS NULL`, and
`DeleteTerminalRun` refuses verdict-less rows — reproduced. Closing invariants #6
and #7: **CLOSED.** Table→writer map now shows terminal UPDATEs on
`run_cases`/`assertions`/`alert_attempts` (previously none). Residual: bundle
re-zip authenticity (MED-4, documented, pending signing).

---

## 7. Cleanup / recovery analysis

Acquisition remains proposal-first and full-identity. Recovery is materially
fixed: `AdoptResource` accepts `PROPOSED,UNKNOWN` (`resource_repository.go:37-38`),
so `RetryCleanup` re-adopts and exact-ID-deletes a lost-create `UNKNOWN` rule —
reproduced end-to-end with the owned rule behind a full 100-row page, reaching
CLEAN; `FindRules` paginates to completion so a truncated page is no longer read
as absence. The happy path deletes rules first, then resolves observed
source+synthetic history by **server fingerprint** via `ResolveHistory`, refusing
records lacking run-id/fingerprint, and marks cleanup DIRTY on any non-Accepted
resolution. Cleanup is exact-ID only — no prefix/name-only deletion exists, so no
non-owned OSCAR resource can be deleted. Closing invariants #4, #5, #8, #9: PASS,
**with the RC-1 caveat on #8** — the error/cancel path (`failAndCleanup`,
`runner.go:375`) passes `nil` histories and therefore does not resolve the alerts
it already injected, and can report `CleanupClean` on an errored run. That is the
one substantive open cleanup gap (MEDIUM).

---

## 8. Semantic-model and mutation results

`testoscar/model.go` is a genuine manual-clock semantic model that evaluates
`MatchCriteria` against label-derived fingerprints, timers, and audit outcomes
with **no** reading of case codes, polarity, expected-assertion values, or
`P01`/`N01` substrings (scan-verified). Mutation campaign in a clone: **16 KILLED
/ 14 SURVIVED** (prior: 10/14). All previously-surviving load-bearing mutations
are now killed: assertion-value MA1–MA4, eligibility→INCONCLUSIVE (M08),
cardinality comparison (M18), per-fingerprint dedup, `UNKNOWN` re-adopt, bundle
write-side hash, flood `min_count` (M14), and `absent_for` — the last two killed
**end-to-end through the semantic fake**, which is the direct proof the substring
anti-pattern is gone (HIGH-9). The remaining survivors are coverage/defense gaps
with no false-PASS or wrong-verdict consequence: the outbound history-request
contract (NF-1, MEDIUM), and LOW items (wrong-run/audit-rule guards with
compensating run-ID controls, untested bundle-Verify negatives, loosely-bounded
compiled criteria, bypassed decode branches).

---

## 9. Web / security analysis

Remote-mode auth continues to wrap every route. New closures verified on an
ephemeral listener: `loopbackHostGuard` (`server.go:591-607`, applied only in
`SecurityNone`) requires a literal loopback IP plus the exact listener port —
`evil.example`→421, `localhost`→421, wrong-port→421, `127.0.0.1`/`[::1]`→200, and
`POST /runs` with a foreign Host→421 (DNS-rebind cannot launch injection). Sessions
now sign an issued-at, enforce an 8h expiry and reject future timestamps
(`auth.go:187-212`), and logout revokes by nonce via a GC'd revoked-map;
non-loopback modes still require secure auth. Residuals: RC-BUILD-2 (`localhost`
literal rejected, docs use `127.0.0.1`); prior LOWs (CSRF cookie `Secure` under
TLS, no login rate-limit, error-string disclosure) and the UI-parity gap (invariant
36) remain disclosed follow-ons.

---

## 10. Build / CI / container / live-gate analysis

`make clean release-gate` passes (exit 0) and now runs the plan3–7 gates,
`check-release-contract.sh`, container/package-content/reproducible checks, and
byte-identical packaging. Every publish-capable lane (GitHub `ci.yml`+`release.yml`,
GitLab verify) invokes `make clean release-gate`, enforced by
`check-release-contract.sh`, which also `exit 1`s on `[no tests to run]` (the prior
empty-selection hole is closed; the timer selection now runs full packages). Stale
archives: GitHub uploads exact tag-named artifacts after `make clean`; GitLab uses
`make clean` + artifact-passing on ephemeral executors — residual only on
non-default self-hosted shell runners. Container: owned writable
`/var/lib/oscar-corrtest` on scratch, non-networking `CMD ["help"]`, documented
`docker run`. Live gate: `live-qualification.sh` fails closed (exit 2, verified,
**before** binary or network) on unset target or wrong acknowledgements, requires
doctor `compatible:true`, then all-eight PASS+CLEAN with verified bundles; it is
never a dependency of `ci`/`release-gate` (closing invariant #10: **CONFIRMED**).
The script's `"verdict":"PASS"` grep can match an embedded per-case verdict, but
the authoritative control is the exit-code check, so it is not a false-PASS vector
(recommend tightening for clarity).

---

## 11. Command audit

Executed in throwaway clones (coordinator + sub-reviewers), exit codes recorded:

| Command | Exit | Result |
|---|---|---|
| `make clean release-gate` | 0 | full gate incl. contract/container/reproducible checks |
| `go test -shuffle=on -count=20 ./internal/{compiler,oscar,runner,runtime,web,testoscar}` | 0 | no failures |
| `go test -race -count=1 ./...` | 0 | no races |
| ephemeral listener + `Host: evil.example` / `localhost` / wrong-port | 421 | rebind blocked |
| ephemeral listener + `Host: 127.0.0.1[:port]` / `[::1]` | 200 | legit loopback works |
| `live-qualification.sh` with unset / wrong acks (sentinel binary) | 2 | fail-closed before binary/network |
| 25 prior + assertion/threshold mutations | mixed | 16 killed / 14 survived (survivors are coverage gaps, no false PASS) |
| real flood run → read-only DB reopen | — | `RESIDUAL_PLANNED_OR_NULL=0`; verdict recomputed from artifact |
| crash-window (COMPLETED, no verdict) → restart | — | non-deletable; recovers INTERRUPTED/UNKNOWN |

Static validation alone was not relied upon; behavioral claims were reproduced.

---

## 12. Residual / deferred-gate ledger

| Item | Class | Enforcing gate |
|---|---|---|
| RC-1: error-path alert resolution + false CLEAN | **OPEN (MEDIUM)** | none yet — required before v1 release |
| MED-FIX: recorded fixtures inert | **OPEN (MEDIUM)** | none — presented as a gate but unwired |
| NF-1: history request contract untested | **OPEN (MEDIUM)** | none — prior MED-TESTS-6, not in closure list |
| Bundle re-zip authenticity (signing) | DEFERRED-WITH-GATE | disclosed in resolution + `live-qualification.md`; hashes retained externally |
| Dispatch mode operator-declared (Phase B) | DEFERRED-WITH-GATE | live gate requires explicit Phase-B ack; fail-closed (cannot false-PASS) |
| Parent-child notifier name | DEFERRED-WITH-GATE | asserted at the **audit** layer, not the live notifier; `email` is target-specific, not a correctness prerequisite |
| Retention >500 pagination | DEFERRED-WITH-GATE | disclosed; bounded workaround (re-run) |
| Post-deadline negative-absence emit | DEFERRED-WITH-GATE | recommend one-line `live-qualification.md` note |
| UI parity, login rate-limit, run-by-stored-id, custom rule criteria | **DEFERRED-WITHOUT-GATE (4)** | disclosed operability follow-ons; non-correctness |

Residual audit conclusion: the ledger is honest — **no silent prerequisite is
masquerading as a residual.** Phase-B and the `email` notifier are fail-closed and
target-specific; a wrong declaration or missing notifier can only lower a verdict
(FAIL/INCONCLUSIVE), never fabricate a live PASS, and cleanup stays exact-ID
regardless. One scope narrowing worth recording: parent-child P01 now proves
suppression at the correlator **audit** layer (`suppressed_per_notifier`), not
end-to-end at the notifier, so design invariant 9's "notifier-specific
suppressed/tagged/released result" is met at the decision layer only — safe, but
narrower than the design's letter.

---

## 13. Final invariant matrix and conditions for controlled live use

**Prior BLOCKERs:** 1 CLOSED · 2 CLOSED · 3 CLOSED.
**Prior HIGHs:** 1,2,3,4,5,6,7,9 CLOSED · 8 PARTIAL (RC-1).
**Report-two items:** all CLOSED / not-a-defect.
**Resolution closing invariants #1–#10:** all CONFIRMED (#8 with the RC-1 caveat on the error path).
**New findings:** 0 BLOCKER · 0 HIGH · 3 MEDIUM (RC-1, MED-FIX, NF-1) · 8 LOW/DEFERRED.

**Conditions for controlled live qualification (may proceed now):**
1. Run only via the isolated `make live-qualification` lane against a **disposable** Phase-B target with both acknowledgements — it is fail-closed and non-PASS by default.
2. Retain the summary and bundle SHA-256 externally (bundle signing is deferred).
3. Treat any `ERROR`/`FAIL`/`DIRTY`/`INCONCLUSIVE` as a stop; because of RC-1, manually confirm no `CORRTEST_*` alerts remain firing on the target after any non-PASS run.

**Required before a v1 release (and before single-run use against non-disposable targets):**
1. **RC-1** — resolve run-owned firing alerts on the error/cancel path, or force `CleanupDirty` when injection occurred without completed resolution.
2. **MED-FIX** — wire the recorded `testdata/public-v1/*.json` fixtures into asserting tests (or document inline coverage as the gate and remove the dead files).
3. **NF-1** — add a regression pinning the outbound history route, filter DSL field, and query parameters.
4. Address the LOW items opportunistically; keep the four DEFERRED-WITHOUT-GATE operability items disclosed.

The remediation genuinely closes the two prior reviews' live-qualification trust
failures. The harness has moved from *cannot be trusted against live OSCAR* to
*trustworthy for controlled, isolated, disposable-target live qualification*, with
a short, honest list of MEDIUM changes standing between it and a v1 release.
