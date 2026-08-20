# Timer-Driven Patterns Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persistence and absence proofs with deterministic fake-clock coverage and honest live timing qualification.

**Architecture:** Introduce a narrow clock/timer interface into the runner and poller. Persistence emits a firing alert then optionally a resolution; absence emits or withholds heartbeats. The runner records scheduled deadlines and long-observation progress durably so cancellation and restart never fabricate success.

**Tech Stack:** Existing Go stack with standard-library timers and fake clocks in tests.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`

## Global Constraints

- Production defaults respect OSCAR persistence minimums and effective grace; tests use fake time, while live qualification uses real bounded timing.
- Absence positive proof requires readiness/backlog eligibility at timer fire time; Phase A/unknown cannot pass either synthetic positive or negative assertions.
- Interrupted timer runs never resume injection; recovery performs cleanup only.

---

### Task 1: Clock-aware observation engine

**Files:** Add `internal/clock`, refactor runner polling/deadlines, and tests.

- [ ] Write failing tests for monotonic deadlines, bounded jitter, stabilization, full negative windows, cancellation, and restart interruption.
- [ ] Implement injected clock/timers without adding test-only methods to production services.
- [ ] Run runner race tests and commit `refactor: make observation timing deterministic`.

### Task 2: Persistence scenarios

- [ ] Write failing compiler/fake-server tests for unresolved-through-duration positive and resolved-before-duration negative, including stable fingerprint across firing/resolution.
- [ ] Implement persistence rule/stimuli and cancellation audit evidence.
- [ ] Run tests and commit `feat: add persistence correlation proofs`.

### Task 3: Absence scenarios

- [ ] Write failing tests for missing-heartbeat positive, continuing-heartbeat negative, readiness loss at fire time, and full decision-window enforcement.
- [ ] Implement absence schedule/stimuli and eligibility normalization.
- [ ] Run tests and commit `feat: add absence correlation proofs`.

### Task 4: Long-run UX and qualification

- [ ] Write failing UI/SSE tests for next deadline, elapsed/remaining time, reconnect, cancel, and interrupted cleanup states.
- [ ] Implement accessible long-observation status and report timing evidence.
- [ ] Add `plan5-gate` with fake-clock cases plus opt-in `OSCAR_CORRTEST_LIVE_TARGET` timing qualification; make offline CI require recorded contract fixtures and skip only the live target.
- [ ] Run `make clean && make ci`; commit `test: qualify timer-driven patterns`.

