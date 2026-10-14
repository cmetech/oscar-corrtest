# Ordinary webhook input implementation plan

Scope: one explicitly ordinary `inputPath: ordinary-webhook`, fixed `corrtest_ordinary_v1` profile and one basic named-source Flood fixture. Selected `receiver-admission` remains refused. Existing direct scenarios keep their current transport. No source epoch/order/accepted-intent claim, live changes or new backend endpoint.

## Interfaces and files

- `domain/target.go`: optional `OrdinaryWebhook` endpoint, separate credential/TLS and explicit validity seconds (60–3600, no inferred default). Endpoint ends `/api/v1/webhookreceiver/webhook/corrtest_ordinary_v1`; no query/userinfo/fragment. Persist nullable JSON in migration0003 and target repository; history/CLI/target UI carry only references.
- `oscar`: route-scoped client construction for a compiled plan; public rule/History/audit client stays unchanged. Receiver body is one object with labels/annotations/startsAt/endsAt, no status/fingerprint/group. Record exact body and endpoint before send; separate receiver credential/client, no redirects. Only200 `{status:ok}` plus the matching `X-Oscar-Profile-Revision` acknowledgment means forwarded; acl_denied and other outcomes never qualify. Add explicit `forwarded` class.
- Bound scope to Flood, named unique firing sources, no refire/repeated source or user recovery stimuli in this first fixture. Freeze millisecond source start plus configured validity horizon. Reject execution before mutation if validity does not cover full scenario plus 60s preflight and 30s cleanup budgets. No renewal.
- Receiver client retains exact original source labels/start and forwarding certainty. Source cleanup requires exact matching History episode and known forward; sends explicit recovery endsAt through same receiver with unchanged original labels/start. Unknown POST blocks native cleanup even if History appears. No blind retry. Synthetic cleanup stays direct. Cleanup bodies also record before send. Source expiry/forwarded clear never proves physical recovery.
- Ordinary preflight probe runs only inside a durable scenario: record requests, observe exact source History and clear via same receiver; unresolved probe makes run cleanup DIRTY. Doctor remains explicitly direct and does not claim ordinary receiver readiness.
- Compiler/scenario schema and generated JSON expose ordinary inputPath without relaxing receiver-admission. Preview shows receiver body/path and unresolved target/timestamp placeholders; public rule/audit stages stay public. Reports/CLI/UI state ordinary forwarding versus selected admission and unproved Native callback/physical-clear evidence.

## Test-first vertical slices

1. Target persistence/config round-trip, URL/credential/validity rejection, CLI and UI binding. RED then minimal GREEN.
2. Real HTTP-client request/credential routing and exact request recording;200ok versusacl_denied/503/lostresponse; timestamp precision and same-path clear; unknown or stale/refired History refuses clear. RED then GREEN.
3. Route-specific durable probe, execution and preflight failure cleanup; runtime metadata and existing direct doctor behavior. RED then GREEN.
4. One Flood positive+below-threshold fixture through separate receiver/public testservers using real client, canonical body→native/callback model; broken forwarding/History/audit mustfail orremaininconclusive. No actual native-grouping acceptanceclaim.
5. Preview/schema/export/report regression, existing direct suite. Archive failures/passes as deterministicgzip. Full `go test ./... -count=1 -json`; fresh-context whole-cut review, onefixpass and final suite; feature commit/clean.

## Review focus

No credential crosses to the other endpoint; no200acl_deniedforwarding; no downstream ResolveHistory used for native sourceclear; exact original native label/start preserved; ambiguous original cannot be overtaken by cleanup; unresolved probe cannot becomeNOT_REQUIRED/CLEAN; validity expires only as transporttimeout, neverphysicalrecovery. Timestamp equality is semantic milliseconds, not `.123Z` versus `.123000Z` string equality.

Backend mapping is user-provisioned from the verified `corrtest_ordinary_v1.json` artifact. It projects labels/annotations and parses both times without static label additions; current handler adds generatorURL only. Backend contract proof is provided by parent before implementation qualification. No API/config deployment is performed by CorrTest.
