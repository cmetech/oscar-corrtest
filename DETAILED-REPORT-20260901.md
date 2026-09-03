# OSCAR Correlation Campaign — Detailed Engineering Report

**Compiled:** 2026-09-01
**Campaign window:** 2026-08-26 → 2026-09-01
**Environment:** OSCAR dev box `192.168.29.195` (`/ext/mw`), correlator in **Shadow** mode
(`CORRELATOR_DISPATCH_ENABLED=false`, declared `--pipeline-mode phase_a_audit_only`) throughout.
**Sources:** every claim below is traceable to a file in this repository; source filenames are
cited inline. Raw evidence directories: `sweep/`, `sweep2/`, `sweep3/`, `flake/run1..run8/`,
`scen2/`, `scen3/`, `neg1/`, `sweep-postfix/`. Where the source carries raw audit rows they
are reproduced **verbatim**.

---

## 1. Executive summary

The campaign set out to prove or disprove every OSCAR correlation pattern (all 8), positive
and negative, using evidence from OSCAR's own CorrelationAudit API — find defects, write
fixes on disk, deploy nothing without approval (CAMPAIGN-STATE.md §MISSION). It ended with
**fix set 1 deployed and live-proven on 2026-09-01, and the first fully green sweep of the
campaign: 8/8 patterns PASS, both cases each, cleanup CLEAN** (`sweep-postfix/*.json`,
HANDOFF-20260901-correlation-fixes.md).

Along the way the campaign found that the correlator itself was largely sound — 6 of 8
patterns verified working over 8 sweeps (MORNING-REPORT.md) — but that the plumbing around
it, the UI in front of it, and its time model carried serious defects:

| Defect | Sev | One line | Status |
|---|---|---|---|
| **D1** | HIGH | JetStream `Nats-Msg-Id` dedup keyed on the bare alert fingerprint → a resolve within ~2 min of its firing never reaches the correlator; persistence emits a false parent AND parent_child keeps suppressing children of an already-cleared incident (**missed pages**, proven live) | **DEPLOYED + proven 09-01** |
| **D1-b** | HIGH | Twin of D1 one layer down: the correlator's own decision-dispatch dedup key carried no lifecycle state, so a same-window resolve was skipped as a "redelivery no-op" — discovered *by the D1 deploy verification itself* | **DEPLOYED + proven 09-01** |
| **D2** | MED | `/validate` accepted rules that create rejects; the 422 create still persisted the row (leaked rows); the UI import dialog bypassed the gate entirely | **DEPLOYED + proven 09-01** |
| **D3** | HIGH | Alert View Correlation tab queried the 12-hex history fingerprint against a table keyed on the 16-hex `am_fingerprint` → "not correlated" for EVERY alert, in every mode, forever; its empty-state text falsely blamed Shadow | **DEPLOYED 09-01** (bundle-verified; browser check by user) |
| **D4** | HIGH | No cross-message ordering: absent `startsAt` collapses event time onto evaluate-time wall clock under 10-way concurrency → sequence misses 60% of correct-order pairs under load AND fires on wrong-order input; parent_child loses 40% of child suppressions | **OPEN — design decided (event time), not built** |
| **D5** | MED | threshold's distinct-count HyperLogLog is a *tumbling* bucket inside a *sliding* alert window → bursts straddling the bucket boundary under-count and never fire | **OPEN — decision: make it sliding** |
| **D6** | HIGH | Window *membership* also follows processing time, not event time: alerts 28 s apart correlated by a 20 s rule (false correlation) and alerts 2 s apart missed by the same rule — the first false-correlation class outside sequence | **OPEN — same root as D4, one fix (startsAt end-to-end)** |
| **Synthetic-clearing gap** | GAP | Nothing ever resolves a synthetic parent; design finalized (negation-of-birth + hold_seconds, no cascade), not built | **OPEN — build before Phase B** |

**Phase B (dispatch on) remains blocked** independently of all of the above: the pre-existing
AlertHistory dict-vs-list label defect and the dead scheduler lag-observer
(FINDINGS-OSCAR-BACKEND.md §5, HANDOFF §PHASE B). `CORRELATOR_DISPATCH_ENABLED` was never
flipped and must not be.

What was proven to work is equally important: no-rule baseline (correlation is strictly
additive to normal alerting), 12 rules live at once with **zero mis-attribution**, exact
`group_by_labels` partitioning under 20-way load, resolves correctly ignored by all counting
patterns, and no alert loss at moderate concurrency (FINDINGS-COVERAGE.md, FINDINGS-FLAKE.md).

---

## 2. Background

### 2.1 What the correlator is

`oscar-correlator` is a FastAPI service (:5400) that consumes every stored alert off NATS
JetStream and evaluates it against operator-defined correlation rules, one of 8 patterns
each. In **Shadow** (Phase A, the state of this box since 2026-07-29 — FINDINGS-DEFECTS.md)
it evaluates and writes CorrelationAudit rows **unconditionally** but emits nothing; in
**Authoritative** (Phase B) it would also emit synthetic parent alerts and become the sole
notification dispatch path (FINDINGS-OSCAR-BACKEND.md §2, §8 Option B).

Ingest path (proven live, FINDINGS-OSCAR-BACKEND.md §1): the alertmanager celery worker
publishes each stored alert to subject `oscar.alerts.stored`
(`oscar-alertmanager/src/app/core/nats_publisher.py`); the correlator binds a durable PULL
consumer `correlator-main` on stream `OSCAR_ALERTS` (created by eventmesh-api
`streams_bootstrap.py` with **no explicit `duplicate_window`**, so the JetStream default of
2 minutes applies — the seed of D1). Crucially, the NATS publish and the HTTP notifier
fan-out (AlertHistory / ESB ticketing / SMTP / LOGGER) are **separate branches**
(`oscar-alertmanager/src/tm/tasks.py`, "publish first, HTTP POST second") — which is why D1
is correlator-only and why prior ESB resolve testing had always passed.

### 2.2 The 8 patterns

Rule schema empirically confirmed for all 8 (FINDINGS-OSCAR-BACKEND.md §7). Top level:
required `name`, `pattern`, `window_seconds` (≤3600), `match_criteria`; optional
`group_by_labels` (List[str] — a dict is rejected), `emit_spec`, `for_notifiers`,
`priority` (default 100), `max_synthetic_per_minute`, `enabled`, `description`. Every
`match_criteria` schema is `extra="forbid"` **including the inner match clause — the only
key allowed is `alertname`** (no regex, no operators, no extra label matchers), so test
isolation must come from unique alertnames and partitioning from `group_by_labels`.

| pattern | match_criteria shape (validated `{"valid":true}` live) | constraint |
|---|---|---|
| flood | `{"match":{"alertname":"A"},"min_count":3}` | min_count ≥ 1 |
| co_occurrence | `{"required_matches":[{"alertname":"A"},{"alertname":"B"}],"min_matches":2}` | required ≥ 2 |
| sequence | `{"sequence":[{"alertname":"A"},{"alertname":"B"}]}` | ≥ 2, ordered |
| persistence | `{"match":{"alertname":"A"},"unresolved_for_seconds":60}` | **≥ 30 hard floor** |
| absence | `{"expected_match":{...},"expected_every_seconds":60,"absent_for_seconds":180}` | absent_for ≥ expected_every |
| parent_child | `{"parent_match":{...},"child_matches":[...],"suppress_children_for_notifiers":[],"tag_children_for_notifiers":[]}` | child_matches ≥ 1 |
| cross_source | `{"required_sources":[{"source":"snmp","match":{...}},{"source":"splunk","match":{...}}]}` | ≥ 2 |
| threshold | `{"match":{...},"distinct_label":"host","min_distinct_count":5}` | HyperLogLog |

Live guardrails (silent rejection risk at load): `MAX_WINDOW_DURATION=3600`,
`MAX_ALERTS_PER_WINDOW=1000`, `MAX_GROUP_CARDINALITY=10000`, `MAX_MATCHERS_PER_RULE=10`,
`MAX_SYNTHETIC_PER_MINUTE_HARD_CAP=1000`, `MAX_ACTIVE_WINDOWS=10000`;
`WINDOW_GRACE_SECONDS=5`, `TIMER_POLL_INTERVAL_MS=100` (timer patterns need
≥ unresolved_for + ~10 s of observation). (FINDINGS-OSCAR-BACKEND.md §7.)

### 2.3 Shadow vs Phase B — what the flag actually gates

`oscar-correlator/src/app/core/consume.py` (FINDINGS-OSCAR-BACKEND.md §2):
- `:539` `await audit_writer.buffer(decision)` — **before any gate** (D-20 buffer-first);
  the comment at `:544-546` says buffering is UNCONDITIONAL: Phase A records what WOULD
  have notified.
- `:556-571` only inside the `dispatch_eligible` gate: `synthetic_emitter.emit()` and the
  held-child accumulation.

So Shadow gives full windows, pattern evaluation, and
`parent_emitted / enriched / suppressed_per_notifier / released_no_trigger` audit rows,
withholding only the synthetic alert and correlator→notifier dispatch. `syntheticCount: 0`
is *correct* in Shadow — a fact whose misunderstanding cost the campaign its first days
(§3.2) and whose mis-statement in the UI's empty-state text is part of D3.

### 2.4 How the harness tests

`oscar-corrtest` is a self-contained Go black-box qualification harness (~24k lines,
CODE_CONTEXT.md). Lifecycle per run (CODE_CONTEXT.md §1):

```
target add ──► doctor ──► plan (compile, offline) ──► run ──► evidence ──► cleanup
```

1. **Doctor**: validates a compiled flood rule against `/correlation_rules/validate`,
   injects one probe alert, polls history until it appears, diffs every reserved label
   sent-vs-returned (`Compatible` = accepted ∧ found ∧ non-empty server fingerprint ∧ no
   missing labels).
2. **Run**: `QUEUED → PREFLIGHT → SETTING_UP → INJECTING → OBSERVING → ASSERTING →
   CLEANING_UP → COMPLETED`; each run creates temporary rules tagged `run=<runId>` in the
   description, injects deterministic alerts through the public API, and serializes under
   `runMu`.
3. **Evidence**: per case, poll AlertHistory + **CorrelationAudit** (+ notification audit
   for parent_child) until assertions pass and hold through a stabilization re-read, or the
   observation deadline expires (INCONCLUSIVE, not FAIL, on a missing read).
4. **Cleanup**: delete only rule IDs OSCAR returned; resolve every injected alert by the
   *server* fingerprint; cleanup status (`CLEAN|DIRTY|…`) tracked independently of verdict;
   exit code 4 (dirty) dominates the verdict.

The load-bearing assertion convention of the whole campaign: **verdicts come from
CorrelationAudit rows keyed on the 16-hex `am_fingerprint` LABEL** — never the 12-hex
AlertHistory `fingerprint` field, never `syntheticCount`, never the UI
(FINDINGS-COVERAGE.md preamble, SKILL.md §4, PITFALL 1).

---

## 3. Timeline of the campaign

Dates from the source files themselves.

### 3.1 Harness bring-up — the two contract adapters (2026-08-26/27)

The upstream v1.0.2 binary does not work against this OSCAR build. Two harness-side
contract adapters were required (TEST_CAMPAIGN_CONTEXT.md, uncommitted local edits):

1. `internal/oscar/flextime.go` (new): a `flexTime` type accepting OSCAR's **zone-less**
   timestamps (`2026-08-27T17:10:36.326000`). Go's `time.Time` requires RFC3339, so all
   three decode sites (historyWire.createdAt, auditWire.created_at,
   notificationWire.created_at) previously failed with "OSCAR returned malformed JSON".
2. `internal/oscar/client.go` `historyWire.normalized()`: OSCAR returns `alertname` as a
   **top-level field**, not inside the labels array; it is folded into the label map,
   otherwise doctor reports `missingLabels:["alertname"]` and `labelsSurvived:false`.

These are harness-side adapters; the explicit note is "Do NOT 'fix' OSCAR for them."
Build is via podman (no host Go toolchain):
`golang:1.27.0-bookworm go build -o bin/oscar-corrtest ./cmd/oscar-corrtest`.
Target `tgt_6KYCH7ESVS65T87RNBPQ97SSFK` registered `--insecure` (self-signed cert
`CN=reverseproxy.ericsson.com` addressed by IP → SAN mismatch; `--ca-file` alone fails).
Doctor then passed: `Compatible: true, EXIT 0`.

### 3.2 The INCONCLUSIVE mystery → fingerprint-domain discovery (2026-08-29)

The first real run, `run builtin:flood` (run `crt_48E25NV13XH51S3E0PC1M8HFX2`), came back
**INCONCLUSIVE**: all 5 source alerts reached history, rules created and cleaned correctly,
yet `syntheticCount: 0, auditOutcomes: null` (TEST_CAMPAIGN_CONTEXT.md §BLOCKER).

The backend investigation (FINDINGS-OSCAR-BACKEND.md §0) found **the correlator was NOT
broken** — it had correlated that very run and written a `parent_emitted` audit row at
20:39:33. The harness saw nothing for two independent reasons:

1. **Fingerprint domain mismatch (harness bug).** corrtest polled
   `audit?fingerprint=<12-hex>` using the AlertHistory `fingerprint`; but
   `CorrelationAudit.alert_fingerprint` is a different 16-hex value carried on the alert as
   the `am_fingerprint` **label**. Zero rows can ever match. Verbatim evidence:
   ```
   26158  rule 3  5964b28730884e38  parent_emitted   <-- FLOOD FIRED
   26154/26160/26162/26163 rule 3   enriched
   Alert whose history fingerprint is 31038f7b3e62 carries:
     oscar_fingerprint 31038f7b3e62      (12 hex, AlertHistory)
     am_fingerprint    0be9385a228040e7  (16 hex, CorrelationAudit key -> row 26162)
   ```
2. **Shadow suppresses only the emit**; `syntheticCount:0` is correct and expected; audit
   rows are written unconditionally.

The third patched harness behaviour — audit keying on `am_fingerprint` — followed from
this. This same wrong-domain class later turned out to be a **product** bug too (D3).
The session also mapped the env-file chain, the real debug endpoints
(`/debug/windows`, `/debug/timers`, `/debug/simulate` — side-effect-free dry run), the
full rule schema, and recorded the two Phase-B blockers (FINDINGS-OSCAR-BACKEND.md §4-§9).

### 3.3 Iterated campaign loop, sweeps and the flake investigation (2026-08-29 → 08-30)

The resumable loop (`/correlation-campaign`, state in CAMPAIGN-STATE.md) then ran 8
iterations, with two agent lanes (LANE-FLAKE, LANE-FIXES) in flight:

- **ITER1**: a reporting bug of the campaign's own (a shell loop aborting on one bad file)
  had dropped threshold from the sweep3 scoreboard; corrected — threshold PASSed.
  sweep3 true result: flood/co_occurrence/cross_source/threshold PASS; sequence FAIL;
  parent_child FAIL; persistence FAIL (N01=D1); absence INCONCLUSIVE.
- **ITER2**: LANE-FIXES absorbed — D1/D2/D3 fixed **on disk only** (326 insertions /
  2 deletions across 7 files), unit-tested in isolation (17+24+22+62 passing), nothing
  deployed.
- **ITER3**: interim flake read after 6 sweeps produced **two corrections of earlier
  conclusions**: (a) persistence.N01 is DETERMINISTIC (6/6 FAIL), not flaky — it is D1;
  (b) the sweep3 parent_child FAIL and absence INCONCLUSIVE did NOT reproduce (transient,
  plausibly a mid-session OSCAR restart) — sweep3 declared not a reliable baseline;
  "results are flaky" was too broad a framing.
- **ITER4**: stale-window suspicion **closed as a non-issue** — `active_windows_total`
  bounded at ≤2 across all 8 runs, zero accumulation; the 2 persistent entries are
  pre-campaign `cooccur:7/8` keys. Also: an in-flight LANE-FLAKE rule looked like a leak
  twice; the `run=<id>` description check "earned its keep".
- **ITER5** (08-30): LANE-FLAKE absorbed → FINDINGS-FLAKE.md; **two new OSCAR defects
  (D4, D5)** plus one harness artifact (absence.N01) out of the 8-sweep flake table;
  MORNING-REPORT.md written.
- **ITER6** (W7): D4 quantified at scale — 20-group sequence load test, 20/20 sequential
  vs **8/20 simultaneous** (60% miss). Method warning recorded: the sequential run's 20/20
  does NOT exercise D4 (across-group concurrency only). API gotcha found: history ignores
  `size`, always 10/page — produced a false "alerts being dropped" read before caught.
- **ITER7** (W6): D1 blast radius answered **from source** across all 8 patterns — only
  persistence and parent_child act on resolves; the parent_child consequence (missed pages)
  identified as an untested prediction.
- **ITER8**: the prediction **proven live** (rule 277, delay-controlled A/B — §4.1 below).
  Recommendation reordered: D1 above D3 — the only defect that makes OSCAR silently
  withhold real alerts.

### 3.4 Coverage pass and D6 (2026-08-31 → 09-01)

FINDINGS-COVERAGE.md closed out the coverage matrix: the 10 authored scenarios finished,
10 *new* negative scenarios authored and run, load extended to all patterns, 12 rules at
once, resolve/clear paths, sustained volume. It found **D6** (window membership in
processing time — the first false-correlation class outside sequence), quantified D4 for
parent_child (40%), showed D5 is worse than a boundary-straddle corner case, and hit the
pre-existing ingest burst cap (~60 alerts/batch) as a measurement constraint.

### 3.5 Adversarial review → corrections (2026-09-01)

REVIEW-FIXES-ADVERSARIAL.md attacked the three on-disk fixes ("assume broken until
proven"; every attack executed as a code trace, isolated unit test, or read-only live
probe). Verdicts: Fix 1 **DEPLOY-WITH-CHANGE** (the flap hole — §4.2), Fix 2
**DEPLOY-WITH-CHANGE** (mirror drift + the UI import bypass), Fix 3 **DEPLOY**. Both
demanded corrections were applied on disk the same day (FIXES-PROPOSED.md header).

### 3.6 Deploys, D1-b discovery-by-verification, final green sweep (2026-09-01)

Per the approved overnight orders (CAMPAIGN-STATE.md §OVERNIGHT ORDERS: gate on the
coverage report, tag rollback images, deploy one at a time, live-verify each, roll back on
any failure), fix set 1 was deployed: D1 (alertmanager), D3 (adminui), D2 (correlator);
rollback tags `oscaraiapp/alertmanager_middleware:pre-corrfix-20260901` and
`oscaraiapp/correlator:pre-d1b-20260901` (HANDOFF).

**The D1 live verification itself discovered D1-b**: with transport dedup fixed, the flap
probe still lost the same-window resolve — the correlator's *internal* decision-dispatch
dedup key had the identical hole one layer down (FIXES-PROPOSED.md §D1-b, proven live with
rule 362). It was fixed and deployed the same day. The document's own record shows the
process was self-correcting, not flawless: a harness bug found first, a product UI bug of
the same class found next, a fix hole found at adversarial review, and a second layer of
the same defect found only by deploy-time verification.

**Final state:** post-fix regression sweep 8/8 PASS both cases, cleanup CLEAN
(`sweep-postfix/`); persistence flipped from 0-for-8 FAIL to PASS; no-rule baseline
re-proven on the fixed code — with 0 rules the correlator now observes firing AND resolve
(2 audit rows, `released_no_trigger/no_matching_rules`) and adds nothing (HANDOFF §RESULT).

---

## 4. Defect chapters

### 4.1 DEFECT 1 (HIGH) — a resolve within ~2 min of its firing never reaches the correlator

**Plain language.** When an alarm clears shortly after it fires, the correlator never hears
about the clear. It goes on believing the problem is still active — so it raises a
"persistent problem" parent for a condition that already went away, and (worse) keeps
suppressing the children of an incident that has already cleared, silently withholding real
alerts from notification.

**Root cause** (FINDINGS-DEFECTS.md). `oscar-alertmanager/src/app/core/nats_publisher.py`
`publish_stored_sync()`:
```python
fingerprint = alert.get("fingerprint")
headers = {"Nats-Msg-Id": str(fingerprint)} if fingerprint else None
```
A resolve carries the SAME fingerprint as its firing — that is what makes it the same
alert — so JetStream treats the resolve as a duplicate publish and silently discards it
inside the stream's dedup window. `oscar-eventmesh/api/src/app/core/streams_bootstrap.py:71-73`
creates OSCAR_ALERTS with no explicit `duplicate_window` → JetStream default **2 minutes**.
Not a regression: commit 84670ea78 ADDED the header (91-CR-02) for redelivery idempotency;
no later commit re-keyed it. The correlator's cancellation logic is correct and present
(`patterns/persistence.py:253` → `is_resolved()` → `persistence_resolved_cancelled`) — it
simply never receives the message.

**Scope correction (2026-08-30, after user challenge).** CORRELATOR ONLY. The dedup drops
only the correlator's NATS copy; the HTTP fan-out to AlertHistory / ESB / SMTP / LOGGER is
a separate branch (`tm/tasks.py`, publish-first-POST-second, publish failure falls through)
and every probe confirmed history recorded `status=resolved` correctly. This is why prior
ESB resolve testing passed, and why the defect stayed invisible: the correlator only began
consuming when Shadow was enabled 2026-07-29, and Shadow emits nothing — a false
persistence parent leaves an audit row and no visible symptom until Phase B.

**Live evidence — hand-built probes, same fingerprint both runs, only the delay differed
(FINDINGS-DEFECTS.md, verbatim):**
```
Probe A — resolve 8s after firing (INSIDE dedup window), rule 65, unresolved_for=30:
  04:25:41 firing  sent -> NO audit row
  04:25:49 resolved sent
  04:25:49.673  enriched       persistence_first_seen scheduled_at=1788063979012
  04:26:19.690  parent_emitted persistence_threshold_reached unresolved_for=30
  => the RESOLVE was mistaken for the first firing; FALSE synthetic parent

Probe B — resolve 150s after firing (PAST dedup window), rule 66, unresolved_for=300:
  04:28:04.703  enriched  persistence_first_seen scheduled_at=1788064381003
  04:30:14.730  enriched  persistence_resolved_cancelled          <-- CORRECT

Control — resolve-only alert, fresh fingerprint never published before:
  audit row present (released_no_trigger / no_decision_from_pattern)
  => resolves DO reach the correlator when not deduped. Ingest path is fine.
```
Independently reproduced by the harness before any probe was built: builtin persistence
N01 (`resolution-cancels-persistence`) failed with `parent_emitted` in two separate
scenarios — and, per FINDINGS-FLAKE.md §5, was **8/8 FAIL, deterministic** (the builtin
resolves at +10 s, always inside the window).

**Blast radius inside the correlator (W6, answered from source).** All 8 patterns call
`is_resolved()`; six (`flood.py:69`, `co_occurrence.py:64`, `cross_source.py:65`,
`threshold.py:80`, `sequence.py:70`, `absence.py:113`) just `return None` on a resolve —
no impact. Two act on it:
- **persistence** (`persistence.py:253`) cancels the timer → swallowed resolve = **false
  synthetic parent** (proven above);
- **parent_child** (`parent_child.py:114-117`) DELETEs the parent-present suppression key
  → swallowed resolve = the key survives its full TTL and children of an already-cleared
  incident keep being suppressed = **MISSED PAGES**. The code comment at that site
  documents this precise failure as the pre-fix bug a deliberate fix had addressed —
  *"a resolve event fell through to SET-EX below — re-arming the key and EXTENDING
  suppression for a fresh window (children of a cleared incident kept getting suppressed
  -> missed pages)"* — **D1 reintroduces that exact failure at the transport layer**,
  making the `is_resolved` fix unreachable under production timing.

Why the campaign could not have caught this empirically: every builtin case except
persistence.N01 is FIRING-ONLY; parent_child's 8/8 PASS said nothing about this path.

**Missed-page proof, live 2026-08-30 (rule 277, delay-controlled A/B, verbatim):**
```
ARM A — parent resolve at +14s (INSIDE the ~120s dedup window), site siteA:
  child#1 bbbb..0001 (before resolve) -> suppressed_per_notifier  parent_child_active_parent_present
  child#2 bbbb..0002 (AFTER resolve)  -> suppressed_per_notifier  <-- MISSED PAGE
  parent  aaaa..0001                  -> enriched/parent_registered ONLY
                                         NO parent_resolved_suppression_cleared row at all

ARM B — parent resolve at +156s (OUTSIDE the dedup window), site siteB:
  child#1 dddd..0001 (before resolve) -> suppressed_per_notifier
  child#2 dddd..0002 (AFTER resolve)  -> released_no_trigger  parent_child_no_active_parent  <-- CORRECT
  parent  cccc..0001                  -> released_no_trigger  parent_resolved_suppression_cleared  <-- resolve PROCESSED
```
The absence of any audit row for the Arm A parent resolve is the smoking gun; delay is the
only variable; causal chain closed (FINDINGS-DEFECTS.md).

**Impact.** Any alert clearing within ~2 minutes is invisible to the correlator —
short-lived flapping alarms are exactly the common case. In Phase B the false parent
becomes a notification and a ticket; the parent_child direction silently withholds real
alerts. Missed pages outrank false pages, which is why D1 moved to the top of the deploy
order (MORNING-REPORT.md).

**Fix** (FIXES-PROPOSED.md §D1). One-line key change in `nats_publisher.py:431-432`, plus
the review's correction (§4.2):
```python
if fingerprint:
    status = str(alert.get("status") or "unknown").strip().lower() or "unknown"
    starts_at = alert.get("starts_at") or ""
    headers = {"Nats-Msg-Id": f"{fingerprint}:{status}:{starts_at}"}
```
Firing and resolve become distinct messages while genuine redelivery still dedupes per
lifecycle state. `status` is the top-level string set in `tm/tasks.py
create_notification_payloads()` — the same field the correlator's `is_resolved()`
(`patterns/base.py:136`) reads. Blast radius: only the NATS path to `oscar.alerts.stored`;
JetStream now accepts up to 2 messages per fingerprint per window per lifecycle state; the
only consumer is the correlator, which is designed to see resolves. Deliberately NOT
changed: `OSCAR_ALERTS duplicate_window` (mitigation-only option, cross-service blast
radius). Tests: 20 passed on `test_nats_publisher.py` (4 new + 3 post-review).

**Deployed 2026-09-01** (rebuild + restart alertmanager API **and** celery worker — the
worker is the publisher). **Live proof:** fire+clear@10 s → audit shows
`persistence_resolved_cancelled` (HANDOFF fix table); persistence builtin flipped to PASS
in `sweep-postfix/persistence.json` (P01=PASS N01=PASS, CLEAN).

#### The flap-hole correction found at adversarial review (self-correction record)

The originally authored key was `f"{fingerprint}:{status}"` only. REVIEW-FIXES-ADVERSARIAL
CONFIRMED attack 1a: fire(t0) → resolve(t+10s) → **RE-FIRE(t+20s)** yields keys
`fp:firing, fp:resolved, fp:firing` — the re-fire collides with the first firing inside the
window and JetStream ACKs it `duplicate=true` and discards a genuinely NEW episode.
Consequences: persistence never re-arms (missed page — "the fix converts D1 from a
false-page defect into a missed-page defect for flapping alerts"); parent_child suppression
never re-registers (noise pages); and **under Phase B, total loss** — `tm/tasks.py:570-586`
skips the legacy HTTP fan-out when the correlator is authoritative and
`nats_publish_failed` is False, and a dedup drop is a *successful* publish, so the re-fire
reaches neither the correlator nor AlertHistory/ESB/SMTP. Proven by executable attack test
`test_attack_flap_hole.py` (passes against the then-fixed publisher, i.e. the collision
existed). The `starts_at` suffix closes it: AM keeps `startsAt` stable per firing episode
(group_interval re-notifications carry the same value → still deduped) and mints a new one
for a re-fire → new key → delivered. `starts_at: None` (trap sources) degrades to exactly
the status-only key — no worse; closing that residue is the D4 startsAt work. Attacks that
failed to break the fix: resolve→resolve (correctly deduped), status-domain edge cases,
re-notification double-count, header consumers (nothing else parses `Nats-Msg-Id`),
fingerprint-absent path preserved.

### 4.2 DEFECT 1-b (HIGH) — the correlator's own dedup key eats a same-window resolve

**Plain language.** Fixing the transport was not enough: the correlator has its *own*
duplicate-suppression key for decisions, and it had the identical blind spot — a resolve
landing in the same window as its firing was mistaken for a redelivery of the firing and
skipped. Found only when the D1 deploy was being verified live.

**Root cause** (FIXES-PROPOSED.md §D1-b). `oscar-correlator/src/app/core/consume.py`
`_dedup_key(fingerprint, rule_id, window_id)` carried no lifecycle state. **Proven live
2026-09-01**: rule 362, fp `feed0000feed0002`, window `362:global:1788247542000`, resolve
@+8 s → log `"dedup skip (redelivery no-op)"` — the persistence CANCEL decision
(`persistence_resolved_cancelled`) silently skipped. Both decisions carry outcome
`enriched`, so outcome cannot discriminate; reason strings embed evaluate-time values
(`persistence_first_seen` embeds `scheduled_at`, derived from `now_ms` when startsAt is
absent), so keying on the reason would break redelivery dedup.

**Fix.** Mirror the transport fix: `_lifecycle_token(alert)` =
`{status(normalized)}:{starts_at (fallback starts_at_ms), verbatim, unparsed}` appended to
the key: `oscar:correlator:dedup:{fp}:{rule_id}:{window_id}:{status}:{starts}`. Built ONLY
from fields carried verbatim on the inbound message — never evaluate-time values — so a
NATS redelivery deterministically reproduces its key. A self-attack table in
FIXES-PROPOSED.md covers: flap trio → 3 distinct keys; redelivery of each → deduped;
group_interval re-notification → deduped; resolve→resolve → deduped (idempotent cancel);
timer-fired decisions unaffected (verified: `timer_subsystem.py` `_fire_one` never calls
`_process_decision`); rolling-deploy key-shape change → at worst one bounded duplicate
audit row per in-flight message (D-08 bounded-duplicate stance, no loss direction).
Residual: startsAt-less (trap-source) alerts degrade to status-only — fire/resolve still
discriminated (the proven defect closed); a re-fire in the SAME window still collides,
bounded by window rotation, owned by D4. New test file `test_consume_dedup_d1b.py`
(4 tests); full correlator suite **472 passed, 1 skipped**, zero pre-existing failures.

**Deployed 2026-09-01** (correlator only; rollback tag `:pre-d1b-20260901`). **Live
proof:** the same-window flap probe now shows all 4 audit rows including the cancel
(HANDOFF fix table); persistence 8/8-FAIL → PASS in sweep-postfix confirms D1/D1-b closed
at pattern level.

### 4.3 DEFECT 2 (MEDIUM) — /validate lies; a 422 create persists the row; import ungated

**Plain language.** The rule API's dry-run endpoint said a rule was valid that the create
endpoint then rejected — and the rejection itself still wrote the broken rule into the
database, leaking rows. Later, the review found a third door: the UI's Import dialog
skipped the check entirely, silently creating rules that appear in the list but never
evaluate.

**Evidence** (FINDINGS-DEFECTS.md, verbatim):
```
POST /api/v1/correlation_rules/validate  (flood rule, NO emit_spec)
    {"valid":true,"errors":[]}
POST /api/v1/correlation_rules           (identical body)
    HTTP 422 {"detail":"rule persisted but rejected by loader:
              emit_spec_alertname_required_for_pattern_flood","error_code":"HTTP_422"}
```
Confirmed live: an orphaned rule (id 63) left behind by a 422 create had to be deleted
manually. During the coverage pass two more rules leaked the same way (`emit_spec` uses key
`alertname`, not `alert_name` — FINDINGS-COVERAGE.md §5).

**Fix** (FIXES-PROPOSED.md §D2, post-review shape). Both halves live in one service,
`oscar-correlator`. The emit_spec.alertname predicate was **hoisted into
`rule_store.emit_spec_alertname_error()`** (module level, next to the loader gate that
consumes it, including the `parent_child` carve-out — that pattern never emits a synthetic
parent, so it legitimately has no emit_spec); the loader AND every router gate (create,
batch, update, validate, **import**) call that ONE function. The router's hand-copied
mirror was deleted (a drift-probe test pins this). The gate runs **before** any INSERT, so
the 422 opens no DB session; the post-load belt-and-suspenders branch is kept but now
DELETEs the row + reloads (message changed to "rule rejected by loader (not persisted)");
`update_rule` is gated on the effective merged pattern+emit_spec.

**Self-correction record — two review findings reversed the original scoping:**
- *Mirror drift is structural* (review 2a): the copy was faithful today but any new loader
  rejection reason would resurrect validate-lies, and batch/update have no post-load
  backstop (a drifted rejection would leak a whole batch with a 200). → shared function.
- *The import carve-out was wrong* (review 2e): the original fix claimed import was
  deliberate/unreachable; the review proved
  `oscar-adminui/src/views/pages/rules/CorrelationImportDialog.js:75` posts to the ungated
  `POST /rules/import` — a normal UI user importing a historical export with
  `emit_spec: null` on a flood rule got HTTP success + a persisted row the loader silently
  rejected — "the 201-then-broken rule the fix claims to have eliminated, minus even the
  422". → import now runs the same shared gate per document, reporting rejections in
  `RuleImportResult.errors[{filename, message}]` — the existing per-file partial-error
  contract honoured, no adminui change needed. Two stale import fixtures that encoded the
  ungated behaviour were updated.

Tests: 25 (rules router) + 12 (import) + 12 (regression) passed; six pre-existing test
payloads had *encoded the buggy behaviour* (flood rules with no emit_spec) and were
updated. Blast radius: callers that used to get a 201-then-broken-rule now get a clean 422;
nothing that used to *work* stops working.

**Deployed 2026-09-01** (correlator). **Live proof:** invalid flood create → 422 AND rule
row count unchanged; validate and import both reject (HANDOFF fix table).

### 4.4 DEFECT 3 (HIGH) — the Alert View "Correlation" tab could never show correlation

**Plain language.** The UI tab built to show operators whether an alert was correlated
queried the wrong identifier — so it said "This alert was not correlated" for every alert
that has ever existed, in every mode, and its explanatory text blamed Shadow mode, which
told operators the blank tab was expected. The feature appeared dead and nobody could tell.

**Root cause** (FINDINGS-DEFECTS.md). Same class as the harness bug of §3.2:
`oscar-adminui/src/views/pages/alerts/CorrelationTab.js:90`
```js
const fingerprint = alert?.fingerprint          // AlertHistory 12-hex
...:104  axios.get('/api/correlation-audit/' + encodeURIComponent(fingerprint))
```
`CorrelationAudit.alert_fingerprint` is the 16-hex `am_fingerprint` label.

**Proof — same alert (CORRTEST_COOCCURRENCE_P01_DISKFULL_11CY4VT9), both keys, verbatim:**
```
GET /api/v1/correlation_rules/audit?fingerprint=0564b2d974c0      -> total 0   []
    (12-hex, what the UI sends)
GET /api/v1/correlation_rules/audit?fingerprint=106f21bede4aee8f  -> total 1   ['enriched']
    (16-hex am_fingerprint, what the audit table is keyed on)
```
The correlation demonstrably happened (harness: co_occurrence P01 PASS,
`['parent_emitted','enriched']`).

**Why it stayed hidden:** the empty-state text — *"If correlation dispatch is disabled
platform-wide, most alerts will show this state until it's enabled"* — attributed the blank
tab to Shadow, so operators stopped investigating. Factually wrong: audit rows are written
unconditionally in Shadow (`consume.py:539`); the tab would stay empty after Phase B too.

**Fix** (FIXES-PROPOSED.md §D3). New `correlationFingerprintOf(alert)` in
`correlationTabLib.js` resolves the `am_fingerprint` label **client-side** from the labels
array the component already receives (no extra round-trip, no server-side 12→16 hex
translation table to keep in sync); one const feeds BOTH the audit and
`children?parent_fingerprint=` queries; returns **null** (never falls back to the 12-hex
value, which could only reissue the wrong-domain query); empty-state copy split into an
honest "not correlated" vs "cannot be looked up (no am_fingerprint label)" and the Shadow
claim removed, with a code comment recording why the old wording hid the bug.

**Adversarial review verdict: DEPLOY — every attack failed**, including live probes: the
oldest history rows (page 327 of 327, back to 2026-07-02) ALL carry `am_fingerprint`; a
real audit row (id 37867) with `parent_fingerprint: "5a450322c3bc"` (12-hex canonical —
the review noted the fix's "16-hex" comment over-specifies but the code is correctly
agnostic); label-shape, injection and duplicate-label attacks all failed. **Caveat the
review could not close live:** zero synthetic parents exist in the entire 327-page history
(Shadow has never let one land), so the parent/children view is code-proven only —
verify-live step 4 (children grid on a synthetic parent) must run when one first exists.

**Deployed 2026-09-01** (adminui rebuild — client-side code requires a rebuilt bundle).
**Proof:** `correlationFingerprintOf` present in the served bundle; browser-verified
(HANDOFF fix table); tests 62 passed (3 new, incl. `not.toBe(alert.fingerprint)`).

### 4.5 DEFECT 4 (HIGH) — no cross-message ordering; sequence fires on the WRONG order

**Plain language.** The correlator does not know when an event actually happened unless the
sender tells it (`startsAt`). When senders don't, it stamps each alert with the wall-clock
time it happened to *process* it — and it processes many alerts in parallel, in no
particular order. Order-sensitive patterns therefore both miss real sequences and raise
correlations for sequences that never happened in that order.

**Root cause chain** (FINDINGS-DEFECTS.md, FINDINGS-FLAKE.md §2 — all confirmed live):
1. No `startsAt` sent → AM publishes `"starts_at": None`
   (`oscar-alertmanager/src/tm/tasks.py:723`).
2. `parse_event_ts_ms()` (`oscar-correlator/src/app/patterns/base.py:90-113`) falls
   through to `return now_ms` — evaluate-time wall clock.
3. Processing order is nondeterministic twice over: AM celery `--pool=prefork
   --autoscale=8,2`, and the correlator `sub.fetch(batch=10)` (`consume.py:652`) drained by
   `CORRELATOR_EVAL_CONCURRENCY=10` workers (`main.py:510-513`) with no per-group
   serialisation.
4. sequence's ts_ms sort (`sequence.py:130-147`) tie-breaks equal timestamps by arrival
   index — with step 2 collapsing all events onto processing time, the defence is inert.
   parent_child has no ordering logic at all (`parent_child.py:182`).

**Live evidence — run8 produced both failure directions in a single sweep (verbatim,
FINDINGS-FLAKE.md; `depth=N of 2` is the true processing order):**
```
run8 sequence.P01  FAIL   (correct order sent, NOT fired)
  LOGINFAILURE      05:48:10.092  enriched depth=2 of 2
  PRIVILEGEDCOMMAND 05:48:11.124  enriched depth=1 of 2
run8 sequence.N01  FAIL   (DELIBERATELY WRONG order sent, FIRED anyway)
  LOGINFAILURE      05:48:12.102  enriched depth=1 of 2
  PRIVILEGEDCOMMAND 05:48:11.148  parent_emitted sequence_triggered
                                  tail=[..._LOGINFAILURE_1F5EX2H4, ..._PRIVILEGEDCOMMAND_...]
```
A sequence rule fired on out-of-order input — a **false positive**, not just a miss.

**Deterministic reproducer** — `scripts/repro_seq.sh` (fresh rule per trial):
| mode | what it does | result |
|---|---|---|
| race | A then B back-to-back, no startsAt | fired **3/6** (coin flip) |
| spaced | A, sleep 20 s, B, no startsAt | fired 6/6 |
| stamped | back-to-back, startsAt 60 s apart | fired 5/6 |
| inverted | **B posted first**, startsAt says A earlier | fired **4/4** — event time GOVERNS |

`inverted` is the proof of mechanism: wall-clock arrival fully reversed, event time
correct → fires every time.

**Load quantification** (W7, 2026-08-30, then extended in coverage): two 20-group sequence
tests, all 40/40 alerts landed both runs:
| within-group timing | groups fired |
|---|---|
| A then B sequential (~100 ms) | 20/20 (100%) |
| A and B SIMULTANEOUS | **8/20 (40%)** |

**60% of correct-order sequences silently missed** when the legs arrive concurrently.
**D4 is not sequence-specific**: the coverage pass fired parent+child simultaneously across
20 groups — **8/20 (40%) lost the child suppression** (child `released_no_trigger` instead
of `suppressed_per_notifier`) — the same 40%, supporting one shared root cause
(FINDINGS-COVERAGE.md §5). The authored scenario `parent_child-multi-child` also failed
1-in-4 with the +2 s child released (FINDINGS-COVERAGE.md §3), and in the 12-rule block the
one miss (R10 sequence, correct pair 3 s apart inside a 120 s window, tail `[Q, LG]`) was
another D4 data point (§6). Method note recorded: the sequential run's 20/20 must not be
read as evidence ordering is fine — its concurrency was across groups only.

**Status: OPEN — design decided, not built** (§6.1). Note this is the noise/miss direction
on order; D1's parent_child arm was the missed-page direction on resolves.

### 4.6 DEFECT 5 (MEDIUM) — threshold's tumbling HLL inside a sliding window

**Plain language.** The threshold pattern counts *distinct* label values (e.g. distinct
hosts) with a probabilistic counter that resets on fixed clock boundaries, while the alerts
themselves are kept in a smoothly sliding window. A burst that happens to straddle one of
those reset boundaries has its distinct count split in half and the rule never fires.

**Root cause** (FINDINGS-DEFECTS.md, FINDINGS-FLAKE.md §3). `threshold.py:107-109`
buckets the HLL key by `ts_ms // window_ms` (tumbling; `lua/add_threshold_and_check.lua`
KEYS[2], header comment "PFCOUNT resets per window") while KEYS[1], the alert ZSET backing
the same rule, is evicted by `ts_ms - window_ms` — a true sliding window.

**Live evidence — run5 threshold.P01 FAIL (verbatim):**
```
d1 05:33:28.770  enriched threshold_advancing distinct=2 of 3 alerts=2
d2 05:33:29.849  enriched threshold_advancing distinct=1 of 3 alerts=1
d3 05:33:30.963  enriched threshold_advancing distinct=1 of 3 alerts=3   <- alerts=3, distinct=1
```
`alerts=3` (sliding ZSET saw all three) with `distinct=1` (the HLL had just reset at the
30 s boundary, 05:33:30.000, exactly between d2 and d3).

**Deterministic reproducer** — `scripts/repro_threshold2.sh`, startsAt-pinned, identical
3-second spread, only the phase relative to the 30 s boundary differs:
```
straddle  fired 0/3   distinct 2,1,1 (alerts=3)
inside    fired 3/3   distinct 1,2,3
```
Independent of the D4/D6 ordering race (timestamps pinned). Expected miss probability
≈ burst_spread / window_seconds.

**Worse than a boundary corner case** (FINDINGS-COVERAGE.md §1, §7): during the
resolve-path test a plain 300 s threshold window missed a genuine 3-distinct-device
condition spread over 2m40s — `distinct` advanced 1,1,2 across `alerts=1,2,3` and never
reached 3 (`parent_emitted = 0`; no-resolve control with identical timing in
`scen3/thr_control.log`).

**Status: OPEN — decision: make the distinct-count sliding** (§6.2).

### 4.7 DEFECT 6 (HIGH) — window membership follows processing time, not event time

**Plain language.** Beyond ordering, the very question "did these two alerts happen within
N seconds of each other?" is answered with the times the correlator got around to looking
at them, not the times they happened. So alerts 28 seconds apart can be correlated by a
20-second rule, and alerts 2 seconds apart can be missed by the same rule. Found in the
final coverage pass; the only defect class that produces *false correlations* in patterns
other than sequence.

**Evidence** (FINDINGS-COVERAGE.md §4.2, all verbatim):

*A — false correlation, cross_source rule 327, window 20 s:*
```
history  20:46:53.890  snmp leg injected
history  20:47:21.xxx  api leg injected      (28 s later)
audit    20:47:08.411  enriched         crosssource_advancing distinct=1 of 2
audit    20:47:23.419  parent_emitted   crosssource_triggered distinct=2 of 2
```
The snmp leg waited 14.5 s to be evaluated; at evaluation the legs were only 15 s apart —
inside the 20 s window — so the rule fired on a pair 28 s apart in reality.

*B — false correlation, sequence rule 321, window 20 s:* same shape — legs injected 28 s
apart, audit rows only 10 s apart (first leg lagged 22.2 s).

*C — missed correlation, cross_source rule 331, window 20 s
(`scripts/repro_d6_eventtime.py` ARM A):*
```
posted   20:52:14.258  snmp leg
posted   20:52:16.237  api leg          (2 s later)
audit    20:52:18.489  enriched  distinct=1 of 2     (lag 4.2 s)
audit    20:52:43.498  enriched  distinct=1 of 2     (lag 27.3 s)
RESULT fired = False
```
The second leg's evaluation lagged 27 s and the Lua eviction bound (`ts2 - window`) evicted
the first leg.

*D — the A/B that isolates the variable:* ARM B is identical but the two legs carry
explicit `startsAt` 300 s apart while posted 2 s apart → **did not fire** (correct). The
correlator honours event time when present; the problem is that it is absent.

**Mechanism** (three links confirmed from source): producer omits `startsAt` and OSCAR does
not backfill it (`tm/tasks.py`); `parse_event_ts_ms()` → `now_ms`; and evaluation lag is
not uniform — measured over ~30 alerts, normally 4–5 s, but **~10–15% of alerts lag
14–27 s** (`scripts/repro_d6_fetchlag.py`; the pull consumer fetches `batch=10,
timeout=30`, so an alert landing in a fetch cycle that does not fill waits for the expiry).

**Consequence:** every sliding-window pattern's `window_seconds` is effectively
`± ~25 s` of jitter; **any rule with window_seconds below ~30 is unreliable in both
directions**. All 8 patterns pass through `parse_event_ts_ms`; it surfaces first in
two-leg patterns because one late leg is enough. It also explains much of the flake
previously attributed to D4, and the four "outside window" negatives that passed
(flood/co_occurrence/threshold/parent_child) "were simply lucky on lag; D6 exposes them
equally" (FINDINGS-COVERAGE.md §4.3). Relationship to D4: same missing-event-time root,
different consequence (D4 = ordering, D6 = window boundary). One fix addresses both.

**Status: OPEN — folded into the event-time decision** (§6.1). Scenario-level
reproducers: `scenarios/sequence-outside-window.yaml`,
`scenarios/cross_source-outside-window.yaml` (both FAIL-FIRED with passing P01 controls,
so not authoring errors).

### 4.8 The synthetic-clearing GAP

**Plain language.** Once the correlator emits a synthetic parent alert, *nothing in the
system ever resolves it*. The window-expiry release path was descoped
(`consume.py:92` — memory record correlation_campaign_status.md), so in Phase B every
fired correlation would be an alert that never clears.

Untestable in Shadow (FINDINGS-COVERAGE.md §9 — no synthetic is ever emitted, so
emit_spec templating, the per-minute synthetic cap, and dispatch back-pressure are also
unexercisable). Design finalized 2026-09-01, **build before Phase B** — see §6.3.

### 4.9 NOT defects — the artifacts and corrections worth recording

- **absence.N01 INCONCLUSIVE (1/8) — harness artifact.** The harness hardcodes the absence
  observation window to 55 s (`internal/compiler/compiler.go:132-134`) while the last
  expected heartbeat is at +48 s, and `AuditReady` requires an audit row for all 7 alerts
  (~7 s left for the whole AM→NATS→evaluate→buffered-flush→read-back path). run3: 7 history
  records, 6 audit rows at deadline; the assertion itself PASSED (0 expected, 0 observed).
  Fix belongs in the harness (~70 s), not OSCAR (FINDINGS-FLAKE.md §4).
- **ITER1 scoreboard bug** (own reporting error, threshold dropped by an aborting loop) and
  **ITER6 false "alerts are being dropped" read** (history ignores `size`, always 10/page —
  callers must page) — both self-caught and recorded (CAMPAIGN-STATE.md).
- **sweep3 as baseline retracted**: its parent_child FAIL and absence INCONCLUSIVE did not
  reproduce over 8 runs — transients around a mid-session OSCAR restart (CAMPAIGN-STATE.md
  ITER3).
- **D1 scope corrected after user challenge** from "resolves are lost" to "the correlator's
  NATS copy of resolves is lost" — normal alerting/ESB unaffected (FINDINGS-DEFECTS.md).

---

## 5. What was proven to WORK

### 5.1 Post-fix regression sweep — first fully green sweep of the campaign (2026-09-01)

`sweep-postfix/*.json`, all `--pipeline-mode phase_a_audit_only`:

| pattern | verdict | cleanup | cases | audit outcomes |
|---|---|---|---|---|
| absence | PASS | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |
| co_occurrence | PASS | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |
| cross_source | PASS | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |
| flood | PASS | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |
| parent_child | PASS | CLEAN | P01=PASS N01=PASS | enriched, released_no_trigger, suppressed_per_notifier |
| **persistence** | **PASS** | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |
| sequence | PASS | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |
| threshold | PASS | CLEAN | P01=PASS N01=PASS | enriched, parent_emitted |

persistence flipped from 0-for-8 FAIL to PASS — direct pattern-level confirmation D1/D1-b
are closed (HANDOFF). (sequence/threshold pass here on the idle-box single sample; their
D4/D5/D6 exposures remain open by design — §9.)

### 5.2 Pre-fix stability picture — 8-sweep flake table (FINDINGS-FLAKE.md §1)

Only four cases ever varied across 8 consecutive sweeps on an idle box, three distinct
causes: sequence.P01 3/8 FAIL + sequence.N01 1/8 false positive (D4), threshold.P01 1/8
(D5), absence.N01 1/8 INCONCLUSIVE (harness), persistence.N01 8/8 deterministic (D1).
Everything else — flood, co_occurrence, cross_source, parent_child, absence.P01,
persistence.P01, threshold.N01 — was 0/8 non-PASS.

### 5.3 No-rule baseline — correlation is strictly additive

With zero rules: an alert flows normally, keeps its labels, gains no
`oscar_correlation_*` labels, audit shows `released_no_trigger`, status goes
firing → resolved (MORNING-REPORT.md). **Re-proven on the fixed code**: with 0 rules the
correlator now observes firing AND resolve — 2 audit rows,
`released_no_trigger/no_matching_rules` — and adds nothing (HANDOFF §RESULT; `neg1/`).

### 5.4 Load and concurrency positives (FINDINGS-COVERAGE.md §5, FINDINGS-DEFECTS.md)

20 concurrent groups per pattern, all legs fired simultaneously from a 64-thread pool,
window 120 s (so D6 lag could not confound):

| pattern | alerts posted | landed | fired exactly once |
|---|---|---|---|
| flood | 100 | 64 (ingest cap, §9) | 12/13 complete groups — 12/12 of groups with a full set |
| co_occurrence | 40 | 40 | 20/20 |
| threshold | 60 | 60 | 20/20 |
| cross_source | 40 | 40 | 20/20 |
| parent_child | 40 | 40 | 12/20 (the 8 failures = D4) |

Across all five: **no double-firing** (never >1 `parent_emitted` per group), **no
cross-group contamination**, **no guardrail rejections**, and `group_by_labels`
partitioning exact in every case. The earlier sequence load test likewise showed no alert
loss (40/40) and correct group isolation — "the defect is strictly within-group ordering,
not group isolation and not ingest".

### 5.5 Twelve rules at once — zero mis-attribution (FINDINGS-COVERAGE.md §6)

`scripts/multirule.py`: 12 rules across 6 patterns live simultaneously, deliberately
overlapping (duplicate flood rules on one alertname; a third grouped differently; two
parent_child rules on the same pair with different group_by; three must-never-fire rules):
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
Zero mis-attribution; no cross-rule interference (duplicates fired independently);
correlation is additive per rule (no priority pre-emption at equal priority); rules that
must not fire, did not. The one miss (R10, sequence) was D4, not a multi-rule defect.

### 5.6 Resolves correctly ignored by counting patterns (FINDINGS-COVERAGE.md §7)

With the resolve placed 150 s out (past the then-live D1 window so it genuinely arrived):
flood — no decrement, window still completed on the fifth firing; co_occurrence — the
resolved role stayed in the window, pair completed; threshold — the `alerts=` counter
advanced 1→2→3 across the resolve (its miss was D5, not the resolve). The source-derived
claim `is_resolved() → return None` confirmed empirically; D1's blast radius stands as
persistence + parent_child only.

### 5.7 Negative depth held (FINDINGS-COVERAGE.md §2, §4)

"Wrong group_by" held for all six patterns where meaningful; "below threshold" held
everywhere except persistence (D1); "wrong alertname" held for all 8 (three
must-never-fire rules produced 0 firing rows; zero rows in the whole 12-rule block were
attributed to a rule whose match set did not contain the alert's alertname).

### 5.8 Rejected hypotheses (tested with evidence — FINDINGS-FLAKE.md §6)

| hypothesis | verdict |
|---|---|
| timer-subsystem race (`WINDOW_GRACE_SECONDS=5`, `TIMER_POLL_INTERVAL_MS=100`) | Rejected — the two genuinely timer-driven positives (persistence.P01, absence.P01) 8/8 PASS; `pending_timers:0, backlog_drained:true` after every iteration |
| stale Redis windows leaking between runs | Rejected — keys namespaced by rule id, fresh ids every run; ≤2 windows mid-run, `active_windows_total:0` at every boundary over 8 sweeps |
| D1 dedup affecting patterns beyond persistence (as the flake cause) | Rejected — only persistence.N01 injects a resolve; D1 explains a deterministic failure, not variance |
| harness deadlines too short for timer patterns | Confirmed for exactly one case (absence.N01); not the cause of sequence/threshold |
| NATS redelivery / consumer lag / ordering | Ordering CONFIRMED (D4) but via publish/evaluate concurrency, not redelivery — no redelivery/nak-ladder/poison/lag lines during the campaign; `/ready` stayed green |
| history read-back lag vs polling deadline | Contributory only — a late read yields INCONCLUSIVE, never FAIL |

---

## 6. Design decisions (recommended; pending owner sign-off, NOT built)

All from HANDOFF-20260901-correlation-fixes.md §OPEN unless noted.

### 6.1 Event time end-to-end (D4 + D6, HIGH — one decision closes both)

**Decision:** propagate `startsAt` end-to-end so the correlator always has a real event
time. **The discovery that reduces the scope:** the correlator ALREADY prefers event time
— `patterns/base.py:90-113` honours `starts_at_ms`/`starts_at` when present, and windows
and ordering follow it (proven by the `inverted` reproducer 4/4 and the D6 ARM B probe).
The gap is **per-source stamping**, i.e. a per-source table of ingest paths:

| source | state | action |
|---|---|---|
| trapreceiver | code APPEARS to stamp (`session.py:910-918`) — **VERIFY LIVE FIRST; this decides most remaining scope** | live verification |
| webhookreceiver handlers | unknown per handler | audit + patch |
| corrtest harness | never sends it (`grep startsAt internal/` → nothing, FINDINGS-FLAKE.md) | add per-event startsAt |
| vmalert / AM webhooks | real producers do send startsAt (FINDINGS-COVERAGE.md §9) | per-source audit was out of campaign scope |

Trailers: ms precision in the trapreceiver strftime; same-second ties. Closes: the 60%
sequence miss under concurrency, false wrong-order fires, window-membership drift (the
28 s-pair-correlated / 2 s-pair-missed pair), and the D1-b residual for startsAt-less
alerts. Secondary reinforcements proposed in the findings (FINDINGS-FLAKE.md §2 verdict,
FINDINGS-COVERAGE.md §4.2): backfill `starts_at` at AM ingest when a producer omits it
(receipt time, assigned once); make the wall-clock fallback observable (counter + WARN);
bound consumer fetch lag (`consume.py:652` batch/expiry); optionally serialise evaluation
per `(rule_id, group_hash)`.

### 6.2 D5 — make the threshold distinct counter sliding (MED)

The tumbling `ts_ms // window_ms` HLL bucket inside a sliding alert ZSET is the defect;
**decision: make the distinct-count window sliding** to match the alert window (candidate
mechanisms from the findings: rotating/overlapping HLL buckets merged with PFMERGE over
the window, or a per-value ZSET of last-seen ts + ZCARD after eviction). Also a
**prerequisite for the threshold clear condition** in §6.3 ("below N still-firing" cannot
be computed honestly on a tumbling counter). The documented alternative — declare
threshold explicitly tumbling — was considered in FINDINGS-DEFECTS.md but the campaign
recommendation is sliding.

### 6.3 Synthetic clearing (design final — build before Phase B)

A synthetic parent clears **when its claim stops being true** — negation-of-birth,
per pattern, plus a per-rule `hold_seconds` damper (HANDOFF §OPEN item 3):

| pattern | synthetic clears when |
|---|---|
| threshold | below N still-firing distinct values |
| co_occurrence / cross_source | any required member clears |
| persistence | the source alert resolves |
| absence | the heartbeat returns |
| flood | a quiet window |
| sequence | TTL only |

**Authority rules:** real alerts clear ONLY on their own authority; **children never clear
a real parent** — the counter-example is failover: all children of an incident clearing
does not prove the parent condition is gone, so all-children-cleared triggers a
**VERIFICATION PROBE**, not a clear. A **parent clear LIFTS suppression — it never
cascades** resolution to children. Follow-ups: a UI badge for suppressed children; probe
automation.

### 6.4 parent_child position

Summarising the same decision from the suppression side: parent_child suppression is
"own-authority, no cascade" — the parent's own resolve lifts suppression of children
(exactly the path D1 was breaking, §4.1); children's state never resolves the parent
(failover counter-example); the verification probe bridges the gap; the UI badge makes
suppression visible to operators (today `suppressed_per_notifier` is only an audit
outcome — FINDINGS-COVERAGE.md §9 notes notifier-level withholding itself is a
dispatch-side behaviour untestable in Shadow).

---

## 7. Phase B blocker register

Do NOT flip `CORRELATOR_DISPATCH_ENABLED`. There is no per-rule or per-test scoping — the
flag is global (FINDINGS-OSCAR-BACKEND.md §8B). Current blockers:

1. **AlertHistory label-shape defect (pre-existing, root-caused 2026-08-12, verified still
   present in the running container).** With dispatch on, NO AM_AlertHistory row is written
   for ANY alert: the correlator posts `alerts[].labels` as a dict
   (`consume.py:768 _normalize_labels`) while `AlertHistoryHandler` reads the alertmanager
   v1 list form `[{"Label":k,"Value":v}]`; `dispatch_planner._build_alert_group()`
   (`core/dispatch_planner.py:402-428`) coerces only commonLabels/commonAnnotations —
   "The alerts[] entry is passed through untouched". Notifier returns 200 and writes
   nothing, silently. The ~10-line fix has not landed (FINDINGS-OSCAR-BACKEND.md §5).
2. **Scheduler lag-observer 500s** ("Correlator lag observer trigger HTTP error: 500",
   scheduler log 08-27 17:08). The Redis key
   `oscar:alertmanager:correlator_dispatch_enabled` (SETEX TTL 300 s from the observer)
   never goes true, the system fails open to legacy, and Phase B never actually engages —
   a Phase B test would prove nothing (FINDINGS-OSCAR-BACKEND.md §8B).
3. **Fix set 2 items** (HANDOFF §PHASE B): event time (D4+D6), D5, and above all the
   **synthetic-clearing build** — without it every dispatched synthetic is a never-clearing
   alert; and in Shadow none of emit_spec templating, the synthetic-per-minute cap, or
   dispatch back-pressure has ever been exercised (FINDINGS-COVERAGE.md §9).
4. **Preflight friction**: `phase99_cutover_preflight.sh --live` check #5 CONFIG-COHERENCE
   demands both flags true in `oscar/conf/env`, conflicting with this box keeping them in
   `overrides.env` — expect a false FAIL (FINDINGS-OSCAR-BACKEND.md §8B).
5. **Stale windows** for pre-campaign deleted rules (cooccur:7/8) would dispatch on expiry
   if the flag were flipped (FINDINGS-OSCAR-BACKEND.md §8B; cosmetic in Shadow — §9 there,
   blocker 6).
6. **D3 parent view live-proof** and the **flap probe under Phase B semantics**: the
   children grid on a synthetic parent has never rendered against a real history record
   (zero synthetics in 327 pages of history), and the Phase B legacy-fan-out skip
   (`tasks.py:570-586`) interacts with dedup as documented in the review — both need
   re-proof at cutover time (REVIEW-FIXES-ADVERSARIAL.md §3a caveat, §Fix 1).

---

## 8. Test infrastructure delivered

### 8.1 Harness patches (uncommitted, in this repo's diff — a fresh clone will NOT work)

| patch | why |
|---|---|
| `internal/oscar/flextime.go` (new) | OSCAR emits zone-less timestamps; Go time.Time demands RFC3339 → every decode failed as "malformed JSON" |
| `internal/oscar/client.go` alertname fold | OSCAR returns `alertname` top-level, not in labels → doctor `labelsSurvived:false` |
| audit keying on `am_fingerprint` | the fingerprint-domain discovery (§3.2) — polling by 12-hex history fingerprint can never match |

Known but unpatched harness gaps: no `startsAt` sent (so order-sensitive scenarios were
testing the race, not the pattern — FINDINGS-FLAKE.md §2 verdict), and the absence
observation window hardcoded 55 s (`compiler.go:132-134`).

### 8.2 Skills, slash commands, campaign loop

- **Skill `oscar-correlation-test`** (`.claude/skills/oscar-correlation-test/SKILL.md`):
  full protocol — preflight, declare-the-true-pipeline-mode (an *operator declaration*;
  OSCAR exposes no discovery endpoint, and declaring `phase_b_dispatch` on a Shadow box
  fails every positive on `synthetic-alert-count` while `audit-count` passes — "that is
  the tell"), run-sequentially rule, N≥3 flake-rate rule, direct audit verification, the
  no-rule baseline envelope, cleanup, and **8 confirmed pitfalls** (fingerprint domains,
  the broken UI tab, the 2-min resolve drop, the 422-persists leak, alertname-only match
  clauses, the persistence ≥30 floor + timer margins, `--insecure` TLS, stale windows).
- **Skill `oscar-correlation-campaign`** + **CAMPAIGN-STATE.md**: the resumable loop —
  read state → ONE unit of work → write findings → update state → stop; designed to run
  under `/loop` across fresh contexts; hard rules (never touch OSCAR config, rules back to
  0 every block, sequential only, am_fingerprint keying) restated at the top of every
  iteration.
- **Slash commands**: `/test-correlation` (full suite), `/correlation-campaign`
  (one resumable iteration).

### 8.3 Reproducers and probes (`scripts/`)

`flakeloop.sh` (8× sweep driver + debug snapshots), `score.py` (flake table),
`repro_seq.sh` (race|spaced|stamped|inverted), `repro_threshold2.sh` (straddle|inside,
deterministic; supersedes `repro_threshold.sh`), `repro_order.sh` (parent_child/sequence
gap sweep), `audit.py` (audit formatter by 16-hex key); coverage-session additions
(pure-API, harness-independent — FINDINGS-COVERAGE.md preamble): `lib_probe.py`,
`repro_d6_eventtime.py`, `repro_d6_fetchlag.py`, `load_patterns.py`, `multirule.py`,
`resolve_paths.py`, `thr_control.py`, `sustained.py`, `sc.py`. Plus the adversarial
review's executable attack test `test_attack_flap_hole.py` (scratchpad; updated post-fix
to assert the hole is CLOSED).

### 8.4 Scenario corpus

10 authored scenarios (flood boundary/window-expiry/group-partition/wrong-groupby;
parent_child multi-child/child-before-parent/group-isolation/tag-only; threshold
distinct-boundary; persistence long-window) + 10 negative scenarios in `scenarios/`
(wrong-groupby / outside-window / missing-leg per pattern), each pairing a positive
control with the targeted negative, independently validated by the DSL's `semantics.go`.

### 8.5 Evidence directory map

| dir | contents |
|---|---|
| `sweep/`, `sweep2/`, `sweep3/` | early single sweeps (sweep3 later retracted as a baseline) |
| `flake/run1..run8/` | the 8-sweep flake campaign: per-pattern json/log, `windows.pre/after_*`, `timers.post`, `rules.post`, `times.txt` |
| `scen2/`, `scen3/` | authored-scenario runs incl. `pcmc-r2..r4` reruns, `d6-lag-distribution.txt`, `thr_control.log`, `resolve_paths.log` |
| `neg1/` | negative-scenario runs (outside-window / wrong-groupby) |
| `sweep-postfix/` | the 09-01 post-fix 8/8 green sweep |
| shareable summary artifact | https://claude.ai/code/artifact/6eba1789-16bd-4072-9bc2-70d539afe17c |

---

## 9. Known limitations & residual risks

- **Shadow-only evidence.** Every positive result is a CorrelationAudit outcome, not a
  delivered notification. Untestable in Shadow: synthetic emission + emit_spec label
  templating, the per-minute synthetic cap, dispatch back-pressure, notifier-level
  suppression (whether the page is actually withheld), the Phase-B fan-out itself, and D3
  end-to-end in the UI (FINDINGS-COVERAGE.md §9).
- **Idle-box caveats.** D4 is load-sensitive — "a quiet box hides it"
  (FINDINGS-DEFECTS.md); the pre-fix 8/8 PASSes for parent_child said nothing about its
  resolve path (firing-only builtins), and the post-fix green sweep is a single idle-box
  sample for the still-open D4/D5/D6 exposures.
- **startsAt-less residual.** Both deployed dedup fixes degrade for alerts published with
  `starts_at: None` (trap sources today): D1's key degrades to `fp:status` (a flap re-fire
  within the window is still lost) and D1-b to status-only (re-fire in the SAME window
  still collides, bounded by window rotation). Owned by the D4/D6 event-time work
  (FIXES-PROPOSED.md residual-risk sections).
- **Rolling-deploy duplicate.** The D1-b key-shape change can produce at most one bounded
  duplicate audit row per in-flight message across the deploy boundary (no loss
  direction) — accepted per the D-08 stance (FIXES-PROPOSED.md).
- **Alert-burst measurement cap (pre-existing, not re-raised).** Bursts of ≳60 alerts are
  silently dropped before AlertHistory (~40% loss at N=100, all HTTP 200; reproduced:
  30 sequential 30/30; 100 sequential 58/100; 100@8 threads 60/100; 100@64 threads
  64/100; re-polls to 360 s stable). The already-closed "alerts dropped under load"
  behaviour — it caps every load test on this box (~50/burst) and explains the flood load
  numbers (FINDINGS-COVERAGE.md §5).
- **Uncommitted status at time of writing.** The deployed fixes are on-disk source edits
  in `oscar/` + `oscar-adminui/`, built into running containers, **not committed or
  pushed**; the harness patches are uncommitted local edits; rollback is via the
  `:pre-corrfix-20260901` / `:pre-d1b-20260901` image tags. `./oscar compile <svc>` both
  builds AND recreates (correlator has a 45 s stop grace) (HANDOFF).
- **D3 browser check pending** (bundle-verified; user to confirm in the UI), and the
  synthetic-parent children view has never rendered against a real record (§7 item 6).
- **Pipeline-mode declaration risk.** `--pipeline-mode` is an operator declaration with no
  discovery endpoint; declaring wrongly produces systematic false FAILs (SKILL.md §1).

---

## 10. Appendix

### 10.1 Glossary

- **Fingerprint domains.** An alert carries at least two identifiers: the 12-hex
  AlertHistory `fingerprint` (also seen as the `oscar_fingerprint` label) and the 16-hex
  `am_fingerprint` **label**, which is what `CorrelationAudit.alert_fingerprint` is keyed
  on. Querying the audit with the 12-hex value always returns 0 rows — the mistake made
  independently by the harness (fixed) and the product UI (D3, fixed). Nuance: for
  *synthetic* parents the same label carries the correlator's 12-hex canonical fingerprint
  (`synthetic_emitter.py` `generate_fingerprint`) — the code copies whatever the label
  holds, deliberately not enforcing 16-hex (REVIEW-FIXES-ADVERSARIAL.md §3b, wording nit).
- **Parent terminology collision.** "Parent" means two things: (1) the **synthetic
  parent** — the alert a fired rule emits (audit outcome `parent_emitted`), for any
  pattern; (2) the **parent_child pattern's parent** — a real alert whose presence
  suppresses matching child alerts (`suppressed_per_notifier`). D1's two arms hit one of
  each: a false synthetic parent (persistence) and an overstaying real-parent suppression
  (parent_child).
- **Outcomes vocabulary** (CorrelationAudit.outcome): `parent_emitted` (rule fired) ·
  `enriched` (matched, window advanced) · `suppressed_per_notifier` (parent_child
  suppression) · `released_no_trigger` (evaluated, no fire). Reasons carry pattern detail
  (`persistence_first_seen`, `persistence_resolved_cancelled`,
  `persistence_threshold_reached`, `sequence_triggered`, `threshold_advancing`,
  `crosssource_advancing/_triggered`, `parent_registered`,
  `parent_resolved_suppression_cleared`, `parent_child_active_parent_present`,
  `parent_child_no_active_parent`, `no_matching_rules`, `no_decision_from_pattern`, …).
- **Pipeline modes.** Shadow / Phase A (`CORRELATOR_DISPATCH_ENABLED=false`, harness
  declaration `phase_a_audit_only`): evaluate + audit unconditionally, emit nothing.
  Authoritative / Phase B (`phase_b_dispatch`): also emit synthetics and dispatch
  notifications, with AM's legacy broadcast standing down only when the scheduler-fed
  Redis flag is true (fail-open to legacy otherwise).
- **Dedup layers.** Transport: JetStream `Nats-Msg-Id` on OSCAR_ALERTS (2-min default
  window) — D1. Internal: the correlator's per-decision Redis key
  `oscar:correlator:dedup:…` — D1-b. Both now carry `status` + `starts_at`.

### 10.2 File index

| file | one line |
|---|---|
| `HANDOFF-20260901-correlation-fixes.md` | Master handoff: result, deployed fix table + rollback tags, open design work, Phase B block, evidence map, re-verify protocol |
| `FINDINGS-DEFECTS.md` | D1–D5 with proofs, D1 blast radius + missed-page A/B, load quantification, rejected hypotheses |
| `FINDINGS-FLAKE.md` | 8-sweep flake table, root causes A (ordering) / B (tumbling HLL), absence.N01 harness artifact, hypothesis table, reproducer results |
| `FINDINGS-COVERAGE.md` | Coverage matrix close-out: D6 discovery, negatives, load on all patterns, 12-rule block, resolve paths, ingest cap, Shadow-untestables |
| `FINDINGS-OSCAR-BACKEND.md` | Correlator internals: ingest path, what Shadow gates, env chain, rule schema all 8 patterns, debug endpoints, Phase B option analysis + blockers |
| `REVIEW-FIXES-ADVERSARIAL.md` | Hostile review of the 3 fixes: flap hole + import bypass CONFIRMED; every other attack failed; attack artifacts |
| `FIXES-PROPOSED.md` | Full diffs, correctness arguments, blast radius, deploy + live-verify steps for D1/D2/D3 and D1-b; test counts |
| `MORNING-REPORT.md` | 08-30 owner briefing: pattern truth table, defect table, recommended approval order, caveats |
| `CAMPAIGN-STATE.md` | The loop's single source of truth: hard rules, iteration log ITER1–8, overnight deploy orders |
| `TEST_CAMPAIGN_CONTEXT.md` | Environment, build recipe, the two contract adapters, confirmed API shapes, rules of engagement |
| `CODE_CONTEXT.md` | Harness architecture map: lifecycle, packages, exit-code contract |
| `CLAUDE.md` | Repo entry point: start-here pointer, build command, hard rules |
| `.claude/skills/oscar-correlation-test/SKILL.md` | Testing protocol + the 8 confirmed pitfalls |
| `scenarios/*.yaml` | Authored positive + negative scenario corpus |
| `scripts/*` | Sweep drivers, scorers, deterministic reproducers, pure-API probes (§8.3) |
| `sweep*/ scen2/ scen3/ neg1/ flake/` | Raw JSON/log evidence per §8.5 |
| `DETAILED-REPORT-20260901.md` | This document |

---

*Compiled read-only from the files cited above; no OSCAR API was touched during
compilation. Figures not present in the sources are omitted rather than estimated.*
