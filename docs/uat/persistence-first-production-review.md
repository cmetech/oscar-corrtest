# Persistence production UAT matrix review — 2026-10-19

An independent read-only reviewer compared `persistence-first-production.md` with the prerequisite specification, original SQL and migration disposition. All four use cases/five timing variants were represented; no claim of executability or live acceptance was found, and cross-policy precedence was explicitly unresolved.

Four findings were applied:

1. Added P22 for unsafe mapping/threshold configuration, aggregate-severity conflicts, capability gating, and failed reload state.
2. Expanded P09 with producer restart continuity, not only worker restart.
3. Distinguished PhysicalSlot3 healthy ineligibility from a selected rule's missing/unusable severity evidence in P12/P15.
4. Made P17 retry mutations explicit: same event ID with changed content/count, versus a new ID reusing an old source order.

This review establishes requirements coverage only. The closed Persistence runner, protected test producer, scoped readiness, actual parent transport/recovery evidence and production feed/precedence contracts remain qualification work. Existing Flood evidence is unchanged.
