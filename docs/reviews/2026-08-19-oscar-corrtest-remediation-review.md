# Focused Remediation Review — OSCAR Correlation Test Harness

**Review date:** 2026-08-19
**Reviewer:** independent focused reviewer (did not author the remediation or the original adversarial review)
**Verdict scope:** may Plan 1 implementation begin from the remediated design and plan?

---

## 1. Scope, HEAD, worktree state, and digest verification

**Repository under review:**

```text
/Users/coreyellis/code/github.com/cmetech/oscar_app/oscar-corrtest
```

**HEAD:** `34f2cb911f97d1bf295b76855f1490d0f29c5f24` (`docs: remediate adversarial harness review`)

**Full commit history:**

```text
34f2cb9 docs: remediate adversarial harness review
e920bf3 docs: preserve adversarial plan review
a68502e docs: harden adversarial review contract
74adce8 docs: add adversarial harness plan review prompt
```

**Worktree state:** clean — `git status --porcelain` returns empty. No implementation source, `go.mod`, `Makefile`, CI workflow, or packaging file exists. The repository contains only:

```text
docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-prompt.md
docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-resolution.md
docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md
docs/reviews/2026-08-19-oscar-corrtest-remediation-review-prompt.md
docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md
docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md
```

This review output will be the only untracked file when complete.

**Digest verification — PASSED:**

```text
77ef05571d0a1223a602d62dce2584492b8bbe5995c2177191dbd07a14071ec0  docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md    ✔ EXACT
70c280c92d4074fe7eb4be7d77902cfc8699dc6f3fb025b3bdedd9274bcd32ea  docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md     ✔ EXACT
```

Both digests match the resolution ledger's stated remediated SHA-256 values. No scope error.

**Artifacts read completely:**

1. `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md` (634 lines — complete)
2. `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-resolution.md` (45 lines — complete)
3. `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md` (1055 lines — complete)
4. `docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md` (778 lines — complete)

**OSCAR source inspected (read-only, for design-correction verification):**

- `oscar/oscar-correlator/src/app/core/consume.py` (dispatch gate, audit buffering)
- `oscar/oscar-correlator/src/app/routers/audit.py` (fingerprint-keyed queries)
- `oscar/oscar-correlator/src/app/routers/rules.py` (create vs import semantics)
- `oscar/oscar-correlator/src/app/main.py` (internal health/ready)
- `oscar/oscar-alertmanager/src/app/routers/alerts.py` (injection response, history)
- `oscar/oscar-alertmanager/src/app/core/fingerprint.py` (algorithm, exclusions)
- `oscar/oscar-alertmanager/src/app/core/db.py` (alertname index)
- `oscar/oscar-middleware/src/app/routers/alerts.py` (label-safe passthrough)
- `oscar/oscar-middleware/src/app/core/route_permissions.py` (correlation surface proxy)

---

## 2. Executive verdict

## **CLEARED FOR PLAN 1**

All four original HIGH findings are closed. All seven MED findings are closed. All ten LOW findings are closed or acceptably deferred. No new BLOCKER or Plan-1 HIGH finding was discovered during this verification. Plan 1 implementation may begin.

---

## 3. Original-finding closure matrix

| Finding | Ledger disposition | Remediation-review verdict | Evidence |
|---|---|---|---|
| **HIGH-1** dispatch mode invisible | Resolved in design; implementation deferred with gate | **CLOSED WITH ACCEPTED DEFERRAL** | Design §13.2 item 1 (line 526) now names both `CORRELATOR_NATS_PUBLISH_ENABLED` and `CORRELATOR_DISPATCH_ENABLED`. Lines 539–542 define four explicit pipeline states (`publication_disabled`, `phase_a_audit_only`, `phase_b_dispatch`, `unknown`) with assertion-surface consequences; Phase-A and unknown states cannot produce `PASS` on synthetic-parent or notifier assertions. §21.2 (line 891) requires Phase-A false-pass contract tests. Plan 3 owns the gate (line 1012). Verified against `consume.py`: dispatch gate is real, audit rows are unconditional, synthetic emission is gated. |
| **HIGH-2** audit requires server fingerprint | Resolved in design; implementation deferred with gate | **CLOSED WITH ACCEPTED DEFERRAL** | Design §12.1 (line 468) now explicitly specifies: "the harness resolves each server-assigned alert fingerprint by polling alert history with the exact run-unique physical `alertname` and bounded creation-time range, then reads the fingerprint from that history row before querying correlation audit. Client-side fingerprint calculation is permitted only as a diagnostic cross-check; it is never an assertion key." §13.2 item 6 (line 531) names injection response fingerprint return as an improvement; compat-mode bridge specified. §21.2 (line 890) requires mutation test proving wrong client computation cannot affect verdict. Plan 3 owns the gate. Verified: `alerts.py:848` returns only Celery task id; `audit.py:63-67` requires fingerprint; `alertname` is indexed in `db.py`. |
| **HIGH-3** repository already exists | Resolved in Plan 1 | **CLOSED** | Plan Task 1 Step 1 (lines 93–111) now verifies the existing repository: checks top-level path, branch `main`, ancestry from `74adce8`, clean worktree, and absence of implementation files. No `mkdir` or `git init` appears anywhere in the plan. Planned structure (lines 38–73) includes all four `docs/reviews/` files. Verified: actual repo state matches exactly. |
| **HIGH-4** archive calls Git-dependent module gate | Resolved in Plan 1 | **CLOSED** | Plan Task 7 (lines 660–738) introduces `archive-mod-check` target (lines 682–684) using `go mod verify` plus `go mod tidy -diff` — no Git dependency. The archive lane (line 673) calls `make archive-mod-check test build` instead of `mod-check`. The in-repo `mod-check` retains `git diff --exit-code` for the worktree lane. Resolution note about `exit 1` vs `exit 128` acknowledged (resolution line 14). |
| **MED-1** scanner rejects review/design prose | Resolved in Plan 1 | **CLOSED** | Plan Task 7 Step 1 (lines 664–674) specifies an explicit source/build-file allowlist: `*.go`, `go.mod`, `go.sum`, `Makefile`, `scripts/**`, `.github/workflows/**`, `.gitlab-ci.yml`, `packaging/**`, `internal/web/templates/**`, `internal/web/static/**`. Documentation is explicitly excluded (`docs/**` and `README.md` per line 669 item 3). Scanner patterns are constructed from non-contiguous shell fragments (line 667). Task 7 Step 2 (lines 689–697) proves both controls: forbidden path in docs → PASS; forbidden path in source → FAIL. |
| **MED-2** non-loopback guard unowned | Resolved more strictly | **CLOSED** | Plan Task 2 Step 5 (line 333) implements: parse with `net.SplitHostPort`, require `net.ParseIP`, permit only `IsLoopback()`. Rejects hostnames, wildcard, unspecified, empty-host, and non-loopback addresses. No `--allow-remote-unauthenticated` override — resolution (ledger line 25) explicitly rejected it as contradicting the security contract. Table-driven tests (line 335) cover `127.0.0.1`, `127.0.0.2`, `[::1]` as accepted; `localhost`, `0.0.0.0`, `[::]`, `:8787`, `192.0.2.10` as rejected. Design §17 (line 804) documents the restriction. Plan 7 (design line 1016) owns future remote mode. Stronger than the original finding required. |
| **MED-3** injection route ambiguous | Resolved in design | **CLOSED** | Design §13.1 (line 514) now names `POST /api/v1/alerts` as the supported injection route and explicitly excludes the mapping-enabled `/alerts/webhook` and upstream Alertmanager `/api/v2/alerts`. Line 520 requires a label-survival preflight probe. Verified: `oscar-middleware/src/app/routers/alerts.py` confirms the middleware route is a verbatim passthrough. |
| **MED-4** 2xx drop/limit ambiguity | Resolved in design | **CLOSED** | Design §13.1 (line 520) enumerates accepted-and-scheduled, ACL-filtered, per-fingerprint-rate-limited, circuit-breaker-queued, partially accepted, and unknown responses. Adapter must never supply `fingerprint`/`am_fingerprint` fields. §20 (line 858) names all drop-path responses. §21.2 (line 888) requires injection-response contract fixtures for every body class. |
| **MED-5** readiness not externally exposed | Resolved as OSCAR prerequisite | **CLOSED WITH ACCEPTED DEFERRAL** | Design §13.2 item 9 (line 534) adds externally reachable correlator readiness. §13.3 (line 546) records readiness confidence in the capability snapshot; unverified readiness fails preflight. Verified: correlator `/ready` exists on internal `:5400` only; no middleware proxy in `route_permissions.py`. |
| **MED-6** rule import/upsert and create collision | Resolved in design | **CLOSED** | Design §13.1 (line 511) states temporary rules use "create/read/delete only and never use the name-upserting import route." §19.1 (line 831) specifies unknown-outcome reconciliation by unique-name read-back with full ownership verification; never blindly re-POSTs or deletes a lookalike. §20 (line 857) repeats the reconciliation contract. §24 (line 1026) adds acceptance criterion. Verified: `rules.py` POST /rules is INSERT-only; POST /rules/import upserts by name. |
| **MED-7** archive determinism claim | Resolved in Plan 1/design | **CLOSED** | Plan Task 4 Step 3 (line 488) specifies GNU tar requirement with `gtar` fallback detection, GNU format, sorted members, numeric owner/group zero, `--mtime=@<source-date-epoch>`, and `gzip -n`. Task 4 Step 5 (lines 510–520) includes a package-twice checksum comparison gate. Design §22.2 (line 954) mirrors the specification. |
| **LOW-1** toggle semantics | Resolved | **CLOSED** | Plan Task 3 Step 4 (line 395): stable accessible name `Light theme` with `aria-pressed` indicating state; visible icon/title may describe next action but accessible name does not change. Design §16.2 (line 712) mirrors this. Resolves the `aria-pressed` + action-label contradiction. |
| **LOW-2** content-type equality | Resolved | **CLOSED** | Plan Task 2 Step 1 (line 275): "content type whose media-type prefix matches the table." Prefix matching, not equality. |
| **LOW-3** design-copy/provenance wording | Resolved | **CLOSED** | Plan Task 1 Step 2 (line 136): "Do not copy or rewrite the design: the committed `docs/superpowers/specs/…` is canonical and its adversarial-review provenance remains in `docs/reviews/`." No `byte-for-byte`, no `apply_patch`, no design copy. |
| **LOW-4** `ci` versus `ci-core` | Resolved | **CLOSED** | Design §22.2 (line 941) now names `ci` in the minimum target set. Plan Task 4 (line 478) defines `ci` as a sequential recipe composing `tools`, `ci-core`, `standalone-check`, `package`, and `checksums`. |
| **LOW-5** systemd provisioning/hardening | Partially incorporated | **CLOSED** | Plan Task 7 Step 3 (lines 699–728) includes hardened unit with `ProtectSystem=strict`, `NoNewPrivileges`, etc. Line 728 documents user/group provisioning requirement and future state-directory change. Additional optional sandbox directives remain implementation-time compatibility checks — acceptable for an example unit. |
| **LOW-6** prior testkit relationship | Resolved | **CLOSED** | Design §3 (line 67): "The 2026-05-02 `oscar-testkit` proposal is superseded for correlation testing by this design. Its useful requirements…are retained here. Broader alert-flow/load tracks remain outside this harness, and recurring execution is delegated to GitHub/GitLab schedules or an operator scheduler rather than an in-process cron subsystem." |
| **LOW-7** positive negative-proof anchor | Resolved in design | **CLOSED** | Design §12.1 (line 470): "Negative cases require a positive eligibility anchor" — names `released_no_trigger`, `pass_through`, and pattern-specific cancellation. "Absence of all evidence cannot produce `PASS`." Line 485 documents flush/backpressure/retention caveats. |
| **LOW-8** guardrail config preflight | Resolved in design | **CLOSED** | Design §13.2 item 8 (line 533) includes guardrail limits in the capabilities endpoint. §13.3 (line 546) includes guardrail configuration in the capability snapshot. |
| **LOW-9** CI cache key evolution | Resolved in Plan 1 | **CLOSED** | Plan Task 6 Step 1 (line 603): "cache by `go.mod` while the standard-library-only module has no `go.sum`. Require the first dependency-adding commit to change both GitHub and GitLab cache dependency paths to `go.sum`." |
| **LOW-10** live GitLab release behavior | Deferred with external gate | **CLOSED WITH ACCEPTED DEFERRAL** | Plan Task 6 Step 2 (lines 633): documents first real semantic tag as an operator release gate. Cannot be closed by static review. |

---

## 4. Plan 1 command/commit-order re-audit

| Task/Step | Command/artifact | Remediation verdict |
|---|---|---|
| T1 S1 | Verify existing repo: top-level, branch, ancestry, clean worktree, no implementation files | **OK** — no `mkdir`/`git init`; repo state verified matching |
| T1 S2 | `go.mod` with `go 1.27.0`; `.gitignore`; `.editorconfig`; design not copied | **OK** — canonical design stays in-place |
| T1 S3–S6 | version/command tests → red → implement → `go test ./...` → commit | **OK** — `New(stdout, stderr, info)` matches test; Task 2 extends to `New(stdout, stderr, info, serve)` |
| T2 S1–S6 | HTTP tests with media-type prefix matching; handler; CSP; loopback guard with table-driven rejection tests | **OK** — prefix matching resolves LOW-2; loopback guard resolves MED-2 with test coverage for all rejection classes |
| T3 S1–S5 | Token palette, `Light theme` + `aria-pressed`, reduced-motion, focus-visible | **OK** — resolves LOW-1 |
| T4 S1–S5 | Makefile, `ci` sequential, `SOURCE_DATE_EPOCH`, GNU tar packaging, package-twice gate | **OK** — `ci` sequential recipe resolves LOW-4; GNU tar resolves MED-7 |
| T5 S1–S4 | GitHub CI with SHA-pinned actions, `make ci` only, tag release | **OK** — pin check included |
| T6 S1–S4 | GitLab with digest-pinned images, Make-only parity, `go.mod` cache | **OK** — cache evolution documented |
| T7 S1–S4 | `test-standalone.sh` with file-class allowlist, `archive-mod-check`, scanner fixtures, systemd unit | **OK** — resolves MED-1 and HIGH-4; `standalone-check` inserted into `ci` between `ci-core` and `package`; GitLab verify adds it after `ci-core` |
| T8 S1–S5 | Acceptance gate, architecture check, HTTP/shutdown, CI audit | **OK** — no empty commits; no tag creation |

**Commit ordering:** every commit N leaves `go test ./...` green. CI files (Task 5) reference only Task 4 targets. `standalone-check` enters CI in the same Task 7 commit as its script. GitHub calls `make ci` from Task 5 onward and automatically picks up the Task 7 gate. GitLab verify job is updated in Task 7's commit. No commit references a future file or target.

**Sequential `ci` under `make -j`:** Plan line 478 requires `ci` to be a sequential recipe using `$(MAKE)` sub-invocations, not parallelizable prerequisites. After Task 7: `$(MAKE) tools` → `$(MAKE) ci-core` → `$(MAKE) standalone-check` → `$(MAKE) package` → `$(MAKE) checksums`. Correct.

---

## 5. Design-remediation re-audit against OSCAR source

### 5.1 Pipeline mode: publication-disabled, Phase-A, Phase-B, and unknown states cannot produce vacuous PASS verdicts

**VERIFIED.** Design lines 539–542 define the four states. `publication_disabled` → correlation scenarios "cannot pass." `phase_a_audit_only` → synthetic-parent/notifier assertions are `SKIPPED` or `INCONCLUSIVE`, "including negative absence assertions." `unknown` → "no scenario whose proof depends on pipeline state may report `PASS`." Only `phase_b_dispatch` permits positive assertion.

Confirmed against OSCAR source: `consume.py` unconditionally buffers audit rows but gates `synthetic_emitter.emit()` on `correlator_dispatch_enabled`; a default install has this `false`. The design's Phase-A profile correctly prevents synthetic-parent assertions from passing on such a target.

### 5.2 Server-assigned fingerprints via history read-back

**VERIFIED.** Design §12.1 (line 468) specifies exact-alertname/time-bounded history read-back as the fingerprint acquisition path. Client computation is "diagnostic cross-check" only, "never an assertion key." Zero/multiple candidates → `INCONCLUSIVE` or `ERROR`, never negative proof.

Confirmed against OSCAR source: `alertname` is indexed in `AM_AlertHistory` (`db.py`); history endpoints return labels including `oscar_fingerprint`; the fingerprint is stamped server-side at `alerts.py:790-815` after severity normalization.

### 5.3 Injection route, label survival, and response body parsing

**VERIFIED.** Design §13.1 (line 514) pins middleware `POST /api/v1/alerts` and explicitly excludes webhook and upstream AM routes. Line 520 requires: no `fingerprint`/`am_fingerprint` supplied; full response-body parsing for all drop/queue states; label-survival preflight probe; mutation-budget respect for target rate limiter.

Confirmed against OSCAR source: middleware `POST /api/v1/alerts` is a verbatim `model_dump_json()` passthrough — label-safe. The `/alerts/webhook` route runs the mapping pipeline which can rewrite labels.

### 5.4 Correlator readiness representation

**VERIFIED.** Design §13.2 item 9 (line 534) adds externally reachable readiness as an OSCAR prerequisite. §13.3 (line 546) includes readiness confidence in the capability snapshot. Unverified readiness fails preflight.

Confirmed against OSCAR source: `/health` and `/ready` exist on internal `:5400` only; middleware `route_permissions.py` exposes no correlator readiness proxy.

### 5.5 Temporary rules: no import/upsert; lost/5xx create outcomes reconcile safely

**VERIFIED.** Design §13.1 (line 511): "harness-owned temporary rules use create/read/delete only and never use the name-upserting import route." §19.1 (line 826–831): unknown outcomes reconcile by unique-name read-back with full ownership verification; no blind re-POST or lookalike deletion.

Confirmed against OSCAR source: `POST /rules` is a plain INSERT with `UNIQUE` name constraint; `POST /rules/import` upserts by name — exactly the risk the design now avoids.

### 5.6 Negative proof requires a positive history/audit eligibility anchor

**VERIFIED.** Design §12.1 (line 470): "Negative cases require a positive eligibility anchor. The harness must first observe the injected child in alert history and, where the target exposes it, a non-triggering correlation-audit outcome such as `released_no_trigger`, `pass_through`, or the pattern-specific cancellation outcome. Only then may the absence of a forbidden synthetic parent or dispatch result be evaluated…Absence of all evidence cannot produce `PASS`."

Confirmed against OSCAR source: non-triggering alerts produce audit rows with outcomes like `released_no_trigger` and `pass_through`; persistence cancellation writes a dedicated `persistence_resolved_cancelled` reason.

### 5.7 Every deferred obligation has a named Plan 2–7 owner and mandatory gate

**VERIFIED.** Design §23.1 (lines 1004–1016) maps all seven plans to product slices with explicit mandatory gates:

| Plan | Gate |
|---|---|
| Plan 1 | Standalone archive, loopback-only, reproducible-package |
| Plan 2 | Migration/WAL/recovery/backup tests |
| Plan 3 | Pipeline-mode snapshot, label-survival probe, fingerprint read-back, Phase-A false-pass tests |
| Plan 4 | Per-pattern positive/negative fixtures |
| Plan 5 | Fake-clock + live timing qualification |
| Plan 6 | Phase-B qualification, notification evidence |
| Plan 7 | Authentication for remote serving, full browser/security qualification |

The non-loopback auth guard, previously unowned (MED-2), is now closed in Plan 1 (reject all non-loopback) with Plan 7 owning the future remote mode. No orphaned obligation remains.

---

## 6. New blocker or Plan-1 HIGH findings

**None found.**

Strongest remediation attacks attempted:

1. **Can the loopback guard be bypassed via hostname resolution?** No — the plan requires `net.ParseIP` to succeed (rejects hostnames including `localhost`) and then checks `IsLoopback()`. Only literal loopback IPs are accepted. Wildcard (`0.0.0.0`), unspecified (`::`), and empty-host (`:8787`) are explicitly tested as rejected.

2. **Can the archive lane module gate still fail without Git?** No — `archive-mod-check` uses `go mod verify` + `go mod tidy -diff`, neither of which requires a Git repository. The standard `mod-check` with `git diff` is used only in the in-repo lane.

3. **Can the scanner match its own policy literals?** No — the plan specifies constructing scanner patterns from "non-contiguous shell fragments" (line 667), so `test-standalone.sh` does not contain the literal forbidden strings it searches for.

4. **Can the scanner be bypassed by hiding an OSCAR dependency in documentation?** By design, yes — and this is correct. Documentation (`docs/**`, `README.md`) is explicitly excluded because it legitimately references OSCAR paths for review provenance. The Task 7 Step 2 red-team fixture (lines 689–697) proves both directions: doc reference passes, source reference fails.

5. **Does the `ci` target remain sequential under `make -j`?** Yes — the plan (line 478) specifies `$(MAKE)` sub-invocations in a sequential recipe body, not parallelizable prerequisites. Each sub-make runs to completion before the next begins regardless of `-j`.

6. **Can the package-twice gate produce false-equivalent checksums?** No — `SOURCE_DATE_EPOCH` is derived from `git show -s --format=%ct HEAD`, which is deterministic for a given commit. GNU tar with sorted members, zero owner/group, and epoch-derived mtime produces identical output. `gzip -n` removes timestamps. The plan's verification (lines 510–520) explicitly diffs checksums.

7. **Does any planned commit reference a future file or target?** No — verified: Task 5 CI references only Task 4 targets; `standalone-check` enters CI in the same Task 7 commit as its script; Task 8 is verification-only.

---

## 7. Deferred Plan 2–7 gates

| Deferred item | Owner | Mandatory gate |
|---|---|---|
| SQLite schema/migrations/WAL/backup/recovery | Plan 2 | Migration, WAL read-during-write, crash-recovery, backup/restore tests; acceptance criteria 11–12 |
| OSCAR adapter, capability snapshot, pipeline-mode discovery | Plan 3 | Phase-A false-pass contract tests; label-survival probe; recorded fixtures for every supported API route |
| Fingerprint acquisition via history read-back | Plan 3 | Contract test using server-assigned value; mutation test proving wrong client computation changes nothing |
| Scenario compiler, reserved-label enforcement | Plan 3 | Reserved-label rejection, name normalization, collision tests |
| Safe rule lifecycle (create-only, reconcile, cleanup) | Plan 3 | Crash-between-create-and-record test; collision fixture; no import/upsert |
| Window/order pattern stimuli | Plan 4 | Per-pattern positive/negative fixtures mirroring current OSCAR semantics |
| Timer patterns (persistence, absence) | Plan 5 | Fake-clock tests; live qualification for 60s persistence grace and absence fire-time gates |
| Parent-child and notifier evidence | Plan 6 | Phase-B-only qualification; notification-audit label carriage |
| Custom scenarios, operational hardening, remote serving | Plan 7 | Authentication for remote mode; full browser/security/all-pattern qualification |

No deferred item lacks an owner or gate. The resolution ledger's closure of MED-2 (non-loopback rejected outright in Plan 1) eliminated the only previously unowned obligation.

---

## 8. Final recommendation

**CLEARED FOR PLAN 1.** No BLOCKER or Plan-1 HIGH finding remains.

All four original HIGH findings are closed: the repository-verification rewrite (HIGH-3), the archive-safe module gate (HIGH-4), the pipeline-mode model (HIGH-1), and the fingerprint-acquisition contract (HIGH-2). HIGH-1 and HIGH-2 are design-level with implementation deferred to Plan 3 under explicit gates — correct, because Plan 1 makes no OSCAR contact and freezes no interface that those findings would need to change.

All seven MED findings are closed, with MED-2 resolved more strictly than originally requested (full rejection rather than an override flag). All ten LOW findings are closed or acceptably deferred.

The strongest remediation attacks — hostname-as-loopback bypass, archive-lane Git dependency, scanner self-match, scanner doc-laundering, `make -j` parallelism, and commit-ordering future-reference — all failed against the remediated artifacts.

**Remaining HIGH count: 0**

Plan 3 (first OSCAR-contact plan) must consume the remediated pipeline-mode, fingerprint-acquisition, injection, readiness, and rule-lifecycle design contracts. It must not be authored from the original (pre-remediation) design revision.
