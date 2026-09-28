# OSCAR Correlation Campaign — LIVE STATE
# This file is the single source of truth between loop iterations.
# Each iteration: read this -> do ONE unit of work -> update this -> stop.
# Keep it under ~200 lines. Move detail into the FINDINGS-*.md files.

LAST_UPDATED: 2026-08-30T08:05Z
ITERATION: 8

## MISSION
Prove or disprove every OSCAR correlation pattern (all 8), positive + negative, with
evidence from OSCAR's own CorrelationAudit API. Find defects, write fixes on disk,
deploy nothing.

## HARD RULES (never violate, every iteration)
- NEVER change OSCAR config/env/containers. Never restart an OSCAR service.
  Never flip CORRELATOR_DISPATCH_ENABLED (Phase B is destructive on this box).
- Write fixes to source on disk. DO NOT rebuild/redeploy/commit/push/branch.
- Run correlation patterns SEQUENTIALLY. Parallel runs cross-contaminate windows.
- Clean up every iteration: correlation_rules total MUST return to 0.
- Verify with the 16-hex am_fingerprint label, NEVER the 12-hex history fingerprint.
- The Alert View Correlation tab is BROKEN (DEFECT 3) — never use the UI as evidence.

## ENVIRONMENT
See TEST_CAMPAIGN_CONTEXT.md. Key facts:
  CT=/home/splunk/oscar_latest_all/oscar-corrtest/bin/oscar-corrtest
  TGT=tgt_6KYCH7ESVS65T87RNBPQ97SSFK
  B=https://192.168.29.195/ext/mw     OSCAR_API_KEY=<OSCAR_API_KEY - webhookreceiver test key, see team secrets>
  TRUE MODE = phase_a_audit_only  (CORRELATOR_DISPATCH_ENABLED=false / Shadow)
  Build: podman run --rm -v "$PWD":/src:z -w /src -e CGO_ENABLED=0 \
         docker.io/library/golang:1.27.0-bookworm go build -o /src/bin/oscar-corrtest ./cmd/oscar-corrtest

## AGENTS IN FLIGHT (do not duplicate their lanes)
- LANE-FLAKE  : flake root-cause -> FINDINGS-FLAKE.md      [status: DONE, absorbed ITER5]
- LANE-FIXES  : author fixes on disk -> FIXES-PROPOSED.md  [status: DONE, absorbed ITER2]
If a lane's output file exists and is complete, mark it DONE here and absorb its result.

## CONFIRMED DEFECTS (do not re-report as new)
D1 HIGH   JetStream Nats-Msg-Id dedup keyed on fingerprint -> a RESOLVE within ~2min of its
          FIRING never reaches the correlator; persistence emits a false parent.
          CORRELATOR-ONLY (AlertHistory/ESB/SMTP use the HTTP fan-out, unaffected).
          Reproducer: delay-controlled A/B, resolve at 8s (fails) vs 150s (cancels).
D2 MEDIUM create returns 422 "rule persisted but rejected by loader" but the row IS written
          and leaks; /validate accepts what create rejects.
D3 HIGH   Alert View Correlation tab queries the 12-hex fingerprint against a table keyed on
          16-hex am_fingerprint -> "not correlated" for EVERY alert, every mode.
          adminui CorrelationTab.js:90. Phase B will not fix it.
Detail + proof: FINDINGS-DEFECTS.md

## PATTERN STATUS  (verdict source = CorrelationAudit outcomes, mode phase_a_audit_only)
| pattern       | best seen | flaky? | notes                                        |
|---------------|-----------|--------|----------------------------------------------|
| flood         | PASS      | no     | parent_emitted + enriched, stable 3 sweeps   |
| co_occurrence | PASS      | no     | stable                                       |
| cross_source  | PASS      | no     | stable                                       |
| threshold     | FAIL      | YES    | D5. P01 1/8 FAIL (tumbling HLL boundary)     |
| sequence      | FAIL      | YES    | D4. P01 3/8 FAIL + N01 false positive 1/8    |
| parent_child  | PASS      | no     | 8/8 PASS but EXPOSED to D4 (no ordering logic)|
| absence       | PASS      | no     | N01 1/8 INCONC = HARNESS artifact, not OSCAR |
| persistence   | FAIL      | no     | 6/6 FAIL = DETERMINISTIC. D1, reproducible.  |
Extra scenarios in scenarios/ (10 files) all PASSED in sweep A — re-verify under flake rates.

## WORKLIST — pick the FIRST unblocked item each iteration
[ ] W4  Re-run the 10 authored scenarios in scenarios/ under the true mode, 3 iterations,
        record flake rate per scenario.
[ ] W5  Author MORE negative scenarios: for each of the 8 patterns, a case that must NOT
        correlate (wrong group_by, below threshold, outside window, wrong alertname).
        Validate with `scenario validate`, compile with `plan`, then run.

## DONE
- ITER8: D1 MISSED-PAGE CONSEQUENCE PROVEN LIVE (rule 277, delay-controlled A/B, now in
  FINDINGS-DEFECTS.md + MORNING-REPORT.md). ARM A resolve +14s -> child after clear STILL
  suppressed_per_notifier, and the parent resolve produced NO audit row at all. ARM B
  resolve +156s -> child released_no_trigger AND parent_resolved_suppression_cleared row
  present. Delay is the only variable; causal chain closed. The prediction from ITER7 is
  now evidence. RECOMMENDATION REORDERED: D1 now ranks ABOVE D3 — it is the only defect
  that makes OSCAR silently WITHHOLD real alerts. Cleanup verified: rule 277 deleted,
  correlation_rules = 0.
- ITER7 W6 ANSWERED FROM SOURCE — D1 SEVERITY GOES UP. All 8 patterns call is_resolved();
  6 of them (flood/co_occurrence/cross_source/threshold/sequence/absence) just `return None`
  on a resolve, so swallowing it changes NOTHING for them. Two are affected:
    persistence  (persistence.py:253) -> cancels timer  -> FALSE parent (already proven)
    parent_child (parent_child.py:114-117) -> DELETEs the parent-present suppression key
      -> if swallowed, suppression SURVIVES its full TTL and children of an ALREADY-CLEARED
         incident keep being suppressed = MISSED PAGES (alerts silently withheld).
  The parent_child code comment documents this precise failure as the PRE-FIX bug they
  fixed. D1 REINTRODUCES IT AT THE TRANSPORT LAYER — the is_resolved fix is correct but
  unreachable inside the dedup window. Missed pages are more dangerous than false pages.
  NOTE the campaign could not have caught this empirically: every builtin except
  persistence.N01 is FIRING-ONLY, so parent_child 8/8 PASS says nothing about this path.
  UNTESTED PREDICTION — follow-up: parent_child scenario that resolves the PARENT and
  asserts children release (expect suppression inside the dedup window, release outside).
- ITER6 W7 LOAD TEST DONE — D4 QUANTIFIED. Two 20-group sequence tests, all alerts landed:
    A then B sequential (~100ms apart) -> 20/20 fired (100%)
    A and B SIMULTANEOUS               ->  8/20 fired (40%)
  => 60% of CORRECT-ORDER sequences silently MISSED under within-group concurrency.
  POSITIVE results from the same tests: no alert loss (40/40 both runs under 20-way
  concurrency), no cross-group contamination, group_by_labels partitioning CORRECT under
  load. The defect is strictly within-group ordering — not ingest, not group isolation.
  METHOD WARNING recorded in FINDINGS-DEFECTS.md: the first test's 20/20 does NOT exercise
  D4 (its concurrency was across groups only). Only the simultaneous variant tests the race.
  API GOTCHA found: GET /api/v1/alerts/history IGNORES `size` and always returns 10/page.
  Must PAGE. A single call looks like alert loss when it is only paging — this produced a
  false "alerts being dropped" read mid-test before it was caught.
  Cleanup verified: rules 275,276 deleted, correlation_rules total = 0.
- ITER5 W1+W9: LANE-FLAKE absorbed. TWO NEW OSCAR DEFECTS found (now D4, D5 in
  FINDINGS-DEFECTS.md), plus one harness artifact. MORNING-REPORT.md written.
  D4 HIGH: no cross-message ordering. No startsAt -> parse_event_ts_ms (base.py:90-113)
    falls back to evaluate-time now_ms; AM celery prefork autoscale 8,2 + correlator
    fetch(batch=10) x EVAL_CONCURRENCY=10 with no per-group serialisation. sequence's sort
    (sequence.py:130-147) is inert; parent_child has NO ordering logic (parent_child.py:182).
    run8 showed BOTH directions in one sweep: correct order -> no fire; WRONG order ->
    parent_emitted. A FALSE POSITIVE, not just a miss. Reproducer scripts/repro_seq.sh:
    race 3/6, spaced 6/6, stamped 5/6, inverted 4/4 (event time governs when present).
    LOAD-SENSITIVE — worse under concurrency, hidden on an idle box.
  D5 MED: threshold HLL bucketed by ts_ms//window_ms (threshold.py:107-109,
    lua/add_threshold_and_check.lua KEYS[2]) = TUMBLING, inside a SLIDING alert ZSET.
    Burst straddling the boundary loses cardinality (run5: alerts=3 distinct=1).
    Reproducer scripts/repro_threshold2.sh: straddle 0/3, inside 3/3.
  NOT a defect: absence.N01 = harness artifact, observation window hardcoded 55s
    (internal/compiler/compiler.go:132-134) vs last heartbeat +48s.
  REJECTED with evidence: timer races, stale-window leak, dedup beyond persistence.
  Final flake rates over 8 sweeps: sequence.P01 3/8 FAIL, sequence.N01 1/8 false positive,
  threshold.P01 1/8 FAIL, absence.N01 1/8 INCONC, persistence.N01 8/8 FAIL (D1);
  flood/co_occurrence/cross_source/parent_child/persistence.P01/absence.P01/threshold.N01 0/8.
  Post-lane state verified CLEAN: rules 0, active_windows 0, no leaks.
- ITER4 W8 CLOSED — stale windows are NOT a defect. active_windows_total across all 8
  flake runs: pre=0,2,2,2,2,2,2,2 | after_flood=2 every run | after_persistence=0 every run.
  Bounded and stable, no accumulation over 8 full sweeps. The 2 persistent entries are the
  pre-campaign cooccur:7/8 keys from rules deleted before the campaign. Windows expire
  correctly. Downgrade the earlier 'stale window leak' suspicion to a non-issue.
- ITER4: LANE-FLAKE sweeps ALLDONE (8 iterations) but agent STILL WORKING — it is building
  a deterministic reproducer for sequence (rule 254 reproseq-1788070049_inverted_4, created
  06:09). FINDINGS-FLAKE.md not yet written. Rule 254 is IN-FLIGHT, NOT a leak — do not
  delete. (Second time an in-flight rule looked like a leak; the run= check earned its keep.
  Note: flake/progress.txt ALLDONE refers ONLY to the sweep loop, not the whole lane.)
- ITER3: LANE-FLAKE still RUNNING (rules 187/188 in flight, run=crt_4Z4ZN5X). No pattern
  runs performed — would collide. READ-ONLY interim read of flake/ after 6 iterations:
    flood 6/6 PASS | co_occurrence 6/6 PASS | parent_child 5/5 PASS | cross_source 5/5 PASS
    absence 5/5 PASS | threshold 4/4 PASS | sequence 4 PASS / 2 FAIL | persistence 6/6 FAIL
  TWO CORRECTIONS to earlier conclusions:
   (a) persistence is DETERMINISTIC (6/6 FAIL), not flaky. It is D1, reproducible.
   (b) parent_child FAIL and absence INCONCLUSIVE in sweep3 did NOT reproduce (5/5 PASS
       each). Those were transient — plausibly the mid-session OSCAR restart. sweep3 is
       therefore NOT a reliable baseline; prefer flake/ run1..N.
  => sequence (~33% flake) is the ONLY genuinely non-deterministic pattern. Everything
     else is stable. The "results are flaky" framing was too broad.
  Agent also captured debug/windows pre + after EVERY pattern, debug/timers, and rules.post
  per iteration — good material for the stale-window question (W8).
- ITER2 W2: LANE-FIXES absorbed. All 3 defects fixed ON DISK, nothing deployed.
  D1 nats_publisher.py:447 Nats-Msg-Id -> f"{fingerprint}:{status}" (status confirmed real
     at tm/tasks.py:721, same field patterns/base.py:136 is_resolved() reads); missing
     status -> "unknown"; no-fingerprint-no-header preserved.
  D2 oscar-correlator routers/rules.py — both halves in ONE service. New
     _emit_spec_alertname_error() mirrors the loader gate (rule_store.py:205, incl.
     parent_child carve-out), called BEFORE insert in create/batch/update and folded into
     /validate. 422 now opens no DB session. Post-load branch kept but DELETEs + reloads.
  D3 adminui correlationTabLib.js correlationFingerprintOf() resolves am_fingerprint from
     the labels array CLIENT-SIDE (label already present -> no extra round-trip); one const
     feeds BOTH audit + children queries; returns null instead of falling back to the
     12-hex fp so a wrong-domain query cannot silently return; empty-state copy rewritten
     (the Shadow claim was factually wrong — audit buffers unconditionally consume.py:539).
  Diffs verified tight: 326 insertions / 2 deletions across 4 oscar files + 3 adminui files.
  Unit tests (isolated, mocked, no live stack): am 17, correlator 24+22, adminui 62 PASS.
  NOT changed (deliberate, documented): OSCAR_ALERTS duplicate_window; emit_spec gate on
  POST /rules/import (follow-up: import can still create an unloadable rule); Phase-B
  _build_alert_group label-shape defect (real, unfixed, out of scope).
  NOTE adminui src/lib/exportLimits.js(.test.js) were ALREADY dirty pre-session — not ours.
- ITER1 W3: threshold NOT broken. sweep3/threshold.json = PASS both cases
  (P01 audit-count 1/1, N01 0/0, cleanup CLEAN). It was absent from the scoreboard only
  because the shell loop aborted on a python parse error before reaching it. My reporting
  bug, not a test failure. sweep3 true result: flood/co_occurrence/cross_source/threshold
  PASS; sequence FAIL; parent_child FAIL; persistence FAIL(N01=D1); absence INCONCLUSIVE.

## OVERNIGHT ORDERS (2026-09-01, user asleep — approved sequence)
1. WAIT for FINDINGS-COVERAGE.md (agent pinged). Absorb it.
2. GATE: if it reports ANY new defect touching the nats publisher, rule create/import, or
   the correlation-audit read path -> DO NOT DEPLOY, write up, stop.
3. If clean: deploy ONE AT A TIME with rollback safety:
   a. BEFORE each rebuild: docker tag the service's current image as <name>:pre-corrfix-20260901
   b. D1: rebuild+restart alertmanager (API+worker). VERIFY: fire+clear@10s probe ->
      audit must show persistence_resolved_cancelled AND a re-fire@20s must land (flap test).
   c. D3: rebuild adminui. VERIFY: correlated alert's tab shows audit rows (curl the
      Next.js API route; browser check left for user).
   d. D2: rebuild+restart correlator. VERIFY: invalid flood create -> 422 AND rule count
      unchanged; import of bad doc -> per-file error, no row.
   e. ANY verify fails -> roll back THAT service to :pre-corrfix-20260901, stop the
      sequence, write up exactly what failed.
4. Keep ESB/alerting healthy: after each restart confirm /ext/mw history returns 200 and
   a plain alert still flows (no-rule baseline probe).
5. Update MORNING-REPORT.md + this file with what happened.

## NEXT ACTION — ALL MAIN LANES + LOAD TEST DONE.
Remaining optional depth: W4 (re-run the 10 authored scenarios under flake rates),
W5 (more negative scenarios per pattern), W6 (D1 blast radius across absence/parent_child).
Nothing is blocking. User should read MORNING-REPORT.md first, then FINDINGS-DEFECTS.md.
Budget note: user asked to stop at 20% of weekly limit (SESSION-ONLY rule). I cannot read
usage; user runs /usage and tells me. Keep iterations cheap.

## SUPERSEDED
W1 if FINDINGS-FLAKE.md is complete, else W2 if FIXES-PROPOSED.md is complete.
If BOTH lanes still RUNNING: do analysis-only work. DO NOT run any pattern while
LANE-FLAKE is executing — concurrent runs cross-contaminate windows, and its in-flight
rules must never be deleted (check rule descriptions for its run= id before cleaning).

## ITERATION NOTES
- Rule-count preflight is NOT a clean leak signal while LANE-FLAKE runs. Rules whose
  description carries an active run= id belong to it. Only delete rules from a run id
  that is demonstrably finished.
- Scoreboard loops must not abort on one bad file: wrap per-file parsing in try/except
  (ITER1 lost threshold this way).
