# Adversarial code review — OSCAR Correlation Test Harness v1

Reviewer: independent hostile senior review (did not author the harness). Method:
read-only inspection of both workspaces, first-hand verification of every
load-bearing claim against current OSCAR source and the harness source, plus
build/test/mutation experiments executed only in throwaway clones outside both
workspaces. No implementation file was modified, no service was started, no live
OSCAR was contacted, no alert/rule was created.

---

## 1. Scope, frozen commit, digests, and evidence inspected

- **Workspace:** `/Users/coreyellis/code/github.com/cmetech/oscar_app`
- **Harness repo:** `oscar-corrtest`, module `github.com/cmetech/oscar-corrtest`, Go directive `1.27.0`
- **Frozen implementation:** `e8ab6d0460d14e67cae2f889665499daa70f6011`
- **Repository HEAD:** `6f04cf88c620e03f151e458fafe2645bb2a1c5ad` — a prompt-only descendant of the frozen commit. The single path changed after the frozen commit is `docs/reviews/2026-08-19-oscar-corrtest-adversarial-code-review-prompt.md`. No implementation, plan, design, build, workflow, or dependency path changed after the frozen commit. **Scope is valid.**
- **Worktree:** clean (`git status --short` empty). Not altered by this review.
- **Frozen contract digests:** all 12 verified byte-identical at the frozen commit (design spec, seven plans, README, Makefile, go.mod, go.sum) via `shasum -a 256`. No digest differs.

**Build/test evidence (throwaway clone at frozen commit, `GOWORK=off`, caches redirected outside both workspaces):**

- `make clean` → exit 0.
- `make ci release-gate` → exit 0 (tool install of gosec v2.28.0 + govulncheck v1.6.0, fmt-check, mod-check, vet, gosec, plan2-gate, full unit suite, race suite, static build, standalone-check, package, checksums, plan3–7 gates, container-check, package-content-check, reproducible-check).
- `go test -shuffle=on -count=20 ./internal/compiler ./internal/runner ./internal/runtime ./internal/web` → exit 0.
- Independent observation from the same log: the plan5 timer gate's `-run 'Persistence|Absence|Timer|Timing|Builtin'` selection prints `internal/scenario ... [no tests to run]` and the gate still passes.

**Authoritative comparison sources read (current OSCAR):** middleware `main.py`/`middleware/auth.py`/`core/settings.py`/`routers/{alerts,correlation_rules,notification_audit}.py`; alertmanager `routers/alerts.py`/`core/{fingerprint,rate_limiter,correlation_ingest}.py`; correlator `patterns/*.py`/`core/{consume,synthetic_emitter,audit_writer,redis_client}.py`/`core/lua/add_alert_and_check_trigger.lua`/`routers/{rules,audit}.py`/`schemas/*`; taskmanager `tm_notifier/{handlers,fingerprint}.py`; `oscar-util/scripts/send_custom_alert.py`. OTTO admin UI templates/CSS/JS read for comparison (readable). Harness: all of `internal/`, `cmd/`, `scripts/`, workflows, packaging, docs, JSON schema, the design spec, seven plans, and the three prior review/remediation docs.

**Independent code confirmations performed by the coordinating reviewer** (not merely relayed from sub-reviews): the auth-header mismatch (`client.go:397` vs middleware `main.py:205-213` + `send_custom_alert.py:549`); the flood counting model (`add_alert_and_check_trigger.lua` line 30/34/62 + `flood.py` + `fingerprint.py` exclusions + `AlertRateLimiter` at `alerts.py:105`) against the harness stimulus (`builtin.go:33`, `compiler.go:138-150` labels-vs-annotations); the empty set of terminal `UPDATE`s to `run_cases`/`assertions`/`alert_attempts` and the dead `report.Build`/`SaveCanonicalReport` while the runner builds an inline `canonicalReport` (`runner.go:412`, `CompleteRun`); the `AdoptResource` `PROPOSED`-only guard (`resource_repository.go:37-38`); the runner oracle reading only `item.Polarity`/`item.Rule.Pattern` with zero references to `item.Assertions` (`runner.go:266-322`); release.yml running `make ci` only; `runExitCode` returning 4 with no 130; and the absence of `OSCAR_CORRTEST_LIVE_TARGET`/`fixtures/public-v1/` anywhere but unchecked plan boxes.

---

## 2. Executive verdict

> **BLOCK LIVE QUALIFICATION.**

The harness is well-engineered at its edges — the acquisition side of mutation
ownership (proposal-before-create, full-identity reconciliation, ownership-verified
deletes), the SQLite durability contract, remote-mode auth coverage, CSP/CSRF/template
hardening, static reproducible packaging, and the fingerprint read-back discipline are
all genuinely correct and non-tautologically tested. But its **center is hollow or
wrong for current OSCAR**, and that is precisely the failure class a correlation test
oracle must not have:

- It **cannot authenticate** to a documented OSCAR deployment (`Authorization: Bearer` vs the required `X-API-Key`).
- Its **flagship flood pattern cannot fire** against current OSCAR, which counts distinct fingerprints, not repeats.
- Its **oracle is unsound in both directions**: it reads the correlation audit once, before the correlator can have decided (systematic false FAIL of every positive), and it closes negative windows on evidence gathered before the window opened (demonstrated false PASS).
- **Declared assertions are never evaluated**; the oracle is hard-coded by polarity/pattern, so every custom scenario is judged against fixed built-in logic.
- **Evidence is not materialized**: normalized tables stay `PLANNED`, no raw OSCAR response is retained, and every verdict rests on a single self-attesting JSON blob that cannot be recomputed from stored facts.
- Two **recovery holes** can permanently leak an operator's OSCAR rule while the harness insists the run is clean.

Not one of the sixteen built-in pattern rows can be shown to PASS against current
OSCAR source. Three defects were confirmed by executable demonstration in a throwaway
clone; the shipped suite is green throughout only because its fakes mirror the client's
own wrong assumptions. The build/packaging findings are release-only, but the contract,
oracle, evidence, and cleanup findings block controlled live qualification.

---

## 3. Blocker findings

Each blocker meets the prompt's bar (mutate an operator-owned OSCAR resource, or
fundamentally cannot exercise current OSCAR). All three are CONFIRMED and were
verified first-hand by the coordinating reviewer.

### BLOCKER-1 — Harness authenticates with `Authorization: Bearer`; OSCAR's external API requires `X-API-Key` (CONFIRMED)
- **Evidence:** harness sets only `Authorization: Bearer <credential>` (`internal/oscar/client.go:396-397`) and never any other auth header. OSCAR's external surface is gated on the `X-API-Key` header: middleware OpenAPI security is `APIKeyHeader` named `X-API-Key` with `security=[{"APIKeyHeader":[]}]` (`oscar-middleware/src/app/main.py:205-213`); `API_KEY_NAME` defaults to `"X-API-Key"` (`core/settings.py:1089`); the external branch reads `request.headers.get(API_KEY_NAME)` and 401s on a missing/mismatched key (`middleware/auth.py:576`). A non-JWT Bearer value falls through the agent-JWT path and lands in the API-key branch. No `Authorization→X-API-Key` translation exists in the reverseproxy. OSCAR's own sender uses `headers["X-API-Key"]` (`oscar-util/scripts/send_custom_alert.py:549`). No external alert route accepts Bearer.
- **Failure scenario:** an operator following `docs/operator.md` against any production OSCAR (`ENABLE_API_KEY_VALIDATION=true`, reverseproxy fronted) gets 401 on every call — validate, create, inject, history, audit, delete. Doctor and all runs fail. Pointed *internally* (no `/ext/` prefix) the request is treated as internal and passes with **no auth at all**, silently ignoring the resolved credential.
- **Why tests missed it:** `internal/testoscar` mirrors the harness's Bearer assumption; nothing compares against real OSCAR auth code.
- **Remediation:** send the resolved credential as `X-API-Key` (keep Bearer additionally if agent-JWT is ever needed); document the target base URL must include `/ext/mw` (MED-11).
- **Closing gate:** contract test whose fake rejects Bearer-only with 401 unless `X-API-Key` matches; doctor surfaces a `"No API key provided"` 401 as an auth-shape error.
- **Blocks:** live OSCAR qualification (fake-server/local use unaffected).

### BLOCKER-2 — The flagship flood pattern cannot fire against current OSCAR (CONFIRMED)
- **Evidence (two independent kill paths, both verified against OSCAR source):** flood P01 sends role `interface_down` with `Repeat: 5` and identical labels `{site: corrtest-p01}` (`internal/scenario/builtin.go:33`); the compiler puts per-event uniqueness (`oscar_test_event_id`, `oscar_test_event_index`) only in *annotations*, while the label set from `identityLabels` + `source.Labels` is byte-identical across all five repeats (`internal/compiler/compiler.go:138-150`). OSCAR's canonical fingerprint is labels-only and excludes description/summary/timestamps (`oscar-alertmanager/src/app/core/fingerprint.py`), so all five collapse to **one** `oscar_fingerprint`.
  - *Kill path A (ingress):* a per-transport-fingerprint rate limiter (`AlertRateLimiter`, `routers/alerts.py:105`; `ALERT_RATE_LIMIT_MAX_PER_MINUTE=3` in shipped `conf/env/alertmanager.env`) drops repeats 4–5, returning `200` with prose body `"Alert rate limited (fingerprint: …)"`. The harness classifier matches only exact tokens (`client.go:206-215`), maps that body to `InjectionIndeterminate`, and `runner.go:193-195` aborts the run with `VerdictError`.
  - *Kill path B (window):* even with the limiter off, the flood Lua does `ZADD win_key, ts_ms, fp` (member = fingerprint) and counts `ZCARD` (`core/lua/add_alert_and_check_trigger.lua` lines 30/34/62 — explicitly "idempotent on duplicate fingerprints"). Five identical fingerprints → `ZCARD = 1 < min_count = 5` forever. OSCAR flood is a distinct-fingerprint counter; the stimulus is one fingerprint repeated. (Corroborated independently: the correlator additionally dedups decision side-effects on `(fingerprint, rule_id, window_id)` — `core/consume.py:498-529` — so even the fifth arrival's `parent_emitted` would be a redelivery no-op.)
- **Failure scenario:** on any default-configured live OSCAR, `run flood` terminates `ERROR injection was indeterminate`; with the limiter off it reports `FAIL` for a correctly-functioning OSCAR. The plan-2 flagship vertical slice can never PASS.
- **Why tests missed it:** the runner's `fakeAPI` returns `InjectionAccepted` and one history row unconditionally; no fake models fingerprints, the limiter, or window counting.
- **Remediation:** give each flood event a distinct varying non-group label (e.g. `oscar_test_event_seq=N`, excluded from `group_by` but included in the fingerprint), assert the distinct-fingerprint count, and respect/classify the documented per-fingerprint limiter.
- **Closing gate:** a contract test computing OSCAR's canonical fingerprint over each compiled flood alert and asserting 5 distinct values; a classifier test on the literal live rate-limit body.
- **Blocks:** live OSCAR qualification (flood); release (flagship scenario non-functional).

### BLOCKER-3 — The oracle's time model is unsound in both directions (CONFIRMED, two executable demonstrations)
- **Evidence:**
  - *False PASS (negative window):* for negative cases the synthetic-history query runs once with `waitForOne=false` (a single immediate `FindHistory`), then the runner sleeps the 35s observation window, and the verdict uses the **pre-sleep** `SyntheticCount`; audit outcomes were fetched even earlier (`runner.go:249, 270-299`). Nothing is re-queried after the window. Demonstrated in a throwaway clone (`TestNegativeCasePassesEvenWhenParentAppearsDuringObservationSleep`): a fake exposing a synthetic parent for the entire post-sleep window still yields `PASS "eligible source remained non-triggering through the full decision window"` with `lateQueriesObserved=0`.
  - *Systematic false FAIL (audit read):* the correlation audit is read exactly once, immediately after the source history row appears — which in current OSCAR is on the storage path *before* the correlator has consumed the alert from JetStream, and audit rows are buffered on a 5s flush (`audit_writer.py`). Demonstrated (`TestPositiveCaseFalseFailsWhenAuditRowsFlushLate`): one late flush turns a correct correlation into `FAIL`. For the timer patterns (`parent_emitted` written 30s+ later) this is deterministic on every live run; instant patterns are a coin-flip race against the ≤5s flush.
- **Failure scenario:** a correct OSCAR fails the harness's positive cases (persistence P01, absence P01 deterministically; the rest flakily), and a broken OSCAR passes the harness's negative cases (any correlation completing inside the 35s window is invisible). The oracle cannot be trusted in either direction, so no live PASS/FAIL is meaningful.
- **Why tests missed it:** every runner fake answers time-invariantly and correctly on the first call, keyed on `_P01_` substrings — it cannot distinguish "queried before" from "queried after."
- **Remediation:** after the negative window elapses, re-query history AND audit and compute the verdict exclusively from post-window evidence; poll audit with the same deadline discipline as history; derive per-case deadlines from the compiled timer durations (see MED-13).
- **Closing gate:** the two demonstration tests above, inverted (late parent must FAIL N01; late audit flush must still PASS P01).
- **Blocks:** all use, live qualification, release.

---

## 4. High-severity findings

Consolidated across sub-reviews; where multiple reviewers found the same defect the
corroborating IDs are listed. All CONFIRMED unless marked.

### HIGH-1 — Declared scenario assertions are decoded, validated, persisted, and never evaluated (ORACLE-6 / TESTS-1 / TRACE-3)
The runner contains zero references to `item.Assertions`; `observeCase`/`evaluateParentChild` choose behavior from `item.Polarity` and `item.Rule.Pattern` alone (`runner.go:266-322`), while the compiler carries assertions into the plan (`compiler.go:135`), the decoder whitelists them (`decode.go:145-155`), the JSON Schema requires them, and the design says the harness "evaluates explicit assertions." Demonstrated in a clone: mutating every built-in assertion to `Equals: 7` / an impossible outcome still yields PASS. Any custom scenario (Plan 7's headline feature) asserting a different cardinality/outcome silently gets the fixed two-case logic. **Blocks** all custom-scenario use; makes built-ins' persisted assertion rows misleading.

### HIGH-2 — Normalized case/assertion/alert-attempt rows are inserted `PLANNED` and never updated (EVIDENCE-1 / TRACE-1)
`SetRunExecutionDocuments` inserts `run_cases` (`status='PLANNED'`), `assertions` (`expected_json` only), and `alert_attempts` (`send_state='PLANNED'`, NULL fingerprint) at preflight (`run_repository.go:223/239/254`). There is **no** production `UPDATE` to any of the three tables. After a real PASS run in a clone, every case is `PLANNED`, every assertion verdict/observed is NULL, every attempt is `PLANNED` with NULL fingerprint — while `runs.canonical_report_json` (written by `CompleteRun` from the runner's in-memory `canonicalReport`, `runner.go:412`) claims PASS with per-case detail. The design's own premise — "a table created but never updated is not durable evidence" — is exactly what shipped. **Blocks** live qualification (material evidence loss).

### HIGH-3 — Raw OSCAR evidence is never persisted; the artifact store, `report.Build`, and `SaveCanonicalReport` are dead code (EVIDENCE-2 / TRACE-2)
`artifacts` has 0 rows after a run; `Store.Write`, `report.Build`/`Marshal`, and `SaveCanonicalReport` have zero production callers (verified). The raw facts a verdict depends on (history records, correlation-audit outcomes, notification statuses, fingerprints) are consumed transiently in `observeCase` and survive only as interpreted strings inside the report blob. A verdict cannot be independently recomputed from persisted facts; the designed redaction pipeline (`internal/oscar/redact.go`, `internal/export`, per-run `evidence/*.json`) was never built. **Blocks** live qualification (auditability).

### HIGH-4 — Lost-create resources in `UNKNOWN` state can never be reconciled; the OSCAR rule leaks permanently (LIFECYCLE-1)
When a create response is lost and ownership cannot be proven, the runner marks the resource `UNKNOWN` (`MarkResourceCleanupError`, `resource_repository.go:69`). `RetryCleanup` later re-finds and proves ownership but calls `AdoptResource`, which only updates rows `WHERE lifecycle_state='PROPOSED' AND external_id IS NULL` (`resource_repository.go:37-38`) — so an `UNKNOWN` row affects 0 rows and returns "not eligible for adoption" forever. Reproduced in a clone: three retries, zero deletes, permanently DIRTY. The created OSCAR rule is unmanaged with no in-harness removal path. (Note: `MarkResourceDeleted` *does* accept `UNKNOWN` (`resource_repository.go:53-54`), so the fix is small — let the retry path tolerate `UNKNOWN` or add a dedicated re-adopt.) **Blocks** live qualification.

### HIGH-5 — Non-atomic terminal write can mark a leaked-rule run cleanup-safe and let deletion erase the evidence (LIFECYCLE-2)
Completion is two writes — `transition(COMPLETED)` then `CompleteRun` (`runner.go:407-417`) — and `cleanup_status` defaults to `NOT_REQUIRED` until `CompleteRun`. Startup recovery skips `COMPLETED` runs; `RetryCleanup` requires DIRTY/UNKNOWN; `DeleteRun`/retention accept `COMPLETED + NOT_REQUIRED`. A crash or single failed write between the two (silently discarded in serve mode, `runtime.go:228`) yields a `COMPLETED`, verdict-less, "cleanup-safe" run that recovery skips, retry refuses, and delete happily erases — destroying the only ledger evidence of a live leaked rule. Reproduced end-to-end including restart and deletion. **Blocks** live qualification.

### HIGH-6 — Threshold P01 and cross_source P01 are structurally INCONCLUSIVE against OSCAR's episode store (CONTRACT-3 / ORACLE-4)
The runner demands exactly one history row per source alertname (`runner.go:244-247`). AlertHistory is a fingerprint-keyed episode store. Threshold P01 sends 3 same-name alerts with `device=d1/d2/d3` (3 distinct fingerprints → 3 rows); cross_source P01 sends 2 same-name alerts with differing `oscar_source` (2 rows). Both yield `len(sources)>1 → INCONCLUSIVE`, always. These positives can never PASS live. **Blocks** live qualification (threshold, cross_source).

### HIGH-7 — Absence timing is impossible for P01 and its N01 control is invalid; a firing synthetic parent is stranded (CONTRACT-4 / ORACLE-5)
OSCAR fires absence at `last_heartbeat + (expected_every + absent_for)` = heartbeat+40s and reschedules (`patterns/absence.py`); the runner's observation window is 35s (`runner.go:68`), so P01 deterministically times out → FAIL. N01 stops heartbeats at +15s, so a genuine absence begins ~+45s and OSCAR emits a parent the harness either FAILs (correct OSCAR) or (via BLOCKER-3) false-PASSes — while leaving an unmanaged `CORRTEST_ABSENCE_N01_*` synthetic alert in the operator's OSCAR after a CLEAN verdict. **Blocks** live qualification (absence); N01 is a false-PASS + residue class.

### HIGH-8 — Injected firing alerts and synthetic parents are never resolved or tracked (ORACLE-7)
Every source event is sent `firing`; nothing sends a matching `resolved`; cleanup deletes only `correlation_rule` resources and the ledger records only rules (`runner.go:340-379`). Synthetic parents emitted in positive cases and all source alerts remain FIRING on OSCAR's active-alert surface after a nominally-PASS run, unmanaged and un-ledgered — potentially generating operator pages/tickets. This is "leave a created resource unmanaged" and is arguably a BLOCKER if those alerts route to real notifiers. **Blocks** live qualification (mutation residue on an operator-owned surface).

### HIGH-9 — The fake OSCAR is not independent; pattern coverage is canned off case codes (TESTS-2 / ORACLE-12)
`internal/testoscar` is a 109-line scripted replay queue used only by client tests; the runner's `fakeAPI` decides outcomes via `strings.Contains(fingerprint, "_P01_")` / `"PARENTCHILD"` and history via `Contains(name, "SYNTHETIC")` (`runner_test.go:321-352`). No fake models windows, counts, ordering, distinct labels, timers, or the limiter. Proven by surviving mutations: persistence `unresolved_for_seconds` 30→3 (M12) and flood `min_count` 5→4 (M14, which makes N01 fire on real OSCAR) both leave the whole suite green. This is the exact anti-pattern the charter names and is why BLOCKER-2/3 and HIGH-1/6/7 all survive a green CI. Invariant 28 FAIL; 29 materially weakened.

---

## 5. Medium- and low-severity findings

### Medium
- **MED-1 — DNS rebinding defeats same-origin on the loopback default bind (WEB-1).** `sameOrigin` compares `parsed.Host == r.Host` but never pins `r.Host` to a loopback allowlist, and returns `true` when `Origin` is absent (`server.go:565-582`); default bind is `127.0.0.1:8787`, no auth. During a rebind window a hostile page becomes same-origin, reads the CSRF token, then `POST /runs` (launch OSCAR injection) or `POST /runs/{id}/delete`. Both browser-mutation controls are bypassed. **Arbitration note:** defensible as HIGH because exploitation reaches OSCAR mutation; rated MED because it needs the tool actively running and the victim on a hostile page during rebind. Fix: ~10-line Host allowlist.
- **MED-2 — Bearer sessions never expire server-side; logout cannot revoke (WEB-2).** `validSession` verifies only `HMAC(bearerToken, nonce)` — no issued-at/expiry, no server store (`auth.go:146-158`); the 8h `MaxAge` is client-side only; logout clears only the client cookie. A captured cookie authenticates until deployment-wide token rotation. Fix: sign `issuedAt`(+epoch), enforce TTL, bump epoch on logout-all.
- **MED-3 — UI omits required operator actions (WEB-6 / TRACE-7).** No UI route for doctor/preflight, custom-scenario **run** (`POST /runs` is builtin-only), cleanup retry (a dirty run has no UI remediation — `CanDelete` needs clean), retention preview/apply, bundle verification, backup, or `builtin:all`. Drives invariant 36 FAIL.
- **MED-4 — Evidence-bundle `Verify` proves internal consistency, not authenticity (EVIDENCE-3 / TESTS-3).** The manifest is a file inside the ZIP; a full re-zip with a recomputed manifest passes `Verify` (demonstrated: FAIL→PASS re-zip verifies clean). The only anchor is the export-time SHA-256 printed to stdout. The tamper test (`TestVerifyDetectsTamperedBundle`) never tampers a bundle — it feeds `"not a zip"`; disabling the SHA-256 compare (M11b) and the undeclared-files check (M21) both survive.
- **MED-5 — Retention silently caps at 500 and filters eligibility after the cap (EVIDENCE-4).** `PreviewRetention` applies `terminal && cleanupSafe` in Go after SQL `LIMIT 500` (`runtime.go:546-563`), so preview under-reports and apply deletes a partial page with no "more remain" signal. Invariant 17 explicitly requires >500 transparency.
- **MED-6 — RetryCleanup concludes "never created" from one truncated search page (LIFECYCLE-3).** Zero matches on `page=1,perPage=100` → `MarkResourceDeleted` → CLEAN, with no truncation check (`client.go:151-167`, `runtime.go:455-459`). On a busy deployment the owned rule can sit beyond page 1 → false CLEAN + unmanaged rule.
- **MED-7 — A single failed lifecycle-transition write aborts cleanup and strands the run non-terminal (LIFECYCLE-4).** `failAndCleanup`/`cleanup` return before any OSCAR deletion if the CANCELLING/CLEANING_UP transition write fails (`runner.go:341-343, 384-386`); in serve mode the error is discarded and the run stays OBSERVING/ASSERTING forever — uncancellable, retry-ineligible — with rules leaked until process restart.
- **MED-8 — Persistence P01 is flaky live (CONTRACT-5).** 30s timer + 1–5s pipeline lag vs the 35s deadline leaves ~0–4s slack.
- **MED-9 — Injection classifier cannot recognize OSCAR's real drop/queue bodies (CONTRACT-6 / ORACLE-11).** OSCAR returns prose (`"Alert group filtered by ACL rule …"`, `"Alert rate limited (fingerprint: …)"`); the classifier keys on absent enum tokens (`client.go:206-224`), so ACL/rate-limit responses degrade to Indeterminate and the `InjectionRejected/Partial` branches are dead. Fail-closed (no false PASS), but wrong evidence and only nominal invariant-6 independence.
- **MED-10 — History annotations parsed with the wrong JSON key (CONTRACT-7).** OSCAR serializes `{"Annotation","Value"}`; the harness decodes `{Label,Value}` (`client.go:452-476`), so every annotation name collapses to `""`. The harness's own per-event identity lives in annotations. No oracle impact today; read-back evidence fidelity loss.
- **MED-11 — Documented target URL omits the mandatory `/ext/mw` base path (CONTRACT-8).** `operator.md:10` shows `--url https://oscar.example`; externally OSCAR serves the API only under `/ext/mw`. Compounds BLOCKER-1.
- **MED-12 — Pipeline mode is declared, never probed; correlator readiness never checked (ORACLE-9).** `pipelineMode` is threaded verbatim; `Ready` is rule-validation success only (`runtime.go:275-304`). A Phase-A target declared `phase_b` is undetected (fail-closed FAIL, not false PASS).
- **MED-13 — `MaxDuration` compiled but never enforced; fixed observation constants ignore compiled timers (ORACLE-10).** No deadline derives from `plan.MaxDuration`; 35s/5s/1s constants ignore the 30s persistence/absence timers.
- **MED-14 — CLI exit-code contract diverges from design §9.3 (TRACE-5).** Exit 130 is never returned; FAIL+dirty exits 4 (hiding product failure behind a cleanup code); case-level INCONCLUSIVE aggregates to FAIL so exit 2 is unreachable (`execution_commands.go:295-308`, `runner.go:213-218`).
- **MED-15 — No CI path runs `release-gate` (BUILD-2 / TRACE-6).** `release.yml:37` runs `make ci`; GitLab runs `ci-core`+package. plan3–7 gates, container-check, package-content-check, reproducible-check execute in no pipeline, while README/development.md present `release-gate` as the release qualification.
- **MED-16 — plan5-gate passes with zero tests selected; no guard on empty selection (BUILD-1 / TESTS-5).** `-run 'Persistence|Absence|Timer|Timing|Builtin'` matches none of `./internal/scenario`'s tests → `[no tests to run]`, gate green. No Make target guards `[no tests to run]` (a bogus regex also exits 0).
- **MED-17 — Stale-archive leak via broad release globs + no `make clean` in CI (BUILD-3).** `dist/*.tar.gz` globs on non-ephemeral runners (GitLab shell executor default `GIT_STRATEGY=fetch`, self-hosted GitHub) can attach prior-version archives to a release; SHA256SUMS is version-scoped and would not cover them (proven: seeded stale archive is excluded from checksums, so it would ship uncovered).
- **MED-18 — Shipped Containerfile cannot serve as configured (BUILD-5).** Default CMD binds `127.0.0.1` inside the container (published ports unreachable, `EXPOSE` dead) and points `--data-dir` at `/var/lib/oscar-corrtest`, which uid 65532 cannot create on `scratch`; no `VOLUME`, no container docs.
- **MED-19 — The oracle's hostile-evidence guards have zero regression coverage (TESTS-4).** Surviving mutations: negative source anchor removed (M08), positive cardinality `==1→>=1` (M18) and stabilization recheck (M19), wrong-run label guard removed (M20), and reconcile description-ownership conjunct removed (M09 — a hostile lookalike with the right name but wrong description would be adopted and later **deleted**, i.e. operator-resource deletion). Any future refactor can silently drop these.
- **MED-20 — The alerts-history route and filter DSL are pinned by no test (TESTS-6).** History route → `/api/v1/alertsx/history` (M02) and filter field `alertname→alert_name` (M03) both survive; this is the authoritative fingerprint read path (invariant 7).
- **MED-21 — Imported scenarios are unexecutable dead records (TRACE-8).** `ImportScenario` persists by digest, but no CLI/UI path runs a stored scenario by ID.
- **MED-22 — Design's Phase-A audit-only assertion mode is unimplemented but offered (TRACE-9).** The runner refuses all mutation unless `phase_b_dispatch`, so every advertised `phase_a_audit_only` run is INCONCLUSIVE with no audit-only evidence (fail-closed).
- **MED-23 — Plan-committed rate limiting on remote auth is absent (TRACE-10 / WEB-4).** No throttle/lockout on `/login` or bearer checks (`auth.go:106-135`); mitigated by the ≥16-byte token floor.
- **MED-24 — Startup auto-cleanup and the `RECOVERING` state were dropped without record (TRACE-11).** Design §8 promises automatic provable-ownership cleanup and `INTERRUPTED→RECOVERING→CLEANING_UP`; only manual `cleanup retry` exists; `RunRecovering` is dead code (docs match code; design is stale).
- **MED-25 — Custom scenarios cannot express rule criteria (TRACE-12).** The compiler hard-codes `min_count:5`, `min_distinct_count:3`, `unresolved_for_seconds:30`, `expected_every:10/absent_for:30`, and requires exactly two P01/N01 cases (`compiler.go:188-214`, `decode.go:123-135`), contradicting design §10.2/§10.4. A team cannot validate their real rule shape.
- **MED-26 — Parent-child P01 depends on an unverified live notifier named "email" (ORACLE-8, UNPROVEN).** The runner requires `NotificationStatuses` containing `"suppressed"` (`runner.go:312`), which needs a configured/enabled `email` notifier; the harness never lists/validates notifiers. Undocumented, ungated live dependency.

### Low
WEB-3 (CSRF cookie omits `Secure` under TLS); WEB-5 (data-layer error strings rendered — escaped — in operator HTML); TESTS-7 (delete 404-idempotency untested, M13 survives); TESTS-8 (decode semantic branches untested, M22/M23/M24 survive); TESTS-9 (empty 2xx body → empty list, no false PASS today); LIFECYCLE-5 (`run builtin:<bogus>` persists a stuck QUEUED run before validation); LIFECYCLE-6 (run serialization is process-local only); LIFECYCLE-7 (no in-run pre-delete read-back; post-create check omits Pattern; dead RECOVERING transitions); EVIDENCE-5 (dead evidence-assembly code misrepresents the durability contract); BUILD-4 (leak scan misses embedded `migrations/*.sql`); BUILD-6 (explicit `--config` missing file silently ignored — the systemd unit works only by this accident); BUILD-7 (GitHub action-pin authenticity UNPROVEN from sandbox); BUILD-8 (glab/`CI_JOB_TOKEN` release rights UNPROVEN, documented as operator gate); TRACE-13 (`internal/report` dead code with tests); TRACE-14 (small design/code divergences: no `NO_COLOR`, no `forms.js`, raw-JSON Inspect panel, no suite/date filters, unversioned container binary, run-level-only JUnit, untested `scenario_repository`, timer durations absent from docs); TRACE-15 (planned package/commit structure compressed); CONTRACT-9 (`NotificationRecord.Labels` never populated); CONTRACT-10 (RFC3339Nano >6-digit fractional seconds vs Pydantic datetime — UNPROVEN); CONTRACT-11 (residual live deps: API-key RBAC grants, pipeline-mode detectability, `email` notifier).

---

## 6. 40-invariant verdict matrix

PASS requires code plus a non-tautological test or direct command evidence. `*` = PASS with a required change / material caveat detailed above.

| # | Invariant (abbrev) | Verdict | Basis |
|---|---|---|---|
| 1 | Clean standalone checkout builds/tests, GOWORK=off, no sibling OSCAR/Python/Node/DB | **PASS** | `make standalone-check` exit 0; git-archive build in isolated temp |
| 2 | Every used OSCAR route/method/auth/query/response/pagination/delete matches source | **FAIL** | BLOCKER-1 auth; MED-9 classes; MED-10 annotations |
| 3 | All 8 patterns have valid P01/N01, rule constant, only stimulus differs | **FAIL** | rule-constancy good, but flood/threshold/cross_source/absence stimuli invalid (BLOCKER-2, HIGH-6/7) |
| 4 | Alert-name/reserved-label grammar exact and manually filterable | **PASS** | `compiler.go:170-179`, reserved-label override is a compile error; filter strings emitted per case |
| 5 | Rule validation, label-survival, pipeline mode, correlator readiness fail closed | **FAIL** | pipeline mode never verified; correlator readiness never checked (MED-12) |
| 6 | Injection acceptance classified independently from correlation evidence | **PASS*** | typed `InjectionResult`, aborts before observation; classifier accuracy caveat (MED-9) |
| 7 | Audit queries use OSCAR's history-read-back fingerprint | **PASS** | `runner.go:248-249`; pre-stamped fingerprints refused; `TestRunnerUsesHistoryFingerprintNotDiagnosticHash` |
| 8 | Negatives anchor eligible sources + observe full window + stabilization | **FAIL** | pre-window snapshot, no post-window re-query (BLOCKER-3) |
| 9 | Parent-child uses explicit notifiers, proves linkage + suppressed/released result | **FAIL** | unverified `email` dependency (MED-26); audit leg races single read (BLOCKER-3) |
| 10 | Rule creation records proposal before mutation, safe reconciliation | **PASS** | proposal-first, full-identity reconciliation; real tests |
| 11 | Cleanup uses exact IDs + full ownership, 404-idempotent, retryable, no CLEAN for unknown | **FAIL** | HIGH-4 UNKNOWN dead-end; HIGH-5 NOT_REQUIRED; MED-6 truncated page |
| 12 | Cancellation at every boundary reaches durable terminal state + bounded cleanup | **PASS*** | detached 30s cleanup verified; caveat MED-7 double-fault, live-ctx finishes |
| 13 | Restart marks interrupted w/o resuming; safe reconcile path for every owned resource | **FAIL** | HIGH-4/HIGH-5; reconcile is CLI-only |
| 14 | Normalized tables reflect terminal facts, not empty/PLANNED while JSON claims done | **FAIL** | HIGH-2 (demonstrated) |
| 15 | Raw/normalized source evidence retained separately, sufficient to recompute verdict | **FAIL** | HIGH-3 (demonstrated) |
| 16 | Exports deterministic, redacted, path-safe, non-overwriting, self-describing, tamper-evident | **FAIL** | deterministic/redacted/path-safe PASS; not tamper-evident (MED-4) |
| 17 | Deletion/retention refuse active/dirty/unknown/pending/missing/hash-mismatch, no race | **FAIL** | deletion sound; retention >500 not transparent (MED-5) |
| 18 | YAML/JSON validation rejects unknown/dup/alias/multi-doc/unsafe consistently | **UNPROVEN** | shared decoder + all four surfaces call it, but duration/budget/kind/polarity branches untested (M22-24 survive) |
| 19 | CLI and UI use same compiler/runtime/runner/oracle + stable exit/error | **PASS** | one engine; pinned exit codes (UI custom-run gap → inv 36) |
| 20 | Loopback only unauth bind; non-loopback bearer needs TLS+secure sessions; proxy exact identity+CIDR | **PASS*** | enforced/tested; session-lifetime caveat (MED-2) |
| 21 | Every route protected in remote mode; mutations enforce body+same-origin+CSRF | **PASS*** | remote auth wraps every route; same-origin lacks Host pinning (MED-1) |
| 22 | No credential/sensitive values in config/SQLite/URL/log/HTML/SSE/report/bundle/CI | **PASS** | references only; token process-memory only |
| 23 | SSE monotonic, disconnect-independent, bounded, authenticated, terminal-aware, no cancel authority | **PASS** | `server.go:443-483`; browser disconnect never cancels run |
| 24 | SQLite migrations/WAL/concurrency/backup/permissions/corruption/FK/recovery match contract | **PASS** | WAL+FULL, per-conn pragmas, digest-pinned migrations w/ rollback, verified cancellable backup, FK ordering — all tested |
| 25 | Canonical reports/history interpretable after restart/edits/compiler evolution/artifact loss | **UNPROVEN** | report+events survive, but per-case facts absent (HIGH-2) |
| 26 | Run serialization prevents cross-run interference; no deadlock/SQLite-after-close | **PASS*** | in-process serialization + bounded shutdown; not cross-process (LOW) |
| 27 | Custom/built-in budgets, roles, criteria, states, timings, labels, assertion kinds map to OSCAR | **FAIL** | criteria/roles/labels map; flood/absence/threshold/cross_source semantics do not (BLOCKER-2, HIGH-6/7) |
| 28 | Fake OSCAR independent enough to reject wrong client behavior | **FAIL** | HIGH-9 (M12/M14 survive) |
| 29 | Tests cover all 16 cases + hostile/error timelines; filters/regexes select intended tests | **FAIL** | semantically hollow pattern runs; hostile timelines absent; plan5-gate empty selection |
| 30 | Race/security/vuln/standalone/cross-build/package/content/checksum/reproducibility gates from Make | **FAIL** | all run + pass, but empty-selection clause fails (MED-16) |
| 31 | Linux AMD64/ARM64 packages: static exe, CA strategy, docs/schema/systemd/container, correct modes | **PASS** | `file(1)` static+stripped; content check green; 0755/0644 |
| 32 | GitHub+GitLab immutable pins, least privilege, equivalent Make behavior, tag rules, artifact flow | **FAIL** | no CI runs release-gate (MED-15); stale-glob leak (MED-17); 2 UNPROVEN externals |
| 33 | Repeated packaging byte-identical; checksums exclude stale; upload globs cannot leak them | **FAIL** | byte-identical proven 3×, checksums version-scoped, but release globs can leak (MED-17) |
| 34 | Container + systemd examples match actual flags/creds/paths/TLS/shutdown/security | **FAIL** | systemd exact; Containerfile broken as shipped (MED-18) |
| 35 | UI theme before paint; system/light/dark; keyboard/focus/reduced-motion/contrast/no-JS; OTTO-consistent | **PASS** | nonce'd pre-paint theme; reduced-motion; no-JS forms; `theme_contract_test.go`; OTTO consistent |
| 36 | UI covers targets/doctor/preview/run/progress/cancel/cleanup-retry/export/verify/delete/retention/filters/history | **FAIL** | missing doctor, custom-run, cleanup-retry, retention, verify (MED-3) |
| 37 | Docs/help/schema/examples/packages/routes mutually accurate, no unimplemented promises | **PASS** | every documented flag/command verified against built-binary help |
| 38 | Error paths bound bodies/waits, sanitize, preserve classification, no malformed→absence | **PASS** | 4MiB cap, `safeDetail`, `invalid_json` class, deadline-bounded polls |
| 39 | No test/release gate silently contacts live OSCAR; optional live qual explicit/isolated/non-PASS | **PASS** | in-process fakes; smoke target `https://oscar.invalid`; no OSCAR endpoint in gates |
| 40 | No runtime/build/workflow assumes parent OSCAR/OTTO; leak scan not bypassable | **FAIL** | no parent dependency, but embedded `migrations/*.sql` outside scan list (LOW) |

Totals: **PASS 18** (6 with caveats), **FAIL 20**, **UNPROVEN 2**, **NOT-APPLICABLE 0**.

---

## 7. 16-row pattern / oracle matrix

Result = whether the row achieves its documented intent against **current OSCAR**
(source-derived; no live system contacted). All rows additionally inherit BLOCKER-3
(unsound negative window; single pre-trigger audit read). None collapsed. **PASS: 0.**

| # | Pattern / Case | Compiled criteria | Sent alerts (names/labels/state/order/delay) | Expected current-OSCAR behavior | Evidence queried | Negative anchor / window | Result | Test / missing test |
|---|---|---|---|---|---|---|---|---|
| 1 | flood P01 | `alertname=CORRTEST_FLOOD_P01_INTERFACEDOWN_<S>`, `min_count=5`, win 30s, group [site] | 5× same name, identical `site=corrtest-p01`, firing, no delay | limiter drops #4-5; else 1 fingerprint → ZCARD=1<5 → never fires | history→fp; audit by fp; synthetic history | n/a | **FAIL** (BLOCKER-2) | passes only because fake has no fingerprint/limiter model; need distinct-fingerprint contract test |
| 2 | flood N01 | same rule (`min_count=5`) | 4× identical, firing | #4 rate-limited → run ERROR; limiter off: count 1, no parent (correct) | same | anchor `len(audit)>0`; pre-sleep only | **FAIL** (ERROR on shipped conf; unsound window) | need rate-limit body classification test |
| 3 | co_occurrence P01 | `required_matches=[DISKFULL,CPUHIGH]`, `min_matches=2`, 30s | DISKFULL@0s, CPUHIGH@1s, distinct names | HLEN→2 → parent_emitted + synthetic | history×2→fp; audit per fp; synthetic | n/a | **UNPROVEN** (flaky false FAIL: single audit read races 5s flush) | coordinator test keyed `_P01_`; need audit-poll test |
| 4 | co_occurrence N01 | same rule (both names still required) | DISKFULL only, firing | distinct=1<2 → enriched only, no parent (correct) | history, audit, synthetic | anchor: enriched rows; pre-sleep | **UNPROVEN** (false-PASS-capable window) | demonstrated false-PASS test; need post-window re-query test |
| 5 | sequence P01 | `sequence=[LOGINFAILURE,PRIVILEGEDCOMMAND]`, 30s | LOGIN@0s, PRIV@1s | tail match → parent_emitted | history×2, audit, synthetic | n/a | **UNPROVEN** (audit race) | coordinator test only |
| 6 | sequence N01 | same rule | PRIV@0s, LOGIN@1s | tail≠expected → advancing only, no parent (correct) | as above | anchor: advancing rows; pre-sleep | **UNPROVEN** (window unsound) | compiler test proves order; no oracle test |
| 7 | persistence P01 | `alertname=…SERVICEDOWN…`, `unresolved_for_seconds=30`, 30s | 1× firing @0s | timer @+30s → parent_emitted ~+30-32s | history, audit (@~+2s!), synthetic (≤35s) | n/a | **FAIL** (deterministic false FAIL: parent_emitted written ≥28s after the only audit read) | `TestTimerStimulusScheduleIsDurableBeforeWaiting` uses fake clock+instant audit — masks it |
| 8 | persistence N01 | same rule | firing @0s, resolved @+10s (same fp) | resolve cancels timer → no parent (correct) | history (→resolved), audit, synthetic | anchor: resolve-cancel rows; pre-sleep | **UNPROVEN** (blind to the exact regression it exists for) | no late-parent test |
| 9 | absence P01 | `expected_match=…HEARTBEAT…`, `expected_every=10`, `absent_for=30` | 1× heartbeat firing @0s | fire at ~+40s (> 35s window) | history, audit (@~+2s), synthetic (≤35s) | n/a | **FAIL** (window too short + audit race; HIGH-7) | coordinator test only |
| 10 | absence N01 | same rule; recurring timer | heartbeats @0s,@+15s then silence | genuine absence ~+45s → parent emitted, recurring | history, audit, synthetic | anchor present; parent lands inside blind sleep or after N01 read | **FAIL** (invalid control; firing-parent residue; HIGH-7) | none drives timers vs the control gap |
| 11 | parent_child P01 | `parent_match/child_matches`, `suppress_children_for_notifiers=["email"]`, no emit_spec | PARENT@0s, CHILD@+1s, same site | parent SET; child → `suppressed_per_notifier` audit + notifier `status='suppressed'` IF `email` notifier exists | history×2, audit per fp, notification-audit | n/a | **UNPROVEN** (audit race + undocumented live `email`; MED-26) | coordinator test supplies the status via the fake |
| 12 | parent_child N01 | same rule | CHILD only, firing | no parent → `released_no_trigger` (correct) | history, audit, notification-audit | anchor: `released_no_trigger` (matches OSCAR); pre-sleep | **UNPROVEN** (anchor race; window unsound) | coordinator test (canned) |
| 13 | threshold P01 | `alertname=…CPUHIGH…`, `distinct_label=device`, `min_distinct_count=3` | 3× same name, device=d1/d2/d3, @0/1/2s | PFCOUNT→3 → parent. BUT 3 fingerprints → 3 history rows for one name | history by name → **3 rows** → `len!=1` | n/a | **FAIL** (structural INCONCLUSIVE; HIGH-6) | no multi-row history fake |
| 14 | threshold N01 | same rule | 2× device=d1 (one fp), @0/1s | PFCOUNT=1<3 → advancing (correct) | history (1 row), audit, synthetic | anchor: advancing; pre-sleep | **UNPROVEN** (window unsound) | compiler test only |
| 15 | cross_source P01 | `required_sources=[{snmp,IFDOWN},{api,IFDOWN}]` | 2× same name, `oscar_source`=snmp then api, @0/1s | HLEN→2 → parent. BUT 2 fingerprints → 2 history rows for one name | history → **2 rows** → INCONCLUSIVE | n/a | **FAIL** (structural; HIGH-6) | no multi-row history fake |
| 16 | cross_source N01 | same rule | 2× `oscar_source=snmp` (one fp), @0/1s | HLEN=1 → advancing (correct) | history (1 row), audit, synthetic | anchor: advancing; pre-sleep | **UNPROVEN** (window unsound) | compiler test only |

Row tally: 16/16 present. **PASS 0 · FAIL 7** (flood P01/N01, persistence P01, absence P01/N01, threshold P01, cross_source P01) **· UNPROVEN 9.** Rule-constancy between P01/N01 is genuinely good for all eight patterns; the failures are stimulus-physics and oracle failures (except absence N01, whose control does not sustain its own invariant).

---

## 8. OSCAR public API / schema compatibility matrix

Harness base path = target URL + `/api/v1/...` (`client.go:376-378`). External truth: traefik `/ext/mw` → middleware routers.

| # | Route (harness) | Harness usage | Current OSCAR | Verdict |
|---|---|---|---|---|
| 1 | POST `/correlation_rules/validate` | full rule payload; parse `{valid,errors[{field,message}]}` | middleware→correlator `/rules/validate`, `extra="forbid"` schema | **MATCH** (all payload keys declared) |
| 2 | POST `/correlation_rules` | create; parse `{id,name,pattern,description}` | correlator create 201 `RuleResponse` | **MATCH** |
| 3 | GET `/correlation_rules/{id}` | fetch by int id | correlator `/rules/{id}`, 404 if absent | **MATCH** |
| 4 | GET `/correlation_rules?page&perPage=100&search` | list; client-side exact-name filter | perPage≤100; LIKE substring; `{rows,total,page,perPage}` | **MATCH** (exact-name post-filter present) |
| 5 | DELETE `/correlation_rules/{id}` | 404 = already-deleted | correlator delete 200 `{deleted:id}`/404 | **MATCH** (404-idempotent) |
| 6 | POST `/alerts` | Alertmanager envelope | `mw_receive_alert(AlertGroup)`→AM `insert_alert`; `fingerPrint`/`groupKey` required | **MATCH (envelope) / PARTIAL (response classes, MED-9)** |
| 7 | GET `/alerts/history?...filter=alertname equals` | parse `{records}`, labels `[{Label,Value}]` | AM `list_alert_history`, perPage≤100, filter DSL; fingerprint episode store | **MATCH (query/labels) / MISMATCH (annotations key, MED-10) / semantic caveat (HIGH-6, BLOCKER-2)** |
| 8 | GET `/correlation_rules/audit?fingerprint` | parse `{rows}` audit fields + outcome | correlator `/audit` newest-first, perPage≤100; outcome enum | **MATCH** (harness outcome vocab ⊆ OSCAR) |
| 9 | GET `/notification-audit/?alert_fingerprint` | parse `{items}` | notifier list; `{items,total,page,per_page}` | **MATCH** (`labels` never present, LOW) |
| — | Auth header (all routes) | `Authorization: Bearer` only | `X-API-Key` | **MISMATCH (BLOCKER-1)** |
| — | Base path | docs example without `/ext/mw` | `/ext/mw` mandatory | **MISMATCH (docs, MED-11)** |

**match_criteria (all 8 patterns):** field-exact against correlator `extra="forbid"` schemas — flood, co_occurrence, sequence, cross_source, threshold, persistence, absence, parent_child all validate; guardrail caps respected; `emit_spec` keys consumed correctly. **Fingerprint discipline:** correct end-to-end — the 16-hex transport `fingerPrint` is deliberately never an identity; history `fingerprint` = NATS `fingerprint` = audit `alert_fingerprint` = canonical labels-only `oscar_fingerprint`; pre-stamped fingerprint labels are refused. The compatibility failures are auth shape, injection-response classes, the annotations key, and the semantic timer/identity mismatches (BLOCKER-2, HIGH-6/7).

---

## 9. Mutation ownership, cleanup, cancellation, and restart audit

**State machine (`domain/run.go`):** QUEUED→PREFLIGHT→SETTING_UP→INJECTING→OBSERVING→ASSERTING→CLEANING_UP→COMPLETED, any pre-terminal→CANCELLING→CLEANING_UP, any active→INTERRUPTED (restart only), plus a dead RECOVERING branch. Verdict/cleanup are separate columns written by `CompleteRun` only after the COMPLETED transition (the non-atomicity behind HIGH-5).

**Acquisition (strong).** Proposal-first: a ledger row (PROPOSED, ownership token = run ID, globally-unique active external name enforced at insert) is committed before `ValidateRule`/`CreateRule`. A successful create is round-tripped on name+description; a failed create attempts exactly one reconciliation via `FindRules` requiring a single candidate matching name+pattern+description (description embeds `run=<runID>`), else the resource is marked UNKNOWN and the run fails dirty. No blind retry, no upsert, no name-only adoption. In-run cleanup deletes by exact returned ID, is 404-idempotent, refuses unknown identities, reports DIRTY on any failure; CLEAN only when every resource reached DELETED. **Invariant 10 PASS.**

**Cancellation (good, one caveat).** Every injection sleep, poll, and HTTP call takes the run context; on cancellation the runner detaches (`context.WithoutCancel`) with a 30s cleanup budget for the terminal writes and OSCAR deletions. Verified by code walk of every transition point and by existing detached-cleanup tests. **Invariant 12 PASS**, caveated by MED-7 (a single failed transition write skips cleanup and strands the run) and the empty-plan/preflight finishes using the cancellable context.

**Recovery (two holes).** Startup marks every non-terminal run INTERRUPTED (cleanup UNKNOWN if resources exist) and never resumes injection. `cleanup retry` (CLI only) re-reads ownership before each delete and refuses lookalikes. The holes: **HIGH-4** (UNKNOWN-state rows are permanently unadoptable → permanent rule leak) and **HIGH-5** (a crash-window COMPLETED/verdict-less run is invisible to recovery and retry while remaining deletable), plus **MED-6** (absence concluded from a truncated page). **Invariants 11 and 13 FAIL.**

**Shutdown/serialization.** `runMu` serializes runs; `Close` cancels active runs, waits on `runWG`, then closes SQLite — no use-after-close or deadlock. Per-run alertname/short-token scoping isolates concurrent runs' rules and oracle queries. Serialization is process-local only (LOW). **Invariant 26 PASS** (caveat).

---

## 10. SQLite, artifacts, reports, export, deletion, and retention audit

**Table → writer map (production):**

| Table | INSERT | Terminal UPDATE | Note |
|---|---|---|---|
| `targets` | CreateTarget | — | reference-only credentials |
| `scenarios` | CreateScenario (import) | — | dedup by digest; unexecutable (MED-21) |
| `runs` | CreateRun | TransitionRun, SetRunExecutionDocuments, CompleteRun, SetTerminalRunCleanup, RecoverInterruptedRuns | verdict/cleanup/report land here |
| `run_events` | appendEvent | append-only | monotonic — good |
| `run_cases` | SetRunExecutionDocuments (`PLANNED`) | **NONE** | HIGH-2 |
| `assertions` | SetRunExecutionDocuments (expected only) | **NONE** | HIGH-2 |
| `alert_attempts` | SetRunExecutionDocuments (`PLANNED`) | **NONE** | HIGH-2 |
| `resources` | CreateResource | AdoptResource, MarkResourceDeleted, MarkResourceCleanupError | lifecycle maintained (HIGH-4 adoption gap) |
| `artifacts` | CreateArtifactPending (**never called**) | MarkArtifactAvailable (**never called**) | HIGH-3 (always empty) |

**Durability (invariant 24) — genuinely strong.** `busy_timeout(5000)`, `foreign_keys(1)`, `journal_mode(WAL)`, `synchronous(FULL)`, `_txlock=immediate`, pool ≤4; per-connection pragma re-verification; min-version enforcement; `PRAGMA quick_check` degrades to read-only rather than crashing; digest-pinned migrations with history-mismatch/newer-than-binary detection and per-migration rollback; context-cancellable online backup via the driver `Backup` API with no-overwrite install + integrity/parity verification; FK-ordered deletion; `0600`/`0700` modes — all non-tautologically tested.

**Artifacts / store — robust but unused.** `Store.Write` is traversal-safe (rejects `..`, backslash, absolute, non-canonical), temp+`os.Link` no-overwrite, hash-on-write; `StageRunDeletion` verifies every manifest and rejects symlinks/untracked files. Correct — but nothing writes artifacts on the run path (HIGH-3).

**Export/bundle (invariant 16).** Deterministic (sorted names, clamped mtimes, fixed modes), non-overwriting, self-describing, with size/entry-count/duplicate/basename checks. **Not tamper-evident** against a re-zip (MED-4).

**Deletion (invariant 17) — sound.** `DeleteRun` refuses non-terminal/not-cleanup-safe/pending-artifact runs, re-verifies every manifest, two-phase stage with rollback, and re-checks status+cleanup inside the transaction. **Retention** is not >500-transparent and dilutes eligibility by filtering after the cap (MED-5).

---

## 11. Web security, authentication, CSRF, SSE, and accessibility audit

**Route/auth coverage (remote mode).** `authHandler` wraps the entire base mux, so in bearer/trusted-proxy mode every route flows through `authorized()` — healthz, readyz, static, dashboard, targets, run-test, scenarios, runs (list/detail/cancel/delete/export/SSE), settings. Only `/login` and `/logout` are intentionally public in bearer mode. **No static/health/SSE/download/mutation gap** — invariant 21's coverage half PASSes.

**Confirmed strong controls.** Loopback is the only unauthenticated bind and is enforced; bearer requires TLS + secure cookies; trusted-proxy checks the real TCP peer plus a canonical header + exact value + source CIDRs (not a spoofable forwarded header); constant-time comparisons throughout; per-request nonce CSP with `frame-ancestors 'none'`, `base-uri 'none'`, `form-action 'self'`; `nosniff`/`no-referrer`/`no-store` on HTML; downloads use `Content-Disposition: attachment` + nosniff; every mutation wraps `MaxBytesReader` + `sameOrigin` + double-submit HMAC CSRF; `html/template` auto-escaping with **no** raw `template.HTML/JS/URL` sinks; SSE DOM updates via `textContent` only; credentials are references and the bearer token is process-memory only.

**Gaps.** MED-1 (DNS-rebinding on the loopback default — no Host allowlist; defensible as HIGH), MED-2 (sessions never expire server-side; logout cannot revoke), MED-3/invariant-36 (missing operator UI actions), plus LOW-WEB-3/4/5.

**SSE (invariant 23) PASS.** Monotonic `id:`/`after` replay, disconnect-independent, bounded per-tick, authenticated in remote mode, terminal-aware; a browser disconnect ends only the stream (`r.Context().Done()`) and never calls `CancelRun` — a closed tab cannot own cancellation.

**Accessibility / theme (invariant 35) PASS.** A nonce'd head script selects theme from `localStorage`+`prefers-color-scheme` before stylesheets/paint; `[data-theme]` tokens + `color-scheme`; `prefers-reduced-motion` honored; pages are server-rendered forms usable without JS; the theme toggle carries `aria-pressed`/`aria-label`. OTTO's admin UI is readable and follows the same before-paint pattern; corrtest is consistent with that inspiration.

**Plan-7 CLI → UI coverage:** target mgmt ✅, doctor/preflight ❌, built-in preview/run ✅, custom preview ✅, **custom run ❌**, live progress ✅, cancel ✅, **cleanup retry ❌**, export ✅, verify ⚠️ partial, delete ✅, **retention ❌**, filters ✅, history ✅, backup ⚠️.

---

## 12. Test-effectiveness and surviving-mutation ledger

25 mutations executed for real in a throwaway clone at the frozen commit (edit → `go build` → `go test` → revert). **10 KILLED, 14 SURVIVED, 1 build-broken (excluded)** — a 42% kill rate on load-bearing logic.

| ID | Mutation | Result |
|----|----------|--------|
| M01 | validate route → `/validatex` | KILLED |
| M02 | history route → `/alertsx/history` | **SURVIVED** |
| M03 | history filter `alertname`→`alert_name` | **SURVIVED** |
| M04 | reserved label `oscar_test_run_id`→`oscar_test_runid` | KILLED |
| M05 | audit param `fingerprint`→`fp` | KILLED |
| M06 | pipeline-mode gate inverted | KILLED |
| M07 | audit literal `parent_emitted`→`parent_emittedX` | KILLED |
| M08 | negative source anchor `len(AuditOutcomes)>0` removed | **SURVIVED** |
| M09 | reconcile description-ownership conjunct removed | **SURVIVED** |
| M10 | auth bypass for GET | KILLED |
| M11b | bundle SHA-256 compare always-pass | **SURVIVED** |
| M12 | persistence `unresolved_for_seconds` 30→3 | **SURVIVED** |
| M13 | delete idempotency 404→418 | **SURVIVED** |
| M14 | flood `min_count` 5→4 | **SURVIVED** |
| M15 | accepted-status literal `accepted`→`acceptedz` | KILLED |
| M16 | flood positive Repeat 5→2 | KILLED |
| M17 | `sameOrigin` forced true | KILLED |
| M18 | positive parent cardinality `==1`→`>=1` | **SURVIVED** |
| M19 | stabilization recheck `==1`→`>=1` | **SURVIVED** |
| M20 | wrong-run source-label guard removed | **SURVIVED** |
| M21 | undeclared-files check removed | **SURVIVED** |
| M22 | assertion-kind whitelist removed | **SURVIVED** |
| M23 | polarity/code consistency removed | **SURVIVED** |
| M24 | 5-minute maxDuration ceiling removed | **SURVIVED** |

Kills concentrate where tests assert recorded request shapes and explicit negative behaviors (rule-lifecycle routes, audit query param, auth middleware, CSRF, pipeline gate, reserved-label emission, injection classification, flood stimulus count). Survivals concentrate exactly at the harness's purpose: the history read contract, the oracle's hostile-evidence guards, ownership reconciliation detail, bundle integrity, and decode semantics. Additional tautology observations: the runner's `fakeAPI` keys outcomes off `_P01_`/`PARENTCHILD` substrings; `testoscar` is a scripted replay queue that validates nothing; the tamper test never tampers; no zero-length fixtures (web tests are genuinely adversarial and killed both auth mutations).

---

## 13. Build, dependency, packaging, GitHub, and GitLab command audit

**Local Make contract — strong.** `make clean && make ci release-gate` exit 0 (verified twice, in the coordinator's and BUILD's clones), including gosec v2.28.0 + govulncheck v1.6.0. `ci` = tools→ci-core(fmt/mod/vet/security/plan2-gate/test/test-race/build)→standalone-check→package→checksums; `release-gate` adds plan3–7 + container/package-content/reproducible checks. CGO-free runtime (`modernc.org/sqlite`; libc pin matches the driver's `go.mod`); `test-race` sets `CGO_ENABLED=1` as required. Packaging refuses non-GNU tar, stages under a validated mktemp path with pattern-checked destructive cleanup, uses `--format=gnu --sort=name --numeric-owner --owner=0 --group=0 --mtime=@SOURCE_DATE_EPOCH | gzip -n`; archives are **byte-identical across three rebuilds** at the frozen commit; SHA256SUMS is version-scoped and `LC_ALL=C` sorted; `check-package.sh` enforces all seven members per arch; both packages are `file(1)`-confirmed statically linked + stripped. CA strategy coherent (scratch bundles `ca-certificates.crt`; host binary uses system roots; `--ca-file` for private CAs). No gate contacts live OSCAR (smoke target `https://oscar.invalid`, never dialed).

**Release wiring — weaker than the contract.** MED-15 (no CI runs `release-gate`), MED-16 (plan5-gate empty selection; no empty-selection guard), MED-17 (stale-archive leak via broad globs + no `make clean` in CI), MED-18 (Containerfile broken as shipped). Pins: container image digests verified pullable; GitHub action SHAs are pinned and self-enforced but their authenticity is **UNPROVEN** from the sandbox (github.com unreachable); glab/`CI_JOB_TOKEN` release rights **UNPROVEN** (documented operator gate). GitHub release uses `contents:write` least privilege, `fetch-depth:0`, semver re-check, `--verify-tag`; GitLab restricts release to `^v\d+\.\d+\.\d+$`. Both systems call only Make targets.

**Invariant results (this section):** 1 PASS · 30 FAIL · 31 PASS · 32 FAIL · 33 FAIL · 34 FAIL · 37 PASS · 39 PASS · 40 FAIL — all bounded to release quality; none enables a false PASS or live-OSCAR mutation.

---

## 14. Design / plan / implementation / docs traceability gaps

Counts: **PLAN-ONLY 6, DIVERGED 9, CODE-ONLY 3** (benign additions). Plans 1–2 and the earlier plan-review remediations (loopback guard, standalone scanner, archive-mod-check, GNU-tar reproducibility, create-only rule lifecycle, fingerprint read-back) are faithfully implemented. The gaps concentrate in Plans 3–7's evidence, oracle, UI, and qualification commitments.

- **Never built (PLAN-ONLY):** `fixtures/public-v1/*.json` recorded route fixtures (design §21.2 / Plan 3); `internal/oscar/redact.go` adapter redaction (§12.4 / Plan 3); `OSCAR_CORRTEST_LIVE_TARGET` live timing qualification (Plan 5 / Plan 7); long-run deadline/remaining UX; remote-auth rate limiting (Plan 7). **These two — recorded fixtures and live timing qualification — were the explicit closure conditions under which the prior review's HIGH-1/HIGH-2 deferrals were accepted (HIGH-TRACE-4).** The deferral happened; the gates did not.
- **Silently redesigned (DIVERGED):** startup auto-cleanup + RECOVERING state dropped (§8); `report.Build`/artifact pipeline dead (§12/§15); declared-assertion oracle hard-coded (§12); normalized tables never updated (§14.4); exit-code contract (§9.3); Settings page shows readiness only (§16.4); custom rule criteria hard-coded (§10.2). All corroborated in §§3–5.
- **Claimed-remediation verification:** every remediation the prior docs claim was applied was verified present in the frozen code (loopback guard, scanner, archive-mod-check, GNU-tar reproducibility, create-only rules, fingerprint read-back) — **except** the two deferred-gate items above, which are absent.
- **Docs vs executable:** README/operator/development/builtins are largely accurate against the built binary's help; overclaims are README "incomplete negative windows … cannot produce PASS" (the negative window is a stale pre-window snapshot — BLOCKER-3), README "preserves run reports in SQLite" (evidence is interpreted then discarded except counts/outcomes — HIGH-2/3), design §9.3 exit codes, and design §6/§8/§10/§12/§14.6/§16.4/§17 surfaces that do not exist.

---

## 15. Required changes before controlled live qualification

Blocking (must fix and add the closing regression):

1. **Auth (BLOCKER-1):** send `X-API-Key`; document `/ext/mw` base path; doctor surfaces auth-shape 401s.
2. **Flood stimulus (BLOCKER-2):** distinct per-event fingerprint label + assert distinct count; respect/classify the per-fingerprint limiter.
3. **Oracle time model (BLOCKER-3):** re-query history AND audit after the negative window; poll audit with deadline discipline; derive per-case deadlines from compiled timer durations.
4. **Declared assertions (HIGH-1):** evaluate `CasePlan.Assertions`, or reject divergent custom assertions at compile time and document them as non-executable everywhere.
5. **Evidence materialization (HIGH-2/HIGH-3):** update `run_cases`/`assertions`/`alert_attempts` to terminal facts in the completion transaction; persist redacted raw OSCAR request/response as hashed artifacts and build the report through `report.Build` from durable facts (or explicitly, documentedly de-scope raw retention for v1).
6. **Recovery holes (HIGH-4/HIGH-5):** allow re-adoption/deletion of `UNKNOWN` resources on retry; make the terminal write atomic and treat `COMPLETED + NULL verdict` as cleanup-unknown everywhere.
7. **Pattern semantics (HIGH-6/HIGH-7):** fix the one-row-per-alertname gate to per-fingerprint cardinality; size the observation window to each pattern's timer; sustain the absence N01 heartbeat across the full window.
8. **Alert residue (HIGH-8):** resolve every injected alert and emitted parent at cleanup, or track them as owned resources; gate/document any residue.
9. **Fake independence (HIGH-9):** add a stateful fake modeling per-rule windows/counts/timers/fingerprints and run all eight compiled timelines through it end-to-end.

Recommended before qualification: MED-1, MED-2, MED-3, MED-6, MED-7, MED-12, MED-19, MED-20, MED-26. Recommended before release: MED-4, MED-5, MED-14, MED-15, MED-16, MED-17, MED-18, and the recorded-fixtures/live-qualification gates (HIGH-TRACE-4).

---

## 16. Residual live-system and external-account qualification

Facts that require a live OSCAR or external account and are correctly classified UNPROVEN (do not infer PASS):

- The API key's RBAC grants for correlation-rule/alert/history/notification-audit routes (CONTRACT-11).
- Whether an enabled notifier literally named `email` exists and processes the child, for parent-child suppression evidence (MED-26).
- `phase_a_audit_only` / `phase_b_dispatch` have no counterpart in OSCAR source (repo-wide grep: zero hits) — they are undetectable operator declarations, not probeable facts.
- RFC3339Nano >6-digit fractional seconds vs Pydantic datetime query parsing — one contract probe needed (CONTRACT-10).
- GitHub action-pin SHA authenticity (BUILD-7) and glab/`CI_JOB_TOKEN` release/package-upload rights (BUILD-8) — verify from a networked host / the target GitLab instance before first release.

Live timing behavior (persistence 30s grace, absence fire-time, pipeline lag) cannot be qualified offline and is exactly what the missing `OSCAR_CORRTEST_LIVE_TARGET` gate was supposed to cover.

---

## 17. Final recommendation

**BLOCK LIVE QUALIFICATION.** Do not run this harness against an operator-owned
OSCAR until at least the nine blocking items in §15 are fixed with their closing
regressions. The strongest hostile mutations were not defeated: two independent OSCAR
kill-paths make the flagship flood pattern non-functional, the oracle produced a
demonstrated false PASS and a demonstrated systematic false FAIL in a throwaway clone,
the documented deployment cannot authenticate, declared assertions are inert, evidence
is never materialized, and two recovery holes can permanently leak an OSCAR rule while
the harness reports the run clean. Fourteen of twenty-five mutation probes survived a
fully green suite, concentrated precisely at the correlation oracle, the evidence chain,
and ownership reconciliation — so the shipped tests cannot detect the class of failure
the harness exists to catch.

This is not a verdict on engineering care: the acquisition-side ownership model, SQLite
durability, remote auth coverage, template/CSRF/CSP hardening, and reproducible packaging
are genuinely well built and would pass on their own. But a correlation **test oracle**
is judged by whether its PASS means what it says, and today — against current OSCAR
source — it does not. Re-review after the §15 changes, with the added contract fixtures
and an isolated, cleanup-gated, non-PASS-by-default live qualification path, before
reconsidering for controlled live qualification and a v1 release.
