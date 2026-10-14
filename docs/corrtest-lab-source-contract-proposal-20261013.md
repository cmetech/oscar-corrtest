# CorrTest lab-source contract proposal — 2026-10-13

**Design only. Not implementation-ready, an approved wire protocol, an activation grant, or production source capability.** No endpoint, outbox, source epoch allocator or automatic retry is implemented by this document. Backend protocol review must settle the dependencies below before product changes. Existing `receiver-admission` scenarios remain refused before mutation. Enhanced admission/dispatch/suppression/automatic physical clear remain disabled; no UAT case or capacity target is accepted here.

CorrTest can define source identity and ordering for observations it generates itself. That authority applies only to a dedicated authenticated lab origin; it says nothing about SNMP, generic webhooks, vmalert or a third-party feed. The backend boundary is `oscar/docs/superpowers/specs/2026-10-13-corrtest-source-origin-boundary.md` in the sibling OSCAR feature worktree. The selective-admission addendum and producer-barrier integration boundary remain authoritative.

## Current foundation

Feature commit `36d4a45` preserves exact direct Alert Engine request bytes/hash/target in the existing `alert.attempting` event before HTTP. `internal/oscar/recorded_injection.go:61` freezes/marshals once and invokes a required callback; `internal/runner/runner.go:274` commits the evidence before send. No automatic retry follows an ambiguous response. This improves current downstream evidence but establishes neither source ordering nor receiver acceptance. It does not send a lab envelope.

`oscar_test_event_id` is a stable named child identity reused across firing, recovery and refire (`internal/compiler/compiler.go:183`). It is not an immutable operation ID. `RunEvent.Sequence` counts all timeline events (`internal/persistence/sqlite/run_repository.go:425`); it is not source order. Runtime starts/ends are excluded from the compiled plan (`internal/compiler/compiler.go:70`) and cannot be reconstructed with a new case anchor after restart.

## Minimum proposed operation record

The names below describe a candidate contract, not an existing endpoint/schema.

| Field | Meaning and constraint |
| --- | --- |
| `run_id`, `case_id`, `plan_hash` | Full retained identity and immutable plan binding; short display tokens are insufficient. |
| `principal_ref`, `profile_revision`, `lab_partition` | Nonsecret expected identity binding. Receiver authentication/configuration must independently validate it. |
| `child_key` | Stable named source identity across firing, recovery and refire; current child label can contribute to it. |
| `operation_id` | New immutable ID for each logical stimulus; UUID-sized ASCII fits the existing backend 64-character bound. Never regenerate after an uncertain result. |
| `source_epoch` | Positive integer identifying a verified source lifetime; distinct from receiver producer generation. |
| `source_sequence` | Explicit increasing transition order persisted before send; distinct from event index, timeline sequence and broker/WAL positions. |
| `episode_origin_operation_id` | Original firing operation for this episode. Recovery preserves it; refire uses a new firing origin. |
| `original_source_start_ms`, `native_starts_at_ms` | Frozen original pair, preserved by recovery. Initial lab proposal requires equality and millisecond precision. |
| `observed_at_ms`, `native_ends_at_ms` | Frozen observation and validity/recovery times. Expiry cannot represent source recovery. |
| `previous_operation_id` | Retained predecessor for the same child, including recovery-to-refire continuity. |
| `request` | Exact body/base64/hash/method/endpoint evidence, using a new versioned lab-envelope format only after protocol approval. |

One explicit source sequence can cover all ordinary and selected stimuli within a run-scoped lab partition. A particular child then sees increasing, possibly nonconsecutive values. This order is not receiver producer completeness or a fleet seal. The simplest initial scheduler serializes source operations and stops the partition on uncertainty; this is functional lab coverage, not a throughput benchmark.

The receiver derives adapter capability, trusted scope and observation provenance after authenticating the lab profile. CorrTest cannot assert trusted selector, ownership, coverage or adapter authority in labels. The local frozen-label selector chooses firing admission; recovery resolves retained ownership. Bundle revision, receipt identity and producer generation come from receiver evidence. HTTP body hashes and the backend's canonical retained-frame hash are distinct bindings; neither should be substituted for the other.

## Persistence and interface proposal

The bounded lab ledger can reuse `run_events.detail_json`, backed by current WAL/FULL/immediate SQLite transactions (`internal/persistence/sqlite/database.go:72`). No new table is inherently required. Generic `AppendRunEvent` plus in-memory counters is insufficient: unique timeline rows do not enforce operation identity, source order or episode transitions.

Proposed repository boundary:

```go
LoadLabSourceState(runID) (VerifiedLabState, error)
CommitLabAttempt(runID, expectedLedgerHead, frozenOperation) (CommittedOperation, error)
AppendLabObservation(operationID, expectedOperationState, validatedReceiptOrUnknown) error
```

`CommitLabAttempt` uses one immediate transaction to verify source/profile/plan binding and exclusive writer ownership; compare the expected ledger head; reject operation-ID reuse with changed content; enforce epoch/sequence and child predecessor rules; and commit the complete immutable operation before returning permission for one HTTP attempt. Payload construction is pure local preparation, with no network call inside the transaction. A commit outcome that cannot be established fences sending until the exact record can be reconciled; it must not allocate replacement identity.

The existing `alert.attempting` event can carry a stronger versioned record; subsequent outcomes append observations. `alert_attempts` remains a report projection because its current rows are finalized later (`internal/persistence/sqlite/execution_repository.go:44`). Bounded typed replay must reject missing, conflicting or unknown-version records. Existing guards at `domain/input_responsibility.go:23` must recognize any added record types, conservatively retaining responsibility.

A single active writer/run is a prerequisite, not an existing cross-process guarantee. SQLite serialization can reject stale compare-and-append operations; it does not fence an older process already authorized to send or a late network request. Receiver-supported lifetime/fencing evidence remains necessary. For a bounded first cut, reject concurrent writers and do not resume interrupted partitions automatically. Avoid deleting ledger rows while source, callback, parent action or delivery obligations are unresolved.

## State and interruption behavior

```text
PLANNED
  -> COMMITTED_POSSIBLE_ATTEMPT
  -> selected receipt / ordinary forwarding observation / definitive rejection
  -> dependent-step readiness only when its required evidence is verified

ambiguous response, commit outcome, missing record or restart
  -> FENCED_UNKNOWN
```

These are conceptual source-operation states, not replacements for existing run verdicts or cleanup states. HTTP success alone does not establish readiness for recovery/refire. Native acceptance, applied membership and eventual notification delivery are different observations. The backend must specify the evidence each dependent step requires. A definite rejection also needs a protocol guarantee that the earlier request cannot later become accepted.

- Epoch `1` is proposed only for a demonstrably fresh run-scoped source identity.
- Intact restart retains the epoch/sequence but does not automatically resume.
- Restart never implies an epoch increment, producer-generation replacement or a new operation ID.
- Recovery reuses the original frozen episode pair; refire starts a later distinct pair. Repeated-firing/renewal semantics need an explicit contract before inclusion; the current downstream compiler is not that authority.
- A restored/rolled-back SQLite database cannot prove later operations were never sent. It stays fenced pending external reconciliation.
- New run/partition isolation does not clear the old run's obligations.
- No automatic POST replay, timestamp reconstruction, guessed recovery or rule-only discharge of unresolved input work.

Current `RecoverInterruptedRuns` (`run_repository.go:299`) and `RetryCleanup` (`runtime.go:494`) preserve UNKNOWN/DIRTY responsibility, but do not provide safe resumption. History and CorrTest's own timeline cannot prove that an earlier request or correlation action will not arrive later.

## Backend integration dependencies

1. **Actual receiver seam.** Dedicated authenticated lab profile through normal receiver normalization, frozen-label local selection, captured acceptance/WAL and central Alert Engine authority. Existing `CanonicalAlert` cannot carry this evidence merely through labels. The private selected handler/NATS proof path and ordinary-only native-array route are not substitutes.
2. **Trust and source domain.** Bind principal, immutable profile revision, partition and full run outside untrusted labels. Establish a fresh-source/reset contract and prevent restored copies or competing writers from assuming authority.
3. **Receipt semantics.** Bind operation/content, producer generation, bundle revision and retained selected receipt. Keep healthy ordinary no-match forwarding separate and free of selected receipt/WAL/SQL work. Neither a generic HTTP success nor History can be promoted to selected acceptance.
4. **Dependent-step evidence.** Expose agreed evidence for predecessor native acceptance and applied membership. Backend `native_source_continuity.py` and `source_membership.py` currently require retained predecessor bindings; CorrTest cannot invent them.
5. **Unknown/in-flight fencing.** Define how reconciliation prevents a late original request after a later observation, and who can replace a recovered/sealed receiver producer generation. This remains required even if exact wire bytes are available.
6. **Native projection/time policy.** Freeze firing validity horizon and recovery end, source/native original start pair, permitted observation skew and late-submission policy. Current direct-injection firing payloads omit `endsAt`; selected native handoff requires it. Expired firing validity must not authorize a physical clear.
7. **Bounds.** Selected retained input/frame is capped at 64 KiB, including receiver selection evidence; current direct-injection 1 MiB bound cannot be reused. The envelope and accepted rule coverage need an explicit budget. Current ZIP export has a separate 16 MiB per-entry limit and preserves SQLite evidence when export is refused.
8. **Native identity mapping.** Trusted source and partition must map consistently to native labels under `NativeSourceIdentityPolicy` while callers cannot forge control labels. Ordinary delivery must remain unaffected when selected evidence storage is unavailable.
9. **Retention/read interfaces.** Specify versioned reads, lifetime and ownership for accepted intents, source episodes, applied checkpoints, parent actions, delivery and barriers. These are necessary for real E2E assertions and cleanup completion, not just source transmission.

## Required counterexamples before activation

Crash before/after preparation and send; lost acknowledgment; same ID/different bytes or canonical content; stale ledger compare-and-append; restored source state; competing writer; profile or selector change; partial batch (initial proposal uses one operation per request); stale recovery after refire; late predecessor after reconciliation; selected store outage with healthy ordinary no-match; and missing/contradictory native/application receipt. All uncertainty must prevent acceptance/cleanup PASS, without dropping normal production alerts.

Next implementation should follow backend protocol review: the smallest complete transactional lab ledger plus authenticated receiver adapter path, initially serialized. Building an unused outbox or exposing the private selected handler does not establish receiver E2E. Existing five downstream fixtures and the Threshold control retain their current qualification limits.
