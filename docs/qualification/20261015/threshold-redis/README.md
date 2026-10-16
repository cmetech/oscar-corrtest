# Finite Threshold Redis oracle — 2026-10-15

**Actual isolated Redis qualification passed.** The root-owned run used the current backend Lua file with a frozen SHA-256, observed the expected first-trigger, duplicate-value, group and fixed-bucket outcomes, and verified owned key/container cleanup. The opt-in test still skips explicitly outside its owned lane. No deployed E2E or UAT acceptance is claimed.

## Scope

The existing test-only HTTP responder can now take an explicit Threshold oracle. Without one it still refuses multi-value positives. With one, firing audit outcomes come from the observed Redis/Lua tuple, not the exact-set model. HTTP request/response handling, compiler, runner, checkpoints, report and owned cleanup still execute the real CorrTest code. History/recovery and rule APIs remain modeled; Native is bypassed, receiver admission untested and physical clear unproved.

The finite test executes a separately compiled P01 and N01 using chosen device values and a deterministic test clock. P01 observes two values below threshold, a third value triggering once, and a fourth fingerprint repeating the third value without another trigger. N01 observes three fingerprints with the same value without a trigger. Separate controls exercise group isolation and a fixed-bucket boundary. Each HTTP case must stay within one observed event-time bucket. Resolutions exercise modeled source History only; they do not invoke Redis or prove lifecycle recovery.

The current backend Lua file is read from an explicit absolute path and verified against the root's explicit SHA-256. Actual PFCOUNT results are checked for these selected values; no general exact HLL cardinality guarantee is inferred. The test supplies three-device, 30-second semantics only. It does not execute Python ThresholdPattern, qualify production event-time parsing/clamping, TTL/cooldown expiry/rearm, or validate distributed delivery.

## Root-owned invocation

Root must supply a disposable isolated Redis container and a credential-free argv vector for its cached redis-cli. The test creates no containers, installs nothing, evaluates no shell command, and never flushes a database. Do not point this at an application Redis.

Required environment:

- `CORRTEST_THRESHOLD_REDIS=1`
- `CORRTEST_THRESHOLD_LUA=/absolute/feature/oscar/oscar-correlator/src/app/core/lua/add_threshold_and_check.lua`
- `CORRTEST_THRESHOLD_LUA_SHA256=<root-frozen 64-character SHA-256>`
- `CORRTEST_THRESHOLD_REDIS_ARGV=["/absolute/path/to/docker","exec","<owned-container-id>","redis-cli","--json"]`

Run from the CorrTest feature worktree:

```sh
go test ./internal/runner -run '^TestThresholdRedisOracleThroughRealHTTPClient$' -count=1 -v
```

The JSON array is parsed into argv; no shell interpretation or automatic retries. The adapter appends Redis command arguments including the frozen Lua bytes. Missing or malformed opted configuration fails; unset opt-in skips explicitly. CLI errors and malformed JSON/integer evidence fail without model fallback. Every command has a ten-second deadline.

Keys use a random `corrtest:threshold:<nonce>:` namespace. The actual Lua also adds these window keys to its existing `oscar:correlator:active_windows` index. Cleanup removes only the exact owned keys and index members, verifies key absence, and emits `THRESHOLD_REDIS_CLEANUP`. It does not delete the shared index or inspect/modify unrelated members. Root still owns container teardown and records immutable Redis image identity, test source hashes and actual command/env. Require test PASS with no skip, `THRESHOLD_REDIS_ORACLE_PASS`, and cleanup success; a marker before failed cleanup is insufficient.

## Local evidence

- `fixture-compile-error.log`: initial test authoring pointer/value mismatch; not a behavior RED.
- `fixture-source-error.log`: initial fixture source identifiers used invalid hyphens; not a behavior RED.
- `red-host.log`: two reproduced missing-oracle failures: positive refused and unavailable oracle never called.
- `red-parser.log`: malformed replies accepted by the initial parser stub.
- `red-null.log`: JSON null incorrectly decoded to zero by an integer slice; corrected to strict literal integer parsing.
- `green-host.log`: initial focused pass, actual Redis test skipped.
- `green-host-final.jsonl`: **35 passing tests/subtests, zero failures, one explicit actual-Redis skip**.
- `full-host.jsonl`: **927 passing tests/subtests across 27 passing packages, zero failures, one explicit actual-Redis skip**. Two command packages have no test files.

Host checks deliberately supply fake oracle results only to prove refusal/error propagation and that the exact-set model cannot override oracle evidence. They do not qualify a positive Redis result. The subprocess transport check proves literal argv preservation including spaces and shell metacharacters. Source afterward gained only additional real-run diagnostic log lines; no acceptance logic changed.

The author created no resources. Root then reviewed the test-only changes and executed the isolated Redis lane; see [actual run manifest](actual-1/manifest.json) and compressed log. The real Lua observed positive counts 1/2/3 with one trigger, a fourth duplicate value without retrigger, same-value negative counts of 1, and separate group/bucket behavior. All 16 exact owned keys and five window memberships were cleaned up; the disposable Redis container was removed, with no persistent volumes or published ports. Source and Lua hashes stayed unchanged. No new dependency, production adapter, public scenario, backend change, capacity claim or any of the 39 UAT acceptances.
