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

The runner observes terminal snapshots after all stimuli. The fixture's existing audit/count assertions therefore do **not** prove that every intermediate recovery or refire was applied, that a parent stayed open between steps, or that clear was safe. A prepared stimulus is an attempted input, not an accepted-intent receipt. History visibility is not notification-delivery confirmation. Missing source/audit evidence remains INCONCLUSIVE, and unsupported assertions are rejected.

## Next shared backend contracts

1. A supported authenticated receiver input adapter that exercises actual selection and recovery-owner lookup. The current private native-array receiver endpoint is ordinary-only and is not this adapter.
2. Read-only, versioned evidence tying accepted inputs, firing episodes, bundle revisions, parents/members, delivery receipts and expected producer barriers to the run. No endpoint is invented by CorrTest.
3. Staged observation: send five children; prove one parent; recover four; prove the fifth still blocks clear; recover the fifth with complete coverage; prove one safe parent clear; refire without stale recovery affecting it.
4. Interleaved ordinary controls proving normal delivery and no exact-source persistence, plus restart/retry and missing-evidence cases. The current negative below-threshold case still matches a correlation rule and **is not an ordinary no-match control**.
5. Exact ownership and cleanup for newly required selector rules and receiver resources, in addition to today's temporary correlation rules.

Enhanced admission, parent dispatch/suppression and physical clear stay disabled. No production capacity figure or any of the 39 UAT cases is accepted by local CorrTest tests. Live SNMP remains deferred; absence/collector-health is unsupported and ticket-provider tests are excluded from reliability qualification. Older built-ins remain for compatibility, not as support claims.
