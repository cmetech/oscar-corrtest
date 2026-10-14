# Repeatable correlation reliability testing

CorrTest now supports named source lifecycles for Flood and other existing scenarios. This is preparation for consistent E2E runs; it does not turn the older audit-only campaign into acceptance of the selective-admission feature.

## What works now

`scenarios/reliability/flood-lifecycle-downstream.yaml` creates five distinct children, recovers A and B independently, then refires and recovers A with the same labels and a new firing episode. Its negative case remains below five children. Both use the existing temporary-rule ownership, bounded observation, saved report and cleanup path.

```yaml
inputPath: alert-engine
# In a case's events:
# - {role: interface_down, source: A, status: firing}
# - {role: interface_down, source: B, status: firing, delay: 1s}
# - {role: interface_down, source: A, status: resolved, delay: 5s}
# - {role: interface_down, source: A, status: firing, delay: 7s}
```

`source` names one child within its role and case. Its first firing creates a unique run-owned label set. A resolved event reuses that exact set. Refire is allowed only after resolution, with a later event time and no replacement labels. It preserves the label fingerprint while changing `startsAt`. Named offsets must use whole milliseconds and the case time anchor is truncated to milliseconds, matching OSCAR History storage precision; the smallest refire advance is 1 ms. The runner records prepared episode timestamps before sending each named stimulus. Recovery carries the original `startsAt` and its scheduled `endsAt`. Cleanup retains the latest known firing episode returned by History when present, independent of ascending or descending API row order. A cleanup request being accepted is not terminal recovery proof.

Omitting `source` preserves existing behavior: each firing creates a distinct source, and resolution addresses the latest firing for that role. Existing scenarios do not silently change to named-source behavior.

## The injection boundary matters

The implemented adapter sends **POST /api/v1/alerts to OSCAR Alert Engine**. It supplies an Alertmanager-shaped envelope directly; it does not prove delivery from Native Alertmanager, native grouping, receiver selection, the selected-input journal or producer barriers. `inputPath: alert-engine` makes this explicit and is the default for older scenarios. `inputPath: receiver-admission` can be stored as a requirement but compilation/execution is refused before any OSCAR API mutation. It never falls back to downstream injection.

Saved reports include these boundaries, and CLI run summaries and the run-detail page display them. The operator's `pipeline_mode` flag remains a declaration, not proof of deployed capabilities. Shadow mode may PASS its audit assertions while synthetic assertions are SKIPPED; this remains **downstream audit evidence only**.

Scenarios without checkpoints retain terminal observation after all stimuli. The Flood fixture now checks audit counts after four and five inputs, then checks current source History counts after partial recovery and refire. Audit counts alone do **not** identify a recovery/refire episode. The new History checks establish downstream visibility for the exact sent episode, not that correlation applied the transition, that a parent stayed open between polls, or that clear was safe. A prepared stimulus is an attempted input, not an accepted-intent receipt. History visibility is not notification-delivery confirmation. Missing source/audit evidence remains INCONCLUSIVE, and unsupported assertions are rejected.

## Staged checkpoints

A case may declare up to 16 checkpoints, ordered by their one-based `afterEvent` index:

```yaml
checkpoints:
  - name: before-threshold
    afterEvent: 4
    window: 1s
    assertions:
      - {kind: audit-count, outcome: parent_emitted, equals: 0}
```

The runner pauses after the fourth accepted HTTP input, reads History/audit for only that sent prefix, and polls for the entire checkpoint window before proceeding. Assertion counts are cumulative for that prefix and rule, not a delta since the preceding checkpoint. All assertions need an explicit `equals` value. A skipped assertion (including synthetic counts in shadow mode) makes the checkpoint INCONCLUSIVE, even if its other assertions pass. A zero-count violation observed at any poll stays failed even if it disappears by the final read. Polling does not prove that an unobserved transient never occurred between reads.

Checkpoint names must be unique within a case; event indices must increase. Windows are positive, at most two minutes, within the case/scenario observation budget, and must fit before the next declared event delay. Delays remain absolute from case start: checkpoint waiting does not add another full delay. Slow API calls can make later inputs late; they do not authorize extra run time or change the declared event timestamp. The existing overall run deadline still applies.

On FAIL, INCONCLUSIVE, cancellation or read error, later stimuli stop and owned cleanup runs. The canonical report retains checkpoint snapshots, assertions and intervals, including earlier checkpoints when a later operation fails. The timeline records start/completion; CLI run summaries and `runs show` print checkpoint verdicts. Preview shows the pause and the existing History/audit reads. A missing cleanup identity remains DIRTY. Accepted cleanup requests remain distinct from proven terminal recovery.

## Current source episode History checks

```yaml
- name: two-sources-visible-as-resolved
  afterEvent: 7
  window: 1s
  assertions:
    - {kind: source-episode-state-count, outcome: resolved, equals: 2}
    - {kind: source-episode-state-count, outcome: firing, equals: 3}
```

This assertion requires every event in the case to name its source. It counts each source once, binding its latest sent firing episode to the exact millisecond `startsAt`, original input labels (including presence of empty-valued keys) and authoritative History fingerprint. Only `firing` and `resolved` are supported outcomes. It does not count old episodes as current recoveries. The result is a count across named sources, not an assertion of which individual source is resolved.

Missing timestamps, a stale-only or future row, changed required labels, unknown statuses, competing fingerprints or conflicting statuses for the same current episode make the result INCONCLUSIVE. Old rows may coexist with an unambiguous matching current row; identical current duplicates count once. Every candidate row and expected timestamp are retained in `sourceEpisodes` in the canonical checkpoint/case evidence. A zero-count check with missing episode evidence at any poll stays INCONCLUSIVE, even if evidence arrives by its final read. For this assertion, an observed zero-count violation stays failed in terminal observations as well as checkpoints. Positive counts may become satisfied by the deadline. API lag can therefore cause a conservative inconclusive or failed checkpoint; it does not justify relaxing episode identity.

Checkpoints containing only source episode assertions query History without requiring correlation audits, notification records or synthetic parents. Mixed checkpoints retain the audit eligibility required by existing assertions. Old audit rows cannot substitute for current episode History. Existing cumulative audit assertions remain cumulative and do not become episode-scoped by being combined with this new assertion.

A visible `resolved` source is **not physical-clear authority**. No accepted-intent receipt, producer completeness, correlator application checkpoint, delivery receipt or parent recovery evidence is exposed by this assertion. The report's lifecycle/clear coverage warnings remain unchanged. The sample's short intervals illustrate the contract; live lab timing still needs qualification.

## Next shared backend contracts

1. A supported authenticated receiver input adapter that exercises actual selection and recovery-owner lookup. The current private native-array receiver endpoint is ordinary-only and is not this adapter.
2. Read-only, versioned evidence tying accepted inputs, firing episodes, bundle revisions, parents/members, delivery receipts and expected producer barriers to the run. No endpoint is invented by CorrTest.
3. Staged observation: send five children; prove one parent; recover four; prove the fifth still blocks clear; recover the fifth with complete coverage; prove one safe parent clear; refire without stale recovery affecting it.
4. Interleaved ordinary controls proving normal delivery and no exact-source persistence, plus restart/retry and missing-evidence cases. The current negative below-threshold case still matches a correlation rule and **is not an ordinary no-match control**.
5. Exact ownership and cleanup for newly required selector rules and receiver resources, in addition to today's temporary correlation rules.

Enhanced admission, parent dispatch/suppression and physical clear stay disabled. No production capacity figure or any of the 39 UAT cases is accepted by local CorrTest tests. Live SNMP remains deferred; absence/collector-health is unsupported and ticket-provider tests are excluded from reliability qualification. Older built-ins remain for compatibility, not as support claims.
