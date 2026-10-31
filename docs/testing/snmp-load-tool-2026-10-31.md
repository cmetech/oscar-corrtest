# SNMP load utility — 2026-10-31

Scope: extend CorrTest's existing SNMP check with a native UDP load mode. This is utility verification, not an OSCAR capacity qualification. No OSCAR mappings, rules, notifiers, images or worker settings were changed, and no packets were sent to oscar.home.arpa during implementation.

## Delivered behavior

The Run test page has a separate SNMP load form. It supports nominal 1–25 local sends/s for 10–600 seconds, selectable mixed or individual migrated Juniper DHCPv6, DHCPv4, AAA server and address-pool families, repeated problems and delayed clears. Defaults are 5/s for 60 seconds, clear delay 30 seconds and History observation 300 seconds. Every family receives a separate problem/repeat/clear preflight before bulk traffic.

The total local-send budget includes every transition. Remaining clears run after the offer window. Exact OIDs, varbinds, mapping source hashes and run identities are saved in the immutable plan. Run details and verified evidence export retain local send attempts/results, timing, preflight and exact History reconciliation.

Existing H3C single-pair and webhook checks are preserved. Full run tokens in mapped alert keys prevent the compressed numeric collisions found in the first proposed H3C bulk fixture.

## Adversarial review dispositions

Claude Opus and Codex Astra reviewed implementation and failure boundaries twice. Both found no remaining HIGH blocker in their second review; Opus requested one cleanup change and additional coverage before landing.

- Fixed: preflight crashes and event persistence errors could lose cleanup responsibility. A SQLite transaction now records `alert.prepared` and UNKNOWN cleanup before external I/O. Tests cover interrupted recovery, retention protection and an injected event-write failure rolling back the entire transaction.
- Fixed: numeric H3C bulk keys could collide across runs. DHCPv6 replaces that family in bulk mode; all four keys retain the complete run token.
- Fixed: pacing originally measured before durable preparation and ignored some phases. Every invocation is paced after preparation. Cumulative scheduler jitter has a bounded tolerance of max(1 second, 5% scheduled elapsed); excess lag stops the run. Rate claims explicitly refer to local invocations, not wire emission/arrival.
- Fixed: repeats could occur immediately before clears. Planned repeats leave one second; actual problem/repeat invocation times also bound load clear deadlines.
- Fixed: final polling overwrote preflight results; separate per-family firing/repeat-state/resolved results now remain in the report.
- Fixed: identical pagination overlap looked like duplicate History. Identical rows are deduplicated, contradictory versions of the same ID are unstable/inconclusive, and distinct duplicate IDs still fail.
- Fixed: run details initially fell through to the direct Alertmanager coverage text. SNMP load now has its own coverage description.
- Added: preflight repeats, mixed-family runtime coverage, cancel-during-clear testing, local-failure/unattempted counts, community verification on actual UDP wire, bounded canceled-run observation and evidence export verification.
- Deliberately retained: no automatic replay of ambiguous clear sends. Opus proposed retrying failed/killed clear subprocesses, but a killed sender may already have emitted UDP. The implementation keeps `clearAttempted` explicit, documents that only not-yet-attempted clears are sent during cleanup, and tests DIRTY/non-PASS plus no replay for a canceled clear. This preserves the stated ambiguity contract instead of assuming repeated external side effects are harmless.
- Explicit limit: storage failure stops further external I/O, including cleanup, because those sends could not be durably recorded. Interrupted runs retain UNKNOWN responsibility for external reconciliation.

## Verification

- Full Go suite and `go vet ./...`.
- Race tests for runtime, UI, SQLite and SNMP package.
- Actual Net-SNMP loopback UDP decoding: all four families, each problem/repeat/clear, numeric OIDs, community and typed varbind values.
- Local migration corpus hash and problem/clear mapping parity. This optional corpus check skips when the sibling migration checkout is absent; portable fixture and wire tests remain available.
- Runtime tests: complete lifecycle, four-family preflight, cancellation, cancellation during a clear, missing preflight, slow sender, concurrent-start rejection, generic-cleanup rejection, ledger ordering, export and bundle verification.
- UI valid form submission, CSRF/origin rejection, numeric/profile bounds and SNMP-specific run-detail coverage.

Raw review and test evidence is retained locally under `~/.local/share/oscar-load-qualification/snmp-load-tool-20261031-01/`.

## Live boundary and next run

The local CorrTest service initially had only a saved Beast target and no runs. That target is preserved. Do not select Beast for this campaign: OSCAR's target URL is `https://oscar.home.arpa/ext/mw`; Beast is the sender/sink. The service needs the intended target credentials, a configured SNMP community, Net-SNMP on PATH and confirmed receiver admission/mappings before live use.

Next: an isolated 1/s mixed smoke against oscar.home.arpa, observing each mapped problem/repeat/clear preflight and final owned History. Then separately instrument 5/s, 10/s and 25/s traffic with receiver counters, queues, CPU/memory, UI/tasks and notifications. No sustained SNMP throughput, ticketing, outbound delivery, individual repeated UDP receipt or clear-attribution qualification is claimed here. A resolved History record alone does not prove which clear caused it. A hard process kill may leave the owner-only temporary community file; inspect/remove only the stopped run's residue.
