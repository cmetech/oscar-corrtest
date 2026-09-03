# OSCAR Correlation — Morning Report (2026-08-30)

Everything below was found in SHADOW mode. No OSCAR config, env, or container was
changed. Nothing was rebuilt, restarted, committed, or pushed. Final state verified
clean: correlation_rules = 0, active_windows = 0.

## BOTTOM LINE
The correlator works for 6 of 8 patterns. Two patterns are broken by real OSCAR defects,
and one of those raises FALSE correlations rather than merely missing them. Five defects
are confirmed with deterministic reproducers; three already have fixes written on disk,
awaiting your review. Two are new as of last night and are NOT yet fixed.

## PATTERN TRUTH — 8 sequential sweeps on an idle box
| pattern       | result        | notes                                              |
|---------------|---------------|----------------------------------------------------|
| flood         | 8/8 PASS      | stable                                              |
| co_occurrence | 8/8 PASS      | stable                                              |
| cross_source  | 8/8 PASS      | stable                                              |
| parent_child  | 8/8 PASS      | but EXPOSED to the D4 ordering race (no ordering logic) |
| absence       | P01 8/8 PASS  | N01 1/8 INCONCLUSIVE = HARNESS artifact, not OSCAR  |
| threshold     | N01 8/8 PASS  | P01 1/8 FAIL = DEFECT 5                             |
| sequence      | P01 3/8 FAIL  | DEFECT 4. N01 also produced a FALSE POSITIVE 1/8    |
| persistence   | N01 8/8 FAIL  | DEFECT 1, deterministic                             |

Also: 10 authored scenarios (flood boundary/window-expiry/group-partition/wrong-groupby,
parent_child multi-child/child-before-parent/group-isolation/tag-only, threshold boundary,
persistence long-window) all passed in their sweep.

No-rule baseline PASSES: with zero rules an alert flows normally, keeps its labels, gains
no correlation labels, and the correlator returns released_no_trigger. Correlation is
additive and does not disturb normal alerting.

## DEFECTS
| # | Sev | What | Fix on disk? |
|---|-----|------|--------------|
| D1 | **HIGH — worst of the five** | JetStream Nats-Msg-Id keyed on fingerprint -> a RESOLVE within ~2min of its FIRING never reaches the correlator. TWO consequences, both PROVEN live: (a) persistence emits a FALSE synthetic parent for a cleared condition; (b) parent_child keeps SUPPRESSING children of an already-cleared incident = **MISSED PAGES, real alerts silently withheld**. CORRELATOR-ONLY. | YES |
| D2 | MED | create returns 422 "rule persisted but rejected by loader" but the row IS written and leaks; /validate accepts what create rejects. | YES |
| D3 | HIGH | Alert View Correlation tab queries the 12-hex fingerprint against a table keyed on 16-hex am_fingerprint -> "not correlated" on EVERY alert, every mode. Its empty-state text blames Shadow, which is factually wrong. | YES |
| D4 | HIGH | No cross-message ordering. No startsAt -> event time falls back to evaluate-time wall clock, and processing is concurrent. sequence MISSES real sequences AND FIRES on wrong-order input. parent_child exposed too. Worse under load. | NO |
| D5 | MED | threshold distinct-count uses a TUMBLING HLL bucket inside a SLIDING window; a burst straddling the boundary loses half its cardinality and never fires. | NO |
Full proof + reproducers: FINDINGS-DEFECTS.md. Flake analysis: FINDINGS-FLAKE.md.
Fix diffs + blast radius + deploy steps: FIXES-PROPOSED.md.

## WHAT I RECOMMEND YOU APPROVE FIRST  (revised after the D1 missed-page proof)
1. D1 (alertmanager nats_publisher) — one-line key change + tests, fix written. It is the
   only defect that causes OSCAR to SILENTLY WITHHOLD REAL ALERTS (parent_child children of
   a cleared incident). Proven live with a delay-controlled A/B. Bigger blast radius than
   D3, but missed pages outrank a broken tab.
2. D3 (adminui) — smallest blast radius, UI-only, no backend restart. Stops every alert
   reading "not correlated". Fix is written.
3. D2 (correlator rules router) — stops the leaked-row failure mode. Fix is written.
Then decide on D4 and D5, which need design calls, not just patches (see below).

## PHASE B IS STILL NOT SAFE
Do NOT flip CORRELATOR_DISPATCH_ENABLED. Independent blockers:
- AlertHistory label-shape defect (correlator posts alerts[].labels as a dict; the handler
  expects the alertmanager v1 list form) -> NO AlertHistory row for ANY alert. Unfixed
  since 2026-08-12, verified still present in the running container.
- Scheduler lag-observer 500s mean the Redis dispatch flag never goes true, so Phase B
  would not actually engage — the test would prove nothing.
- D1, D4, D5 would all turn silent audit-only wrongness into real notifications and tickets.

## D1 MISSED-PAGE PROOF (added after the report was first written)
Delay-controlled A/B on a parent_child rule, identical sequence, ONLY the resolve delay differed:
  ARM A resolve at +14s  (INSIDE dedup window):  child after clear -> suppressed_per_notifier
                                                 parent resolve -> NO AUDIT ROW AT ALL
  ARM B resolve at +156s (OUTSIDE dedup window): child after clear -> released_no_trigger  (correct)
                                                 parent resolve -> parent_resolved_suppression_cleared
=> Within ~2min of a parent clearing, children of the cleared incident keep being suppressed.
   Real alerts are silently withheld. The is_resolved fix in parent_child.py:117 is correct
   but UNREACHABLE under production timing.

## LOAD TEST RESULT (added after the report was first written)
D4 quantified with two 20-group sequence tests (all alerts landed, no loss):
  A then B sequential (~100ms)  -> 20/20 fired (100%)
  A and B SIMULTANEOUS          ->  8/20 fired (40%)
=> 60% of correct-order sequences silently MISSED under within-group concurrency.
Positives from the same tests: no alert loss under 20-way concurrency, no cross-group
contamination, group_by_labels partitioning correct under load. The defect is strictly
within-group ordering — not ingest, not group isolation.

## OPEN QUESTIONS FOR YOU
- D4 fix is a design call: populate/honour startsAt end-to-end, and/or serialise evaluation
  per correlation group. The harness should also always send startsAt.
- D5: make the distinct-count window sliding to match the alert window, or document
  threshold as explicitly tumbling. Behaviour change either way.
- D2 follow-up not fixed: POST /rules/import can still create an unloadable rule. Different
  error contract, so it is a decision rather than an obvious bug.
- absence.N01: harness observation window hardcoded 55s vs last heartbeat at +48s
  (internal/compiler/compiler.go:132-134). Harness fix, low priority.

## TOOLING BUILT
- /test-correlation           full suite on demand
- /correlation-campaign       one resumable iteration (state in CAMPAIGN-STATE.md)
- skills: oscar-correlation-test (how to run + 8 pitfalls),
          oscar-correlation-campaign (loop/handoff protocol)

## CAVEATS — read these before trusting the above
- The harness itself needed 2 fixes to work at all (zone-less timestamps, alertname
  location). Those are UNCOMMITTED local edits in oscar-corrtest/internal/oscar/. A fresh
  clone will NOT reproduce these results.
- A 20-group load test HAS now been run (see LOAD TEST RESULT). It confirmed D4 at 60%
  miss under within-group concurrency, and cleared ingest/group-isolation. Still untested:
  sustained high volume, many DIFFERENT rules active at once, and any pattern other than
  sequence under load.
- Shadow mode means no synthetic alert is ever emitted; all positive evidence is
  CorrelationAudit outcomes, not delivered notifications.
