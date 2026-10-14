# CorrTest lifecycle stimuli increment

Scope: strict scenario decoder/schema/authoring contract, compiler identity and episode offsets, existing alert request and runner, saved coverage report, tests and a Flood fixture. No backend edits, new endpoint, dependency, activation, container, or live run.

- [x] Address multiple same-role sources explicitly, resolve the intended source, refire with the same labels and a new firing episode.
- [x] Carry event time through the existing OSCAR Alert Engine request; keep old unnamed scenarios compatible.
- [x] Record downstream input coverage. Reject receiver-admission scenarios before mutation until an actual adapter exists.
- [x] Preserve cleanup ownership and terminal evidence limits. Add focused counterexamples, run the Go suite, save failures and passes.

The existing runner observes terminal snapshots after injection. This increment does not establish intermediate parent-open/clear transitions, delivery receipts, ordinary fast-path isolation, or producer barriers. The subsequent staged runner/evidence adapter must verify those before a reliability E2E PASS. Absence/collector-health and ticket providers are excluded from reliability qualification.
