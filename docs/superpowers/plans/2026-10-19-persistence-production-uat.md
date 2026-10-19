# Persistence first-production UAT implementation plan

> **For agentic workers:** Use superpowers:executing-plans or the already authorized parallel implementation/review workflow. Execute against feature worktrees only. Existing continuous-work authorization remains in effect; missing production semantics are requirements questions, not per-task permission requests.

**Goal:** Qualify the exact Persistence linked-parent use cases from the user's Netcool prerequisite with repeatable CorrTest inputs and independently verifiable full-flow evidence.

**Architecture:** Preserve the closed Flood installed-pack contract. Add a separate finite Persistence contract over the existing installation, source journal, lifecycle, authenticated observations and evidence-export mechanisms. A protected, isolated test producer must supply source occurrences and original severity; arbitrary public labels cannot grant authority. Production feed equivalence remains a separate gate.

**Tech stack:** Existing Go CorrTest runtime/SQLite artifacts; OSCAR Python source admission/lifecycle; isolated MySQL, Redis, Native Alertmanager and History stack.

**Spec:** `docs/uat/persistence-first-production.md`, traced to workspace `netcool_triggers_automation/OSCAR_Persistence_Occurrence_Gate_Prerequisite_Spec.md` and its linked SQL/disposition.

## Global constraints

- Preserve unchanged source records and one linked parent for each accepted policy episode. No production policy activation, main edits, pack uninstall or broad container/volume cleanup.
- UAT uses actual 60/120/300/600-second waits; the one-hour negative retains its real observation requirement. Accelerated SQL tests are labeled component evidence only.
- Keep source count separate from deliveries, manual resends and History counters. No global runtime capability flag or public authority header.
- Preserve original failed attempts and source ownership; uncertain input/output remains inconclusive. No replay into a rewritten PASS.
- History-only controlled destinations; ticket-provider tests excluded, collector-health/absence unsupported; no production capacity or full39-case acceptance claim.

## Review focus

False authority through arbitrary incoming labels; under-count after coalescing or source restart; duplicate parents from unchanged-child cross-policy overlap; falsely accepted parent delivery inferred from an audit decision; lost or fabricated recovery after uncertain input/output. P17, P05/P09, P16, P08/P20, and P07/P10/P18 respectively cover these risks.

### 1. Establish requirements and component evidence

Files: `docs/uat/persistence-first-production.md`; backend `oscar-correlator/tests/test_persistence_production_requirements.py`; backend `docs/qualification/20261019/persistence-production/`.

- [x] Trace #30/#14/#18/#28 predicates, durations and parent severity to the source specification and SQL.
- [x] Define all six occurrence-gate event-order rows plus severity, eligibility, overlap, source preservation, clear/refire, restart and visible failure variations.
- [x] Run combined SQL lifecycle tests at the production threshold values, recording initial failures and corrected results without treating fixture clocks or fabricated History receipts as UAT.
- [x] Independently review the matrix and tests; commit the scoped evidence. All matrix findings and four test-oracle findings addressed; final combined run87 passed, no skips.

### 2. Qualify an isolated source before extending live injection

Touch points: backend `app/core/selected_source_startup.py`, `selected_source_handler.py`, `selected_worker.py`, `correlation_delivery.py`, `producer_journal.py`, and `rule_validation.py` in their existing services; matching focused test modules. Read the existing startup/identity policy contracts before designing the bootstrap. This is the next implementation dependency, not a claim an endpoint already exists.

- [ ] Trace and pin a protected fixture producer through normal selector, local journal, staged admission, Native handoff and callback/History using real clients. Retain its durable event ID, source episode, ordering, cumulative occurrence count, original severity and start time across restart.
- [ ] Add refusal tests before registration: wrong producer/scope, substituted rules or selector revision, expired readiness, arbitrary label/header authority, regressed/changed duplicate count, adopted count>1 without original episode proof, and ordinary no-match behavior.
- [ ] Supply narrowly bound fixture readiness only after those checks pass; leave production runtime capability reporting closed for unqualified feeds. Do not solve this by patching a capability dictionary in a running server.
- [ ] Prove an ordinary negative travels the ordinary delivery route. A selected-worker no-match NAK is not ordinary delivery.

### 3. Install a separately versioned finite Persistence pack

Touch points: `test-packs/` and `internal/testpack/{load,archives,installation}.go`; exact existing backend validation invoked by the builder.

- [ ] Add the five rule variants and real normal Rule Engine classifier/mapping, source-alarm identity grouping, and trusted PhysicalSlot propagation. Add exact predicate positive/negative tests; MatchClause supports equality/IN/presence, not the SQL regex/NOT/OR expressions.
- [ ] Keep rules disabled until scoped readiness is proved. Use bounded per-run identity and pin the complete installed inventory, including the retained Flood pack. Do not relax v1's exactly-one-rule contract.
- [ ] Settle and test N28 overlap and N14/N18/#15 precedence for the chosen migration scope before accepting those policies. Record whether existing Netcool automation is retained or replaced.

### 4. Execute and independently score the closed scenario set

Touch points: new Persistence-specific peers beside `internal/oscar/installed_pack*`, `internal/runner/installed_pack.go`, `internal/runtime/installed_pack.go`, `internal/command/installed_pack_commands.go`, `internal/persistence/sqlite/installed_pack*`, and `internal/evidence/installed_pack*`.

- [ ] Add finite case schedules from P01–P22 rather than silently increasing generic scenario bounds or changing the existing Flood oracle. Preserve paced, cancellable, durable-before-send, no-automatic-retry behavior.
- [ ] Use authenticated complete History/Active reads plus `/correlation_rules/episodes/{episode}/incidents` and `/incidents/{incident}/members`. Score physical parent identity/receipts, membership, unchanged source, mapped severity evidence and recovery state from original responses.
- [ ] Add failing oracle tests for missing pages, conflicting revisions/identity, audit-only parent evidence, clock jumps, late acknowledgments, changed input labels, duplicate accepted parents and missing visible failure reasons. Export verifier independently recomputes assertions; missing evidence cannot PASS.
- [ ] Test cancellation, interrupted schedule resumption and exact cleanup responsibility without issuing source clears to make an uncertain case appear clean.

### 5. Execute real UAT and review its release evidence

- [ ] Freeze target ownership/images/rules/destinations and preserve baseline ordinary delivery evidence. Run supported positive/negative cases with real production durations; record source and parent UI/metric views alongside machine evidence.
- [ ] Run bounded process interruption and recovery cases using exact owned identities; retain raw before/after state and interruption evidence. Never destroy unknown/shared state.
- [ ] Export and independently verify each attempt, including failures and pending cleanup. Review predicates, parent count, source preservation, severity, clear/refire and stable new-episode recovery against this matrix.
- [ ] Keep the pack installed for user inspection; remove only owned temporary helpers. Report remaining production-feed/precedence/performance gates explicitly.

## Current disposition

The current installed UAT pack has only an ordinary Flood PASS/DIRTY. Component tests exist for the new Persistence logic; 14 combined production variants pass, with 87 passes in the combined component lane; independent review findings are resolved. Tasks2–5 are outstanding; no full-flow Persistence UAT run is being claimed. Exact trigger-replacement scope is a pending user requirement. Source-specific production provenance remains unproved regardless of fixture success.
