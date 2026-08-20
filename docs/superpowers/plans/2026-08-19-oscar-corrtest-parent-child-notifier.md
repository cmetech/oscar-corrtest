# Parent-Child and Notifier Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove OSCAR parent-child linking plus per-notifier suppression/tagging, including a released-no-trigger negative case.

**Architecture:** Extend normalized evidence with correlation child decisions and notification audit rows. Parent-child remains a non-synthetic pattern; its oracle evaluates parent linkage, added labels, affected notifier names, and notification disposition only when Phase B and required evidence surfaces are qualified.

**Tech Stack:** Existing Go adapter/runner/oracle/SQLite/UI stack.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`

## Global Constraints

- Parent-child rules omit `emit_spec` and never assert a synthetic parent.
- Positive notifier assertions require `phase_b_dispatch`, label-survival, exact fingerprints, and notification evidence; ambiguity is `INCONCLUSIVE`.
- Notifier names are explicit scenario inputs and never inferred from UI text.

---

### Task 1: Notification and child evidence adapter

- [ ] Write failing fixtures/tests for `/api/v1/correlation_rules/audit`, child linkage fields, and `/api/v1/notification-audit/` pagination/filtering, redaction, and malformed bodies.
- [ ] Implement normalized child and notification evidence with time/rule/fingerprint bounds.
- [ ] Run adapter tests and commit `feat: collect parent-child notification evidence`.

### Task 2: Parent-child scenarios and oracle

- [ ] Write failing compiler/runner tests for matching active parent with suppression/tagging and child without parent with `released_no_trigger`.
- [ ] Implement stable parent then child stimuli, parent-resolution boundaries, and typed assertions for linkage, labels, suppression set, and notification status.
- [ ] Prove Phase A and unknown modes cannot pass either case where notifier/synthetic absence depends on dispatch.
- [ ] Run tests and commit `feat: prove parent-child correlation behavior`.

### Task 3: Operator evidence and qualification

- [ ] Write failing UI/report tests for parent/child names, fingerprints, notifier table, suppression/tagging explanation, and copyable OSCAR filters.
- [ ] Implement presentation and exports from normalized evidence.
- [ ] Add `plan6-gate` for Phase-B qualification, notification label carriage, no-parent negative anchor, cleanup, race, and archive checks.
- [ ] Run `make clean && make ci`; commit `test: qualify parent-child notifier proof`.

