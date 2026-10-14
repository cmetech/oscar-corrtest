# Recorded direct-ingress request evidence — 2026-10-13

The runner now requires `InjectRecorded`. The real client prepares one Alert Engine payload, freezes its exact JSON bytes and invokes the runner's before-send callback. That callback commits the existing `alert.attempting` event before HTTP starts. Its nested versioned `request` evidence contains base64 body, SHA-256, byte length, method, content type, target ID, API profile and initial URL. The client then sends those same bytes. No extra table, transaction, source outbox, endpoint, retry or receiver capability was added.

The 1 MiB payload limit bounds persisted body size. Named episode times are prepared once; legacy missing times remain absent. Authentication headers/credentials are never included in request evidence. Original URL userinfo, query and fragment are refused before recording or send. User-supplied body values remain exact evidence and must not contain secrets. A copied HTTP client refuses redirects only for recorded injection; legacy `Inject`, cleanup, preflight and other calls retain existing behavior. Source/target strings describe the requested direct Alert Engine operation, not authenticated source provenance or acceptance.

Append failure prevents POST. Response loss or error after recording retains possible input responsibility without retrying the original request. Cancellation after recording may conservatively retain an unsent attempt. Startup still leaves possible interrupted inputs UNKNOWN; rule-only cleanup cannot discharge them. These bytes enable later inspection, not authorized replay or safe source reconciliation. `CLEAN` still means cleanup request acceptance, not terminal recovery. No new writes enter the normal OSCAR alert path.

## Evidence

- `red-client.log`: the initial twelve behavioral cases failed because the required recorded client method was absent. Evidence type scaffolding existed solely to compile these tests.
- `green-client.log`: client suite passed after the preparation/send boundary was implemented.
- `red-runner.log`: real HTTP POST preceded exact committed payload evidence; its target binding was missing. Existing generic attempt-marker coverage alone did not catch this.
- `green-runner.log`: runner, client and model suites passed after making the callback mandatory and adapting all four injector doubles explicitly.
- `initial-crash-test-failure.log`: initial subprocess test failed before injection. Diagnostic rerun identified a missing test API key on rule validation; the fixture credential was supplied. This was a test setup failure, not a backend failure.
- `green-durable.log`: focused client/runner tests pass. They compare committed bytes/hash with actual wire bytes, exercise firing/resolved/legacy timestamps and mutation after preparation, prevent POST on recorder failure, refuse unsafe URL components/oversize bodies/302/307/308 redirects, preserve unrelated redirect policy, and retain ambiguous stored-then-503 evidence in exported `events.json` without the configured credential. Cancellation after commit prevents HTTP. A real runner subprocess is killed after SQLite commit and before POST; reopening retains exact body, episode and target binding, marks the run interrupted/UNKNOWN and sends no alert.
- `full-suite.jsonl`: `go test ./... -count=1 -json` passed **815 tests/subtests across 27 packages**, zero failed/skipped tests.

The recorded request is the initial application HTTP body/destination, not TLS/socket bytes, proof of server acceptance, an operation ID, producer epoch/sequence, a receiver capability or completion barrier. Public `/api/v1/alerts` still bypasses receivers and Native Alertmanager. Existing local HTTP fault tests continue exercising ambiguous transport and cleanup. Cleanup/preflight injection is deliberately outside this new runner-stimulus recording boundary. Process-kill testing does not prove disk/power-loss durability. No live services, containers, capacity or UAT acceptance is claimed; ticket providers are excluded and collector-health/absence remains unsupported.

## Review and correction

Fresh-context independent `gpt-6-astra` / high whole-cut review identified one P2: base64-expanded `events.json` could exceed the bundle verifier's existing 16 MiB entry limit while the writer reported export success. The reviewer found no other scoped correctness/security issue and did not rerun tests or contact live services.

- `red-review-export.log`: reproduced with 200 real prepared request bodies, each well below 1 MiB. The oversized export incorrectly succeeded.
- `green-review-export.log`: evidence/client/runner suites passed after a shared entry limit was enforced before archive staging/publication. The regression also checks no invalid/partial archive remains and the prior valid export is unchanged and still verifies.
- `review-fixed-suite.jsonl`: `go test ./... -count=1 -json` passed **816 tests/subtests across 27 packages**, zero failed/skipped tests.

**Aggregate export limit:** a run's recorded payloads may exceed the existing 16 MiB per-entry ZIP limit even though each stimulus is accepted by the 1 MiB request bound. Export now fails explicitly in that case; full evidence stays in SQLite. No truncation, silently incomplete bundle, raised verifier limit or new bundle format is introduced. Large-run split/streamed export is separate future work.

Failures and passes are retained as deterministic gzip artifacts; filenames above omit `.gz` for readability.
