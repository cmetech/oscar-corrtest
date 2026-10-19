# Persistence first-production UAT acceptance matrix

Status: required acceptance coverage, **not an executable pack and not a UAT pass**. The existing installed pack qualifies ordinary initial Flood audit only. These cases must be exercised before claiming the corresponding Persistence production use cases qualified.

Source: workspace `netcool_triggers_automation/OSCAR_Persistence_Occurrence_Gate_Prerequisite_Spec.md`, plus the exact SQL predicates in `automation_highlighted_triggers.sql` and qualifications in `OSCAR_Migration_Disposition.md`. The agreed OSCAR behavior keeps the source unchanged and creates a linked parent. It does not edit the source severity as Netcool did.

## Five rule variants

| Variant | Source eligibility to preserve | Real unresolved duration | Parent |
|---|---|---:|---|
| N30 VPN | NodeAlias exactly `10.40.7.171` or `10.40.7.174`; Type 1; Major; AlertKey contains Epay-Kansas, Epay-LA, Locus-Primary, Telrite-Alcovy, Telrite-Delk, or RedPocket; at least three verified occurrences in the same source episode | 300s | Critical |
| N14 OCP | Node matches `.*ocp4.*worker.*`; AlertKey exactly AGW Unavailable, AIR Unavailable, VGW server unavailable, Database Unavailable, or Connection To Vesta Unavailable; Type 1; Minor; trusted PhysicalSlot >3 | 300s | PhysicalSlot 4→Major, 5→Critical |
| N18 generic | `(OCP worker AND summary excludes Receiver Id is Reach) OR ENS_Stats ZEECOOL OR EBIDB_Connections ZEECOOL OR 10.202.5.124…CPU Load at`; Type 1; Minor; CorrelationFlag 0; CustomerID not 0/1030; SuppressEsclFlag 3; trusted PhysicalSlot >3 | 600s | PhysicalSlot 4→Major, 5→Critical |
| N28 diameter | CustServiceDesc exactly `ATTM - ATTM hosted PPS - SDP`; Type 1; Minor; trusted PhysicalSlot >3; Summary contains CIP Diameter overload | 60s | PhysicalSlot 4→Major, 5→Critical |
| N28 interface | Same common SDP predicates; AlertKey contains FSCSMSInterfac 317 | 120s | PhysicalSlot 4→Major, 5→Critical |

The occurrence threshold is episode lifetime, not a rolling five-minute counter. Use real source-alarm identity for grouping; host/site grouping alone is insufficient for time-only rules. Test both allowed VPN aliases and all six signatures, and all five OCP keys. Missing, malformed or unsupported original severity must block, including PhysicalSlot >5 rather than inventing an OSCAR mapping.

## Required scenario groups

Each row expands to explicit positive/negative runs or parameter variants; it is not a claimed test count.

| ID | Input sequence or variation | Required outcome / retained evidence |
|---|---|---|
| P01 | N30 first firing at t=0; verified count 3 by t=180; unresolved | No parent before t=300; exactly one Critical parent at/after deadline within the qualified scheduler bound. Original Major record unchanged. |
| P02 | N30 count 2 at t=300; third occurrence at t=420 | Due gate remains retained; no parent at t=300; third occurrence wakes existing gate without a new five-minute wait. One parent. |
| P03 | N30 only count 1, then separate case count 2, unresolved for 3600s | No parent; retained reason says occurrence threshold pending. No repeated polling/writes for a parked gate. A shorter accelerated test is integration evidence only. |
| P04 | Third occurrence before timer; explicit source recovery before t=300 | No parent after original deadline; no live pending action for the resolved episode. |
| P05 | Identical transport retry, ordinary notification repeat, unchanged-count new observation, and manual resend | Neither source count nor original timer increases/restarts. Capture distinct transport/notification and source evidence, rather than treating the same fixture event as every path. |
| P06 | Two different alarms share a VPN/site; two occurrences each | No cross-alarm aggregation. One alarm later reaching three creates only its own linked parent. |
| P07 | Resolve then refire same source identity with a new episode | Old pending gate canceled; new age and count start from one. No late old-episode clear/count changes the new parent. |
| P08 | Third occurrence races due timer; duplicate worker/redelivery | At most one durable create action, one accepted parent History identity and one membership. Repeat after emission cannot create another parent. |
| P09 | Restart worker while waiting for age and separately while parked waiting for count; restart the producer with its original durable episode/count/order | Original persisted episode/start/count/deadline survive; no reinjection or reseeding; later qualifying input emits once. Capture external interruption evidence and continuity. |
| P10 | Explicit source recovery after accepted parent | The same linked parent resolves only from verified recovery. Retain create/clear action IDs and History receipts. Source remains its own unchanged record. |
| P11 | Each mapped variant, PhysicalSlot 4 and 5 | Use actual 300/600/60/120s durations, omit occurrence gate, assert no early parent and correct Major/Critical parent. Child stays Minor; no requirement for three occurrences. |
| P12 | Selected rule has missing label, malformed/non-string value, unsupported 6, or no matching severity command | No parent; visible retained mapping reason. Ordinary source delivery remains available. PhysicalSlot=3 instead fails the original eligibility predicate and belongs to healthy no-match P15, not necessarily a mapping error. No silent fallback severity. |
| P13 | Several matching mapping commands with numeric prefixes | Existing ordered semantics and last matching assignment win; rule revision, matched command and trusted input values retained. |
| P14 | Change labels or rule revision after admission; retry parent creation; later clear | Frozen anchor/selected severity remains stable; incompatible rule revision is held/retired with evidence, never silently substituted. Parent clear addresses original identity. |
| P15 | Wrong node, near-match/excluded key, Type, current severity, service, CustomerID, SuppressEsclFlag, CorrelationFlag, or missing required field | No erroneous selection/parent; ordinary source alert reaches History. Test actual classifier and MatchClause, not a separately reimplemented predicate oracle alone. |
| P16 | Same source matches both N28 signatures; also N14/N18 and N14/#15 overlaps | No accidental duplicate escalation. Record explicit approved precedence policy before accepting this row; current source-preserving semantics do not inherit Netcool's same-row mutation exclusions. |
| P17 | Forged count in public labels/payload, regressed/out-of-order counts, same event ID with changed count/content, new event ID reusing old source order, wrong episode, restart discontinuity | Reject occurrence authority or block escalation visibly; no false clear and no silent ordinary-alert loss. |
| P18 | Corrupt/missing retained state or unavailable rule bundle; remove/edit pending rule | No invented parent/recovery. Bounded retirement/reconciliation and healthy new episodes are observable. Keep unreconciled ownership visible. |
| P19 | Omit gate and use fixed severity | Existing time-only behavior unchanged; ordinary non-correlated alerts continue through their normal route without exact-source writes. |
| P20 | Crash or ambiguous parent handoff after acceptance but before receipt; recover | Reconcile original action without duplicate parent; uncertain outcome stays visible and never permits false physical clear. |
| P21 | Repeated cycles and mixed ordinary traffic, idle and loaded | No lost ordinary alerts, duplicate parents, incorrect group merges or growing stale backlog; record workload/latency/backlog/recovery. Capacity is a separate production-hardware qualification. |
| P22 | Invalid/mutating mapping commands, invalid severity output, `highest_active_child` conflict, invalid/boolean/noninteger occurrence threshold, or enable without verified capability | Authoring/import rejects unsafe configuration with a specific error; failed reload exposes stale/unavailable state and cannot grant uncertain admission. Disabled drafts never grant runtime source authority. Existing valid fixed-severity/time-only rules continue working. |

## What a UAT PASS must contain

- Exact source commit/image IDs, schema and pack/selector/rule revisions; exclusive lab ownership and controlled History-only destinations.
- Original scheduled inputs, durable sender episode/event sequence and count records, acknowledgment bytes and pre-send intent. A timeout or ambiguous send is retained, never silently retried into a pass.
- Source/parent History records and fingerprints, source episode identity, incident membership, rule revision, mapped-severity evidence, create/clear action and acceptance receipts. An audit `parent_emitted` label alone does not prove a real parent was created.
- Before/after source labels and severity; explicit evidence for canceled, parked, blocked, cleared and recovered states. UI correlation details and relevant traffic/backlog/error metrics should tell the same story as the stored evidence.
- Sufficient wall-clock **and monotonic** observation after the last input for each real duration. Counts/timers advanced by a fixture clock are useful component evidence, not live UAT.
- Failed and inconclusive attempts preserved alongside passes. Exported evidence hashes verified independently. Retained pack/data remain visible; no cleanup/physical-clear claim without its own proof.

## Current coverage and blockers

The existing backend source/gate/severity tests exercise real SQL lifecycle state with a controlled clock and fixture authority. Fourteen combined production-duration cases were added under `oscar-correlator/tests/test_persistence_production_requirements.py`. The combined new/gate/severity run passed 87 tests with no skips; initial test-oracle failures are retained in backend `docs/qualification/20261019/persistence-production/engine/`. These do not run the public receiver, Native Alertmanager callback, accepted parent delivery, or real waiting periods.

Current CorrTest generic Persistence hard-codes 30 seconds, two cases and bounded windows too short for these requirements. Installed v1 is closed Flood-only, requires exactly its original rule inventory, and cannot be expanded by merely adding YAML files. Preserve its original installation/evidence. A separate closed Persistence contract must pin its own full inventory and coexistence rules.

Current public source bootstrap grants no verified occurrence adapter and runtime rule capability evidence is empty. A source label, History occurrence counter, global boolean or fabricated capability map cannot enable trustworthy testing. Implement an isolated fixture producer with protected identity, persisted schedule/counts and readiness bound to its exact source, selector and rule revisions. Exercise the real selected-source/journal/native/history/lifecycle route. Negative ordinary alerts must use a proven ordinary route: the selected-only worker's no-match NAK is not a fallback route.

Production qualification additionally needs genuine source episode/order/count/recovery guarantees and true original severity (Netcool PhysicalSlot), including treatment of already-open episodes whose count exceeds one. #14's Grade/#15/mail precedence and cross-policy overlaps need an explicit migration decision. #18 does not set TicketStatus despite a legacy comment. Do not enable these production policies or disable their Netcool triggers on the strength of fixture tests.

Ticket-provider tests remain excluded; collector-health/absence is unsupported. No full 39-case acceptance or production-capacity claim follows.
