# Bounded actual Persistence timer qualification

This is a developer component fixture, not a new CorrTest public scenario or a
deployed E2E test. **Actual attempt 2 passed** the bounded component checks in a
cached correlator dependency image with current backend source mounted read-only.
The first actual run failed during fixture cleanup and remains a failure.
No UAT case is accepted here.

## What the fixture runs

`run.py` imports the current backend `PersistencePattern`, `RuleIndex`,
`RedisClient`, `TimerStore` and `TimerSubsystem`. All Python and Lua source under
the supplied backend `src/app` tree is pinned by the digest in `source-pin.json`.
Imports must resolve inside that tree and the digest is checked again afterward.
The pin refers to source, not an installed release or runtime image.

The fixture uses actual Redis, an actual temporary SQLite database in WAL mode,
the normal 100 ms poll loop, a validated 30-second unresolved threshold and
real wall/monotonic time. It does not manually fire a timer or shorten the
production threshold. Redis is connected explicitly with the installed redis-py
client; this does not test the production Redis connection/bootstrap settings.

One rule has independent groups, with run-specific labels and fingerprints:

1. An unresolved positive schedules an original timer. A repeated input cannot
   replace its anchor or move its deadline.
2. A clear with a different fingerprint in the negative input's matching group
   cancels its anchor and both SQLite/Redis timer entries before the deadline.
3. A clear with the positive fingerprint but a different group cannot cancel the
   positive timer.
4. The subsystem stops, SQLite closes and reopens, and only the positive timer's
   owned Redis ZSET member is removed. Actual `replay_from_store` restores the
   original deadline/identity; the canceled timer remains absent.
5. The restarted poll loop is observed for the full deadline plus five seconds.
   Exactly one positive timer decision must occur, never before the deadline;
   the canceled group must produce none. Both timer stores must finish empty.

`AuditCapture` is an in-process fixture sink for actual timer decisions. It is
not the production audit buffer, durable CorrelationAudit or a delivered parent.
Dispatch is explicitly disabled; no emitter or dispatcher is supplied.

## Runtime and invocation

The root session owns provisioning and teardown. Supply an otherwise-unused,
dedicated Redis process/database, its `INFO server` `run_id`, and an explicit URL.
The fixture verifies process identity and an empty database before mutation.
An application/shared Redis instance is unsuitable: the actual poller consumes
the global timer ZSET. The identity/emptiness checks do not isolate another
writer that starts afterward; exclusivity remains a root/runtime requirement.

Use the existing cached correlator dependency image; no image build/pull or
dependency installation is needed. The passing runtime was Python **3.13.15**,
redis-py **8.1.0** and aiosqlite **0.22.1**. These satisfy the inspected Redis and
aiosqlite requirements (`redis>=5.0`, `aiosqlite~=0.19`; the latter permits 0.22.1).
The failed host attempt used redis-py 4.6.0, below the declared Redis minimum.
The fixture now uses the supported async `close(close_connection_pool=True)`;
newer clients may report its deprecation, which is retained in run logs.

The root-owned [attempt 2 manifest](actual-2-owner.json) records the exact
container argv, image identities, read-only backend mount, resource IDs, source
parity and verified removal of both containers. Its cached controller image is
`sha256:e3a34f7d4a024c6f991a8a6477770ab0a5ae2ad809f80d544e78e908ff63d4c3`.
This imports current source into a dependency image; it does not build or qualify
a newly packaged OSCAR release.

With root-provided mounts (`/backend` = backend `oscar-correlator/src`, read-only;
`/qualification` = this directory, writable) and the owned Redis network:

```sh
PYTHONDONTWRITEBYTECODE=1 \
CORRTEST_PERSISTENCE_REDIS_URL='redis://127.0.0.1:6379/0' \
python -B /qualification/run.py \
  --source /backend \
  --source-sha256 08fc0f78b805d14d31333197473db818912339c001957b5c1178689f49c38a8d \
  --isolated-redis-run-id '<owned-40-hex-run-id>' \
  --output /qualification/actual-3
```

Every attempt requires a fresh output directory; existing evidence is never
overwritten. Expect about 35 seconds with an overall 60-second fixture timeout.
Setup/reopen consuming ten seconds refuses the timing claim. A large wall-clock
jump also refuses qualification. No environment target defaults are provided.

`ownership.json` records exact keys/timer members before the first mutation.
`result.json` retains evaluation inputs/decisions, the original SQLite timer row,
observed timer decisions/times, replay results, source parity and cleanup outcome.
The root captures SQLite file hashes and removes disposable database files before
committing evidence. A normal exit deletes only owned Redis
keys and timer members, verifies the database is empty and closes connections.
It never flushes Redis. Failed/incomplete cleanup prevents PASS. Abrupt process
termination can prevent the final evidence/cleanup handler; the root must use the
ownership record or tear down its dedicated Redis resource. No SIGKILL cleanup
or source recovery guarantee is implied.

## Host evidence

```sh
python3 -B docs/qualification/20261015/persistence-timer/test_fixture.py -v
go test ./... -count=1 -json
```

`red-host.log.gz` retains the initial six host tests with 51 failing subcases:
the placeholder validator admitted incomplete/early/missing/duplicate/foreign
evidence and the placeholder source pin ignored source changes. `green-host.log.gz`
records all six tests passing after implementation. These tests exercise the
fixture's acceptance logic and source pinning; they are not simulated backend
timing evidence. `full-host.jsonl.gz` records **938 passing tests/subtests across
27 packages**, zero failures, one existing opt-in actual Threshold Redis test
skip, and two command packages with no tests. That skip is not a Persistence
timing result.

`actual-1/result.json` and its root-owned run manifest/log retain the actual
failure: the installed Redis client lacked `aclose()`. The run observed one
positive trigger, cancellation and replay, but the cleanup exception prevented
final source-parity/cleanup evidence, so the result remains **FAIL**. The root
verified container removal separately. No first-run files were rewritten.

The correction uses the installed client's supported async close method and
records every cleanup error without hiding an earlier failure or skipping the
source-parity check. `fixture-cleanup-syntax-error.log.gz` retains a test-edit
indentation error, `red-cleanup.log.gz` the initial missing evidence failure,
`red-cleanup-confirmed.log.gz` the two clean assertion failures, and
`green-cleanup-final.log.gz` all **eight passing host tests**, including a real
exception unwinding through cleanup. These are fixture checks, not a rerun of
the actual timing lane.

## Actual result

[Attempt 2](actual-2/result.json) passed after **35.167 seconds** of observation.
Its one positive decision arrived at **30.640 seconds**. The original timer was
replayed once; cancellation remained absent before and after replay; final
SQLite timer rows and Redis timer members were both zero. Exact-owned cleanup
and pinned source parity were true. The root additionally verified unchanged
runner bytes and removal of both owned containers. This is one finite observation,
not a timing distribution or throughput result.

## Committed-timer process-kill cut

**Process-kill attempt 3 passed.** Its first attempt failed on a fixture API-shape
assumption, and attempt 2 stopped during Redis startup; both failures remain
recorded. The default orderly invocation remains unchanged. This cut tests a
specific new boundary: the timer schedule and group cancellation have committed,
but the controller is killed while its SQLite connection and poller are still
open. It does not test interruption before those commits.

Root runs the same pinned image/source/isolated Redis setup, adding
`--prepare-kill` to `run.py` and using a fresh output directory, for example
`/qualification/kill-1-prepare`. The controller writes `checkpoint.json` only
after the real positive schedule, repeat and cancellation checks have completed.
The marker preserves original rule/timer/input identities, original event time
and deadline, source/runtime/Redis identity, and the committed cancellation
observation. It is flushed and atomically renamed before root reads and hashes
it. That marker operation is not a claim of power-loss durability.

Root must SIGKILL its owned controller promptly after this marker appears. The
controller remains open and polling, but refuses preparation if no kill arrives
within ten seconds of the original start. Root records exact controller identity,
SIGKILL/exit 137 and `OOMKilled=false`; a marker alone does not prove a kill.
Keep the original SQLite database/WAL and the same isolated Redis process.
Archive/hash the crash files before opening them again, because normal SQLite
recovery/close can change or remove WAL/SHM sidecars.

Start a new controller in the same supported dependency image, with the same
source/evidence mounts and Redis URL, before the original deadline minus five
seconds. Root supplies the checkpoint hash it captured externally:

```sh
PYTHONDONTWRITEBYTECODE=1 \
CORRTEST_PERSISTENCE_REDIS_URL='redis://127.0.0.1:6379/0' \
python -B /qualification/resume.py \
  --source /backend \
  --source-sha256 08fc0f78b805d14d31333197473db818912339c001957b5c1178689f49c38a8d \
  --prepare /qualification/kill-1-prepare \
  --checkpoint-sha256 '<externally-captured-64-hex-checkpoint-hash>' \
  --isolated-redis-run-id '<same-owned-40-hex-run-id>' \
  --output /qualification/kill-1-resume
```

Resume refuses missing/changed checkpoint bytes, source/runtime/Redis mismatch,
normal preparation exit, missing/non-WAL SQLite, missing/replaced/extra timer
rows, unexpected hot state or insufficient original-deadline margin. Read-only
SQL verifies that only the exact original row exists before normal backend store
bootstrap. Original Redis anchor, timer score and cancellation absence are also
checked before replay mutation. Rejected preflight has no authority to delete
owned source state or create replacement timer rows.

After preflight, the actual `TimerStore` reopens the original database, only its
owned positive Redis timer member is removed, and actual `replay_from_store()`
rebuilds that member. No source inputs are reinjected, no rule identity is
replaced, no timer row is seeded and no deadline is renewed. The actual poller
then must produce exactly one original-anchor decision at/after the original
deadline, with the canceled group absent, through deadline plus five seconds.
Exact-owned cleanup and source parity remain required.

Evidence explicitly uses `observation_mode=split_process`, records the gap from
the preparation marker to resumed observation, and checks wall/monotonic elapsed
time within the resumed process. It does not reuse the orderly fixture's
continuous 35-second observation claim. The resume result must be read with
root's external SIGKILL/resource manifest to qualify a process-kill case.

Host checks:

```sh
python3 -B docs/qualification/20261015/persistence-timer/test_resume.py -v
python3 -B docs/qualification/20261015/persistence-timer/test_fixture.py -v
```

`red-resume.log.gz` retains 56 failing refusal checks before implementation;
`red-prepare.log.gz` retains the missing immutable-marker assertion. Nine new
host tests pass in `green-prepare.log.gz`, with the eight existing checks passing
in `green-existing-after-resume.log.gz`. These are fixture contract checks, not
actual process-kill evidence. `full-host-after-kill.jsonl.gz` records a fresh full
Go suite: 938 passing tests/subtests across 27 packages, zero failures, the same
one opt-in Threshold Redis skip and two command packages without tests.

`kill-1-resume/result.json` retains the first actual failure. Root confirmed the
preparation controller received SIGKILL (exit 137, not OOM), and read-only SQLite
preflight accepted the original committed row. The fixture then incorrectly
treated `TimerStore.select_for_replay()` as a full-row API; it actually returns
only `(timer_id, fire_at_ms)`. The failed attempt preserved source parity and
completed exact-owned cleanup, but it is not a passing process-kill qualification.

Root attempt 2 failed before starting the preparation controller: its Redis
`INFO` query ran before Redis was ready. `kill-2-owner.json` records the startup
failure and removal of its sole container. No prepare/resume timer observation
occurred. Root added a bounded readiness poll to its orchestration for attempt 3;
the fixture scripts were unchanged for this retry.

The corrected resume check first verifies the bounded replay projection, then
uses `fetch(original_timer_id)` to verify the complete unchanged row. The
opt-in `test_store_contract.py` reproduces this failure against the **actual
TimerStore and a temporary SQLite database**, then verifies real close/reopen,
rejecting changed anchors and missing/foreign rows. It does not run Redis or
claim process interruption. Run it with the existing backend Python environment:

```sh
CORRTEST_PERSISTENCE_SOURCE=/absolute/path/to/oscar-correlator/src \
python3 -B docs/qualification/20261015/persistence-timer/test_store_contract.py -v
```

Source must match `source-pin.json`; omitting the environment explicitly skips
this actual-store lane. `red-store-contract.log.gz` retains the original exception,
`red-store-contract-confirmed.log.gz` the explicit failing assertion, and
`green-store-contract.log.gz` its passing corrected run. The subsequent discovery
run (`green-host-after-store.log.gz`) passed 17 fixture checks with the actual-store
test explicitly skipped; its separate actual-source invocation passed as above.

[Attempt 3's root manifest](kill-3-owner.json) confirms SIGKILL with exit 137 and
`OOMKilled=false`, externally pinned checkpoint bytes, preserved preopen
SQLite/WAL/SHM hashes, unchanged fixture scripts, and removal of all three owned
containers. [The resume result](kill-3-resume/result.json) matches those original
file hashes and reports **PASS**: exactly one original row replayed, original
identity/deadline preserved, canceled state absent, final timer stores empty,
exact cleanup successful and source parity true. The observation gap was
**1.716 seconds**; the resumed process observed **32.415 seconds**. Its single
decision occurred **30.698 seconds after the original source start**, against
the original 30-second deadline. This is a split-process observation of one
committed timer, not a continuous observation or a timing distribution.

## Explicit limits

This cut can qualify actual first-trigger timer scheduling, pre-deadline group
cancellation, orderly SQLite reopen and hot-index replay in one isolated process.
The additional process-kill fixture only addresses already-committed timer state
when paired with root's actual interruption evidence. It does not qualify
power-loss durability, WAL fsync guarantees, interruption between Redis anchor
creation and SQLite timer commit, interruption during cancellation,
Redis-loss backlog fencing, audit flush failure, parent emit/retry, cooldown/rearm,
deadline-versus-clear concurrency, future-event timestamp clamping, deployed
worker timing under load or clock skew. It does not run receiver admission,
Native Alertmanager, broker delivery, MySQL History, enhanced episode ownership,
notification providers or physical source recovery. Collector-health/absence
remains unsupported and capacity/UAT remains deferred.
