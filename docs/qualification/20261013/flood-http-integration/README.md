# Flood real-client HTTP integration and uncertain cleanup — 2026-10-13

Scope: CorrTest feature worktree only; loopback `httptest` servers with automatic close, existing independent Flood model and temporary SQLite. No containers, live OSCAR changes, migration, dependency, deployment or push. Parent reviewed the previous co-occurrence cut before this increment.

## Reproduced failures and fixes

- `red-server-hook.log`: new dynamic responder test initially failed to compile because the seam did not exist. `green-server-hook.log` passed after adding the optional stateful responder; scripted queue priority and credential redaction remain intact.
- `http-initial.log`: initial six real HTTP fixture cases passed (lifecycle and no-match plus malformed History/audit and audit 503).
- `red-ambiguous-injection.log`: four targeted failures reproduced. The runner lost a stored first input on 503, reported CLEAN when first input History was absent, omitted the failed eighth/refire attempt, and reported CLEAN after using old episode History for an uncertain refire.
- `green-ambiguous-injection.log`: ten HTTP cases passed after retaining the attempt before the POST and requiring exact named episode evidence for uncertain cleanup.
- `red-indeterminate-refire.log`: a 200 response with unknown acceptance still allowed old refire History to produce CLEAN. Fixed by retaining ATTEMPTED for all nonaccepted results, including a success HTTP status with indeterminate acceptance.
- `green-focused.log`: runner, test server, domain and SQLite suites passed after that fix.
- `full-suite.jsonl`: **710 tests/subtests passed across 27 packages**, no failures. Includes 14 real-client HTTP cases plus strict unknown-status fact validation. Full suite invoked with `go test ./... -count=1 -json`. `final-responder.log` also passed after adding synchronized responder publication to the constructor. Parent whole-cut review found no actionable defect.

The final HTTP matrix covers valid lifecycle/no-match, missing `startsAt`, missing explicit null audit identity, wrong audit fingerprint, audit 503, first input stored then 503, first response lost by transport after storage, first History absent, refire stored then 503, only old History after refire 503 or 200-indeterminate, conflicting exact-episode fingerprints, and wrong identity labels. It asserts no automatic duplicate POST, retained uncertain status/episode facts, correct latest-episode cleanup in descending History order, no cleanup for unproved source identity, and preserved NOT_PROVEN/NOT_TESTED report boundaries.

Production fix: retain in-memory attempt before Inject; retain HTTP status or explicit unknown 0, prepared startsAt, case/event index and indeterminate classification. Error cleanup maps the latest attempt back to its immutable planned source, then requires exact startsAt, all expected labels (including presence), and one authoritative fingerprint before clearing an uncertain named input. Absent/delayed/stale/ambiguous evidence is DIRTY. Unbound legacy uncertain inputs cannot be automatically resolved. No retry of the uncertain input is added. Accepted inputs keep existing cleanup behavior.

## Limits

This proves local runner/client/wire behavior against a test server, not receiver admission, Native Alertmanager routing, backend correlation correctness, a rule-load barrier, accepted intent, recovery completeness, parent lifecycle, notification delivery, crash safety or performance. Attempts are not durably journaled before send; process death can still interrupt finalization. CLEAN means cleanup request acceptance under the established contract, not terminal recovery. The full backend capacity gate and all 39 UAT acceptances remain open. No unsupported absence/collector-health or ticket-provider test was run.

All log artifacts are gzip-compressed with mtime 0; failures and passes are both retained. No changes remain on main from this increment; all mutations used the absolute feature-worktree working directory.
