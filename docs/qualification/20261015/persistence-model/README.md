# Persistence first-timer model correction — 2026-10-15

This corrects CorrTest's existing local semantic model, not OSCAR's production Persistence implementation. No new HTTP fixture, public scenario, endpoint, option, dependency, timer framework or activation was added. No Redis, SQLite timer WAL, worker, receiver or deployed backend was exercised.

## Confirmed backend contract

Inspected sibling feature source `oscar/oscar-correlator/src/app/patterns/persistence.py`:

- `PersistencePattern.evaluate`, lines 235–249: configured group labels determine the rule/group anchor and timer identity.
- Lines 255–273: a matching resolved input cancels that group timer and removes its anchor; its fingerprint need not equal the first input's fingerprint.
- Lines 309–346: successful first NX schedules `first_seen event timestamp + unresolved_for_seconds`; repeated input while the anchor exists does not reschedule it.
- `PersistencePattern.on_timer` and `core/timer_subsystem.py:_fire_one_inner` own actual timer dispatch, backlog checks and failures. This cut does not simulate or qualify those mechanisms.

The source hashes inspected are retained in `source-sha256.json`. No sibling source was edited.

## Reproduced defects and correction

The old model matched cancellation only by fingerprint and removed a candidate for any later resolution, even after its ideal deadline had passed. It also used arrival time despite an explicit original `StartsAt`.

`red.log.gz` reproduces five failing examples: another fingerprint resolving the same group failed to cancel; a later clear removed an elapsed result; canceled-first/fresh-input sequences used the old timer; and an explicit source start was replaced by arrival time. `red-tie.log.gz` adds the documented deterministic equality case as a sixth failing example. The remaining controls already passed.

The new pure `persistenceTriggerIndex` helper keeps one pending first input per group, applies matching group cancellation, and returns the first ideal elapsed timer. A source start supplies the deadline when present; absent starts retain the arrival-time fallback. An already-past deadline cannot execute before its input arrives. Another input cannot postpone an existing group's first deadline. A cancellation before any trigger permits a later input to establish a new first timer; this is not post-trigger cooldown/rearm support.

**Model-only tie rule:** an ideal timer runs before a newly arriving input at exactly its deadline; simultaneous timer candidates break ties by input order. This is deterministic local test behavior, not a claim about actual worker ordering. Real deadline/clear races, event-time clamping/skew, delayed dispatch, backlog, Redis errors/TTL, WAL/restart behavior and post-trigger rearming remain unqualified.

The model still represents only the first trigger for a rule. It does not become a durable historical audit log or prove parent recovery. The tests inspect the first-trigger decision and modeled parent presence; no physical recovery or notification delivery is inferred.

## Verification

Commands executed from the CorrTest feature worktree:

```sh
go test ./internal/testoscar -run '^TestSemanticModelPersistence' -count=1 -v
go test ./internal/testoscar ./internal/runner -count=1 -json
go test ./... -count=1 -json
```

- Focused model + runner suite: **255 passing tests/subtests across two packages**, zero failures, one explicit root-owned Redis-oracle skip.
- Full suite: **938 passing tests/subtests across 27 packages**, zero failures, the same one explicit Redis-oracle skip. Two command packages have no test files.
- `git diff --check` passed. Subsequent source change moved only a comment; behavior and test code were unchanged.
- Actual output is retained as deterministic gzip files. A skipped Redis test is not new Redis qualification; its preceding finite qualification remains a separate committed cut.

Other pattern branches and production code were unchanged. No resources were created and no external services were called. Root owns fresh review and commit. No deployed Persistence E2E, actual timer/WAL qualification, capacity or any of the 39 UAT acceptances is claimed.
