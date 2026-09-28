# OSCAR Correlation — confirmed defects (2026-08-30)

## DEFECT 1 (HIGH) — a resolve within ~2 min of its firing NEVER reaches the correlator
### JetStream Nats-Msg-Id dedup is keyed on the alert fingerprint

`oscar-alertmanager/src/app/core/nats_publisher.py` publish_stored_sync():
    fingerprint = alert.get("fingerprint")
    headers = {"Nats-Msg-Id": str(fingerprint)} if fingerprint else None

A resolve carries the SAME fingerprint as its firing (that is what makes it the same
alert). JetStream therefore treats the resolve as a duplicate publish and silently
discards it inside the stream's dedup window. `oscar-eventmesh/api/src/app/core/
streams_bootstrap.py:71-73` creates OSCAR_ALERTS without an explicit duplicate_window,
so the JetStream DEFAULT (2 minutes) applies.

SCOPE (corrected 2026-08-30 after user challenge): CORRELATOR ONLY.
`oscar-alertmanager/src/tm/tasks.py` publishes to NATS and THEN does the notifier HTTP
POST ("D-01: publish first, HTTP POST second"; a publish failure explicitly falls
through to the POST). The dedup drops only the correlator's NATS copy. The HTTP
fan-out to AlertHistory / ESB ticketing / SMTP / logger is a SEPARATE branch and is
NOT affected. Verified in every probe: alert history recorded status=resolved correctly
(e.g. HIST: resolved 2de6777faf32 am=['aaaa1111bbbb2222']).
=> Normal alerting and ESB create+resolve are UNAFFECTED. This is why prior ESB
   resolve testing passed — it exercises a different transport.
=> Why it went unnoticed: the correlator only began consuming when Shadow was enabled
   2026-07-29, and Shadow emits nothing, so a false persistence parent leaves an audit
   row and no visible symptom. It is invisible until Phase B.
=> NOT a regression of an earlier fix: commit 84670ea78 ADDED the Nats-Msg-Id header
   (91-CR-02) for redelivery idempotency; no later commit re-keys or removes it.

Consequence: the correlator never observes the clear. Cancellation logic is CORRECT and
present (`oscar-correlator/src/app/patterns/persistence.py:253` -> is_resolved() ->
outcome `persistence_resolved_cancelled`; `patterns/base.py:123-144`) — it simply never
receives the message.

### PROOF (hand-built probes, no harness involved, same fingerprint both runs)
Probe A — resolve 8s after firing (INSIDE dedup window), rule 65, unresolved_for=30:
  04:25:41 firing  sent -> NO audit row
  04:25:49 resolved sent
  04:25:49.673  enriched       persistence_first_seen scheduled_at=1788063979012
  04:26:19.690  parent_emitted persistence_threshold_reached unresolved_for=30
  => the RESOLVE was mistaken for the first firing; FALSE synthetic parent

Probe B — resolve 150s after firing (PAST dedup window), rule 66, unresolved_for=300:
  04:28:04.703  enriched  persistence_first_seen scheduled_at=1788064381003
  04:30:14.730  enriched  persistence_resolved_cancelled          <-- CORRECT
  => identical payloads and fingerprint; ONLY the delay changed

Control — resolve-only alert, fresh fingerprint never published before:
  audit row present (released_no_trigger / no_decision_from_pattern)
  => resolves DO reach the correlator when not deduped. Ingest path is fine.

### Impact
Any alert that clears within ~2 minutes is invisible to the correlator.
- persistence: emits a synthetic parent for a condition that already cleared
- in Phase B (dispatch on) that synthetic parent becomes a NOTIFICATION and a TICKET
- affects every resolve-sensitive path INSIDE THE CORRELATOR (not only persistence);
  normal alerting, AlertHistory and ESB ticketing are NOT affected
Short-lived flapping alarms are exactly the common case.

### Independently reproduced by the harness
corrtest builtin persistence N01 is literally named `resolution-cancels-persistence`
(fire, then resolve, unresolved_for_seconds=30, expect NO emit). It FAILED with
`parent_emitted` in two separate scenarios (persistenceA, persistence-long-window)
before any of the probes above were built.

### Fix direction (NOT applied — user decision)
The dedup key must distinguish a firing from its resolve. Options:
  a) include status in the key: Nats-Msg-Id = f"{fingerprint}:{status}"
  b) include the alert's endsAt/updatedAt timestamp
  c) set an explicit, much smaller duplicate_window on OSCAR_ALERTS (mitigation only —
     narrows the race, does not remove it)
(a) is the smallest correct change: it preserves genuine redelivery dedup per lifecycle
state while letting firing and resolve through as distinct messages.

---

## DEFECT 2 (MEDIUM) — /validate accepts a rule that create then rejects, AFTER persisting it

POST /api/v1/correlation_rules/validate with a flood rule and NO emit_spec returns
    {"valid":true,"errors":[]}
POST /api/v1/correlation_rules with the identical body returns HTTP 422
    {"detail":"rule persisted but rejected by loader:
               emit_spec_alertname_required_for_pattern_flood",
     "error_code":"HTTP_422"}

Two problems:
1. validate and create disagree — validate is not a faithful dry run of create.
2. The error text says "rule persisted but rejected" — the row IS written to the rules
   table even though the API returns 422. Confirmed live: an orphaned rule (id 63) was
   left behind by a 422 create and had to be deleted manually. A caller that trusts the
   422 leaks a rule row on every failed create.

---

## Environment note
Both defects were found with CORRELATOR_DISPATCH_ENABLED=false (Shadow). Neither
required Phase B. All diagnostic rules created during this investigation (63, 64, 65, 66)
were deleted; correlation_rules total is back to 0.

---

## DEFECT 3 (HIGH) — Alert View "Correlation" tab can NEVER show correlation
### Wrong fingerprint domain in the UI query (same class as the harness bug)

`oscar-adminui/src/views/pages/alerts/CorrelationTab.js:90`
    const fingerprint = alert?.fingerprint          // AlertHistory 12-hex
...:104
    axios.get('/api/correlation-audit/' + encodeURIComponent(fingerprint))

CorrelationAudit.alert_fingerprint is the 16-hex `am_fingerprint`, NOT the 12-hex
AlertHistory `fingerprint`. The lookup therefore returns 0 rows for EVERY alert,
in every pipeline mode.

### PROOF — same alert (CORRTEST_COOCCURRENCE_P01_DISKFULL_11CY4VT9), both keys
  GET /api/v1/correlation_rules/audit?fingerprint=0564b2d974c0      -> total 0   []
      (12-hex, what the UI sends)
  GET /api/v1/correlation_rules/audit?fingerprint=106f21bede4aee8f  -> total 1   ['enriched']
      (16-hex am_fingerprint, what the audit table is keyed on)
Harness audit for the same run: co_occurrence P01 = PASS, auditOutcomes
['parent_emitted','enriched']. The correlation demonstrably happened.

### Why it stayed hidden
The empty-state text reads "No correlation rule evaluated or matched this alert. If
correlation dispatch is disabled platform-wide, most alerts will show this state until
it's enabled." That attributes the blank tab to Shadow mode, so an operator sees the
expected message and stops investigating. The tab would stay empty after Phase B too.

### Impact
- Operators cannot see correlation for ANY alert; the feature appears dead in the UI.
- Masks both real correlation AND real correlation failures.
- Independent of CORRELATOR_DISPATCH_ENABLED — turning on Phase B will NOT fix it.

### Fix direction (NOT applied)
Key the lookup on the alert's `am_fingerprint` label rather than `alert.fingerprint`.
The label is already present on every history record. Either resolve it client-side in
CorrelationTab.js, or have the /api/correlation-audit/ route translate 12-hex ->
am_fingerprint server-side. The same translation is needed for
/api/correlation-audit/children (parent_fingerprint).

---

## DEFECT 4 (HIGH) — correlator has NO cross-message ordering; sequence can fire on WRONG order
### Event time falls back to processing wall-clock, and processing is concurrent

Chain (all confirmed live):
1. No `startsAt` is sent -> AM publishes `"starts_at": None`
   (`oscar-alertmanager/src/tm/tasks.py:723`).
2. `parse_event_ts_ms()` (`oscar-correlator/src/app/patterns/base.py:90-113`) then returns
   `now_ms` — the EVALUATE-TIME wall clock, not the event time.
3. Processing order is nondeterministic twice over: AM celery runs `--pool=prefork
   --autoscale=8,2`, and the correlator does `sub.fetch(batch=10)` (`consume.py:652`)
   drained by `CORRELATOR_EVAL_CONCURRENCY=10` workers (`main.py:510-513`) with NO
   per-group serialisation.
4. sequence's ts_ms sort (`sequence.py:130-147`) therefore tie-breaks on arrival index and
   is effectively inert. parent_child has no ordering logic at all (`parent_child.py:182`).

### PROOF — run8, both failure directions in a single sweep
  P01 (CORRECT order sent):  depth=2 for the first alert, depth=1 for the second -> NO FIRE
  N01 (WRONG order sent):    parent_emitted / sequence_triggered -> FIRED ANYWAY
A sequence rule fired on out-of-order input. That is a FALSE POSITIVE, not just a miss.

### Deterministic reproducer — scripts/repro_seq.sh
  race     (back-to-back, no startsAt)                 fired 3/6   <- nondeterministic
  spaced   (20s gap)                                   fired 6/6
  stamped  (startsAt supplied)                         fired 5/6
  inverted (B posted first, startsAt says A is earlier) fired 4/4  <- event time GOVERNS
Event time, when present, fully determines the outcome. That pins the mechanism.

### Impact
- sequence correlations are unreliable in BOTH directions: missed real sequences AND
  synthetic parents raised for sequences that never happened in that order.
- parent_child is exposed to the same race (no ordering logic at all).
- Worse under load: more concurrency = more reordering. A quiet box hides it.

### Fix direction (NOT applied)
Populate and honour event time end-to-end: send/propagate `startsAt`, and make
`parse_event_ts_ms()` prefer it over `now_ms`. Optionally serialise evaluation per
correlation group. Harness side: corrtest should always send `startsAt`.

---

## DEFECT 5 (MEDIUM) — threshold distinct-count uses a TUMBLING bucket inside a SLIDING window
The HyperLogLog that counts distinct label values is bucketed by
`ts_ms // window_ms` (`threshold.py:107-109`, `lua/add_threshold_and_check.lua` KEYS[2]) —
a TUMBLING bucket — while the alert ZSET that backs the same rule is a SLIDING window.
A burst that straddles a bucket boundary has its cardinality split across two HLLs and
never reaches `min_distinct_count`.

### PROOF
  run5: alerts=3, distinct=1   (three distinct hosts, counted as one)
Deterministic reproducer — scripts/repro_threshold2.sh, startsAt-pinned, identical 3s spread:
  straddling the boundary   fired 0/3
  entirely inside a bucket  fired 3/3

### Impact
Threshold rules silently under-count and fail to fire, at a rate set by where the burst
lands relative to the bucket boundary. Independent of DEFECT 4.

### Fix direction (NOT applied)
Make the distinct-count window sliding to match the alert window (e.g. rotating/overlapping
HLL buckets summed over the window), or document threshold as explicitly tumbling.

---

## NOT A DEFECT — absence.N01 flake is a HARNESS artifact
corrtest hardcodes the absence observation window to 55s
(`internal/compiler/compiler.go:132-134`) while the last expected heartbeat is at +48s, and
`AuditReady` requires an audit row for all 7 alerts. Too tight; the run goes INCONCLUSIVE.
Fix belongs in the harness, not OSCAR.

## REJECTED HYPOTHESES (tested with evidence, ruled out)
- timer-subsystem races
- stale Redis windows leaking between runs (bounded at 2, never grew over 8 sweeps)
- the DEFECT 1 dedup race affecting patterns beyond persistence

---

## DEFECT 4 — LOAD QUANTIFICATION (2026-08-30, W7)
Two 20-group sequence load tests, same rule shape (window 300s, group_by=["site"]),
20 groups fired concurrently, all alerts confirmed landed in history (no loss):

| within-group timing            | groups fired | rate |
|--------------------------------|--------------|------|
| A then B sequential (~100ms)   | 20 / 20      | 100% |
| A and B SIMULTANEOUS           |  8 / 20      |  40% |

=> 60% of CORRECT-ORDER sequences are silently MISSED when the two legs arrive
   concurrently. This is the D4 ordering race, quantified at scale.

### Secondary results from the same tests (positive)
- NO alert loss: 40/40 alerts landed in both runs under 20-way concurrency.
- NO cross-group contamination: with A->B sequential, all 20 groups fired independently.
- group_by_labels partitioning is CORRECT under load.
=> The defect is strictly within-group ordering, not group isolation and not ingest.

### Method note
The first test posted A then B sequentially inside each group, so its concurrency was
ACROSS groups only — it does NOT exercise D4 and its 20/20 must not be read as evidence
that ordering is fine. Only the simultaneous variant tests the race.

### API note discovered here
GET /api/v1/alerts/history IGNORES the `size` parameter and returns 10 records per page
regardless (`per_page` in the response is always 10). Callers must PAGE; a single call
looks like alert loss when it is only paging. Cost me a false "alerts are being dropped"
read mid-test.

---

## DEFECT 1 — BLAST RADIUS INSIDE THE CORRELATOR (W6, 2026-08-30)
All 8 patterns call `is_resolved()`, so a swallowed resolve reaches every one. What each
DOES with it decides the impact:

| pattern | resolve handling (file:line) | impact if the resolve is SWALLOWED |
|---|---|---|
| flood | `flood.py:69` -> `return None` | NONE — it drops resolves anyway |
| co_occurrence | `co_occurrence.py:64` -> `return None` | NONE |
| cross_source | `cross_source.py:65` -> `return None` | NONE |
| threshold | `threshold.py:80` -> `return None` | NONE |
| sequence | `sequence.py:70` -> `return None` | NONE |
| absence | `absence.py:113` -> `return None` | NONE |
| persistence | `persistence.py:253` -> cancels the timer | **FALSE synthetic parent** (proven) |
| parent_child | `parent_child.py:114-117` -> DELETEs the parent-present key | **MISSED PAGES** |

### The parent_child case is the serious one
`parent_child.py:117` deletes the suppression key when the PARENT resolves, so children of
an already-cleared incident stop being suppressed. The code comment states the pre-fix
behaviour explicitly: *"a resolve event fell through to SET-EX below — re-arming the key and
EXTENDING suppression for a fresh window (children of a cleared incident kept getting
suppressed -> missed pages)"*.

DEFECT 1 REINTRODUCES THAT EXACT FAILURE AT THE TRANSPORT LAYER. The `is_resolved` fix in
parent_child is correct but unreachable: within the ~2min dedup window the resolve never
arrives, so the suppression key is never deleted and survives its full TTL. Children of a
cleared incident keep being suppressed — i.e. real alerts are silently withheld from
notification.

That is a MISSED PAGE, not a false page. It is the more dangerous direction, and it means
D1 is not "a persistence bug" — it defeats a deliberate parent_child safety fix.

### Why the campaign did not catch it empirically
Every builtin case except persistence.N01 is FIRING-ONLY — no builtin injects a resolve into
parent_child. The 8/8 parent_child PASS results say nothing about this path.

### PROVEN LIVE 2026-08-30 (rule 277, delay-controlled A/B — no longer a prediction)
Identical sequence both arms; ONLY the resolve delay differed.

ARM A — parent resolve at +14s (INSIDE the ~120s dedup window), site siteA:
  child#1 bbbb..0001 (before resolve) -> suppressed_per_notifier  parent_child_active_parent_present
  child#2 bbbb..0002 (AFTER resolve)  -> suppressed_per_notifier  <-- MISSED PAGE
  parent  aaaa..0001                  -> enriched/parent_registered ONLY
                                         NO parent_resolved_suppression_cleared row at all

ARM B — parent resolve at +156s (OUTSIDE the dedup window), site siteB:
  child#1 dddd..0001 (before resolve) -> suppressed_per_notifier
  child#2 dddd..0002 (AFTER resolve)  -> released_no_trigger  parent_child_no_active_parent  <-- CORRECT
  parent  cccc..0001                  -> released_no_trigger  parent_resolved_suppression_cleared  <-- resolve PROCESSED
                                         + enriched/parent_registered

CONCLUSION: the absence of any audit row for the Arm A parent resolve is the smoking gun —
the message never reached the correlator, so parent_child.py:117 never ran, the suppression
key survived its full TTL, and a child of an ALREADY-CLEARED incident was silently withheld
from notification. Delay is the only variable. Causal chain closed.

SEVERITY: this makes D1 a MISSED-PAGE defect, not merely a false-parent defect. It also
means the deliberate is_resolved fix in parent_child (whose own comment describes exactly
this failure as the pre-fix bug) is UNREACHABLE under production timing.
