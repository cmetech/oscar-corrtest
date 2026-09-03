# FINDINGS — correlator run-to-run non-determinism (2026-08-30)

Read-only on OSCAR throughout. No config/env/container/service change. No restart.
No commits. `correlation_rules` total returned to **0** after every iteration and at the
end of the campaign; `debug/windows active_windows_total = 0`. **No rule leaks observed.**

Method: 8 sequential full sweeps (`builtin:*`, `--pipeline-mode phase_a_audit_only`),
one directory per iteration (`flake/run1..run8/`), `debug/windows` snapshotted before the
iteration and after every pattern, `debug/timers` + rule list after each iteration.
Then three delay/timestamp-controlled A/B reproducers (`scripts/repro_seq.sh`,
`scripts/repro_threshold2.sh`, `scripts/repro_order.sh`).

---

## 1. FLAKE RATE TABLE — 8 consecutive sweeps, idle box, same binary

`scripts/score.py 'flake/run*/*.json'`

| pattern.case | r1 | r2 | r3 | r4 | r5 | r6 | r7 | r8 | non-PASS |
|---|---|---|---|---|---|---|---|---|---|
| absence.P01 | P | P | P | P | P | P | P | P | 0/8 |
| absence.N01 | P | P | **INCONCL** | P | P | P | P | P | 1/8 |
| co_occurrence.P01 | P | P | P | P | P | P | P | P | 0/8 |
| co_occurrence.N01 | P | P | P | P | P | P | P | P | 0/8 |
| cross_source.P01/N01 | P | P | P | P | P | P | P | P | 0/8 |
| flood.P01/N01 | P | P | P | P | P | P | P | P | 0/8 |
| parent_child.P01/N01 | P | P | P | P | P | P | P | P | 0/8 |
| persistence.P01 | P | P | P | P | P | P | P | P | 0/8 |
| **persistence.N01** | **F** | **F** | **F** | **F** | **F** | **F** | **F** | **F** | **8/8 (deterministic)** |
| **sequence.P01** | **F** | P | **F** | P | P | P | P | **F** | **3/8 FLAKY** |
| **sequence.N01** | P | P | P | P | P | P | P | **F** | **1/8 FLAKY (FALSE POSITIVE)** |
| threshold.P01 | P | P | P | P | **F** | P | P | P | 1/8 FLAKY |
| threshold.N01 | P | P | P | P | P | P | P | P | 0/8 |

Only **four** cases ever varied. Three distinct causes behind them, below.

Note on the earlier sweeps: `sweep/` and `sweep3/` were single samples of the same
distribution. `parent_child.P01` failed once in `sweep3` (`released_no_trigger` for the
child) — same root cause as `sequence` (cause A), just a narrower race; it did not
recur in 8 sweeps or in 6 reproducer trials at gap=0.

---

## 2. ROOT CAUSE A — the correlator has **no cross-message ordering** and falls back to processing wall-clock when the alert carries no `starts_at`

Affects: `sequence` (both directions), `parent_child` (child-before-parent).

### The chain
1. **The harness never sends `startsAt`.** `grep -rn "startsAt\|starts_at" internal/` in
   oscar-corrtest returns nothing. The AM POST body therefore has `alert.starts_at = None`.
2. **AM publishes `"starts_at": None`.**
   `oscar-alertmanager/src/tm/tasks.py:723`
   `"starts_at": alert.starts_at.isoformat() if alert.starts_at else None,`
3. **The correlator then scores the event at *processing* wall-clock.**
   `oscar-correlator/src/app/patterns/base.py:90-113` `parse_event_ts_ms()`:
   `starts_at_ms` absent → `starts_at` None → `return now_ms` (line 113).
4. **Processing order is nondeterministic in two independent places:**
   - AM celery worker runs `--pool=prefork --autoscale=8,2`
     (`docker inspect oscar-alertmanager-alertmanager_worker-1` → Cmd). Two alerts
     ~1s apart are handled by different prefork children → NATS publish order is not
     the POST order.
   - The correlator pulls `sub.fetch(batch=10, ...)` (`core/consume.py:652`) into one
     `asyncio.Queue`, drained by **`CORRELATOR_EVAL_CONCURRENCY=10`** parallel
     `evaluate_worker` tasks (`main.py:510-513`, `core/settings.py:200`; container env
     confirms `CORRELATOR_EVAL_CONCURRENCY=10`). Nothing serialises per group/rule.
5. `sequence` *does* try to defend itself — `patterns/sequence.py:130-147` sorts the
   Redis list by `ts_ms` and tie-breaks equal timestamps by **arrival index**. With
   step 3 collapsing every event onto processing time, that tie-break *is* the arrival
   order, so the defence is inert.
   `parent_child` has no ordering logic at all: the parent's
   `set_ex(oscar:correlator:parent:{rule}:{group})` at `patterns/parent_child.py:182`
   must simply *happen* before the child is evaluated.

### Live evidence — same scenario, opposite outcomes, order visible in the audit rows
The `depth=N of 2` in `CorrelationAudit.reason` is the position each alert took in the
Redis sequence list, i.e. the true processing order.

```
run1 sequence.P01  FAIL
  LOGINFAILURE      (history 05:05:28.596, sent 1st)  enriched  depth=2 of 2
  PRIVILEGEDCOMMAND (history 05:05:29.496, sent 2nd)  enriched  depth=1 of 2   <- processed FIRST
run3 sequence.P01  FAIL   (same inversion)
run2 sequence.P01  PASS
  earlier alert (05:11:34.409) depth=1, later (05:11:35.401) parent_emitted sequence_triggered
```

**run8 is the decisive pair — the same run produced a false negative AND a false positive:**
```
run8 sequence.P01  FAIL   (correct order sent, NOT fired)
  LOGINFAILURE      05:48:10.092  enriched depth=2 of 2
  PRIVILEGEDCOMMAND 05:48:11.124  enriched depth=1 of 2
run8 sequence.N01  FAIL   (DELIBERATELY WRONG order sent, FIRED anyway)
  LOGINFAILURE      05:48:12.102  enriched depth=1 of 2
  PRIVILEGEDCOMMAND 05:48:11.148  parent_emitted sequence_triggered
                                  tail=[..._LOGINFAILURE_1F5EX2H4, ..._PRIVILEGEDCOMMAND_...]
```
`sequence.N01` is the scenario that sends B *then* A on purpose. The correlator emitted a
synthetic parent for it. **A sequence rule can fire on out-of-order input.** In Phase B
that is a spurious notification/ticket.

### DETERMINISTIC REPRODUCER (`scripts/repro_seq.sh <mode> <trials>`)
Fresh sequence rule per trial (window 300s, group_by `site`), two alerts, audit read by
`am_fingerprint`, rule deleted after each trial.

| mode | what it does | result |
|---|---|---|
| `race` | A then B back-to-back, **no** `startsAt` | **fired 3/6** (coin flip) |
| `spaced` | A, sleep 20s, B, **no** `startsAt` | **fired 6/6** |
| `stamped` | A then B back-to-back, `startsAt` 60s apart (A earlier) | **fired 5/6** (the 6th had no audit row yet at read time; in one trial A was processed *second* and it still fired correctly) |
| `inverted` | **B posted first** (`startsAt` T+60), A posted 20s later (`startsAt` T) | **fired 4/4** |

`inverted` is the proof: wall-clock arrival fully reversed, event time correct → fires
every time. `race` vs `spaced` is the proof of the race: identical payloads, only the
inter-arrival gap changed, 50% vs 100%.

`parent_child` at gap 0 (`scripts/repro_order.sh pc 0 6`): 6/6 correct
(`enriched/parent_registered` + `suppressed_per_notifier`). Same class, much narrower
window in practice.

### Verdict
- **OSCAR DEFECT (HIGH).** `sequence` (and `parent_child` suppression) is only correct
  when the source populates `startsAt`. Any producer that omits it gets ordering by
  processing time under 10-way concurrency — which yields both missed correlations and
  **false correlations**. Nothing in the ingest path preserves per-group order, and
  nothing rejects/flags an alert with no event time.
  Fix directions: (a) stamp `starts_at` at AM ingest when the source omits it (receipt
  time, assigned once, monotonic per alert) rather than letting each correlator worker
  invent one at evaluate time; (b) make `parse_event_ts_ms`'s wall-clock fallback
  observable (counter + WARN) so this is not silent; (c) key ordering off the JetStream
  sequence number when no event time exists; (d) serialise evaluation per
  `(rule_id, group_hash)`.
- **HARNESS GAP (MEDIUM).** oscar-corrtest sends no `startsAt`, so its
  order-sensitive scenarios are testing the race, not the pattern. Adding a per-event
  `startsAt` makes `sequence`/`parent_child` deterministic (proven by `stamped`/`inverted`).

---

## 3. ROOT CAUSE B — `threshold` HyperLogLog is a **tumbling** bucket while the alert ZSET is a **sliding** window

Affects: `threshold.P01` (1/8).

`patterns/threshold.py:107-109`
```
window_bucket    = ts_ms // window_ms
window_epoch_ms  = window_bucket * window_ms
```
The HLL key `oscar:correlator:threshold_hll:{rule}:{group}` is bucketed by that epoch
(`core/lua/add_threshold_and_check.lua` KEYS[2], header comment "bucketed by window epoch
Python-side … so PFCOUNT resets per window"), while KEYS[1], the alert ZSET, is evicted
by `ts_ms - window_ms` — a true sliding window. A burst that **straddles a bucket
boundary** therefore has its distinct-value set cut in half even though every alert is
inside one sliding window.

### Live evidence — run5 threshold.P01 FAIL
```
d1 05:33:28.770  enriched threshold_advancing distinct=2 of 3 alerts=2
d2 05:33:29.849  enriched threshold_advancing distinct=1 of 3 alerts=1
d3 05:33:30.963  enriched threshold_advancing distinct=1 of 3 alerts=3   <- alerts=3, distinct=1
```
`alerts=3` (sliding ZSET saw all three) with `distinct=1` (HLL had just reset). The 30s
bucket boundary falls at 05:33:30.000, exactly between d2 and d3.

### DETERMINISTIC REPRODUCER (`scripts/repro_threshold2.sh <straddle|inside> <trials>`)
Same rule (window 30s, `min_distinct_count=3`, distinct_label `device`), same 3-second
spread of d1/d2/d3, explicit `startsAt` so the event times are fully controlled — only
the phase relative to the 30s boundary differs.

```
straddle  ts = [...:14:58.000Z, ...:14:59.000Z, ...:15:01.000Z]   NO-FIRE  distinct 2,1,1 (alerts=3)
inside    ts = [...:16:31.000Z, ...:16:32.000Z, ...:16:33.000Z]   FIRED    distinct 1,2,3
SUMMARY straddle fired=0/3      SUMMARY inside fired=3/3
```

### Verdict
**OSCAR DEFECT (MEDIUM-HIGH), not a harness artifact.** Independent of the ordering race
(the reproducer pins the timestamps). A cardinality-threshold rule silently fails to fire
for any burst crossing a window epoch; expected miss probability ≈ burst_spread /
window_seconds. Fix: make the HLL sliding (e.g. two half-window HLLs merged with PFMERGE,
or per-value ZSET of last-seen ts + ZCARD after eviction) rather than tumbling.

---

## 4. `absence.N01` INCONCLUSIVE (1/8) — HARNESS TIMING ARTIFACT

`internal/compiler/compiler.go:132-134`
```
observationWindow := source.Window + 15*time.Second
if input.Pattern == "absence" { observationWindow = 55 * time.Second }
```
`absence` N01 injects 7 heartbeats, the last at **+48s** (`internal/scenario/builtin.go`,
absence negative case), leaving ~7s for AM celery → NATS → correlator evaluate → the audit
writer's buffered flush → the audit read-back. `observation.go:154`
`AuditReady = SourceReady && len(auditedFingerprints) == len(Sources)` requires *every*
source alert to already have an audit row, and `observation.go:86` turns a miss into
INCONCLUSIVE.

run3 evidence: 7 history records (05:19:14 … 05:20:02), **6** audit rows, deadline
05:20:08.8. The assertion itself PASSED (`parent_emitted` expected 0, observed 0).

**Verdict: HARNESS artifact, no OSCAR defect.** Fix in the harness: raise the absence
observation window to >= last-event-delay + 20s (~70s), and/or bump `MaxDuration` for the
pattern. Not an OSCAR change.

---

## 5. `persistence.N01` — NOT flaky, and already explained

8/8 FAIL, `parent_emitted` where 0 is expected. This is **DEFECT 1** (FINDINGS-DEFECTS.md):
JetStream `Nats-Msg-Id` dedup keyed on the alert fingerprint
(`oscar-alertmanager/src/app/core/nats_publisher.py`) drops the resolve inside the 2-minute
default duplicate window, so the correlator never sees the clear. The builtin scenario
resolves at +10s — always inside the window — hence deterministic, not flaky. Nothing new.

---

## 6. HYPOTHESES TESTED AND **REJECTED**

| # | hypothesis | verdict |
|---|---|---|
| a | timer-subsystem race (`WINDOW_GRACE_SECONDS=5`, `TIMER_POLL_INTERVAL_MS=100`) | **Rejected.** `persistence.P01` and `absence.P01` — the two genuinely timer-driven positives — are 8/8 PASS. `debug/timers` showed `pending_timers:0, backlog_drained:true` after every iteration. The only timer-adjacent variance (absence.N01) is the harness deadline (section 4). |
| b | stale Redis windows from deleted rules leaking into later runs | **Rejected.** Window keys are namespaced by rule id (`oscar:correlator:win:99:...`, `cooccur:101:...`, `seq:103:...`) and every run creates fresh rule ids, so a lingering key can never be re-matched. Snapshots showed <=2 windows mid-run and `active_windows_total:0` at every iteration boundary and at campaign end. Lingering keys for dead rule ids are cosmetic (blocker 6 in FINDINGS-OSCAR-BACKEND). |
| c | the DEFECT 1 dedup race affecting more patterns than persistence | **Rejected as the cause of the variance.** Only `persistence.N01` injects a resolve; every other builtin case is firing-only. DEFECT 1 explains a deterministic failure, not run-to-run variance. |
| d | harness observation deadlines too short for timer patterns | **Confirmed for exactly one case** — `absence.N01` (section 4). Not the cause of `sequence`/`threshold`. |
| e | NATS redelivery / consumer lag / ordering | **Ordering: CONFIRMED (section 2)** — but it is publish/evaluate concurrency, not redelivery. No redelivery, nak-ladder, poison-term, or lag/breaker log lines appeared in the correlator during the campaign; `/ready` stayed `nats:true consumer:true redis:true`. |
| f | alert-history read-back lag vs the polling deadline | **Contributory only.** Seen as an intermittent `NONE` audit read in reproducer trials and as the `absence.N01` mechanism. Never produced a FAIL verdict on its own — a late read yields INCONCLUSIVE, not FAIL. |

---

## 7. RECOMMENDATION PER FLAKY CASE

| flaky case | rate | classification |
|---|---|---|
| `sequence.P01` (missed correlation) | 3/8 | **OSCAR DEFECT** (ordering, section 2). Harness also under-specifies (`startsAt`). |
| `sequence.N01` (false correlation) | 1/8 | **OSCAR DEFECT, more serious than P01** — synthetic parent emitted for out-of-order input. |
| `parent_child.P01` | 0/8 here, 1x in `sweep3` | **OSCAR DEFECT, same class as sequence**; child evaluated before the parent's `set_ex` lands. |
| `threshold.P01` | 1/8 | **OSCAR DEFECT** (tumbling HLL vs sliding ZSET, section 3). Independent of the ordering race. |
| `absence.N01` | 1/8 | **HARNESS timing artifact** (`compiler.go:132-134`). No OSCAR change. |
| `persistence.N01` | 8/8 | Not flaky. Known **DEFECT 1**. |

### Suggested order of work
1. **Do not treat any single sweep as a verdict** until section 2 is fixed — the harness is
   currently measuring the ordering race for `sequence`/`parent_child`.
2. Harness (zero blast radius, no OSCAR change): send a per-event `startsAt`; raise the
   absence observation window to ~70s. This alone should take the suite to a stable
   15/16 (persistence.N01 remaining, correctly, on DEFECT 1).
3. OSCAR: stamp `starts_at` at AM ingest when absent + instrument the wall-clock fallback.
4. OSCAR: fix the threshold HLL windowing.
5. OSCAR: DEFECT 1 dedup key (`fingerprint:status`).

---

## 8. ARTIFACTS

```
flake/run1..run8/{<pattern>.json,.log, windows.pre.json, windows.after_<pattern>.json,
                  timers.post.json, rules.post.json, times.txt}
flake/progress.txt
scripts/flakeloop.sh          8x sequential sweep driver + debug snapshots
scripts/score.py              flake-rate table
scripts/repro_seq.sh          sequence ordering A/B (race|spaced|stamped|inverted)
scripts/repro_threshold2.sh   threshold bucket-boundary A/B (straddle|inside)   <- deterministic
scripts/repro_threshold.sh    earlier wall-clock-aligned variant (superseded)
scripts/repro_order.sh        parent_child / sequence gap sweep
scripts/audit.py              audit-row formatter (query by 16-hex am_fingerprint)
```
Final state: `correlation_rules total = 0`, `debug/windows active_windows_total = 0`.
