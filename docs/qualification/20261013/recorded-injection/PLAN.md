# Exact direct-ingress request evidence

Approved scope: mandatory `InjectRecorded` callback in the runner's API. The real client builds/marshals one public-v1 Alert Engine request, computes its immutable body/hash and non-secret endpoint binding, invokes a before-send callback, then sends those exact bytes. The callback enriches the existing `alert.attempting` RunEvent; no new table, transaction, outbox, automatic retry or receiver contract.

The versioned request evidence includes method, endpoint, target ID/API profile, content type, body byte count, SHA-256 and base64 body. All fields are values; no headers, credential values or mutable byte alias crosses the callback. Request bodies are bounded at 1 MiB. Unsafe base URL userinfo/query/fragment is rejected before persistence or HTTP. Recorded injection refuses redirects using a private HTTP-client copy; ordinary `Inject`, cleanup, preflight and other client calls preserve their existing transport behavior. Caller mutation after preparation cannot alter the request.

Named starts/ends are prepared once by the runner. Legacy missing times stay missing. Append/commit failure prevents POST; cancellation or loss after commit retains possible input responsibility. Exact request evidence is not acceptance, source ordering, a replay grant or physical-clear authority. Existing UNKNOWN/DIRTY semantics remain.

Implementation sequence:
1. RED: committed event must contain exact wire request; recorded client must preserve bytes through caller mutation, reject callback failure/unsafe targets/redirects, and retain timestamps without credential leakage.
2. GREEN: narrow evidence value + client preparation/raw-body send; required runner callback; adapt the four injection doubles explicitly so overridden observations remain active.
3. Verify persisted/reopened and exported request evidence, failure/lost-response behavior, existing real-client scenarios and full Go suite.
4. Fresh whole-cut review, one correction pass if needed, preserve RED/GREEN logs as deterministic gzip, local feature commit and clean worktree.

No backend/main edits, new dependencies, live service writes, containers, activation or push. Tests are local harness qualification only; no UAT/capacity acceptance.
