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

## Downstream no-match control

`scenarios/reliability/flood-downstream-no-match.yaml` keeps the normal positive Flood fixture and opts N01 into `control: downstream-unmatched`. N01 sends **five** uniquely named firing sources using an alertname excluded by every compiled fixture rule. Five controls make this distinct from the ordinary four-source below-threshold negative case. The compiler and runner reject overlapping or unprovable fixture predicates before mutation. This narrow check handles only the fixture's exact Flood alertname clauses, not arbitrary operator rules.

```yaml
control: downstream-unmatched
# Explicit unique named firing events are required.
assertions: [{kind: downstream-no-match-count, equals: 5}]
```

The count requires current named episode History still in `firing` status and a positive CorrelationAudit witness for each authoritative source fingerprint: `released_no_trigger`, reason `no_matching_rules`, and explicitly null rule ID, name and pattern. Missing keys or zero-valued rule identity do not qualify. Audit timestamps must be present, no earlier than the expected episode and no later than observation time. Duplicate valid audit rows count the source once. A concurrent incompatible current audit makes the result inconclusive. Empty audit, `no_rule_index`, `no_alertname_label`, wrong fingerprint, stale/future/missing timestamps, changed episode evidence or a bound rule cannot substitute for that witness; missing proof is INCONCLUSIVE and API failure is ERROR.

This control supports only Flood N01, explicitly named single firing events and positive `downstream-no-match-count` assertions without an `outcome`. Recovery/refire is rejected because these legacy audits have no firing-episode field. Existing below-threshold scenarios are unchanged. Controls run in the existing sequential case order, not an interleaved 90/10 load stream. The checkpoint uses its full declared window and terminal control observation uses the case's bounded deadline. A witness proves an observed downstream decision, not uninterrupted health throughout that interval.

The report and visible coverage text explicitly exclude receiver admission/health, selector revision or fleet coverage, selected-table isolation and notification delivery. The saved fixture is not a global-rule snapshot: unrelated deployed rules may match and correctly prevent the required no-match witness. No ordinary-path performance or delivery acceptance is inferred from History or audit alone. Existing owned cleanup applies; accepted cleanup is not terminal recovery proof.

## Staged co-occurrence fixture

`scenarios/reliability/co-occurrence-lifecycle-downstream.yaml` reuses the same runner and assertions for two different required roles in one `site` group:

1. Send named `disk_full`; observe one `enriched` audit and zero `parent_emitted` audits for the full checkpoint interval.
2. Send named `cpu_high`; observe the original enriched row plus one trigger audit. The required clauses are distinct exact alertnames with `min_matches=2`.
3. Resolve the disk source; observe one current resolved and one current firing History episode.
4. Resolve the CPU source; observe two current resolved source episodes.

N01 sends only the disk role and checks enriched/no-trigger evidence through its bounded observation. It still matches one required clause and **is not a no-match control**. Counts are cumulative per fixture rule. `parent_emitted` is the recorded trigger decision in audit-only mode, not confirmation that a synthetic parent was created or delivered. Partial or complete source History recovery never proves parent recovery or physical clear; those remain unproved in reports. API delay may require longer lab checkpoints and correspondingly later event offsets.

The co-occurrence fixture also runs through the real HTTP client, alongside Flood and sequence. Its independent first-trigger model counts distinct matched alertnames per group within the event-time window, updates the stored timestamp when a name repeats, and excludes resolved/unmatched inputs. This follows the ordinary Lua window contract; it does not model the enhanced retained-episode protocol, Redis TTL expiry, cooldown rearming or distributed retries. Local wire faults verify premature-trigger FAIL, missing-episode INCONCLUSIVE and ignored-recovery FAIL. The missing-role control remains a matched rule below its required distinct-name threshold.

## Staged sequence fixture

`scenarios/reliability/sequence-lifecycle-downstream.yaml` sends named `login_failure` followed three seconds later by `privileged_command` in the same site. It observes one enriched/no-trigger decision after the first step, then one enriched plus one trigger audit after the second. The subsequent two recovery checkpoints check exact source History episodes, not parent recovery. N01 sends the same required roles in reverse order and requires two enriched rows with zero trigger rows throughout a bounded checkpoint. This is a matched-but-wrong-order control, not a no-match control.

The real sequence pattern compares the **chronological tail**, ordered by event timestamp with arrival-order ties, against the rule's exact ordered alertname clauses. It does not merely search for a subsequence somewhere in the window. Resolutions do not advance it. The local test model now follows that first-trigger behavior with per-group state, timestamp filtering, fingerprint replacement and a bounded list. Local tests also cover event order differing from arrival order, duplicate identity and expired first steps. Redis expiry, fired-key cooldown/rearming, clock-skew clamping, concurrent workers and broker redelivery remain outside this fixture's qualification. The HTTP tests exercise the real client with independent model decisions; no deployed backend or UAT acceptance is inferred.

## Local HTTP qualification and uncertain input cleanup

Both Flood fixtures also run locally through the real `oscar.Client` HTTP adapter against a recorded test server. This exercises the compiled payload, actual rule/History/audit JSON decoding, different History and Alertmanager fingerprints, named episode timestamps, checkpoints, and owned cleanup. The server uses the existing independent Flood model; it does not implement or qualify the backend. Reports still mark receiver admission, native grouping, producer barriers, notification delivery, and physical clear as unproved or untested.

If an input request fails after OSCAR may have accepted it, CorrTest records an `ATTEMPTED`/`indeterminate` fact, stops further stimuli and does **not** retry the POST. The fact retains the actual HTTP error code, or `0` when no response status is available, and the named episode's prepared `startsAt`. Cleanup discovers exact run-owned History. For an uncertain attempt it additionally requires the exact episode, every expected identity label, and one authoritative fingerprint. Missing, delayed, stale or conflicting evidence leaves cleanup `DIRTY`; old History cannot satisfy a failed refire that reused the same source labels. A failed legacy unnamed input lacks a prepared episode and also remains `DIRTY` rather than guessing a clear. Other positively owned inputs and rules can still be cleaned up.

Before each input POST, CorrTest now commits an `alert.attempting` event to its existing local SQLite run timeline. The event contains the case, event index, source ID and prepared episode timestamp (when named), with an indeterminate outcome. A failed event write prevents the POST. A crash between committing that event and receiving/finalizing the response leaves possible input responsibility; the event proves a possible attempt, not backend acceptance. This adds one local harness write per stimulus, not a write to the normal OSCAR alert path.

Startup preserves `UNKNOWN` for interrupted runs that may have sent inputs, including legacy runs that had reached observation even when no live test rules remain. The existing rule-only cleanup retry can delete owned rules but leaves source responsibility `DIRTY`; it cannot make such runs clean or eligible for deletion just by removing rules. Actual rule-only runs remain eligible for normal cleanup. Interrupted source reconciliation/replay is a separate remaining task: this guard does not automatically resolve sources, retry the original POST, invent a receiver admission receipt or mark a run accepted. The local process-kill/reopen test proves retention of a committed episode attempt after test-rule deletion, not power-loss durability or complete distributed recovery.

`CLEAN` still means owned cleanup requests were accepted under the existing contract, not that source recovery, a physical clear or downstream notification delivery completed. A missing row after an ambiguous request cannot establish that nothing was accepted. Ambiguous or interrupted input work needs explicit source reconciliation; this version's retry command only handles rules. A safe future operation must bind persisted attempts to the original plan and exact History episode. History alone does not prove that an earlier accepted POST or correlation action has finished: delayed original work might arrive after a cleanup request. A backend completion/ordering contract is still needed before automatic reconciliation can discharge all interrupted responsibility. Existing cleanup facts also lack episode/action identity, so they cannot stand in for such a receipt.

## Next shared backend contracts

1. A supported authenticated receiver input adapter that exercises actual selection and recovery-owner lookup. The current private native-array receiver endpoint is ordinary-only and is not this adapter.
2. Read-only, versioned evidence tying accepted inputs, firing episodes, bundle revisions, parents/members, delivery receipts and expected producer barriers to the run. No endpoint is invented by CorrTest.
3. Staged observation: send five children; prove one parent; recover four; prove the fifth still blocks clear; recover the fifth with complete coverage; prove one safe parent clear; refire without stale recovery affecting it.
4. Interleaved ordinary controls proving normal delivery and no exact-source persistence, plus restart/retry and missing-evidence cases. The lifecycle fixture's negative below-threshold case still matches a correlation rule. The separate downstream-unmatched fixture supplies only the narrower witness above; it does not provide delivery or persistence-isolation evidence.
5. Exact ownership and cleanup for newly required selector rules and receiver resources, in addition to today's temporary correlation rules.

Enhanced admission, parent dispatch/suppression and physical clear stay disabled. No production capacity figure or any of the 39 UAT cases is accepted by local CorrTest tests. Live SNMP remains deferred; absence/collector-health is unsupported and ticket-provider tests are excluded from reliability qualification. Older built-ins remain for compatibility, not as support claims.
