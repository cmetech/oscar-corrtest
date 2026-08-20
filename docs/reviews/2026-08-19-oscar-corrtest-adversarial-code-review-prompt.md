# Adversarial code review — OSCAR Correlation Test Harness v1

> Give everything below this line to a fresh capable model or agent that did not
> author the harness. Give it read-only access to
> `/Users/coreyellis/code/github.com/cmetech/oscar_app` and
> `/Users/coreyellis/code/github.com/cmetech/otto_app/otto-gateway`, with write
> access only to the required review-output file named below. The reviewer may
> run read-only searches in those workspaces and build, test, mutate, or delete
> only a newly allocated throwaway clone under a temporary directory outside
> both workspaces. It must not modify the implementation worktree, commit, push,
> create a remote repository, start or restart shared services, send alerts,
> create rules, or contact a live OSCAR deployment.
>
> Recommended orchestration: run this prompt twice with independent reviewers,
> preserve both reviews, and compare their invariant and pattern matrices
> without showing either reviewer the other's conclusions.

---

You are a hostile senior reviewer for Go services, black-box test harnesses,
alarm correlation, test-oracle design, SQLite durability, web security,
supply-chain CI/CD, and accessible operator interfaces. You did not author this
implementation. Your job is to find ways it can report the wrong result, leave
OSCAR mutated, lose evidence, expose credentials, or ship an unusable release.
Do not reward effort, volume, passing tests, or plausible names.

Assume that:

- A fake OSCAR can faithfully mirror the harness's mistake.
- A test named after an invariant does not prove the invariant.
- A `2xx` alert response proves transport handling, not correlation.
- A missing synthetic alert is not evidence unless source eligibility and the
  full negative decision window are positively proven.
- Client-generated fingerprints can differ from OSCAR's persisted fingerprint.
- Cleanup code is more dangerous than create code when identity is ambiguous.
- Cancellation at a state transition can bypass cleanup even when happy-path
  cancellation tests pass.
- A normalized table that is created but never updated is not durable evidence.
- An offline report can be internally consistent while omitting the raw facts
  needed to audit its verdict.
- A green local Make target does not prove GitHub or GitLab release behavior.
- Authentication on HTML but not static, health, SSE, download, or mutation
  routes is still an exposed application.
- Documentation may describe behavior the executable does not implement.

Do not praise the architecture. Produce evidence-backed findings or explain
which concrete attacks failed. Distinguish confirmed defects, unproven external
assumptions, and preferences.

## Frozen review target

All relative paths in this prompt are relative to this workspace root, not the
current shell directory:

```text
Workspace:              /Users/coreyellis/code/github.com/cmetech/oscar_app
OSCAR source:           /Users/coreyellis/code/github.com/cmetech/oscar_app/oscar
Harness repository:     /Users/coreyellis/code/github.com/cmetech/oscar_app/oscar-corrtest
OTTO comparison source: /Users/coreyellis/code/github.com/cmetech/otto_app/otto-gateway
Review date:            2026-08-19
Frozen implementation:  e8ab6d0460d14e67cae2f889665499daa70f6011
Module:                 github.com/cmetech/oscar-corrtest
Go directive:           1.27.0
```

The repository HEAD may be a prompt-only descendant of the frozen
implementation. Record HEAD and verify that every path changed after the frozen
commit is under `docs/reviews/`. If any implementation, plan, design, build,
workflow, or dependency path changed after the frozen commit, stop with a scope
error and list those paths.

The implementation worktree must be clean before review. Do not clean or alter
it yourself; report a scope error if tracked or untracked implementation files
are present. Generated ignored `bin/`, `dist/`, `.tools/`, and Go cache content
does not change the frozen source, but do not trust or execute those existing
artifacts.

Verify these frozen contract digests using `sha256sum` or `shasum -a 256`:

```text
2d11ca9288f3138ab1d7794fb46779e58baa38f044900f7cf66a9aca1aee0881  oscar-corrtest/docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md
70c280c92d4074fe7eb4be7d77902cfc8699dc6f3fb025b3bdedd9274bcd32ea  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md
70829c775c31dfb84abf992b0413e7092bdbb901b47277b181ae69e207c57da9  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-durable-ledger.md
742268a5cca70a03b8fd3beacb6548444fba845be4ac0e15b632d3493e088cdb  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-flood-vertical-slice.md
67ea899f27828d90475efe033d605364d440039bbeda56a8ca43b9444e922604  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-window-order-patterns.md
5d8a1cc956f927b73da481c6317ea0f6ac8b609d4a9a4bf8444cc19c9a000da2  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-timer-patterns.md
a07c65bb9620ab8d1a2ac9b6b53099579de73df69f0cef23f4aa7b74bd39e506  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-parent-child-notifier.md
cbe6e7f10b51f91d7438a96a5447f7e360230d7cf8c8bd7a7d094ea64bd4cf46  oscar-corrtest/docs/superpowers/plans/2026-08-19-oscar-corrtest-custom-operational-hardening.md
45f18e85c50df0ac403d67749736bbd732a856f3e30e9fa1e3266078a0a7a462  oscar-corrtest/README.md
f1bfa5f3548044d42e50344b66a02b665be29a0787822af39de998edbe3c66b0  oscar-corrtest/Makefile
300a320aa92fbf169095bbacb738cae0fbe8407a5225224c9417e5870005a3a9  oscar-corrtest/go.mod
3906b20106a449ee72171ab2787040c267eb3650e822a0b168a79d030a79a616  oscar-corrtest/go.sum
```

Stop with a scope error if any digest differs at the frozen commit. Review the
design, all seven plans, implementation, tests, docs, packaging, and workflows.
The code is the release candidate; unchecked plan boxes do not mean code is
absent, and checked behavior in prose does not mean code exists.

## Authoritative comparison sources

Read current OSCAR source instead of trusting harness fixtures or comments.
Start with these paths and follow targeted references as needed:

```text
oscar/oscar-util/scripts/send_alert.py
oscar/oscar-util/scripts/send_custom_alert.py
oscar/oscar-util/scripts/send_alert_performance.py
oscar/oscar-docs/docs/12-reference/rule-engine.md
oscar/oscar-docs/docs/07-api-reference.md
oscar/oscar-correlator/src/app/main.py
oscar/oscar-correlator/src/app/routers/rules.py
oscar/oscar-correlator/src/app/routers/audit.py
oscar/oscar-correlator/src/app/schemas/rule.py
oscar/oscar-correlator/src/app/core/rule_store.py
oscar/oscar-correlator/src/app/core/audit_writer.py
oscar/oscar-correlator/src/app/core/synthetic_emitter.py
oscar/oscar-correlator/src/app/patterns/
oscar/oscar-alertmanager/src/app/routers/alerts.py
oscar/oscar-alertmanager/src/app/core/correlation_ingest.py
oscar/oscar-alertmanager/src/app/core/fingerprint.py
oscar/oscar-middleware/src/app/schemas/correlation_rules.py
oscar/oscar-taskmanager/src/tm_notifier/handlers.py
oscar/oscar-taskmanager/src/tm_notifier/fingerprint.py
```

For UI style and light/dark behavior, inspect the current OTTO Gateway admin UI
directly. Treat it as inspiration, not as a harness requirement:

```text
/Users/coreyellis/code/github.com/cmetech/otto_app/otto-gateway/internal/admin/templates/
/Users/coreyellis/code/github.com/cmetech/otto_app/otto-gateway/internal/admin/static/css/
/Users/coreyellis/code/github.com/cmetech/otto_app/otto-gateway/internal/admin/static/js/
```

If OTTO is unreadable, mark only OTTO comparison claims `UNPROVEN`. If a live
OSCAR configuration or deployment is required to prove a fact, classify it as
residual live qualification; do not contact one and do not infer PASS.

## Review execution rules

Network access is expected but not guaranteed. Use it only for read-only checks
of dependency/tool releases, action commits, image manifests, platform support,
and vulnerability data. If unavailable, mark those checks `UNPROVEN`.

Do not execute builds or tests in the implementation worktree. Allocate one
temporary parent with `mktemp -d`, validate that it is outside both workspaces,
make a local no-hardlink clone of the harness there, check out the frozen commit,
and redirect `GOCACHE`, `GOMODCACHE`, `GOBIN`, `GOTMPDIR`, and any tool cache
beneath that parent. Run destructive cleanup only against the exact validated
temporary path. Never use `$HOME`, `~`, a workspace root, an unresolved variable,
or a glob as a destructive target.

In the throwaway clone, execute at minimum:

```text
git status --short
make clean
make ci release-gate
go test -shuffle=on -count=20 ./internal/compiler ./internal/runner ./internal/runtime ./internal/web
```

Statically audit every workflow and release command even when the local gates
pass. You may write focused mutation tests or small programs only in the
throwaway clone. Mutation attacks are encouraged: alter the fake OSCAR so it
returns misleading `2xx` bodies, delayed/stale evidence, wrong fingerprints,
missing labels, duplicate history, hostile lookalike rules, pagination, partial
JSON, cancellation at every transition, delete failures, and tampered artifacts.
Revert or discard only the temporary clone when finished.

Do not run commands that publish artifacts, create releases, use real tokens,
or contact live OSCAR. Validate those paths statically and with harmless help or
syntax commands when possible.

## Evidence and severity discipline

For each finding provide:

1. stable ID (`BLOCKER-`, `HIGH-`, `MED-`, or `LOW-`);
2. classification: `CONFIRMED`, `UNPROVEN`, or `PREFERENCE`;
3. exact evidence with file and line references;
4. a concrete failure scenario;
5. why current tests did not prevent it;
6. the minimum specific remediation;
7. the regression, contract probe, or CI gate that closes it;
8. whether it blocks all use, live OSCAR qualification, or release only.

Severity meanings:

- **BLOCKER:** the harness can mutate an operator-owned OSCAR resource, broadly
  expose mutation capability without the declared security gate, corrupt its
  authoritative state, or fundamentally cannot exercise current OSCAR.
- **HIGH:** the harness can falsely report PASS/clean cleanup, lose or fabricate
  material evidence, leak a credential, leave a created resource unmanaged, or
  ship a release that fails its primary supported deployment.
- **MEDIUM:** material operability, durability, portability, UI, recovery, or
  auditability defect with a bounded workaround and no false PASS.
- **LOW:** bounded polish, diagnostics, maintainability, or hardening issue.

A preference without a concrete failure is not a finding. A live-only fact is
not automatically a code defect, but an undocumented or ungated live dependency
is. The executive verdict answers: **is this implementation ready for controlled
live OSCAR qualification and then a v1 release?**

## Locked invariant matrix

Return `PASS`, `FAIL`, `UNPROVEN`, or `NOT-APPLICABLE` for every invariant. A
PASS requires code plus a non-tautological test or direct command evidence.

1. A clean standalone checkout builds and tests with `GOWORK=off`, no readable
   sibling OSCAR source, and no Python, Node, frontend toolchain, or external DB.
2. Every used OSCAR route, method, auth shape, query, request, response, error,
   pagination, and delete semantic matches current public source.
3. All eight patterns have a valid P01 and N01 whose rule remains semantically
   constant while only the intended positive/control stimulus differs.
4. Alert names and reserved labels exactly implement the documented grammar and
   remain manually filterable by exact run, scenario, pattern, case, and role.
5. Rule validation, label-survival probe, operator pipeline mode, and correlator
   readiness fail closed; Phase A/unknown cannot produce a vacuous PASS.
6. Injection acceptance is classified independently from correlation evidence.
7. Audit queries use OSCAR's history-read-back fingerprint, never the transport
   or harness-computed fingerprint.
8. Negative cases positively anchor eligible sources and observe the entire
   bounded decision window plus required eventual-consistency stabilization.
9. Parent-child rules use explicit notifier names and prove linkage plus the
   notifier-specific suppressed/tagged/released result.
10. Rule creation records proposal before mutation and safely reconciles lost
    responses without blind retry, update, import/upsert, or name-only adoption.
11. Cleanup uses exact returned IDs plus full ownership, is 404-idempotent,
    retryable, and cannot claim CLEAN for unknown create/delete outcomes.
12. Cancellation before execution and at every lifecycle/network/clock boundary
    reaches a durable terminal state and performs bounded cleanup after context
    cancellation.
13. Restart marks interrupted runs without resuming injection and provides a
    safe, operator-visible path to reconcile every possibly owned resource.
14. SQLite normalized cases, assertions, alert attempts, events, resources, and
    artifacts reflect observed terminal facts rather than remaining empty or
    permanently `PLANNED` while a separate JSON report claims completion.
15. Raw OSCAR request/response or equivalent normalized source evidence is
    retained separately from the oracle interpretation and is sufficiently
    complete to independently recompute a verdict.
16. JSON, offline HTML, JUnit, ZIP, and manifest exports are deterministic,
    redacted, path-safe, non-overwriting, self-describing, and tamper-evident.
17. Exact deletion and retention refuse active, dirty, unknown, pending,
    missing, or hash-mismatched evidence and cannot race a live run.
18. YAML/JSON validation rejects unknown/duplicate keys, aliases, multiple docs,
    semantic incompleteness, unsafe durations/budgets/labels/notifiers, and
    digest/source mismatches consistently across CLI, UI, import, and run.
19. CLI and UI use the same compiler/runtime/runner/oracle and stable exit/error
    semantics; no presentation path implements a weaker lifecycle.
20. Loopback is the only unauthenticated bind; non-loopback bearer requires TLS
    and secure sessions; trusted proxy requires exact identity and source CIDRs.
21. Every HTML/API/SSE/static/health/download/mutation route is protected in
    remote mode, and every browser mutation enforces bounded body, same-origin,
    and CSRF controls.
22. Credential values and sensitive payloads do not enter config files, SQLite,
    URLs, logs, errors, HTML, SSE, reports, bundles, process output, or CI assets.
23. SSE replay is monotonic, disconnect-independent, bounded, authenticated,
    terminal-aware, and cannot cause a browser request to own run cancellation.
24. SQLite migrations, WAL settings, concurrency, backup, permissions,
    corruption/read-only behavior, foreign keys, deletion ordering, and startup
    recovery match the documented durability contract.
25. Canonical reports and preserved history remain interpretable after restart,
    scenario edits, compiler evolution, artifact loss, and cleanup retry.
26. Run serialization prevents cross-run correlation-window interference and
    queue cancellation/shutdown cannot deadlock or use SQLite after close.
27. Custom and built-in mutation budgets, roles, match criteria, alert states,
    timings, group labels, and assertion kinds map to current OSCAR semantics.
28. The fake OSCAR is independent enough to reject wrong client behavior rather
    than reproducing the client's route/schema/oracle assumptions.
29. Tests cover all 16 minimum cases and meaningful hostile/error timelines;
    filters and test-name regexes actually select the intended tests.
30. Race, static security, vulnerability, standalone, cross-build, package,
    content, checksum, and reproducibility gates run from the Make contract.
31. Linux AMD64/ARM64 packages contain a static executable, CA certificates or
    an explicit strategy, docs/schema/systemd/container assets, and correct modes.
32. GitHub and GitLab use immutable pins, least privilege, equivalent Make
    behavior, correct tag rules, artifact flow, and viable release commands.
33. Repeated packaging at one commit is byte-identical, current-version
    checksums exclude stale archives, and release upload globs cannot leak them.
34. Container and systemd examples match actual CLI flags, credential handling,
    writable paths, TLS/CA needs, shutdown time, and supported security modes.
35. UI theme is selected before paint, supports system/light/dark behavior,
    keyboard/focus/reduced-motion/contrast/no-JS use, and follows OTTO inspiration
    without copying assumptions that do not fit this tool.
36. UI actions cover target management, doctor/preflight visibility, built-in
    and custom preview/run, live progress, cancellation, cleanup retry, evidence
    export/verification, exact deletion, retention, filters, and later history.
37. Docs, help, JSON Schema, examples, packages, and implemented routes/flags are
    mutually accurate and do not promise an unimplemented behavior.
38. Error paths bound response bodies and waits, sanitize server text, preserve
    machine classification, and do not turn malformed/partial data into absence.
39. No test or release gate silently contacts a live OSCAR target; optional live
    qualification is explicit, isolated, non-PASS by default, and cleanup-gated.
40. No runtime/build/workflow source imports, reads, copies, or assumes a parent
    OSCAR/OTTO checkout, and leak scanning cannot be bypassed by ordinary source
    extensions or generated files.

## Attack campaign 1 — OSCAR contract and pattern reality

- Trace the actual rule, alert, history, correlation-audit, notification-audit,
  and readiness code paths end to end.
- Serialize every built-in rule and compare it with current Pydantic/public-v1
  schemas, including required notifier and emit fields.
- For each P01/N01 pair, write the smallest event timeline current OSCAR needs.
  Detect control cases that accidentally weaken or change the rule.
- Verify firing/resolved payloads, timestamps, Alertmanager `fingerPrint`, label
  rewrite behavior, source fallback, and actual fingerprint inputs.
- Confirm history/audit/notifier pagination and retention cannot hide evidence.

Any plausible PASS without the intended OSCAR outcome is HIGH or worse.

## Attack campaign 2 — oracle, time, and false PASS

- Delay positive evidence until just after a poll/window boundary.
- Remove all source evidence and determine whether N01 can still pass.
- Return duplicate, stale, wrong-run, wrong-label, and wrong-fingerprint history.
- Reorder sequence events; duplicate flood events; repeat threshold values;
  resolve persistence near the deadline; send late absence heartbeats.
- Exercise clock skew, wall-clock jumps, slow requests, context deadlines, and
  eventual consistency. Identify where a fake sleep or wall clock masks bugs.
- Verify the runner evaluates declared assertions, not hard-coded pattern names
  that ignore custom assertion values.

## Attack campaign 3 — mutation ownership, cancellation, and recovery

- Fail before and after each proposal/create/adopt/read/delete/ledger write.
- Lose the create response with zero, one, and multiple same-name candidates.
- Return a hostile lookalike with the same prefix/name but wrong description,
  pattern, run ID, or ID.
- Cancel while queued, during doctor, before create, between two creates, during
  injection, every timer/poll, assertion, cleanup, and completion transition.
- Restart with proposed, created, unknown, deleted, and partially recorded
  resources. Determine whether startup merely labels them unknown or enables
  safe reconciliation.
- Race retention/manual deletion, cleanup retry, export, SSE, and shutdown with
  an active run.

## Attack campaign 4 — persistence and evidence

- Inspect every table across PASS, FAIL, INCONCLUSIVE, ERROR, cancellation,
  restart, cleanup-dirty, and cleanup-retry runs.
- Attempt to recompute a report using only persisted facts. List every value
  that exists only in an in-memory runner result or already interpreted JSON.
- Corrupt, remove, replace, symlink, truncate, or add entries to artifacts and
  bundles. Test ZIP traversal, duplicate names, oversized entries, and manifest
  extra/missing-file behavior.
- Test SQLite busy, disk full, read-only, migration mismatch, backup cancellation,
  artifact publication failure, and database/artifact two-phase disagreement.
- Verify retention preview/apply handles more than 500 records transparently
  and never broadens its cutoff or eligibility after a partial failure.

## Attack campaign 5 — web, auth, and operator UX

- Enumerate routes from the mux and prove remote auth covers every method/path,
  including errors, login/logout, static, health, ready, SSE, and downloads.
- Attempt cross-site forms, missing Origin/Sec-Fetch headers, DNS rebinding,
  cookie replay/fixation, brute force, proxy-header spoofing, untrusted peers,
  oversized bodies, malicious scenario/report text, and concurrent tabs.
- Verify CSP/nonces, escaping, content disposition, cache policy, clickjacking,
  session expiry, TLS-only cookies, and secret-free errors.
- Exercise the UI without JavaScript and with keyboard, reduced motion, system
  theme changes, narrow viewport, long IDs/errors, empty states, and active
  timers. Compare the visual contract with OTTO's real implementation.
- Identify every Plan 7 CLI operation that lacks a corresponding UI action or
  actionable explanation.

## Attack campaign 6 — tests and fake-server independence

- Map each production branch to a test that can fail for the right reason.
- Inspect regex-filtered plan gates for packages reporting “no tests to run.”
- Mutation-test route strings, schema keys, label names, fingerprints, pipeline
  mode, evidence outcome, cleanup ownership, timers, auth middleware, and export
  verification. Record mutations that survive.
- Detect assertions that only compare values generated by the same helper under
  test, fake responses tailored to client output, zero-length fixtures, or tests
  that ignore returned errors.
- Verify all eight patterns run through an independently meaningful fake model,
  not merely a canned response keyed from `P01` in the alert name.

## Attack campaign 7 — build, packaging, and dual CI

- Audit the Make dependency graph and prove each advertised gate runs once with
  the intended environment and cannot pass on an empty test selection.
- Verify Go/tool/image/action versions and digests against primary sources and
  multi-architecture manifests.
- Inspect module tidiness in both Git and archive contexts, CGO posture, race
  prerequisites, static linking, linker metadata, file modes, GNU tar behavior,
  CA certificates, and scratch execution.
- Seed stale archives and prove current checksums/content/release uploads exclude
  them. Repeat builds at the same commit and compare every byte.
- Validate GitHub release permissions/tag filters/actions and GitLab package
  upload/release-link commands, variables, `CI_JOB_TOKEN` assumptions, needs,
  artifacts, and rules. External account permissions remain `UNPROVEN` unless a
  primary source proves them.
- Compare systemd and Containerfile invocation with actual help output and
  non-loopback authentication/TLS requirements.

## Required pattern matrix

For each of the eight patterns, report one row for P01 and one for N01 with:

- compiled rule criteria;
- sent alert names, labels, states, ordering, and delays;
- OSCAR behavior expected from current source;
- authoritative evidence queried;
- negative eligibility/window anchor where applicable;
- result: `PASS`, `FAIL`, or `UNPROVEN`;
- exact test or missing test.

Do not collapse the 16 rows. Parent-child must name notifier behavior;
persistence and absence must state real timer durations; cross-source and
threshold must state the varying label values.

## Required output

Write the complete review to this one permitted workspace file:

```text
/Users/coreyellis/code/github.com/cmetech/oscar_app/oscar-corrtest/docs/reviews/2026-08-19-oscar-corrtest-adversarial-code-review.md
```

Do not modify any other workspace file. Also return a concise executive summary
to the caller after the file is safely written. The review must contain:

1. **Scope, frozen commit, digests, and evidence inspected**
2. **Executive verdict** — exactly one of `READY FOR CONTROLLED LIVE
   QUALIFICATION`, `READY WITH REQUIRED CHANGES`, or `BLOCK LIVE QUALIFICATION`
3. **Blocker findings**
4. **High-severity findings**
5. **Medium- and low-severity findings**
6. **40-invariant verdict matrix** — none omitted
7. **16-row pattern/oracle matrix** — none collapsed
8. **OSCAR public API/schema compatibility matrix**
9. **Mutation ownership, cleanup, cancellation, and restart audit**
10. **SQLite, artifacts, reports, export, deletion, and retention audit**
11. **Web security, authentication, CSRF, SSE, and accessibility audit**
12. **Test-effectiveness and surviving-mutation ledger**
13. **Build, dependency, packaging, GitHub, and GitLab command audit**
14. **Design/plan/implementation/docs traceability gaps**
15. **Required changes before controlled live qualification**
16. **Residual live-system and external-account qualification**
17. **Final recommendation**

Do not implement fixes and do not rewrite the plans. Preserve uncertainty as
`UNPROVEN`. If no blocker or high issue exists, document the strongest hostile
mutations attempted and the evidence showing why each failed.
