# Window and Order Patterns Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add positive and negative black-box proofs for co-occurrence, sequence, cross-source, and threshold patterns.

**Architecture:** Extend the existing compiler with data-driven built-ins while reusing the Plan 3 runner, evidence collector, cardinality oracle, lifecycle, and presentation. Each pattern owns only its rule match criteria, deterministic stimuli, and pattern-specific eligibility details.

**Tech Stack:** Existing Go/SQLite/HTTP stack and sanitized `public-v1` fixtures.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`

## Global Constraints

- Preserve Plan 3 safety, naming, labeling, evidence, cleanup, and false-pass contracts.
- Sequence ordering is event-time ordered with distinct timestamps; cross-source stamps `oscar_source`; threshold counts distinct values; co-occurrence counts distinct required alert names.
- Every negative case observes its full window and first proves source-alert eligibility.

---

### Task 1: Shared pattern fixture DSL

**Files:** Extend scenario/compiler/domain; create literal golden plans and tests.

- [ ] Write failing tests for compiler-produced rule JSON, alert order/timestamps, stable versus varying labels, mutation budgets, and inspection manifests for all four patterns.
- [ ] Implement a closed pattern registry and deterministic event builder; unknown patterns and unknown match fields fail locally.
- [ ] Run compiler tests and commit `feat: compile window and order scenarios`.

### Task 2: Co-occurrence and sequence

**Files:** Add built-in fixtures and oracle tests.

- [ ] Write failing fake-server end-to-end tests for required-type/missing-type and valid-order/invalid-order cases, including same-millisecond rejection protection.
- [ ] Implement literal public-v1 rule criteria and evidence normalization; reuse exactly-one stabilization.
- [ ] Run focused/race tests and commit `feat: add co-occurrence and sequence proofs`.

### Task 3: Cross-source and threshold

**Files:** Add built-in fixtures and oracle tests.

- [ ] Write failing tests proving source fallback semantics, invalid source mixes, distinct threshold values, and repeated non-distinct values.
- [ ] Implement source and distinct-value stimulus variation without changing reserved identity labels.
- [ ] Run focused/race tests and commit `feat: add cross-source and threshold proofs`.

### Task 4: Presentation and qualification

**Files:** Extend scenario docs, UI filters/previews, CLI listing, Make gate, and fixtures.

- [ ] Write failing CLI/UI tests showing all five available patterns and their positive/negative cases with exact filters.
- [ ] Implement previews, reports, and docs from the shared registry.
- [ ] Add `plan4-gate` with eight per-pattern cases, full negative windows, repeated-suite isolation, cleanup, standalone archive, and cross-build checks.
- [ ] Run `make clean && make ci`; commit `test: qualify window and order patterns`.

