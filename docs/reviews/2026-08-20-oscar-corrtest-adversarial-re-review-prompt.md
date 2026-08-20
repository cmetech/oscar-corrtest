# Independent adversarial re-review prompt: OSCAR Corrtest remediation

You are an independent, hostile code reviewer. You did not author this code.
Your task is to determine whether the remediation actually closes the two
prior reviews' live-qualification trust failures. Do not praise design intent.
Do not infer correctness from plan/status documents or from a green suite.

## Workspace and frozen scope

Workspace root:
`/Users/coreyellis/code/github.com/cmetech/oscar_app`

Review repository:
`/Users/coreyellis/code/github.com/cmetech/oscar_app/oscar-corrtest`

OSCAR comparison source:
`/Users/coreyellis/code/github.com/cmetech/oscar_app/oscar`

Frozen remediation implementation:
`ce319b9218fbba038c4d38591d10893ba5ad2b48`

All relative paths in this prompt are relative to the review repository. The
working tree may contain only this prompt/status as descendants of the frozen
implementation. Confirm that no implementation/build/test file differs from
the frozen commit before reviewing. If it does, stop with `SCOPE INVALID`.

You have read-only authority over both repositories and may use the network to
verify public dependency/tool metadata. Do not contact a live OSCAR target.
You may run destructive/build commands only in a throwaway clone or temporary
directory outside the workspace. The sole permitted workspace write is the
review output named below.

## Inputs

Read completely:

- `docs/reviews/2026-08-19-oscar-corrtest-adversarial-code-review.md`
  (`sha256:dcd9416c7ad90213c83db538e3b029354e7631a1865b2b6e624df7a170193685`)
- `docs/reviews/2026-08-20-oscar-corrtest-adversarial-code-review-resolution.md`
  (`sha256:74d8348afdd44c09c64130767ed95851a1589d2fe24dc03d6e5764313727b555`)
- `docs/superpowers/plans/2026-08-20-oscar-corrtest-trust-remediation.md`
  (`sha256:183e2aa6e91fea4a9a5983c2bdf20757e0f1022189a8b92ec916652757f72240`)
- `docs/reviews/2026-08-20-oscar-corrtest-remediation-status.md`
- current design/spec/operator/development/live-qualification documents
- every implementation/test/build/workflow file changed from `e8ab6d0` through
  the frozen remediation commit
- current OSCAR source for external auth, alert ingestion/fingerprinting/rate
  limiting/history, correlation audit, all eight patterns, synthetic emission,
  and dispatch gating

Treat the second reviewer's supplied summary in the resolution as a review
input, but do not invent a missing second full report.

## Required attack campaigns

1. **Public OSCAR contract.** Prove the external path/header, exact payload,
   required Alertmanager `fingerPrint`, authoritative `oscar_fingerprint`,
   response classifiers, annotation schema, pagination, and rule lifecycle
   against current OSCAR source. Look for any 2xx body that can be mistaken for
   acceptance or any secret-bearing error/artifact.
2. **Fingerprint/cardinality.** Independently compute current OSCAR fingerprints
   for every built-in stimulus. Prove flood has five distinct identities,
   grouping still works, and resolved stimuli resolve the firing identity.
   Re-attack transport and NATS rate/dedup limits.
3. **Oracle timing and eligibility.** Reproduce late-audit and late-parent
   schedules. Attack absolute deadlines, stabilization, final negative reads,
   `Plan.MaxDuration`, missing source/audit eligibility, duplicate rows, wrong
   run/rule/name records, timer cases, and notification evidence. Find a way to
   make empty/broken OSCAR evidence PASS.
4. **Assertion authority and mutations.** Repeat all 25 mutation probes from the
   prior review. Additionally mutate every built-in assertion value/outcome,
   rule threshold/order/source/distinct label/timer, history filters, run label,
   and audit rule identity. A green result after a semantically breaking
   mutation is a finding.
5. **Durability and crash consistency.** Interrupt at every boundary between
   proposal/create/adopt/inject/observe/artifact/delete/resolve/finalize. Prove
   normalized cases/assertions/attempts and the canonical report/artifact agree;
   no PASS/FAIL can be terminal without proof; recovery/deletion/retention cannot
   erase a verdict-less or cleanup-unknown run.
6. **Evidence independence.** Recompute assertion counts from the immutable
   normalized artifact and compare them with SQLite/report verdicts. Tamper,
   remove, truncate, duplicate, or leave an artifact PENDING. Verify export
   includes only hash-verified artifacts and rejects extra/changed content.
7. **Cleanup safety.** Attack lost-create reconciliation and `UNKNOWN` retry
   with zero/one/multiple/lookalike rules and pagination. Prove cleanup deletes
   only exact owned IDs, deletes rules before resolutions, resolves only exact
   run-owned history using server fingerprints, retains every classification,
   and never reports CLEAN after persistence or resolution failure.
8. **Independent model.** Prove `internal/testoscar/model.go` does not read case
   codes, polarity, expected assertions, or CORRTEST P01/N01 substrings to decide
   behavior. Rename all cases/alerts and rerun. Break each semantic criterion and
   require the all-eight end-to-end test to fail.
9. **Web/runtime boundary.** Attack loopback Host validation using DNS-rebinding,
   IPv4/IPv6, wrong ports, and actual ephemeral listeners. Attack session future
   timestamps, expiration, signature changes, concurrent logout/replay, and
   revocation cleanup. Reconfirm non-loopback mode still requires secure auth.
10. **Build/release/container.** Run `make clean release-gate` in a throwaway
    clone. Confirm every publish path uses that gate, exact current artifacts,
    nonempty timer selection, standalone archive behavior, writable scratch
    state, non-networking default, and reproducibility. Check GitLab YAML syntax,
    needs/artifact flow, and shell portability rather than only grepping text.
11. **Live-gate isolation.** Prove missing acknowledgements fail before binary or
    network access; offline CI cannot reach the live target; all eight runs must
    be PASS+CLEAN with verified bundles; unsafe parsing, flag ordering, paths,
    partial summaries, credential leakage, or a false overall PASS are findings.
12. **Residual-claim audit.** Confirm the declared residuals are real and not
    prerequisites silently required for v1 correctness. In particular, explain
    whether operator-declared Phase B and notifier names make a live result
    unsafe or merely target-specific.

## Mandatory execution

In a throwaway clone of the frozen implementation, run at minimum:

```sh
make clean release-gate
go test -shuffle=on -count=20 ./internal/compiler ./internal/oscar ./internal/runner ./internal/runtime ./internal/web ./internal/testoscar
go test -race -count=1 ./...
```

Statically validating these commands is insufficient. Record exact command,
exit code, and relevant output. Do not run `make live-qualification` against a
real target. Test its fail-closed preconditions with a false/sentinel binary.

## Finding discipline

Classify each claim as `CONFIRMED`, `UNPROVEN`, or `PREFERENCE`. Use severities:

- `BLOCKER`: can produce a false live PASS, unsafe deletion, credential leak,
  or prevents all supported live use.
- `HIGH`: defeats a major evidence/cleanup/release invariant or a built-in.
- `MEDIUM`: material but bounded reliability/security/operability defect.
- `LOW`: limited quality/documentation issue.

For every finding include ID, severity, classification, invariant violated,
exact file:line evidence, reproduction or mutation, impact, and minimal
remediation. A deferred concern is acceptable only as `DEFERRED-WITH-GATE` with
an enforceable owner/gate; otherwise classify `DEFERRED-WITHOUT-GATE`.

If no blocker/high exists, explain the strongest false-PASS, crash-window,
cleanup, and semantic-fake attacks attempted and why each failed. A green suite
alone is not evidence.

## Required output

Write exactly one Markdown review to:

`docs/reviews/2026-08-20-oscar-corrtest-adversarial-remediation-re-review.md`

It must contain:

1. Scope/digest/worktree verification
2. Executive verdict: `BLOCK LIVE QUALIFICATION`, `READY WITH REQUIRED CHANGES`,
   or `READY FOR CONTROLLED LIVE QUALIFICATION`
3. Prior-finding closure table for all 3 blockers, 9 highs, report-two items,
   and accepted medium gates
4. New findings ordered by severity
5. Oracle/fingerprint/timing analysis
6. Durability/evidence/crash analysis
7. Cleanup/recovery analysis
8. Semantic-model and mutation results
9. Web/security analysis
10. Build/CI/container/live-gate analysis
11. Command audit
12. Residual/deferred-gate ledger
13. Final invariant matrix and the exact conditions for controlled live use

Do not modify implementation, plans, specs, workflows, or tests. Do not create
a PR, tag, release, target, rule, alert, or external message.
