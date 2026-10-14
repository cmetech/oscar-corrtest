# Runtime state ownership qualification — 2026-10-14

Base: `c2dfd8776a597919471c344342d5e5f802853e01`. No OSCAR service, live target, container or grant was touched. The previously prepared Linux CLI binary remains unchanged in its separate ignored joined-plan directory.

## Reproduction and correction

A second `Runtime.Open` while the first held an OBSERVING run succeeded and changed that live run to INTERRUPTED. `runs show` and `runs list` use the same startup/recovery path. An in-process run mutex cannot protect against this.

A runtime now acquires a nonblocking OS file lock in its local state directory before SQLite initialization/recovery. The owner retains it through active cleanup/database close; a competing runtime refuses clearly, including diagnostic mode. Existing UI requests share the owner and remain usable. The lock file remains on disk; the OS releases ownership on close/process death. No PID/stale-file takeover, injection resume, new module/version, source retry or backend endpoint was added.

## Evidence

- `red.log.gz`: initial active-run/second-open reproducer fails with live state changed to INTERRUPTED and no error.
- `red-all.log.gz`: five failing ownership behaviors on baseline, with failed-open/crash-recovery controls retained. The repeated-Close test's baseline failure reflects missing ownership enforcement, not proof of a pre-existing close implementation defect.
- `green.log.gz`: runtime/command/web packages pass after correction.
- `cli-green.log.gz`: real command dispatcher + real Runtime/SQLite: show/list refuse while owner UI reads the unchanged run.
- `full-suite.jsonl.gz`: **899 passing tests/subtests, 27 passing packages, zero failures**. Two package-level no-test-file skips: `cmd/generate-scenario-schema` and `cmd/package-zip`; no individual test skipped. The child-process helper returns in the parent enumeration and is exercised in its subprocess by the ownership/crash test.
- `race.log.gz`: focused ownership and CLI/UI integration pass under the race detector on macOS ARM64.
- `platform-compiles.json.gz` and compile logs: runtime tests compile for Linux ARM64/AMD64, Windows AMD64 and macOS AMD64. Temporary test binaries were removed; no platform execution is claimed.
- `git diff --check` and `go mod tidy -diff` pass without changes. `x/sys v0.46.0` was already present and becomes a direct dependency; no version or go.sum change.

## Limits

Execution evidence is macOS ARM64 only; Windows/Linux kernel locking still needs execution on those platforms. State must remain on a private stable local filesystem. Deleting/replacing the state directory or lock file while owned, or launching an older binary that ignores this lock, is unsupported. Stop older binaries before upgrade. Direct external SQLite tools are outside the application ownership contract.

Stateful standalone CLI calls now refuse while `serve` owns the same data directory; use the current UI or wait for shutdown. This is intentional enforcement of the existing single-process design. Separate directories remain independent. CLI process startup may initialize its existing log/environment path before runtime refusal; database migration/recovery is protected. No full correlation E2E or UAT case is accepted from this cut.

Fresh whole-cut review: **no actionable findings**, Astra/high with fresh context. See `review.txt.gz`. No product or test edits after the passing suite; no fix pass required.
