# Flood Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the first complete OSCAR-contacting run: diagnose a target, safely create a temporary flood rule, inject labeled alerts, prove positive or negative behavior, clean up, and export durable evidence.

**Architecture:** Add a versioned `public-v1` OSCAR adapter behind domain interfaces, a strict built-in scenario compiler, and a serial run coordinator. Network results are redacted before persistence; rule ownership is recorded and verified; the oracle consumes normalized evidence only.

**Tech Stack:** Go 1.27, `net/http`, `database/sql`, embedded fixtures/templates, SSE, existing CGO-free SQLite stack.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`

## Global Constraints

- Use only public OSCAR HTTP APIs; inject only through `POST /api/v1/alerts`.
- Temporary rules use validate/create/read/delete; never import, update, or name-upsert.
- Unknown pipeline mode, failed label survival, or ambiguous evidence cannot produce `PASS`.
- Physical alert names follow `CORRTEST_<PATTERN>_<CASE>_<ROLE>_<RUN_SHORT>` and every reserved label is compiler-owned.
- Persist lifecycle/resource state before exposing progress; never make network calls inside SQLite transactions.
- Redact authorization, API keys, cookies, passwords, tokens, and configured sensitive fields before storage or SSE.

---

### Task 1: Scenario, naming, and executable-plan contracts

**Files:** Create `internal/scenario/model.go`, `internal/scenario/builtin.go`, `internal/compiler/compiler.go`, and tests.

**Interfaces:** Produce `compiler.Compile(run domain.Run, scenario scenario.Scenario, capabilities domain.CapabilitySnapshot) (domain.CompiledPlan, error)`.

- [ ] Write failing table tests for the exact flood positive/negative names, reserved labels, stable repeat fingerprints, collision rejection, strict mutation budgets, and canonical plan digest.
- [ ] Run `go test ./internal/compiler ./internal/scenario` and confirm failures name missing compiler behavior.
- [ ] Implement typed scenario/plan models, `FLOOD` pattern mapping, `P01`/`N01` cases, deterministic alert events, inspection manifest, and SHA-256 canonicalization.
- [ ] Re-run focused tests and `go test ./...`; commit `feat: compile isolated flood scenarios`.

### Task 2: Public-v1 OSCAR adapter and capability preflight

**Files:** Create `internal/oscar/client.go`, `transport.go`, `types.go`, `redact.go`, `fixtures/public-v1/*.json`, and tests; extend `internal/testoscar` only where behavior requires it.

**Interfaces:** Produce `oscar.API` methods `Diagnose`, `ValidateRule`, `CreateRule`, `GetRule`, `FindRules`, `DeleteRule`, `Inject`, `FindHistory`, and `CorrelationAudit`.

- [ ] Write failing contract tests for credential resolution, TLS policy, redaction, pagination, typed machine errors, all injection response classes, history fingerprint read-back, audit parsing, cancellation, and bounded response bodies.
- [ ] Prove the adapter calls `/api/v1/correlation_rules`, `/validate`, `/{id}`, `/audit`, `/api/v1/alerts`, and `/api/v1/alerts/history` with exact query/body contracts.
- [ ] Implement `public-v1`; keep endpoint types out of the domain layer and reject unknown/partial JSON where an assertion depends on it.
- [ ] Add operator-declared pipeline mode to target metadata and require readiness plus a label-survival probe snapshot before mutation.
- [ ] Run focused tests and `go test ./...`; commit `feat: add public v1 oscar adapter`.

### Task 3: Safe resource ledger and create reconciliation

**Files:** Add migration `0002_execution.sql`; create SQLite repositories for scenarios, cases, assertions, and resources; add `internal/ownership/service.go` and tests.

**Interfaces:** Produce create-adopt-delete operations keyed by returned rule ID plus exact name/run-token ownership.

- [ ] Write failing migration/repository tests, including crash-after-create, timeout/5xx name reconciliation, hostile same-name collision, 404-idempotent deletion, and refusal to delete a lookalike.
- [ ] Implement short transactions and append-only evidence; record a proposed resource before create and the external ID immediately after response/adoption.
- [ ] Mutation-test that no code path invokes import/upsert or deletes without full ownership proof.
- [ ] Run migration, recovery, and race tests; commit `feat: add safe oscar resource lifecycle`.

### Task 4: Serial runner, oracle, cleanup, and report bundle

**Files:** Create `internal/runner`, `internal/oracle`, `internal/export`; extend runtime/history/report/artifact packages and tests.

**Interfaces:** Produce `Run`, `Cancel`, `RetryCleanup`, and `ExportBundle`; oracle returns typed assertion evidence and verdict independently from cleanup.

- [ ] Write failing fake-server end-to-end tests for flood P01/N01, Phase A and unknown-mode false-pass prevention, wrong client fingerprint ignored, full negative window, exactly-one stabilization, cancellation, lost-create response, and cleanup retry.
- [ ] Implement persisted state transitions, injection, history polling, server fingerprint resolution, audit/history collection, positive eligibility anchors, oracle evaluation, cleanup, JSON/HTML/JUnit/ZIP projections, and manifest verification.
- [ ] Prove every artifact is redacted and hashed, ZIP paths are safe, and offline HTML contains no external dependency.
- [ ] Run focused integration/race tests; commit `feat: execute and prove flood correlation runs`.

### Task 5: CLI, SSE, run UI, and qualification gate

**Files:** Extend command/web/runtime; add Run Test UI, Inspect in OSCAR panel, `run-stream.js`, templates, CSS, tests, README and development docs.

**Interfaces:** Add `doctor`, `scenario list`, `plan`, `run`, `cleanup retry`, and `export`; add `POST /runs`, `POST /runs/{id}/cancel`, `GET /runs/{id}/events`, and export routes.

- [ ] Write failing command/HTTP tests for stable JSON envelopes and exit codes 0/1/2/3/4/130, CSRF, SSE replay after sequence, disconnect independence, and exact inspection filters.
- [ ] Implement shared service-backed CLI/UI flows and accessible live states; do not duplicate runner logic in handlers.
- [ ] Add `plan3-gate` covering pipeline snapshots, label survival, history fingerprint mutation, create reconciliation, Phase-A false-pass, SSE replay, export integrity, and archive independence.
- [ ] Run `make clean && make ci`, live fake-server smoke, Linux cross-build/file/checksum checks, and secret sentinel scan; commit `test: qualify flood vertical slice`.

