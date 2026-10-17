# Finite owned existing-feed CLI

`owned-existing-feed` connects the durable SQLite recorder, real existing-feed receiver adapter and Native/History identity checks. It is restricted to six fixed firing fixtures for one pinned Splunk or middleware profile in an externally owned immutable lab. The existing backend owned-cohort qualification remains the stronger SQL delivery/task/window proof. This command's PASS covers original Native and public History identity only.

```
oscar-corrtest owned-existing-feed plan --manifest owned-lab.json --output json
oscar-corrtest owned-existing-feed run --manifest owned-lab.json --output json
```

`plan` validates the strict JSON and prints it without opening the runtime or contacting any target. `run` resolves three previously configured target IDs and requires each origin to match the plan before creating a run. The command uses the normal configuration/state options. Keep the private state database across invocations; deleting it removes its local cohort reuse protection.

## Plan contract

The JSON document has exactly these fields:

| Field | Value |
| --- | --- |
| `apiVersion` | `corrtest.oscar/owned-existing-feed-v1` |
| `manifest` | Complete `ExistingFeedManifestSpec` from the [frozen manifest contract](existing-feed-manifest.md), including exact Mapping bytes, both hashes, source provenance, fresh 32-hex cohort, whole-second UTC start, receiver target/origin and fixed Host/User-Agent. |
| `nativeTargetId`, `nativeOrigin` | Explicit Native observer target ID and credential-free HTTP(S) origin. |
| `historyTargetId`, `historyOrigin` | Explicit public History target ID and credential-free HTTP(S) origin. |
| `ownershipReference` | Nonempty reference (at most 1024 bytes, one line) to the external trusted controller's exact owned immutable lab evidence. |

The three target IDs must be distinct. Targets use normal credential references and TLS policies; no secret value belongs in this file. Receiver and History use the existing API-key transport. Native uses its qualified fixed `publisher` Basic-auth account with the resolved target credential as its password. No custom HTTP client or arbitrary profile/route override is accepted. Unknown fields, trailing JSON, missing snapshots and files over 2 MiB are refused.

The reference is an external precondition, not a locally verified attestation. Caller-supplied names and hashes cannot prove ownership, that the receiver loaded the nested Mapping snapshot, or that a deployment stayed immutable. The trusted lab controller must establish those facts before invocation. This CLI must not be used to execute against mutable real feeds. The original qualified controller's independent profile/mapping and ownership gates are not replaced by this JSON file.

## Execution and retained responsibility

Execution is serialized with other Runtime mutation runs and bounded to five minutes, including each request's existing 30-second transport timeout. Before any POST, all six exact normal-label Native filters must return empty arrays and the complete public History lists must contain none of the cohort's own hosts. Existing, changed or ambiguous scoped identities fail closed; they cannot be adopted. The plan, target snapshots and full manifest are persisted before input. Each request must reserve its complete expected identity and exact bytes/hash in SQLite, append its responsibility event and commit UNKNOWN before transport can send.

Each of six indexes is sent once through the real adapter. Only a durably retained forwarded acknowledgment permits observation or a later index. A missing/wrong acknowledgment, cancellation or outcome-write failure stops the run and preserves UNKNOWN. Known forwarding retains DIRTY responsibility because there is no qualified source-clear contract. No automatic POST retry, resume, physical clear or rule creation/deletion exists in this path.

After forwarding, bounded read-only polling verifies one exact original Native label map/start/fingerprint. That full identity is durably appended before History polling. The public History reader refuses redirects and requires complete consistent pagination metadata and nonambiguous label arrays. Every returned row must carry the shipped canonical lowercase UUID4 identity, with no duplicate ID within or across pages. History is scoped using the fixture's existing `node`/`hostname` or the observed Native fingerprint. One firing History row must retain the original identity: Native `am_fingerprint` and OSCAR History `oscar_fingerprint` remain distinct. Full verified Native/History observations are retained in events and the canonical report. Standard export also includes a [validated durable attempt/outcome snapshot](existing-feed-export.md). A partial Native observation also survives a later History failure.

The report explicitly marks correlation, delivery receipt, semantic task and physical clear as unproved. No reserved run labels or generic source-resolution cleanup are used. `cleanup` explicitly refuses these runs without changing UNKNOWN/DIRTY. Reopening cannot replay a recorded index or register the same exact receiver-origin/cohort/profile under a new run in the retained database. This is local no-reuse protection, not remote idempotency, distributed exactly-once delivery, or protection after state deletion.

CLI JSON preserves the run ID and envelope even when execution reports an error. Exit **4** means retained DIRTY/UNKNOWN responsibility, including a successful identity-only PASS. Invalid plans and errors before input return the existing nonzero error codes. The generic UI Runner/scenario path and deployment-wide uncontrolled-feed integration are not implemented by this command.

## Verification checkpoint

The local suite exercises the actual receiver/Native/History HTTP adapters and SQLite through the finite Runner for both profiles. Controls cover exact body/Host/If-Match, committed UNKNOWN visible in the HTTP handler, recorder failure with no send, missing acknowledgment, cancellation after recorded POST, outcome-write failure, preexisting identities, changed or duplicate identity, redirects, target drift, stale execution and fresh-run cohort reuse. The CLI also runs against real Runtime/SQLite/local HTTP and proves an ambiguous attempt survives close/reopen with no second send.

Initial unimplemented Runner and CLI controls failed. Further RED controls exposed generic cleanup changing UNKNOWN to DIRTY, malformed History responses incorrectly allowing input, and Native identity loss when later History verification failed. The dedicated cleanup refusal, strict bounded History reader and early Native event corrected those findings. These failures and final results are retained under `qualification/20261017/existing-feed-composition`. All HTTP tests are local `httptest`; no Docker, backend source or live lab calls were used. This new CLI has not yet been run through the genuine owned backend cohort.

Before independent review, verification with Go 1.27.0 passed 1172 tests/subtests across 27 packages. The reviewer then found permissive non-null History ID coercion and missing duplicate-ID detection across pages. Additional RED HTTP controls reproduced both findings. `AlertHistoryDetailResponse.ID` in the genuine backend schema is UUID4; the reader and direct manifest verifier now enforce its canonical lowercase format, and the bounded reader retains a unique-ID set across all returned pages. Numeric, object, boolean, array, empty, uppercase, wrong-version and wrong-variant IDs are refused, as are same-page and cross-page duplicates. No legacy numeric-ID exception was added.

Final reviewed-source verification with Go 1.27.0: full suite **1184 tests/subtests across 27 packages**, zero failures; the two existing explicit integration controls skipped and two command packages had no tests. Focused `-race` verification passed **105 tests/subtests across four packages**. `git diff --check` passed. The [source manifest](qualification/20261017/existing-feed-composition/source-hashes.json) freezes thirteen source/test files; [log hashes](qualification/20261017/existing-feed-composition/logs.json) bind five RED logs, pre-review results and the final focused/full/race evidence.
