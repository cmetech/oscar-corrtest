# Owned-cohort driver for the real CorrTest feed adapter

This test-only executable calls the real `ExistingFeedLabInput` adapter for the finite Splunk/middleware firing fixtures. It supplies the durable local recording prerequisite for a joined backend cohort proof. It is not the user CLI or generic Runner and does not create rules, observe Native/History, or clear sources. No live cohort has been run for this cut.

## Executable contract

Build the package's test executable for the owned runner's OS/architecture using `go test -c -o <owned-build-path>/existing-feed-cohort.test ./internal/oscar`. For a Linux runner, set `CGO_ENABLED=0 GOOS=linux` and the matching `GOARCH` at build time. Do not use a different source checkout or silently rebuild after recording its hash.

The backend owner invokes exactly one opt-in test per phase:

```sh
CORRTEST_EXISTING_FEED_LAB_CONFIG=/proof/feed-config.json \
  /proof/existing-feed-cohort.test \
  -test.run='^TestExistingFeedOwnedCohort$' -test.v -test.timeout=150s
```

The driver bounds a phase at two minutes. Without this environment variable it skips. A nonzero exit forbids replay or advancing the feed. Retain stdout/stderr, the complete state directory and the owner's resource records before disposal; absence of a response or result file is UNKNOWN, not permission to rerun.

`feed-config.json` is one strict JSON object with these fields:

| Field | Value |
| --- | --- |
| `manifest` | Complete `ExistingFeedManifestSpec` from the [frozen manifest contract](existing-feed-manifest.md), including actual base64 Mapping bytes, fixed source path/digest, top-level profile hash, whole-second UTC `startsAt`, stable cohort/target IDs, receiver origin, `userAgent: owned-existing-feed/1`, `requestHost: owned-receiver` |
| `receiver` | Existing `domain.Target` JSON: `id` and `baseUrl` exactly match the manifest, `apiProfile: public-v1`, explicit receiver credential reference, existing TLS policy; no `ordinaryWebhook` |
| `mappingSnapshotPath` | Path to the same original read-only Mapping fixture mounted for the receiver, such as `/proof/mappings.json` |
| `stateDir` | Existing absolute private directory with mode 0700, such as `/state/splunk`; retained across every invocation for this profile/cohort |
| `phase` | Exactly `first`, `remaining`, or `no-match` |

For the existing HTTPS receiver fixture, a receiver target can use `baseUrl: https://127.0.0.1:9443`, `tls: {"insecure":false,"caPath":"/proof/ca.pem"}`, and `credential: {"kind":"file","reference":"/proof/receiver-key"}`. The certificate must validate the connection origin. The adapter separately preserves the normalization Host `owned-receiver`. Credentials remain referenced files; do not embed their values in config or evidence.

The backend must generate the full manifest from its already pinned bytes and original start instant, not guess hashes or supply only a top-level acknowledgment. The driver validates the known profile/snapshot pair and compares the mounted Mapping file byte-for-byte before each send. Runtime registry/mapping immutability and lab ownership remain **external prerequisites**: reading a caller-provided file does not prove what a receiver has loaded.

The three phases are fixed: `first` sends index 0; `remaining` sends indices 1–4; `no-match` sends index 5. The backend owner must wait for its existing Native and first-History observation before `remaining`, and maintain its existing window/audit gates before `no-match`. Driver phase success means only all phase requests were acknowledged forwarded. It does not substitute for these observations.

## Retained state and interruption

`manifest.json` freezes the entire config except `phase`. The remaining filenames are `0.attempt.json` through `5.attempt.json` and matching `.result.json` files. Attempt records contain full `ExistingFeedRequestEvidence`; results retain the request body SHA-256, actual `InjectionResult`, and any transport error. No credentials are stored.

Every file is created exclusively with mode 0600, written, fsynced, closed, and followed by parent-directory fsync. The request recorder returns success only after those operations. Partial files are deliberately retained. On reopen the driver refuses changed/partial manifests, absent or unreadable paired files, corrupt/mismatched attempts/results, and any result not known forwarded. A completed current/later phase is also refused. Missing earlier attempts prevent advancing. Concurrent attempts cannot overwrite the exclusive attempt file. Failure during recording sends nothing; failure after a possible send cannot be retried.

This journal is private qualification state, not a production idempotency service. Its integrity depends on the owner's retained directory and lifecycle. Do not delete or replace files to force a rerun. Process-kill controls establish local retention/reopen behavior, not power-loss, filesystem or distributed exactly-once guarantees. Directory fsync failures are fatal and retain the created files.

## Backend hook and remaining integration

The concrete backend hook is the firing branch in `utils/tests/ordinary_existing_webhook_probe.py`, currently using `httpx.ASGITransport` and looping over `config['bodies']`. Keep its compiled shipped profile, `FixtureMappings`, fixture auth, strict precondition negative control, Native publication runtime and `verified_native` checks. Start a private receiver listener using the existing `corrtest_ordinary_process.py` lifecycle pattern, then invoke the Go executable for the matching phase instead of the Python positive POST loop. Retain a separate per-profile state directory across phase containers. The profile hash, Mapping hashes, executable/source hashes, read-only mounts and exact resource ownership must be independently checked by the owner.

`utils/tests/test_ordinary_existing_webhook.py` already sequences first, remaining and no-match publication with full Native identity, first-child History, window membership, audit, broker ACK and terminal History observations. Preserve those checks and its bounded owned Native disposal; replacing the sender does not turn lab disposal into source recovery. The existing receiver's Native observations must still pass full labels/start/fingerprint checks and be retained for the backend's original-identity cleanup validation.

Actual CLI/Runner integration remains separate: define an existing-feed input plan and ordinary-label identity scope, atomically persist unique feed attempts and full manifest evidence in SQLite, refuse restart injection, adapt History/audit binding without reserved run labels, and preserve UNKNOWN/DIRTY through interrupted reconciliation. The generic Runner's reserved-label matching and source-clear flow cannot be reused unchanged. No schema, endpoint, main CLI, generic Runner, production grants or automatic cleanup was changed here.

## Local verification

Initial behavioral RED reproduced absent driver execution, missing durable send/result behavior and refusal-control prerequisites. A later corruption control reproduced acceptance of a result marked forwarded whose raw body said rejected; result validation now rejects that contradiction and noncanonical/unknown result fields. Controls cover both feed profiles, all phases, exact evidence-before-HTTP, repeat/reopen refusal, missing/changed snapshots, private state, partial/corrupt/orphan records, ambiguous responses and real child-process kill after recorded POST but before response.

The final `go test ./... -count=1 -json` run passed **1023 tests/subtests across 27 packages**, with zero failures. The new explicitly owned-cohort opt-in test and the existing actual-Redis opt-in test skipped; two command packages had no test files. Focused cohort controls also passed under `-race`, including the child-process interruption. Building the test executable and invoking the documented entrypoint without an owned config correctly skipped. [Retained logs and hashes](qualification/20261016/existing-feed-cohort-driver/logs.json) separate the initial RED, conflicting-result RED, initial GREEN, final race controls, final suite and executable entrypoint check. [Root review](qualification/20261016/existing-feed-cohort-driver/review.md) found no blocker. No live/external lab calls or backend source changes were performed by this driver implementation.
