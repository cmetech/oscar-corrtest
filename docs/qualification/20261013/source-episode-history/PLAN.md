# Named source episode History assertions

- [x] Add a closed `source-episode-state-count` assertion with firing/resolved outcome, limited to explicitly named source events.
- [x] Bind observation to latest sent source episode (millisecond startsAt), expected source labels and authoritative History identity. Missing, stale, future or conflicting rows remain inconclusive.
- [x] Source-only assertions require History evidence and do not borrow old correlation audit rows as proof of a new episode. Preserve existing assertions and coverage warnings.
- [x] Extend staged Flood partial-recovery/refire fixture, add RED/GREEN counterexamples and full-suite evidence; obtain whole-cut review before commit.

Only downstream History visibility is proved. No physical-clear, accepted-intent, receiver, notification, ordinary-isolation or fleet-coverage authority is added.

Product review pending before commit; full-suite result recorded in README.
