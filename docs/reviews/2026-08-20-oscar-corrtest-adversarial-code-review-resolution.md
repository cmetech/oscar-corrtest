# Adversarial code review resolution

Date: 2026-08-20  
Frozen implementation reviewed: `e8ab6d0460d14e67cae2f889665499daa70f6011`  
Review prompt commit: `6f04cf88c620e03f151e458fafe2645bb2a1c5ad`

## Inputs and review boundary

This resolution combines two independent reviews:

1. The complete review stored at
   `docs/reviews/2026-08-19-oscar-corrtest-adversarial-code-review.md`.
2. The second reviewer's executive summary supplied in the review discussion.
   No second complete report exists at a distinct path in this repository, so
   this resolution does not invent findings or evidence beyond that supplied
   summary.

Every accepted load-bearing finding below was checked against both the harness
and the current OSCAR source under the sibling `../oscar` tree. The result is a
qualification verdict, not a general statement that the repository is unsafe
to continue developing.

> **Resolution verdict: BLOCK CONTROLLED LIVE QUALIFICATION AND v1 RELEASE.**
>
> Local development may continue. A run must not be represented as trusted
> live-OSCAR qualification until the trust-core gates in the remediation plan
> pass. This reconciles the reviewers' different headlines: both reviews say
> the suite is not yet trustworthy against live OSCAR; the stronger verdict is
> appropriate because live qualification is the tool's primary purpose.

## Finding adjudication

| Finding | Disposition | Independent confirmation | Required action |
|---|---|---|---|
| BLOCKER-1: Bearer header instead of OSCAR API key | **CONFIRMED** | `internal/oscar/client.go` sends only `Authorization: Bearer`; OSCAR external middleware reads `X-API-Key` and returns 401 when absent. OSCAR's own `send_custom_alert.py` uses `X-API-Key`. | Send `X-API-Key`, add a rejecting contract fixture, classify the auth-shape error, and correct `/ext/mw` examples. |
| BLOCKER-2: flood repeats share one identity | **CONFIRMED** | Event identity is annotation-only. OSCAR hashes labels, the flood Lua stores `member=fingerprint`, `ZCARD` counts distinct members, and the alertmanager limiter defaults to three requests per transport fingerprint. | Add stable per-event identity labels, preserve identity across firing/resolved pairs, assert five distinct compiled flood identities, and classify literal 2xx rate-limit bodies. |
| BLOCKER-3: oracle reads too early and never re-reads negatives | **CONFIRMED** | Audit is queried once immediately after history. A negative parent query is captured before sleeping and never refreshed. OSCAR audit flush is asynchronous and timer decisions occur later. | Use absolute per-case deadlines, poll history/audit to the deadline, and base absence assertions only on a final post-window snapshot. |
| HIGH-1: declared assertions are inert | **CONFIRMED** | `CasePlan.Assertions` is never referenced by runner production code. | Make the assertion list the only verdict contract and reject unsupported assertion kinds at compile time. |
| HIGH-2: normalized rows remain `PLANNED` | **CONFIRMED** | There are inserts but no production terminal updates for `run_cases`, `assertions`, or `alert_attempts`. | Persist injection/fingerprint facts and finalize case/assertion rows atomically with the run verdict. |
| HIGH-3: source evidence and artifact pipeline are unused | **CONFIRMED** | The live runner writes no artifact row/file; raw or normalized history/audit/notification facts are discarded. | Persist a redacted, normalized evidence artifact before terminal PASS/FAIL and include its immutable manifest in the canonical report. |
| HIGH-4: `UNKNOWN` resources cannot be re-adopted | **CONFIRMED** | Retry proves ownership, then calls an adoption update restricted to `PROPOSED`. | Permit exact ownership-proven `UNKNOWN -> CREATED` re-adoption and regression-test delete on retry. |
| HIGH-5: completion is non-atomic | **CONFIRMED** | `COMPLETED` is written before verdict/cleanup/report; recovery skips the crash state and deletion accepts its default cleanup value. | Replace the split writes with one finalization transaction; recover legacy terminal-null rows as interrupted/cleanup-unknown; refuse deletion without a verdict. |
| HIGH-6: one-row-per-alertname is invalid | **CONFIRMED** | Threshold and cross-source intentionally create multiple fingerprints under one alertname; OSCAR can also expose duplicate notifier rows for one fingerprint. | Filter by exact run label, deduplicate by server fingerprint, and compare expected event identities rather than raw row count. |
| HIGH-7: absence timing/control is invalid | **CONFIRMED** | The positive timer is `expected_every + absent_for`; the negative sends too few heartbeats and the fixed 35-second window is shorter than the decision plus evidence lag. | Derive a 55-second bounded observation for current built-ins and sustain the negative heartbeat through it. |
| HIGH-8: alerts remain firing | **CONFIRMED** | Cleanup deletes rules only. | Track authoritative history records and send explicit resolved cleanup events for owned sources and synthetic parents; cleanup failure must remain visible and make cleanup DIRTY. |
| HIGH-9: fake is case-code scripted | **CONFIRMED** | Existing fakes answer from `_P01_`/`_N01_` naming rather than rule semantics or time. | Add stateful model tests for fingerprint cardinality, rule criteria, timer advancement, late audit, and late parents across all built-ins. |
| Report two: `fingerPrint` is always supplied | **NARROWED, NOT A DEFECT BY ITSELF** | OSCAR's current `AlertGroup` schema requires the Alertmanager `fingerPrint` field. Removing it would make injection invalid. | Keep the required field, derive it from stable event labels, never use it as oracle identity, and test the real 2xx drop/queue bodies. |
| Report two: CI publishes without `release-gate` | **CONFIRMED** | GitHub workflows run `make ci`; GitLab splits `ci-core` and packaging but never runs plan 3-7/container/reproducibility gates. | Make all publish-capable paths depend on `release-gate`; remove empty timer-test selection and stale-output ambiguity. |
| Report two: no prefix-delete/operator-rule blocker | **CONFIRMED BUT INCOMPLETE** | Cleanup is exact-ID and ownership verified; it does not prefix-delete. The `UNKNOWN` adoption and terminal crash gaps still leak harness-owned temporary rules. | Preserve exact-ID policy while closing HIGH-4/HIGH-5. |
| Report two: non-loopback still requires remote mode | **CONFIRMED** | Bind validation is correct. | Preserve; separately add a loopback Host allowlist to close DNS-rebinding exposure. |

## Medium finding triage

The following are accepted into this remediation because they directly support
the trust boundary or first release:

- OSCAR history annotation decoding must use `Annotation`, not `Label`.
- Rule, history, audit, and notification reads must follow bounded pagination;
  zero matches on a truncated first page cannot prove absence.
- Literal OSCAR 2xx bodies for API rate limiting, alert fingerprint limiting,
  ACL filtering, queueing, and partial acceptance need contract fixtures.
- Target examples must use the external `/ext/mw` prefix.
- `Plan.MaxDuration` must bound execution and cancellation must still receive a
  detached cleanup budget.
- Transition persistence failure must not suppress a best-effort cleanup of
  already-proven owned external IDs.
- Loopback unauthenticated serving must reject non-loopback `Host` values.
- Bearer UI sessions need an expiry and logout-side revocation.
- The timer gate must fail when it selects no tests.
- GitHub/GitLab release paths must execute the release gate from a clean output
  directory.
- The container must have a writable state directory and documented reachable,
  authenticated invocation rather than an unreachable loopback-only default.
- Recorded public-v1 fixtures and an opt-in isolated live qualification command
  remain mandatory closure gates; offline CI must never infer live PASS.

The following are real but do not block the trust-core repair and are kept as
explicit follow-on work rather than silently claimed complete:

- UI parity for every CLI maintenance operation (doctor, imported custom-run,
  cleanup retry, retention, backup, and bundle verification).
- Retention pagination beyond the current 500-row history cap.
- External authenticity for a re-zipped bundle. Internal SHA-256 verification
  detects damage relative to its manifest, but an external signature or digest
  is required to prove publisher identity.
- Automatic discovery of OSCAR dispatch mode. Current OSCAR does not expose it;
  Phase-B remains an operator declaration and live qualification must say so.
- Remote login rate limiting, imported-scenario execution by stored ID, and
  custom scenario rule-criteria generalization.
- Parent-child notifier-name discovery. The built-in `email` assumption remains
  a live prerequisite until OSCAR exposes a suitable capability surface.

## Non-negotiable closing invariants

1. A missing/late audit row cannot cause false FAIL before its bounded deadline.
2. A late synthetic parent inside a negative decision window cannot cause PASS.
3. PASS is computed from explicit persisted assertions, not pattern names or
   positive/negative case codes.
4. Every source occurrence expected by a case has a unique stable label identity;
   a resolved attempt reuses the firing identity it resolves.
5. OSCAR's server-assigned fingerprint is the only history/audit identity used
   by the oracle.
6. PASS/FAIL has a durable normalized evidence artifact and terminal normalized
   database facts sufficient to recompute the assertion counts.
7. Run finalization is atomic across lifecycle, verdict, cleanup, report, cases,
   and assertions; deletion rejects verdict-less rows.
8. Cleanup deletes only exact owned rule IDs and attempts to resolve only alerts
   proven to carry the exact run ID.
9. Offline fakes cannot manufacture PASS from `P01`, `N01`, alertname substrings,
   or the assertion value they are expected to satisfy.
10. No CI, package, or release path claims live OSCAR qualification. The optional
    live lane is isolated, opt-in, cleanup-gated, and non-PASS by default.

