# OSCAR Corrtest Trust Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:test-driven-development` task-by-task and
> `superpowers:verification-before-completion` before any completion claim.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make live OSCAR correlation qualification trustworthy by repairing
the public API contract, event identity, timing oracle, durable evidence chain,
cleanup recovery, and release gates identified by two independent reviews.

**Architecture:** Keep the existing standalone Go binary, SQLite ledger, and
proposal-first resource model. Move verdict computation to explicit assertion
results gathered by a deadline-driven observer, persist normalized source
evidence as an immutable artifact, and atomically finalize the run and its
normalized rows. Keep the required Alertmanager transport `fingerPrint`
separate from the authoritative server history fingerprint.

**Tech Stack:** Go 1.27, `net/http`, `modernc.org/sqlite`, embedded
`html/template`, Make, GitHub Actions, GitLab CI, scratch container.

**Spec:**
`docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`

**Review resolution:**
`docs/reviews/2026-08-20-oscar-corrtest-adversarial-code-review-resolution.md`

## Global constraints

- Run names remain `CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>`.
- Every alert remains filterable by exact `oscar_test_run_id`, pattern,
  scenario, case, role, category, and rule name.
- User-supplied scenarios cannot stamp `oscar_fingerprint` or
  `am_fingerprint`.
- Server history fingerprints, never client-computed fingerprints, key audit
  evidence.
- External target mutation remains serial, bounded, cancellation-aware, and
  cleanup-gated.
- Cleanup never searches by prefix and never deletes a rule without exact
  run-ID ownership proof.
- Secrets remain credential references and never enter SQLite, artifacts,
  logs, reports, URLs, or error bodies.
- Offline gates do not contact OSCAR and do not imply live qualification.
- Every production behavior change follows a witnessed RED/GREEN TDD cycle.

---

### Task 1: Public-v1 API contract and recorded fixtures

**Files:**

- Create: `internal/oscar/testdata/public-v1/injection-accepted.json`
- Create: `internal/oscar/testdata/public-v1/injection-api-rate-limited.json`
- Create: `internal/oscar/testdata/public-v1/injection-alert-rate-limited.json`
- Create: `internal/oscar/testdata/public-v1/injection-queued.json`
- Create: `internal/oscar/testdata/public-v1/history.json`
- Create: `internal/oscar/testdata/public-v1/audit.json`
- Modify: `internal/oscar/client.go`
- Modify: `internal/oscar/client_test.go`
- Modify: `internal/oscar/types.go`
- Modify: `README.md`
- Modify: `docs/operator.md`

**Interfaces:**

- Produces: all authenticated requests use `X-API-Key`.
- Produces: `InjectionResult` distinguishes accepted, rejected, queued,
  partial, and indeterminate 2xx responses using current OSCAR bodies.
- Produces: paginated exact-name history/rule/audit/notification reads.
- Produces: history annotations decode `{Annotation,Value}`.

- [ ] **Step 1: Write the failing auth and response-contract tests.**

```go
func TestClientUsesExternalAPIKeyHeader(t *testing.T) {
    // Server returns 401 unless X-API-Key is exactly "secret" and rejects
    // Authorization. ValidateRule must succeed.
}

func TestInjectClassifiesCurrentOSCAR2xxBodies(t *testing.T) {
    // Table includes middleware status=rate_limited, alertmanager prose
    // "Alert rate limited (fingerprint: ...)", queued=true, and the async
    // DBResponseSchema success body.
}

func TestHistoryDecodesAnnotationKey(t *testing.T) {
    // Fixture uses {"Annotation":"summary","Value":"..."}.
}
```

- [ ] **Step 2: Run the focused tests and witness the expected failures.**

Run: `go test ./internal/oscar -run 'APIKey|ClassifiesCurrent|AnnotationKey' -count=1`

Expected: the server reports a missing `X-API-Key`, the alert-rate-limit body
is indeterminate, and the annotation map is empty.

- [ ] **Step 3: Implement the exact external contract.**

```go
if c.credential != "" {
    request.Header.Set("X-API-Key", c.credential)
}
```

Parse status/message/details case-insensitively after structured fields. A
body stating rate-limited/dropped/filtered is `rejected`; a body stating
accepted but queued is `queued`; unknown 2xx stays `indeterminate`. Never put
the credential into a returned error.

- [ ] **Step 4: Add bounded pagination.**

For each list route, request pages of 100 until a declared total is satisfied
or a short page is returned. Cap at 100 pages and return a machine error rather
than treating a truncated result as absence. Filter exact names/run identity
after pagination.

- [ ] **Step 5: Correct target documentation.**

Use `https://oscar.example/ext/mw` in README/operator examples. State that
`public-v1` expects OSCAR's external middleware root and an API key credential
reference.

- [ ] **Step 6: Run the package tests and commit.**

Run: `go test ./internal/oscar ./internal/runtime -count=1`

Commit: `fix: align public v1 adapter with oscar`

---

### Task 2: Stable per-event label identity and timer-safe built-ins

**Files:**

- Modify: `internal/compiler/compiler.go`
- Modify: `internal/compiler/compiler_test.go`
- Modify: `internal/compiler/patterns_test.go`
- Modify: `internal/scenario/builtin.go`
- Modify: `internal/scenario/decode_test.go`
- Modify: `docs/builtins.md`

**Interfaces:**

- Produces: source labels `oscar_test_event_id` and
  `oscar_test_event_index` for manual OSCAR filtering and fingerprint
  uniqueness.
- Produces: a resolved attempt reuses the preceding firing event's labels.
- Produces: `CasePlan.ObservationWindow` and `Plan.MaxDuration` are executable
  timing contracts.

- [ ] **Step 1: Write failing identity tests.**

```go
func TestFloodEventsHaveFiveDistinctStableLabelIdentities(t *testing.T) {
    plan := compileBuiltin(t, "flood")
    seen := map[string]bool{}
    for _, alert := range plan.Cases[0].Alerts {
        id := alert.Labels["oscar_test_event_id"]
        if id == "" || seen[id] { t.Fatalf("event identity=%q", id) }
        seen[id] = true
    }
    if len(seen) != 5 { t.Fatalf("identities=%d", len(seen)) }
}

func TestResolvedAttemptReusesFiringIdentity(t *testing.T) {
    plan := compileBuiltin(t, "persistence")
    firing, resolved := plan.Cases[1].Alerts[0], plan.Cases[1].Alerts[1]
    if !maps.Equal(firing.Labels, resolved.Labels) {
        t.Fatal("resolution must target the firing fingerprint")
    }
}
```

- [ ] **Step 2: Run and witness failure.**

Run: `go test ./internal/compiler -run 'DistinctStable|ReusesFiring' -count=1`

Expected: event ID is missing from labels and firing/resolved identities differ.

- [ ] **Step 3: Implement event identity compilation.**

Add event ID/index to `reservedLabels` and `identityLabels`. Allocate a new
identity for each firing event. For a resolved event, copy the most recent
unresolved firing label set for that role and reject a resolution without a
matching firing event. Keep the per-attempt index in annotations. Stamp a
stable explicit `severity=warning` so OSCAR firing/resolved normalization does
not change the fingerprint.

- [ ] **Step 4: Compile trustworthy deadlines.**

Add `ObservationWindow time.Duration` to `CasePlan`. Current built-ins use 45s
for 30s decision windows plus 15s evidence grace; absence uses 55s
(`expected_every=10 + absent_for=30 + grace=15`). Enforce that each case
deadline and its last event delay fit `Plan.MaxDuration`.

- [ ] **Step 5: Sustain the absence negative control.**

Replace the two-heartbeat N01 timeline with heartbeats at 0s, 8s, 16s, 24s,
32s, 40s, and 48s. The final assertion snapshot is at 55s from case start.

- [ ] **Step 6: Prove transport and OSCAR-like fingerprint diversity.**

Tests compute a deterministic labels-only hash over each flood event and assert
five unique values while all `group_by=site` values remain equal.

- [ ] **Step 7: Run and commit.**

Run: `go test ./internal/scenario ./internal/compiler -count=1`

Commit: `fix: give correlation stimuli stable event identity`

---

### Task 3: Deadline-driven explicit assertion oracle

**Files:**

- Create: `internal/runner/assertions.go`
- Create: `internal/runner/observation.go`
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `internal/runner/timing_test.go`
- Modify: `internal/oscar/types.go`

**Interfaces:**

- Produces: `AssertionResult{Kind,Outcome,Expected,Observed,Verdict,Explanation}`.
- Produces: `CaseResult` with deduplicated server fingerprints and normalized
  history/audit/notification evidence.
- Consumes: `CasePlan.Assertions`, `CasePlan.ObservationWindow`, and the first
  injection time for the case.

- [ ] **Step 1: Add the two adversarial timing regressions.**

```go
func TestNegativeFailsWhenParentAppearsBeforeDeadline(t *testing.T) {
    // Parent is absent on initial reads and appears after Sleep advances the
    // fake clock. Expected final verdict: FAIL.
}

func TestPositivePassesWhenAuditFlushesLate(t *testing.T) {
    // Source and parent appear first; parent_emitted audit appears after 5s.
    // Expected final verdict: PASS.
}
```

- [ ] **Step 2: Add assertion-authority regressions.**

```go
func TestRunnerUsesDeclaredAssertionValues(t *testing.T) {
    // Change synthetic-alert-count from 1 to 7. Evidence contains one parent;
    // verdict must be FAIL, proving the oracle did not infer P01 behavior.
}

func TestMultipleFingerprintsUnderOneAlertNameAreValid(t *testing.T) {
    // Three exact-run records with distinct event IDs/fingerprints satisfy
    // threshold P01; duplicate rows for one fingerprint are deduplicated.
}
```

- [ ] **Step 3: Run and witness the stale-snapshot/inert-assertion failures.**

Run: `go test ./internal/runner -run 'NegativeFails|FlushesLate|DeclaredAssertion|MultipleFingerprints' -count=1`

- [ ] **Step 4: Replace `observeCase` with a deadline loop.**

At every poll:

1. Read every exact source alertname and keep only rows with the exact run ID.
2. Deduplicate by server fingerprint and validate expected event IDs.
3. Query audit for every server fingerprint and keep rows for the exact owned
   rule ID/name.
4. Read/deduplicate exact-run synthetic parents and required notification rows.
5. Evaluate every declared assertion from the current snapshot.
6. Positive non-absence assertions may finish after a stabilization re-read.
   Any zero/absence assertion finishes only from the final read at or after the
   absolute deadline.

- [ ] **Step 5: Make missing eligibility fail closed.**

If expected source identities never appear, or no correlation audit proves the
source was processed, return `INCONCLUSIVE`; never PASS because every evidence
surface is empty. HTTP/pagination errors are `ERROR`, not zero counts.

- [ ] **Step 6: Make `Plan.MaxDuration` executable.**

Create a child context bounded by the compiled plan duration around setup,
injection, observation, and assertion. On expiry, preserve the timeout as the
terminal error and run detached cleanup with the existing cleanup budget.

- [ ] **Step 7: Remove polarity/pattern verdict branches and commit.**

Search gate:
`rg 'item\.Polarity|Rule\.Pattern == "parent_child"' internal/runner`
must find no verdict-selection branch.

Run: `go test ./internal/runner -count=1`

Commit: `fix: evaluate correlation assertions at bounded deadlines`

---

### Task 4: Durable normalized evidence and atomic finalization

**Files:**

- Create: `internal/domain/evidence.go`
- Create: `internal/persistence/sqlite/execution_repository.go`
- Create: `internal/persistence/sqlite/execution_repository_test.go`
- Modify: `internal/runner/runner.go`
- Modify: `internal/persistence/sqlite/run_repository.go`
- Modify: `internal/persistence/sqlite/run_repository_test.go`
- Modify: `internal/report/report.go`
- Modify: `internal/report/report_test.go`

**Interfaces:**

- Produces: `domain.ExecutionFacts` containing attempts, cases, assertions, and
  normalized OSCAR evidence.
- Produces: `FinalizeRun(ctx, runID, facts, verdict, cleanup, report,
  terminalError, completedAt)` as one SQLite transaction.
- Produces: deletion eligibility requires a non-null verdict.

- [ ] **Step 1: Write failing repository tests.**

```go
func TestFinalizeRunAtomicallyPersistsTerminalFacts(t *testing.T) {
    // Finalize from CLEANING_UP, then assert run=COMPLETED with verdict/report,
    // cases/assertions terminal, and attempts SENT/OBSERVED with fingerprints.
}

func TestDeleteRejectsCompletedRunWithoutVerdict(t *testing.T) {
    // Seed the legacy crash-window shape and expect deletion refusal.
}

func TestRecoveryCapturesCompletedRunWithoutVerdict(t *testing.T) {
    // Startup converts the legacy crash state to INTERRUPTED + UNKNOWN when
    // an undeleted resource exists.
}
```

- [ ] **Step 2: Run and witness failure.**

Run: `go test ./internal/persistence/sqlite -run 'FinalizeRun|WithoutVerdict' -count=1`

- [ ] **Step 3: Define durable fact types.**

`ExecutionFacts` stores assertion expected/observed JSON, source histories,
audits, notifications, injection classifications/status codes, and artifact
manifests. It stores no HTTP headers, credential references, cookies, or raw
unbounded response bodies.

- [ ] **Step 4: Implement one finalization transaction.**

The transaction verifies current status `CLEANING_UP`, updates every planned
case/assertion/attempt by stable ID, transitions to `COMPLETED`, writes verdict,
cleanup, report, terminal error, and timestamps, and appends the terminal event.
Any validation/update count mismatch rolls back the whole transaction.

- [ ] **Step 5: Repair recovery and deletion.**

Recovery selects active rows plus `COMPLETED AND verdict IS NULL`. Deletion and
retention require terminal status, cleanup-safe status, and non-null verdict.

- [ ] **Step 6: Build reports from `ExecutionFacts`.**

Extend `report.Document` with cases/assertions/attempts/evidence manifests and
derive the canonical JSON from the same normalized facts written by
`FinalizeRun`. Remove the runner-private self-attesting report type.

- [ ] **Step 7: Run and commit.**

Run: `go test ./internal/domain ./internal/persistence/sqlite ./internal/report ./internal/runner -count=1`

Commit: `fix: atomically persist correlation evidence verdicts`

---

### Task 5: Immutable evidence artifact on the production run path

**Files:**

- Create: `internal/runtime/evidence_writer.go`
- Create: `internal/runtime/evidence_writer_test.go`
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `internal/evidence/bundle.go`
- Modify: `internal/evidence/bundle_test.go`

**Interfaces:**

- Produces: runner `EvidenceWriter.Write(ctx, runID, facts) (domain.Artifact,
  error)`.
- Produces: `runs/<runID>/evidence/normalized.json`, registered PENDING before
  publication and AVAILABLE only after immutable hash/size publication.

- [ ] **Step 1: Write a failing production-path test.**

```go
func TestCompletedRunHasVerifiedNormalizedEvidenceArtifact(t *testing.T) {
    // Execute against a semantic fake, list artifacts, verify one AVAILABLE
    // normalized-evidence artifact and assert its JSON contains history/audit
    // facts but no configured credential.
}
```

- [ ] **Step 2: Run and witness zero artifacts.**

Run: `go test ./internal/runtime -run NormalizedEvidenceArtifact -count=1`

- [ ] **Step 3: Implement intent-before-file-before-manifest publication.**

Generate a deterministic artifact ID/path, insert PENDING, marshal the bounded
normalized document, call the existing atomic `artifact.Store.Write`, then mark
AVAILABLE with SHA-256 and size. A publication failure prevents PASS/FAIL
finalization and leaves the pending record visible.

- [ ] **Step 4: Include artifacts in export.**

Export must copy verified available run artifacts into the bundle and list
their hashes in the manifest. Verification must fail if content changes while
keeping the original manifest. Document that publisher authenticity requires
an externally retained SHA-256 or future signature.

- [ ] **Step 5: Run and commit.**

Run: `go test ./internal/artifact ./internal/runtime ./internal/evidence ./internal/report -count=1`

Commit: `feat: retain normalized oscar evidence artifacts`

---

### Task 6: Recovery-safe resource and alert cleanup

**Files:**

- Modify: `internal/persistence/sqlite/resource_repository.go`
- Modify: `internal/persistence/sqlite/resource_repository_test.go`
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/runtime_test.go`
- Modify: `internal/oscar/client.go`
- Modify: `internal/oscar/client_test.go`
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`

**Interfaces:**

- Produces: exact ownership-proven `UNKNOWN -> CREATED` recovery.
- Produces: `API.ResolveHistory(ctx, HistoryRecord) (InjectionResult, error)` for
  exact run-owned source/synthetic history records.
- Produces: cleanup status covers rule deletion and alert resolution.

- [ ] **Step 1: Add the UNKNOWN resource retry regression.**

Seed an `UNKNOWN` resource without an external ID, have `FindRules` return one
exact full-identity rule, and assert retry adopts, deletes, records DELETED, and
sets cleanup CLEAN.

- [ ] **Step 2: Run and witness the current adoption failure.**

Run: `go test ./internal/runtime ./internal/persistence/sqlite -run 'Unknown|UNKNOWN' -count=1`

- [ ] **Step 3: Permit only exact recovery adoption.**

Change repository adoption eligibility to `PROPOSED` or `UNKNOWN`, with null
external ID and a non-empty timestamp. Runtime still performs the existing
name, pattern, description, and run-token proof before calling it.

- [ ] **Step 4: Add alert-resolution contract tests.**

`ResolveHistory` rejects any record without exact `oscar_test_run_id` and
server fingerprint. It creates a resolved Alertmanager envelope with the
authoritative server fingerprint pre-stamped only inside this cleanup-only
adapter. User compiled plans remain unable to stamp fingerprints.

- [ ] **Step 5: Resolve owned alert residue during cleanup.**

Delete rules first, then resolve deduplicated source and synthetic history
records captured by observation. Persist each resolution classification. Any
unknown/rejected resolution makes cleanup DIRTY without changing a behavioral
PASS/FAIL verdict.

- [ ] **Step 6: Keep cleanup running after a transition-write failure.**

When the ledger cannot record `CANCELLING`/`CLEANING_UP`, still attempt deletion
for in-memory resources whose exact returned IDs and ownership tokens are
known. Return both persistence and cleanup errors; never claim CLEAN without a
durable ledger update.

- [ ] **Step 7: Run and commit.**

Run: `go test ./internal/oscar ./internal/runner ./internal/runtime ./internal/persistence/sqlite -count=1`

Commit: `fix: recover rules and resolve owned alert residue`

---

### Task 7: Independent semantic OSCAR model and mutation guards

**Files:**

- Create: `internal/testoscar/model.go`
- Create: `internal/testoscar/model_test.go`
- Create: `internal/runner/builtin_model_test.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `internal/testoscar/server.go`

**Interfaces:**

- Produces: a manual-clock fake that evaluates rule criteria, label-derived
  fingerprints, distinct windows, persistence/absence timers, and audit lag.
- Does not consume case codes, polarity, expected assertion values, or
  `CORRTEST_*_P01/N01` substrings to decide outcomes.

- [ ] **Step 1: Write mutation-guard tests before the model.**

The tests must fail if flood `min_count` changes 5→4, if history filter field is
wrong, if the wrong-run label guard is removed, if positive cardinality becomes
`>=`, or if a late negative parent is ignored.

- [ ] **Step 2: Run and witness missing semantic behavior.**

Run: `go test ./internal/testoscar ./internal/runner -run 'Semantic|MutationGuard|AllBuiltins' -count=1`

- [ ] **Step 3: Implement the manual-clock model.**

The model derives a 12-hex canonical fingerprint from labels after the current
static exclusions; stores history by fingerprint; evaluates the rule's actual
`match_criteria`; queues audits with configurable 5s lag; and advances timers
only when the injected test clock moves.

- [ ] **Step 4: Execute all eight built-ins end-to-end.**

Each positive and negative case must be decided from model state. Add a control
that renames P01/N01 and physical alertnames without changing rule semantics;
results must remain unchanged.

- [ ] **Step 5: Run and commit.**

Run: `go test -shuffle=on -count=20 ./internal/compiler ./internal/testoscar ./internal/runner`

Commit: `test: model oscar correlation semantics independently`

---

### Task 8: Web/session trust boundary and operator recovery actions

**Files:**

- Modify: `internal/web/auth.go`
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/command/app.go`
- Modify: `internal/command/app_test.go`

**Interfaces:**

- Produces: loopback Host guard derived from the actual listener address.
- Produces: bearer sessions with issued-at expiry and server-side logout
  revocation for the current process.
- Produces: UI actions for doctor and cleanup retry, using existing runtime
  methods and existing CSRF/authorization wrappers.

- [ ] **Step 1: Add failing Host/session tests.**

Reject `Host: attacker.example` on unauthenticated loopback serving; accept
`127.0.0.1`, `[::1]`, and the exact configured loopback listener. Advance a
fake clock past eight hours and reject the session. Capture a cookie, log out,
then prove replay is rejected.

- [ ] **Step 2: Implement guard and expiring revocable sessions.**

Bind the guard around the actual listener, not generic handler unit tests.
Session cookie payload contains random ID and issued-at, authenticated with
HMAC; an in-memory revocation map stores only session IDs until their expiry.

- [ ] **Step 3: Add doctor/cleanup forms and tests.**

Both routes require auth, same-origin, CSRF, bounded bodies, and exact IDs.
Render sanitized results into the existing timeline/detail style.

- [ ] **Step 4: Run and commit.**

Run: `go test ./internal/web ./internal/command -count=1`

Commit: `fix: harden local sessions and expose cleanup recovery`

---

### Task 9: Release gates and container execution

**Files:**

- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `.gitlab-ci.yml`
- Modify: `Containerfile`
- Modify: `scripts/check-package.sh`
- Modify: `docs/development.md`
- Modify: `docs/operator.md`

**Interfaces:**

- Produces: every publish-capable lane runs `make clean release-gate`.
- Produces: timer gate selects named tests and fails on empty selection.
- Produces: scratch image has a writable `/var/lib/oscar-corrtest` owned by
  UID/GID 65532 and no misleading unreachable default service.

- [ ] **Step 1: Add static gate regressions.**

Extend the existing build-contract tests/scripts to require `release-gate` in
both GitHub workflows and GitLab verify/package dependencies, and to reject a
timer gate with `[no tests to run]`.

- [ ] **Step 2: Run and witness workflow/gate failures.**

Run: `make plan5-gate container-check`

Expected before repair: scenario timer selection can be empty; container check
does not detect unwritable state or unreachable default.

- [ ] **Step 3: Wire clean release gates.**

CI/release jobs invoke `make clean release-gate`. Upload exact current-version
archives plus `SHA256SUMS`; broad stale globs are not the source of truth.
GitLab package needs the verified release-gate job and does not independently
rebuild with weaker checks.

- [ ] **Step 4: Make the timer gate non-empty.**

Run exact timer/builtin test names and add a shell assertion that output does
not contain `[no tests to run]` for any target package.

- [ ] **Step 5: Repair container state.**

Create/chown the state directory in a builder layer and copy it into scratch.
Default `CMD` prints help. Document two safe launch forms: Linux host networking
with loopback binding, or non-loopback binding with remote auth and TLS/trusted
proxy configuration.

- [ ] **Step 6: Run and commit.**

Run: `make clean release-gate`

Commit: `ci: require the complete release qualification gate`

---

### Task 10: Isolated live qualification gate, docs, and adversarial re-review

**Files:**

- Create: `scripts/live-qualification.sh`
- Create: `docs/live-qualification.md`
- Create: `docs/reviews/2026-08-20-oscar-corrtest-adversarial-re-review-prompt.md`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `docs/operator.md`
- Modify: `docs/development.md`

**Interfaces:**

- Produces: `make live-qualification` only when
  `OSCAR_CORRTEST_LIVE_TARGET_ID`, an explicit Phase-B acknowledgement, and a
  disposable target acknowledgement are present.
- Produces: non-PASS-by-default outcome until cleanup is CLEAN, evidence
  artifacts verify, and all eight semantic runs finish.

- [ ] **Step 1: Add a shell test proving offline gates never invoke live qualification.**

Search Make/workflow dependency graphs and fail if `ci` or `release-gate`
depends on the live target. Running the live script with no environment must
exit non-zero before network activity.

- [ ] **Step 2: Implement the opt-in script.**

The script checks the explicit acknowledgements, runs doctor, records harness
version/target ID without credentials, runs built-ins serially, verifies every
bundle, confirms cleanup CLEAN, and writes a qualification summary. Any
INCONCLUSIVE, ERROR, DIRTY, UNKNOWN, missing artifact, or cancellation prevents
an overall PASS.

- [ ] **Step 3: Document residual live prerequisites.**

State that API-key RBAC, declared Phase-B dispatch, correlator/NATS readiness,
audit retention/lag, enabled notifier names, and external CI account rights
remain live facts. Never infer them from unit tests.

- [ ] **Step 4: Write the re-review prompt.**

Freeze the remediation commit/digests and explicitly re-attack the original
three blockers, nine highs, both late-evidence demonstrations, all 25 original
mutations, normalized artifacts, alert resolution, workflows, and container.

- [ ] **Step 5: Run final verification.**

```sh
make clean release-gate
go test -shuffle=on -count=20 ./internal/compiler ./internal/runner ./internal/runtime ./internal/web
git status --short
```

Expected: all gates pass; only intentional review/plan/remediation files are
present before the final commit; no live OSCAR was contacted.

- [ ] **Step 6: Commit.**

Commit: `docs: add isolated live qualification and re-review gate`

## Plan self-review

- **Spec coverage:** The plan closes all three blockers, all nine HIGH findings,
  both report-two HIGH summaries, the agreed priority medium/release findings,
  and the two previously missing deferral gates. UI parity and external bundle
  signing remain explicitly deferred in the resolution ledger.
- **Placeholder scan:** No task contains TBD/TODO or delegates unspecified
  behavior. Each production change names its test, failure mode, interface, and
  verification command.
- **Type consistency:** `CasePlan.ObservationWindow` flows from compiler to
  runner; `domain.ExecutionFacts` flows from runner through evidence writer,
  report builder, and SQLite finalization; `ResolveHistory` consumes the same
  `HistoryRecord` produced by the OSCAR adapter.
- **Safety:** External mutation remains proposal-first and exact-ID. Alert
  resolution is restricted to history evidence carrying the exact run ID.
  Live qualification is opt-in and excluded from offline gates.

