# Ordinary CorrTest CLI managed-cohort implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute the committed ordinary Flood scenario with the actual CorrTest CLI and real receiver, Native callback, History, rule and audit APIs in a newly owned disposable backend cohort.

**Architecture:** Reuse the backend W16 ordinary-window proof and managed resource owners. Add a bounded real receiver listener and CLI process; permit temporary rule CRUD only in the explicitly declared disposable fixture. CorrTest product behavior and the scenario remain unchanged.

**Tech Stack:** Existing Go CLI/SQLite, packaged W16 FastAPI profile receiver, Native Alertmanager, managed Python/Docker cohort, MySQL 8/5.7, Redis and NATS.

**Spec:** [Read-only integration assessment](../../ordinary-webhook-managed-cohort-assessment-20261014.md), [ordinary input contract](../../ordinary-webhook-input.md), and backend `docs/superpowers/specs/2026-10-13-corrtest-receiver-integration-assessment.md`.

## Status and ownership

Design proposal only. Backend owner must finalize it against the in-progress W16 five-source/repeat/no-match/clear proof before implementation. CorrTest `4553f5b` is the minimum checked source checkpoint. No listener, role, container or live request was created to write this plan. Backend fixture files below belong to the parent workstream; this document does not authorize CorrTest agents to edit them.

## Global constraints

- Feature worktrees only; no production/main grants, deployments, public listener, image pull or enhanced activation.
- Use `phase_a_audit_only`; publication enabled, dispatch/enhanced admission/suppression/automatic physical clear disabled. Archive actual actor settings; the CLI flag alone proves none of them.
- Fixed `corrtest_ordinary_v1` artifact, hash and response acknowledgment; same receiver path for native clear. No selected-source handler, private NATS shortcut or ordinary-array front route.
- Run sequentially. Delete only this run's rules; pre-existing fixture rules must match their original inventory afterward. Do not apply old live-campaign instructions to delete every cohort rule.
- No retry of ambiguous POSTs, no source reset/refire, no ticket-provider or absence tests. No physical-clear, parent-delivery or 39-case UAT acceptance claim.

## Review focus

- Default SQL allowlist accidentally persists beyond the test: scoped patch restoration and exact default snapshot comparison.
- Extra role privilege hidden by self-consistent owner metadata: independently assert the exact added tables and `SHOW GRANTS` delta.
- Native callbacks repeat earlier children or arrive late: compare unique identities, complete observation and at-most-one trigger; never equate callback count with source count.
- Receiver acknowledgment is lost or mapping changes: preserve durable uncertain attempt and DIRTY; do not clear/retry without current exact authority.
- Process failure during cleanup: archive SQLite and resource ownership before teardown; teardown is not recovery evidence.

## One bounded integration task

**Backend files to reuse:** `utils/tests/test_ordinary_webhook_native.py`, `ordinary_webhook_native_probe.py`, `ordinary_window_probe.py`, `test_managed_shadow_preparation.py`, `utils/managed_callback_cohort.py`, `managed_mysql_restore.py` and the owned Docker lifecycle helper. Extend the ordinary-window fixture produced by the current backend work instead of building a parallel stack. A proposed new test-local entry point is `utils/tests/test_corrtest_ordinary_webhook.py`; finalize its reuse seam after that work lands.

**CorrTest inputs:** `Makefile` cross-build convention (`CGO_ENABLED=0`, Linux architecture matching the owned runner), `scenarios/reliability/flood-ordinary-webhook.yaml`, existing target/run/export CLI and a private mounted data directory. No new CorrTest schema, endpoint, scenario or automatic cleanup feature is planned.

**Produces:** CLI report/exit code, retained SQLite/request timeline/export, actual API-created rule IDs, independently scoped native/History/Redis/audit evidence, exact grant/config/image records and joined owned-resource teardown evidence. A passing CLI verdict is necessary but insufficient without these independent checks.

- [ ] **Pin fixture baseline and reproduce the missing real transport/authority boundaries.** The existing helper's ASGI transport is not a CLI-reachable listener. Default disposable Correlator credentials lack rule INSERT/DELETE and writer-revision UPDATE. Preserve expected failure evidence before adding the explicit fixture seams; do not replace APIs with SQL seeding or model responses.
- [ ] **Provision a scoped test-only SQL authority delta before cohort creation.** Temporarily extend only `_APPLICATION_WRITES['correlator']` with `CorrelationRules` and `CorrelationWriterRevision` before `ManagedCallbackCohort.create` calls `provision_application`. Leave the production/helper default unchanged; use scoped restoration even on failure. The existing provisioner grants INSERT/UPDATE/DELETE on each added table, including all three on the revision table. This is an explicitly broader disposable fixture grant, not a claim of minimum production privilege. Assert both tables exist, every other role/table/privilege is unchanged, `application_tables` has exactly this delta and actual grants match. Preserve `_verify_shadow_sql`, binding hashes, account inventory and restore checks. Do not edit a running cohort's grants.
- [ ] **Start the packaged receiver as an owned private listener.** Reuse the actual router/compiler/AlertHandler/native runtime and fixed artifact/auth fixture. Use verified TLS and owned health/shutdown bounds; preserve exact profile acknowledgment. Record that receiver registry/auth are fixture seams and middleware auth enforcement remains unqualified in the existing managed setup. No public port is required: place the CLI in the same owned network namespace or private network, with reachable certificate-matching middleware/receiver names.
- [ ] **Run the actual CLI with separate endpoint bindings.** Mount the cross-built binary, unchanged scenario, CA and credential-reference files read-only, with a private writable SQLite/artifact directory. Configure the public middleware URL and fixed receiver URL independently, validity 600 seconds, then run `plan` and `run` with `--pipeline-mode phase_a_audit_only --output json`. The durable scenario's ordinary probe is the preflight; standalone direct `doctor` is not ordinary transport proof. Capture stdout/stderr and nonzero exits without losing artifacts. Target flags already exist: `--ca-file`, `--credential-file`, `--receiver-ca-file`, `--receiver-credential-file`, `--receiver-validity-seconds`, and `--data-dir`.
- [ ] **Verify real rule ownership and window decisions.** Use actual validate/create/read/delete APIs and their synchronous reload; inspect runtime load where available. P01 must retain all five exact firing episodes and one `parent_emitted` decision, N01 four exact firing episodes and no trigger for the full interval. Native defaults (group_wait 1s/group_interval 5s) may cause partial then repeated whole groups. Independently inspect native fingerprints, scoped Redis membership and audits while the window is still present, rather than waiting until the negative case exhausts the positive window. Scope NATS/task/History evidence to this run; old helper absolute counts are inappropriate. A shadow audit is not emitted parent evidence.
- [ ] **Verify cleanup and invalidated selected readiness honestly.** API-delete owned rules and check original inventory restored. Rule changes advance writer revision and may withdraw selected readiness; do not reuse old capture evidence or claim selected readiness remained qualified. Native clears must retain original labels/start and explicit endsAt through the same pinned receiver. After CLI cleanup, separately observe resolved callbacks and exact History (current `cleared`, original source severity retained) before stopping the cohort. CLI CLEAN by itself proves only clear forwarding. Missing evidence remains bounded/inconclusive and never authorizes a guessed late clear.
- [ ] **Archive and join resources.** Preserve canonical report, exact-request SQLite/timeline and export (or explicit export-limit error), independent observations and failures on both MySQL versions. Retain source responsibility on interruption; inspect owner identity and join every owned actor before existing offline restoration. Remove only resources created by this fixture. Run adjacent backend ownership/grant regressions, one fresh whole-cut review, and document gaps before committing the backend fixture. The already verified CorrTest 889-test suite need not rerun without a product change.

## Explicit limits

This joined cut would qualify one ordinary lab profile and one Flood scenario through real infrastructure. It does not qualify production profile availability/auth, arbitrary native grouping settings, notifier delivery, receiver selected admission, source epoch/order, source completeness, automatic physical recovery or interrupted-run cleanup replay. Unchanged Splunk/common production payloads remain eligible for ordinary window correlation according to their actual contracts; this lab profile is not a demand to change them.
