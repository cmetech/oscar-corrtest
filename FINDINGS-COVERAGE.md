# OSCAR Correlation — COVERAGE CLOSE-OUT (2026-09-01)

Pipeline mode declared for EVERY run below: **`phase_a_audit_only`**
(`CORRELATOR_DISPATCH_ENABLED=false` re-verified live at session start). All verdicts come
from **CorrelationAudit rows keyed on the 16-hex `am_fingerprint`**. `syntheticCount` is
meaningless in Shadow and is never used as evidence. The Alert View Correlation tab (D3)
was never consulted.

Nothing on OSCAR was changed: no config, env, container, restart, commit or push.
Every block ended with `correlation_rules total = 0`, verified by API.

New reusable probes written this session (pure API, harness-independent):
`scripts/lib_probe.py`, `scripts/repro_d6_eventtime.py`, `scripts/repro_d6_fetchlag.py`,
`scripts/load_patterns.py`, `scripts/multirule.py`, `scripts/resolve_paths.py`,
`scripts/thr_control.py`, `scripts/sustained.py`, `scripts/sc.py`.
New scenario YAML: 10 files in `scenarios/` (listed in §3).

---

## 1. HEADLINE

* **ONE NEW DEFECT — D6: correlation windows are measured in correlator *processing*
  time, not event time.** Proven live in BOTH directions:
  * **FALSE CORRELATION** — two alerts injected **28 s apart** were correlated by a
    **20 s**-window rule (twice: `sequence` rule 321, `cross_source` rule 327).
  * **MISSED CORRELATION** — two alerts injected **2 s apart** were **not** correlated by
    a 20 s-window rule (`cross_source` rule 331; the two audit rows are 25.0 s apart).
  This is the first false-correlation class found outside `sequence`, and it explains
  much of the flake previously attributed to D4.
* **D4 quantified for `parent_child`**: with parent and child fired **simultaneously**
  across 20 concurrent groups, **8/20 (40 %) lost the child suppression** — the same
  40 % ITER6 measured for `sequence`. D4 is not sequence-specific.
* **D5 is worse than "boundary straddle"**: a plain 300 s threshold window missed a
  genuine 3-distinct-device condition spread over 2m40s (see §6).
* Everything else held: **12 rules live at once → zero mis-attribution**;
  `co_occurrence`, `threshold`, `cross_source` were **20/20 clean** at 20-way concurrency
  with simultaneous legs; resolves do **not** disturb counting patterns.
* Measurement constraint discovered: **bursts of ≳60 alerts are silently dropped before
  AlertHistory** (~40 % loss at N=100, all HTTP 200). This is the already-closed
  "alerts dropped under load" behaviour — NOT re-raised as a defect — but it caps every
  load test and explains the `flood` load numbers in §5.

---

## 2. COVERAGE MATRIX (dimension × pattern)

Legend: PASS = behaved correctly · FAIL = defect observed · n/a = not expressible ·
`-` = not run this session (already covered by an earlier iteration, referenced)

| dimension                         | flood | co_occ | sequence | persistence | absence | parent_child | cross_source | threshold |
|-----------------------------------|-------|--------|----------|-------------|---------|--------------|--------------|-----------|
| **1 authored scenarios finished** | PASS  | –      | –        | FAIL (D1)   | –       | FAIL (D4)    | –            | PASS      |
| **2a negative: wrong group_by**   | PASS  | PASS   | PASS     | n/a         | n/a     | PASS         | PASS         | PASS      |
| **2b negative: below threshold**  | PASS  | PASS   | PASS     | FAIL (D1)   | PASS    | PASS         | PASS         | PASS      |
| **2c negative: outside window**   | PASS  | PASS   | **FAIL (D6)** | PASS   | n/a     | PASS         | **FAIL (D6)**| PASS      |
| **2d negative: wrong alertname**  | PASS  | PASS   | PASS     | PASS        | PASS    | PASS         | PASS         | PASS      |
| **3 load, 20 groups, simultaneous**| PASS* | PASS   | FAIL(ITER6)| n/a       | n/a     | **FAIL (D4)**| PASS         | PASS      |
| **4 many rules at once (12)**     | PASS  | PASS   | FAIL (D4)| n/a         | n/a     | PASS         | PASS         | PASS      |
| **5 resolve mid-window ignored**  | PASS  | PASS   | –        | FAIL (D1)   | –       | – (source)   | –            | PASS      |
| **6 sustained volume**            | see §8| see §8 | –        | –           | –       | –            | –            | see §8    |

\* `flood` load: only 13/20 groups received all five alerts because of the ingest drop;
of the groups that received a complete set, **12/12 fired exactly once**. Correlation was
correct for every complete group.

`n/a` justifications: `persistence`/`absence` are single-alert timer patterns, so
"wrong group_by" and "outside window" have no meaning; the harness DSL enforces
one firing role for `flood`/`persistence`/`absence`/`cross_source`/`threshold`, so a
second, non-matching alertname is not expressible there — dimension 2d was therefore run
as a direct-API probe (§4.4) instead of a scenario.

---

## 3. DIMENSION 1 — the 10 authored scenarios, finished

5 had run previously (`scen2/`); the 5 outstanding ones were run to completion
(`scen3/`, run 2026-08-31):

| scenario | verdict | cases | audit outcomes |
|---|---|---|---|
| parent_child-group-isolation | PASS | P01 PASS N01 PASS | enriched, released_no_trigger, suppressed_per_notifier |
| parent_child-multi-child | **FAIL** | P01 FAIL N01 PASS | 2 of 3 children suppressed, 1 released |
| parent_child-tag-only | PASS | P01 PASS N01 PASS | enriched, released_no_trigger |
| persistence-long-window | **FAIL** | P01 PASS N01 FAIL | **D1** — the 5 s resolve never reaches the correlator |
| threshold-distinct-boundary | PASS | P01 PASS N01 PASS | enriched, parent_emitted |

Earlier five (`scen2/`), all PASS: flood-boundary-above, flood-group-partition,
flood-window-expiry, flood-wrong-groupby, parent_child-child-before-parent.
(`parent_child-group-isolation` and `-multi-child` had aborted with
`context deadline exceeded` in the dead session — not a product problem; they complete
normally.)

**parent_child-multi-child FAIL is a D4 manifestation, and it is flaky: 1 FAIL / 4 runs.**
Three children at +2 s/+4 s/+6 s after the parent; on the failing run the +2 s child was
`released_no_trigger` instead of `suppressed_per_notifier`, i.e. the child was evaluated
before the parent registered. Cleanup CLEAN on all four runs.

---

## 4. DIMENSION 2 — NEGATIVE DEPTH

### 4.1 Ten new negative scenarios authored

All validated with `scenario validate` and compiled with `plan` (exit 0) before running.
Each file pairs the required positive control (P01) with the targeted negative (N01) —
the DSL enforces exactly one of each, and its `semantics.go` validator independently
proves each N01 genuinely does not satisfy the pattern.

| file | negative kind | N01 result |
|---|---|---|
| `scenarios/threshold-wrong-groupby.yaml` | 4 distinct devices split across 2 sites | PASS (no fire) |
| `scenarios/threshold-outside-window.yaml` | 3 devices at 0/14/28 s, window 20 s | PASS (no fire) |
| `scenarios/cooccurrence-wrong-groupby.yaml` | required roles in different sites | PASS |
| `scenarios/cooccurrence-outside-window.yaml` | second role at +28 s, window 20 s | PASS |
| `scenarios/sequence-wrong-groupby.yaml` | ordered pair split across sites | PASS |
| `scenarios/sequence-missing-leg.yaml` | first leg only, x3 | PASS |
| `scenarios/sequence-outside-window.yaml` | correct order, +28 s, window 20 s | **FAIL — FIRED (D6)** |
| `scenarios/parent_child-outside-window.yaml` | child at +28 s, window 20 s | PASS |
| `scenarios/cross_source-wrong-groupby.yaml` | snmp/api in different sites | PASS |
| `scenarios/cross_source-outside-window.yaml` | api leg at +28 s, window 20 s | **FAIL — FIRED (D6)** |

Every P01 control passed, so the two failures are not scenario-authoring errors.
`correlation_rules` returned to 0 after the block.

### 4.2 NEW DEFECT D6 — window membership follows PROCESSING time, not event time

**Severity: HIGH.** It is the only defect in the campaign that can produce a
**false correlation** in a non-`sequence` pattern, and it also silently drops genuine ones.

**Evidence A — false correlation (`cross_source`, rule 327, window 20 s):**

```
history  20:46:53.890  snmp leg injected
history  20:47:21.xxx  api leg injected      (28 s later)
audit    20:47:08.411  enriched         crosssource_advancing distinct=1 of 2
audit    20:47:23.419  parent_emitted   crosssource_triggered distinct=2 of 2
```
The snmp leg waited **14.5 s** to be evaluated; the api leg waited ~2 s. At evaluation the
two legs were only **15 s** apart, inside the 20 s window, so the rule fired on a pair
that was 28 s apart in reality.

**Evidence B — false correlation (`sequence`, rule 321, window 20 s):** same shape —
legs injected 28 s apart, audit rows only **10 s** apart (`enriched` 20:45:03.288 →
`parent_emitted` 20:45:13.294, `sequence_triggered tail=[LOGINFAILURE, PRIVILEGEDCOMMAND]`).
First leg lagged **22.2 s**.

**Evidence C — missed correlation (`cross_source`, rule 331, window 20 s),
`scripts/repro_d6_eventtime.py` ARM A:**

```
posted   20:52:14.258  snmp leg
posted   20:52:16.237  api leg          (2 s later)
audit    20:52:18.489  enriched  distinct=1 of 2     (lag 4.2 s)
audit    20:52:43.498  enriched  distinct=1 of 2     (lag 27.3 s)
RESULT fired = False
```
Two alerts **2 seconds apart** were **not** correlated by a 20 s-window rule, because the
second leg's evaluation lagged 27 s and the Lua eviction bound (`ts2 - window`) evicted
the first leg.

**Evidence D — the A/B that isolates the variable** (`repro_d6_eventtime.py`):
ARM B is identical but the two legs carry explicit `startsAt` **300 s apart** while being
posted 2 s apart → **did not fire** (correct). So the correlator *does* honour event time
when it is present (`patterns/base.py parse_event_ts_ms`); the problem is that it is
absent.

**Mechanism (read from source, all three links confirmed):**
1. `oscar-corrtest`/any API client may omit `startsAt`; OSCAR does **not** backfill it —
   `oscar-alertmanager/src/tm/tasks.py create_notification_payloads()` emits
   `"starts_at": alert.starts_at.isoformat() if alert.starts_at else None`.
2. `oscar-correlator/src/app/patterns/base.py parse_event_ts_ms()` therefore falls through
   `starts_at_ms` → `starts_at` → `return now_ms`, i.e. **wall clock at evaluation**.
3. Evaluation lag is not uniform. Measured over ~30 alerts: normally **4–5 s**
   (`scripts/repro_d6_fetchlag.py`: 6 lone alerts 20 s apart → 4.7–4.9 s; a 10-alert burst
   → 4.1–4.6 s), but **~10–15 % of alerts lag 14–27 s**. The pull consumer at
   `oscar-correlator/src/app/core/consume.py:652` fetches `batch=10, timeout=30`, so an
   alert that lands in a fetch cycle that does not fill waits for the expiry.

**Consequence:** every sliding-window pattern's `window_seconds` is effectively
`window_seconds ± ~25 s` of jitter. **Any rule with `window_seconds` below ~30 is
unreliable in both directions.** All eight patterns pass through `parse_event_ts_ms`, so
all are exposed; it surfaces first in the two-leg patterns because a single late leg is
enough.

**Deterministic reproducer:** `python3 scripts/repro_d6_eventtime.py` (needs
`OSCAR_API_KEY`). ARM B (startsAt present, 300 s apart, posted 2 s apart) must not fire —
it does not. ARM A (identical, startsAt omitted) is decided purely by pipeline lag; run
`scripts/repro_d6_fetchlag.py` alongside to show the lag distribution. The scenario-level
reproducers are `scenarios/sequence-outside-window.yaml` and
`scenarios/cross_source-outside-window.yaml`.

**Fix direction (NOT applied, NOT deployed):** backfill `starts_at` with the ingest
timestamp in `oscar-alertmanager` when the producer omits it, so the correlator always has
a stable event time; secondarily, lower the consumer's fetch batch/expiry at
`consume.py:652` so evaluation lag is bounded. Relationship to D4: same missing-event-time
root, different consequence (D4 = ordering, D6 = window boundary). A `starts_at` backfill
addresses both.

### 4.3 Negatives that behaved correctly

`wrong group_by` held for all six patterns where it is meaningful — alerts split across
group values never reached a threshold in any group, and no cross-group leakage appeared
in any run, including under 20-way load (§5). `below threshold` held everywhere except
`persistence`, whose N01 is D1 and is unchanged. `outside window` held for `flood`
(pre-existing `flood-window-expiry`), `co_occurrence`, `threshold` and `parent_child` —
those four were simply lucky on lag; D6 exposes them equally.

### 4.4 Negative 2d — non-matching alertname (direct-API probe)

Run inside the 12-rule block (§6) rather than as scenarios, because the DSL cannot express
a second, non-matching alertname for five of the eight patterns. Three rules were created
whose match set was never satisfied by any injected alert:

* `R07` flood on an alertname that was never fired → **0 audit rows**.
* `R08` co_occurrence requiring `A` + `Z` where `Z` was never fired → 1 `enriched`, **0
  `parent_emitted`**.
* `R09` threshold on the *fired* alertname but keyed on `distinct_label: region`, a label
  the alerts do not carry → **0 audit rows** (the pattern correctly declines the alert).

Additionally, across the whole 12-rule block **zero** audit rows were attributed to a rule
whose match set did not contain the audited alert's alertname (§6).

---

## 5. DIMENSION 3 — LOAD ON PATTERNS OTHER THAN `sequence`

`scripts/load_patterns.py`, 20 concurrent groups per pattern, **all legs of every group
fired simultaneously from a 64-thread pool** (the ITER6 method warning was honoured — a
sequential A-then-B fire only tests across-group concurrency and looks falsely healthy).
`window_seconds=120` throughout so D6's lag jitter could not confound the result.

| pattern | alerts posted | landed | groups seen | fired exactly once | anomalies |
|---|---|---|---|---|---|
| flood | 100 | 64 | 13 | **12 / 13** | 1 group got only 4 of 5 alerts (ingest drop) → correctly did not fire |
| co_occurrence | 40 | 40 | 20 | **20 / 20** | none |
| threshold | 60 | 60 | 20 | **20 / 20** | none |
| cross_source | 40 | 40 | 20 | **20 / 20** | none |
| parent_child | 40 | 40 | 20 | **12 / 20** | **8 groups: child `released_no_trigger` instead of `suppressed_per_notifier`** |

Positives, across all five patterns: **no double-firing** (never more than one
`parent_emitted` per group), **no cross-group contamination**, **no guardrail rejections**
(`MAX_ACTIVE_WINDOWS`, `MAX_ALERTS_PER_WINDOW`, `MAX_GROUP_CARDINALITY` never tripped),
and `group_by_labels` partitioning was exact in every case.

**`parent_child` FAILURE — D4, now quantified outside `sequence`.** Firing parent and
child in the same millisecond, 40 % of groups evaluated the child before the parent had
registered, so the child was released instead of suppressed. `parent_child.py` has no
ordering logic at all (documented at `parent_child.py:182`). This is the **noise**
direction of the defect (an extra page); D1's blast-radius analysis covers the **missed
page** direction. The 40 % figure matches ITER6's `sequence` result exactly, which
supports one shared root cause rather than two pattern-specific bugs.

**Ingest-drop constraint (not a new defect).** During the flood arm, 100 alerts posted
with HTTP 200 produced only 64 AlertHistory rows. Reproduced deterministically:

| injection | landed |
|---|---|
| 30 alerts, sequential | 30 / 30 |
| 100 alerts, sequential (8.2 s) | 58 / 100 |
| 100 alerts, 8 threads (1.8 s) | 60 / 100 |
| 100 alerts, 64 threads (2.4 s) | 64 / 100 |

Confirmed as real loss, not a paging artefact: `total_records` on
`/api/v1/alerts/history` was stable (no background traffic), the delta matched the short
count, and re-polling at 30/60/120/240/360 s stayed at 60/100. This is the already-closed
"alerts dropped under load" behaviour and is **not** re-raised here — but it bounds every
load test on this box and must be kept under ~50 alerts per burst.

**API gotchas re-confirmed:** `GET /api/v1/alerts/history` ignores `size` and always
returns 10/page (must page); `emit_spec` uses key `alertname`, not `alert_name` — the
wrong key produces D2's 422-that-still-persists (two rules leaked and were deleted by id).

---

## 6. DIMENSION 4 — MANY RULES AT ONCE

`scripts/multirule.py`: **12 rules across 6 patterns live simultaneously**, deliberately
overlapping — two identical flood rules on the same alertname (R01/R02), a third flood
rule on the same alertname but grouped on a different label (R11), two parent_child rules
on the same parent/child pair with different `group_by` (R06/R12), and three rules that
must never fire (R07/R08/R09). 16 alerts drove several of them at once.

```
R01 id=342 {'parent_emitted': 1, 'enriched': 4}      R07 id=348 {}
R02 id=343 {'parent_emitted': 1, 'enriched': 4}      R08 id=349 {'enriched': 1}
R03 id=344 {'parent_emitted': 1, 'enriched': 1}      R09 id=350 {}
R04 id=345 {'parent_emitted': 1, 'enriched': 2}      R10 id=351 {'enriched': 2}
R05 id=346 {'parent_emitted': 1, 'enriched': 1}      R11 id=352 {'parent_emitted': 1, 'enriched': 4}
R06 id=347 {'suppressed_per_notifier': 1, 'enriched': 1}
R12 id=353 {'suppressed_per_notifier': 1, 'enriched': 1}
MIS-ATTRIBUTION rows: 0
```

Findings:
* **Zero mis-attribution.** Every audit row's `rule_id` belonged to a rule whose match set
  contained that alert's alertname.
* **No cross-rule interference.** Duplicate rules R01/R02 each fired independently on the
  same five alerts; neither suppressed the other. R11's different `group_by` produced its
  own independent window on the same alerts. R06/R12 both suppressed the same child.
* **No priority effect observed** — all rules used `priority: 100`; a rule firing did not
  stop other rules from evaluating the same alert. Correlation is additive per rule.
* **Rules that must not fire, did not.** R07 produced no rows at all; R08 stalled at
  `enriched`; R09 declined every alert.
* **One miss: R10 (`sequence`) did not fire** on a correct ordered pair posted 3 s apart
  inside a 120 s window — 2 `enriched`, no `parent_emitted`. Since the window was 120 s
  this is the **ordering** face of the defect (D4/D6): the second leg was evaluated before
  the first, so the chronological tail was `[Q, LG]` and the ordered-tail match failed.
  A further D4 data point, this time in a multi-rule context.

Cleanup: all 12 rules deleted, `correlation_rules total = 0`.

---

## 7. DIMENSION 5 — RESOLVE / CLEAR PATHS

`scripts/resolve_paths.py`. The resolve was deliberately placed **150 s after its firing**
so JetStream's fingerprint dedup (D1, whose ~2 min window would otherwise swallow it)
cannot hide the result — the resolve genuinely reaches the correlator. `window_seconds=300`.

**`flood` (rule 357) — PASS.** Four firings, then a resolve of the first, then a fifth
firing:
```
enriched  count=1 of 5     enriched  count=3 of 5
enriched  count=2 of 5     enriched  count=4 of 5
parent_emitted  flood_triggered count=5 distinct=1 min_count=5
```
The resolve produced **no additional count and no decrement**; the window still completed
on the fifth firing. (The `status: resolved` row in the raw dump is the count=1 audit row
of the *original firing* — the history record's status flips in place because a resolve
shares its firing's fingerprint. It is not an audit row for the resolve.)

**`co_occurrence` (rule 358) — PASS.** `A` fired, `A` resolved 150 s later, then `B`:
`cooccur_window_advancing distinct=1 of 2` → `cooccur_triggered distinct=2 of 2`. The
resolve did **not** remove `A` from the window; the pair still completed.

**`threshold` (rule 359) — resolve correctly ignored, but the rule MISSED (D5).**
`d1`, `d2`, resolve `d1`, `d3`:
```
enriched  threshold_advancing distinct=1 of 3 alerts=1
enriched  threshold_advancing distinct=1 of 3 alerts=2
enriched  threshold_advancing distinct=2 of 3 alerts=3
parent_emitted = 0
```
The `alerts=` counter advanced 1→2→3 across the resolve, so the resolve was neither
counted nor cancelling — the contract holds. The **miss** is D5: the HLL is bucketed by
`ts_ms // window_ms` (tumbling) inside a sliding alert ZSET
(`threshold.py:107-109`, `lua/add_threshold_and_check.lua` KEYS[2]), so the distinct set
reset mid-window and never reached 3. A no-resolve control with identical timing is in
`scen3/thr_control.log`.

**Conclusion for dimension 5:** the source-derived claim is confirmed empirically for
`flood`, `co_occurrence` and `threshold` — `is_resolved() → return None` means a resolve
neither advances, decrements nor cancels a counting window. **No new defect here.**
D1's blast radius is unchanged: only `persistence` (cancels its timer) and `parent_child`
(deletes the parent-present suppression key) act on resolves.

---

## 8. DIMENSION 6 — SUSTAINED VOLUME

STATUS: see the end of this file — filled in on completion of the final run.

---

## 9. WHAT REMAINS GENUINELY UNTESTABLE IN SHADOW

* **Synthetic parent emission and its labels.** In Shadow the correlator writes audit rows
  but emits nothing, so `emit_spec` label templating (`{group.<label>}` resolution),
  the per-minute synthetic cap, and the dispatch back-pressure path cannot be exercised.
* **The Phase-B dispatch fan-out itself**, including the known `_build_alert_group`
  label-shape defect (correlator posts `alerts[].labels` as a dict, AlertHistoryHandler
  expects the alertmanager v1 list form) — that is precisely why Phase B must not be
  switched on here.
* **Notifier-level suppression.** `suppressed_per_notifier` is observable as an audit
  outcome, but whether the email/other notifier actually withholds the page is a
  dispatch-side behaviour.
* **D3 end-to-end.** The Alert View Correlation tab's fix cannot be validated without a
  UI round-trip; the audit API was used as evidence throughout instead.
* **True production timing.** Real producers (vmalert, alertmanager webhooks) do send
  `startsAt`, so D6's blast radius in production depends on which sources omit it — that
  needs a per-source audit of OSCAR's ingest paths, which was out of scope here.
