# Focused remediation review — OSCAR Correlation Test Harness

> Run this in a fresh session that did not author the remediation. Give the reviewer read-only access to `/Users/coreyellis/code/github.com/cmetech/oscar_app` and write access only to the output file named below. This is a focused gate review, not implementation authorization.

---

You are independently verifying whether the accepted findings in the OSCAR Correlation Test Harness adversarial review were correctly incorporated before Plan 1 begins.

Work from:

```text
/Users/coreyellis/code/github.com/cmetech/oscar_app/oscar-corrtest
```

Read these files completely, in order:

1. `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md`
2. `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-resolution.md`
3. `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`
4. `docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md`

Verify the two remediated artifact digests before reviewing:

```text
77ef05571d0a1223a602d62dce2584492b8bbe5995c2177191dbd07a14071ec0  docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md
70c280c92d4074fe7eb4be7d77902cfc8699dc6f3fb025b3bdedd9274bcd32ea  docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md
```

Stop with a scope error if either differs. Record the actual repository HEAD and worktree state. The new remediation-review output itself may be the only untracked file when you finish.

This is read-and-verify. Do not edit the design, plan, resolution ledger, OSCAR source, or any implementation file. Do not implement Plan 1, create remotes, publish releases, contact live OSCAR, or start/restart services. Read-only source inspection is allowed. Disposable command probes may run only under a newly created temporary directory outside the workspace, with caches redirected beneath it, and must be removed safely.

## Required verification

For every original `HIGH-*`, `MED-*`, and `LOW-*` finding, compare the resolution-ledger disposition to the actual remediated design/plan and return one of:

- `CLOSED`
- `CLOSED WITH ACCEPTED DEFERRAL`
- `OPEN`
- `REGRESSED`
- `NOT APPLICABLE`, with evidence

Concentrate first on the Plan 1 execution gate:

1. Existing-repository verification replaces all repository creation/reinitialization and preserves review history.
2. The archive module gate works without `.git` and still fails an untidy module.
3. The source/build scanner does not match its own policy literals, deliberately excludes documentation, catches forbidden dependencies in source, and can be tested before the clean-worktree gate.
4. Only literal loopback IP listeners reach the server; hostname, wildcard, unspecified, empty-host, and non-loopback listeners are rejected without an unauthenticated bypass.
5. GNU-tar/gzip packaging is byte-reproducible and its package-twice gate is mechanically sound.
6. `ci` remains sequential under `make -j`, includes every final merge gate, and both CI systems consume Make targets without command drift.
7. Every planned commit remains executable and green at the point it is made; no task references a future file or target.

Then verify the design corrections against current OSCAR source:

1. Publication-disabled, Phase-A audit-only, Phase-B dispatch, and unknown states cannot produce vacuous PASS verdicts.
2. Server-assigned fingerprints are acquired through exact-alertname/time-bounded history read-back; client computation cannot control an assertion.
3. The supported injection door is the label-safe middleware `POST /api/v1/alerts`; response-body drop/queue/rate-limit states and label survival are gated.
4. Correlator readiness, pipeline mode, guardrails, rate limits, audit lag/retention, and capability limitations are represented honestly.
5. Temporary rules never use import/upsert; lost/5xx create outcomes reconcile without overwriting or deleting lookalikes.
6. Negative proof requires a positive history/audit eligibility anchor.
7. Every deferred obligation has a named Plan 2–7 owner and mandatory gate.

Do not demand implementation of Plans 2–7 in Plan 1. A deferred later-slice item blocks Plan 1 only if the foundation freezes an incompatible boundary or the design still lacks an owner/gate.

## Required output

Write the complete review to:

```text
/Users/coreyellis/code/github.com/cmetech/oscar_app/oscar-corrtest/docs/reviews/2026-08-19-oscar-corrtest-remediation-review.md
```

Write no other workspace file. Include:

1. Scope, actual HEAD, worktree state, and digest verification
2. Executive verdict: exactly `CLEARED FOR PLAN 1` or `CHANGES REQUIRED BEFORE PLAN 1`
3. Original-finding closure matrix
4. Plan 1 command/commit-order re-audit
5. Design-remediation re-audit against OSCAR source
6. Any new blocker or Plan-1 HIGH finding, with concrete evidence and minimum correction
7. Deferred Plan 2–7 gates
8. Final recommendation

If no blocker or Plan-1 HIGH remains, say so explicitly and identify the strongest remediation attacks attempted. Return a concise summary to the caller after writing the full file.
