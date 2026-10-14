# Cross-source test-label fixture and HTTP qualification — 2026-10-13

**Source tags are injected test labels, not trusted receiver provenance.** This cut only exercises direct OSCAR Alert Engine input through the real local HTTP client. No SNMP receiver, receiver normalization or selected-admission path is exercised.

Scope: CorrTest feature worktree only; new fixture, test model and HTTP tests, with no production runner/schema, backend/API, dependency, container, live service, deployment or push. Threshold bucket/timer work was not started.

## Contract and scenario

Read actual OSCAR `patterns/cross_source.py`, `schemas/match_criteria/cross_source.py`, `core/lua/add_crosssource_and_check.lua` and `core/window_matching.py`. The pattern uses `oscar_source` with a `source` fallback, requires the corresponding source/alertname pair, keeps a group-local source map with event-time eviction and last-arrival timestamp overwrite, ignores resolved inputs, and records enriched until the first parent_emitted trigger. Rule matching first selects relevant alertnames; source-pair requirements belong to the pattern.

`cross-source-lifecycle-downstream.yaml` sends two named InterfaceDown inputs with the same alertname/site but different `snmp` and `api` test tags. It checks the first tag, complete mix, partial source History recovery and complete source History recovery separately. N01 sends two distinct inputs both tagged snmp, matches the rule and proves the expected absence of a trigger through its declared checkpoint. That is a missing-required-tag control, not no-match. Scenario/case names and file comments make the test-label scope visible.

## RED/GREEN evidence

- `red-model.log`: five reproduced mismatches—fallback source tag, cross-group mixing, expired source, resolution advancing and last-arrival timestamp overwrite.
- `green-model.log`: model suite passed with eleven Cross-source cases, including source/name pairing, primary-tag precedence, repeated same tag, unknown tag, and repeat refresh controls. This models the first ordinary trigger, not cooldown rearming or TTL expiry.
- `red-http.log`: all four staged HTTP cases failed before support for Cross-source existed in the test responder.
- `green-focused.log`: runner and testoscar suites passed, preserving previous Flood/no-match, sequence and co-occurrence tests.
- `full-suite.jsonl`: **779 tests/subtests passed across 27 packages**, zero failures (`go test ./... -count=1 -json`).

Four HTTP cases cover valid lifecycle, falsely reported same-tag trigger (FAIL), missing startsAt (INCONCLUSIVE) and History ignoring recovery (FAIL). Incomplete intermediate source evidence stops later inputs. Canonical reports retain receiverAdmission NOT_TESTED, nativeAlertmanager BYPASSED and physicalClear NOT_PROVEN.

## Limits

Labels are not source credentials or real receiver evidence. Audit decisions do not prove a synthetic parent was created/delivered; resolved source History does not prove parent/physical clear. No selector revision, producer barrier, notification delivery, fleet completeness, capacity result or any of 39 UAT acceptances is inferred. Redis TTL/cooldown, concurrency, broker redelivery and enhanced retained-episode reconciliation remain outside this local model. Absence/collector-health remains unsupported for reliability qualification; legacy compatibility tests do not establish support. Ticket-provider and live SNMP qualification remain excluded.

## Review disposition

Review found no blocking staged-YAML mismatch. One LOW gap was the nondiscriminating primary-tag precedence control: its original pair would fail under either precedence. Replaced with conflicting-label positive inputs A(oscar_source=snmp, source=api), B(oscar_source=api, source=snmp). `red-review-precedence-mutant.log` deliberately inverts model precedence to prove the new test detects that error; the original correct model was restored immediately. This is a test sensitivity mutation, not a reported backend defect. `review-fixed-suite.jsonl` records **779 tests/subtests across 27 packages passing**, zero failures after the test correction.

The reviewer also identified a test-responder coverage limit: it maps generic Cross-source nontrigger outcomes to enriched, while rejected tags/pairs or recovery inputs can take other backend fallback paths. Current staged YAML only queries audit for admitted firing source/name pairs. Code and coverage docs now explicitly restrict that HTTP qualification; unit model counterexamples outside those pairs test first-trigger eligibility only, not audit fallback outcomes.

Failures and passes are retained in deterministic gzip artifacts. Final review reported no blocking mismatch for current staged inputs and no further test fix was required. No exact fallback-audit qualification was granted.

