# OSCAR Correlation Test Harness — Adversarial Review Resolution

**Date:** 2026-08-19
**Review:** `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md`
**Original reviewed design SHA-256:** `c401e5587a13c0aa9edc2474f2a5e291a8545481cd75e8da0cf34cf282022908`
**Original reviewed plan SHA-256:** `6ec8194f450a4e5b28100dc07fac94735842cf87d7b24e1947f41d87064e9dc7`
**Remediated design SHA-256:** `77ef05571d0a1223a602d62dce2584492b8bbe5995c2177191dbd07a14071ec0`
**Remediated plan SHA-256:** `70c280c92d4074fe7eb4be7d77902cfc8699dc6f3fb025b3bdedd9274bcd32ea`

## Disposition summary

No implementation began before remediation. The independent review's four Plan 1 prerequisites are closed in the canonical repository plan. Its two design-level HIGH findings and the OSCAR API/operational gaps are incorporated before authoring the first OSCAR-contact plan.

One review detail was corrected without changing its conclusion: outside a repository, the installed Git treated `git diff --exit-code -- go.mod go.sum` as an implicit no-index operation and exited 1 rather than the review's predicted 128. It still cannot implement a module-tidiness gate in a `git archive` extraction, so the archive-safe remediation remains required.

## Finding resolution ledger

| Finding | Disposition | Resolution and gate |
|---|---|---|
| HIGH-1 dispatch mode invisible | **Resolved in design; implementation deferred with gate** | §13 now models publication-disabled, Phase A, Phase B, and unknown pipeline states; §21 requires false-pass contract tests and live flag qualification; Plan 3 owns the gate. |
| HIGH-2 audit requires server fingerprint | **Resolved in design; implementation deferred with gate** | §12 requires exact-alertname history read-back of the server fingerprint; client computation is diagnostic only; Plan 3 must mutation-test this invariant. |
| HIGH-3 repository already exists | **Resolved in Plan 1** | Task 1 verifies path, branch, ancestry, cleanliness, and absence of implementation files; it never runs `mkdir` or `git init`. |
| HIGH-4 archive calls Git-dependent module gate | **Resolved in Plan 1** | Task 7 adds `archive-mod-check` using `go mod verify` plus `go mod tidy -diff`; the extracted checkout runs that target. |
| MED-1 scanner rejects review/design prose | **Resolved in Plan 1** | Scanner uses an explicit source/build-file allowlist, excludes documentation by path, and has positive and negative scan-only controls. |
| MED-2 non-loopback guard unowned | **Resolved more strictly** | Plan 1 rejects all non-loopback/wildcard/empty-host listeners. The suggested `--allow-remote-unauthenticated` bypass was rejected because it contradicts the security contract. Plan 7 may add remote serving only with authentication or an authenticated-proxy declaration. |
| MED-3 injection route ambiguous | **Resolved in design** | §13 pins middleware `POST /api/v1/alerts`, excludes mapping/webhook and upstream Alertmanager routes, and requires a label-survival probe. |
| MED-4 2xx drop/limit ambiguity | **Resolved in design** | Adapter must parse accepted, ACL-filtered, per-fingerprint-limited, breaker-queued, partial, and unknown bodies; it never supplies AM fingerprint fields. Contract fixtures are mandatory. |
| MED-5 readiness not externally exposed | **Resolved as OSCAR prerequisite** | §13 adds public correlator readiness/capability state; compatibility mode records inability to verify and cannot turn missing evidence into PASS. |
| MED-6 rule import/upsert and create collision | **Resolved in design** | Temporary rules use create/read/delete only. Unknown outcomes reconcile by unique name plus full ownership verification; no blind retry or lookalike deletion. |
| MED-7 archive determinism claim | **Resolved in Plan 1/design** | GNU tar, sorted members, normalized owner/group/mtime, commit epoch, and `gzip -n`; package-twice checksum comparison is a gate. |
| LOW-1 toggle semantics | **Resolved** | Stable `Light theme` accessible name with `aria-pressed` representing whether light mode is active. |
| LOW-2 content-type equality | **Resolved** | HTTP tests compare the media-type prefix and allow charset parameters. |
| LOW-3 design-copy/provenance wording | **Resolved** | Design is canonical under `docs/superpowers/specs`; Task 1 neither copies nor rewrites it. |
| LOW-4 `ci` versus `ci-core` | **Resolved** | Plan defines a sequential final `ci` target and retains `ci-core` as its internal stage. GitHub invokes `make ci`; GitLab uses the same Make targets across jobs. |
| LOW-5 systemd provisioning/hardening | **Partially incorporated, non-blocking** | Plan documents required user/group provisioning and later state-directory ownership. Additional optional sandbox directives remain an implementation-time compatibility check. |
| LOW-6 prior testkit relationship | **Resolved** | §3 states supersession/coexistence and delegates scheduling to external CI/operator schedulers. |
| LOW-7 positive negative-proof anchor | **Resolved in design** | §12 requires history plus non-triggering audit evidence before absence can prove a negative, with flush/backpressure/retention caveats. |
| LOW-8 guardrail config preflight | **Resolved in design** | §13 capability snapshot includes guardrail limits and uses them before mutation. |
| LOW-9 CI cache key evolution | **Resolved in Plan 1** | `go.mod` is used while no `go.sum` exists; the first dependency commit must switch both CI systems to `go.sum`. |
| LOW-10 live GitLab release behavior | **Deferred with external gate** | First real semantic tag must prove `CI_JOB_TOKEN`, package-registry upload, and release links; local YAML validation cannot close it. |

## Approval boundary

Plan 1 may enter implementation only after a focused reviewer verifies the two remediated SHA-256 artifacts above and returns no open `BLOCKER` or Plan-1 `HIGH`. Plans 2–7 remain unapproved until their own detailed plans and gates exist. Plan 3 may not be authored from the original design revision; it must consume the remediated pipeline-mode, fingerprint-acquisition, injection, readiness, and rule-lifecycle contracts.
