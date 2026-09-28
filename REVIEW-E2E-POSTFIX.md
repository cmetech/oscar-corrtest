# REVIEW-E2E-POSTFIX — final adversarial gate on the 09-01 correlation fix set

Date: 2026-09-01 10:18–10:32 UTC. Reviewer: adversarial E2E session (live box
192.168.29.195, Shadow mode, CORRELATOR_DISPATCH_ENABLED=false, verified).
All rules created carried `run=e2e-adversarial`; **final correlation_rules total = 0**
(verified 10:31 UTC, and re-verified via API after the last delete).

## FINAL VERDICT: **COMMIT-WITH-NOTES**

Every attack aimed at the four fixes themselves FAILED to break them (D1, D1-b,
D2, and the transport dedup all held under hostile timing, duplicates,
out-of-order delivery, missing startsAt, batch/PUT/import/concurrency abuse).
Normal alerting is undisturbed with 0 rules. All 5 audited handoff claims are
CONFIRMED. Two notes (below) are pre-existing platform behaviors surfaced by
the attacks, NOT regressions from the fix set — record them, then commit.

### NOTES (must be recorded with the commit)
1. **Ingress per-fingerprint rate limiter governs tight flaps, silently.**
   Rapid flapping (~6 events in 13s, one fingerprint) trips
   `[PER-FINGERPRINT-RATE-LIMIT] [RATE_LIMITED]` in alertmanager: events 4-6
   are DROPPED while the API still returns HTTP 200. In attack A3b the FINAL
   re-fire was dropped at ingress → alert ends firing with no persistence page.
   This is upstream of NATS and of both D1 fixes (the correlator handled every
   event it actually received correctly); it is the known "under load alerts
   are dropped" limiter topic previously closed as config/scaling. But it means
   D1's "clears are never lost" guarantee holds only for traffic the ingress
   limiter admits, and the drop is invisible to the sender.
2. **Concurrent same-name rule creates return raw 500s.** 5 parallel POSTs of
   one rule name → 1×201 + 4×500 (pymysql IntegrityError, `ERROR: Exception in
   ASGI application` tracebacks at 10:28:33). No dupes, no orphans — exactly 1
   row, and the state is clean — but the failure mode is an unhandled 500, not
   a 409. Pre-existing (the D2 gate is not involved: name uniqueness is a DB
   constraint). Cosmetic-to-minor; fix-set-2 candidate.

---

## A. D1/D1-b clears under hostile timings (persistence rule id 380/382, unresolved_for=30)

### A1 — resolve 1s after firing: **PASS**
fp `adv0a1000000001a`, fire 10:18:23Z, resolve 10:18:24Z (same startsAt).
Audit: `40202 persistence_first_seen scheduled_at=1788257933000` +
`40201 persistence_resolved_cancelled` (both 10:18:25.652). Re-audited at
+58s: still 2 rows, **no parent_emitted**. Tightest race holds.

### A2 — resolve sent BEFORE the firing: **DOCUMENTED (behavior sane)**
fp `adv0a2000000002b`, resolve 10:19:36Z, firing 10:19:38Z.
Audit sequence: `40203 persistence_resolved_cancelled` (10:19:40 — the stray
resolve is processed as a cancel no-op, nothing to cancel), then
`40204 persistence_first_seen scheduled_at=1788258006000` (10:19:55), then
`40205 parent_emitted persistence_threshold_reached unresolved_for=30`
(10:20:10). The out-of-order resolve does NOT poison the subsequent firing:
the firing re-arms and correctly pages at startsAt+30s. scheduled_at =
1788258006000 = 10:20:06 = startsAt(10:19:36)+30 — live proof the correlator
prefers startsAt over arrival time. No crash, no stuck state.

### A3 — rapid multi-flap x3 within 20s: **DOCUMENTED (limiter-governed, see NOTE 1)**
A3a end-resolved, fp `adv0a3000000003c` (6 events, 10:21:00→10:21:10, 2s gaps):
only fire1/resolve1/fire2 reached the correlator
(`40206 persistence_first_seen`, `40208 persistence_already_tracking`,
`40207 persistence_resolved_cancelled`); resolve2/fire3/resolve3 were dropped
by the ingress per-fingerprint rate limiter (RATE_LIMITED warnings in
alertmanager_middleware at 10:21:06/08/10). End state: no parent emitted at
+70s — the REQUIRED outcome (end-resolved must not page) holds.
A3b end-firing, fp `adv0a3b00000004d` (3s gaps): fire1/resolve1/fire2 processed
(`40212/40213/40214`, same trio), resolve2 AND the final fire (10:24:09Z)
rate-limited → **no page at +60s despite ending firing**. That misses the
"end firing -> must emit once" target, but the miss happens at HTTP ingress
(before alertmanager fingerprints or publishes), so it is outside the D1/D1-b
blast radius: the correlator's dedup keys were never exercised by the dropped
events. Every event that WAS delivered produced the correct audit row and the
correct timer state. Verdict on the fixes: not broken; verdict on the platform:
NOTE 1.

### A4 — duplicate submissions vs flood min_count=3 (rule 381): **PASS**
3 IDENTICAL firing envelopes (fp `adv0a4000000005e`, same startsAt, 10:25:37-39):
exactly ONE audit row `40216 enriched flood_window_advancing count=1 of 3` —
duplicates deduped, count NOT inflated, **no fire from duplicates alone**.
Then 3 distinct-fingerprint alerts (10:26:02/04/06): `40217 count=2`,
`40219 parent_emitted flood_triggered count=3 min_count=3` (exactly once),
`40218 count=4 of 3` post-trigger window-advance, no second fire.
audit parent_emitted count = 1.

### A5 — no startsAt (degraded key path), rule 382: **PASS**
fp `adv0a5000000009a`, fire 10:26:30Z, resolve 10:26:38Z, NEITHER carrying
startsAt. Audit: `40220 persistence_first_seen scheduled_at=1788258420669`
(wall-clock fallback, ms precision) + `40221 persistence_resolved_cancelled`.
+45s re-audit: still 2 rows, no parent. The status-only degraded key
discriminates fire/resolve as designed. (Same-window re-fire without startsAt
remains the KNOWN documented residual — owned by D4, not retested as a gate.)

## B. D2 under abuse

| Attack | Result | Verdict |
|---|---|---|
| B1 batch `[valid, invalid(flood no emit_spec), valid]` → POST /correlation_rules/batch | 422 `emit_spec_alertname_required_for_pattern_flood`; rule total stayed 0 — batch rejected pre-insert, **no orphans** (contract: all-or-nothing batch) | PASS |
| B2 PUT `{"emit_spec":null}` on valid flood rule 383 | 422 same reason; GET shows original `{'alertname':'b2p'}` intact | PASS |
| B2b PUT `{"emit_spec":{}}` | 422 | PASS |
| B3 import zip (good.yml + bad.yml flood-no-emit_spec) | 200, `created:["ADV_B3_GOOD"], errors:[{filename:"bad.yml", message:"emit_spec.alertname: emit_spec_alertname_required_for_pattern_flood"}]`; rule total = 1 (good only), bad left NO row (DB checked) | PASS |
| B4 5 concurrent creates, same name | 1×201 + 4×500; DB shows exactly 1 row `ADV_B4_SAME`, no orphans; but 500 = unhandled IntegrityError (NOTE 2) | PASS-with-note |

All B rules deleted; total returned to 0 after each sub-attack.

## C. E2E normal-alerting regression (0 rules): **PASS**
fp `adv0c100000000aa`, alertname ADV_C1_BASELINE.
- Firing 10:28:56Z → history record created (12-hex `503f290922b5`, labels
  intact incl. `hostname:h1`, `am_fingerprint:adv0c100000000aa`,
  severity critical, occurrence_count 1). **Zero `*correlation*` labels.**
- Audit: `40222 released_no_trigger no_matching_rules` for the firing.
- Resolve 10:29:23Z → SAME row flips to resolved, occ 2; audit adds
  `40224 released_no_trigger` — **both events observed** (D1 proof at 0 rules).
- Re-notification of the same firing envelope at +50s (same startsAt):
  history still exactly **1 row** (occ 3, no duplicate row); no third audit row
  (same-episode transport dedup, by design).
- Logs since deploy (4h window): alertmanager_middleware — no ERROR/Traceback;
  worker — none; correlator — exactly 4 `Exception in ASGI` tracebacks, all
  timestamped 10:28:33 = this review's own B4 concurrency attack (NOTE 2).
  Nothing else.

## D. Claim audit (handoff HANDOFF-20260901-correlation-fixes.md)

| Claim | Method | Verdict |
|---|---|---|
| "8/8 post-fix sweep PASS, cleanup CLEAN" | Parsed sweep-postfix/*.json: all 8 `verdict: PASS`, `cleanupStatus: CLEAN` | CONFIRMED |
| "Rollback tags exist" | `docker images`: `oscaraiapp/alertmanager_middleware:pre-corrfix-20260901`, `oscaraiapp/correlator:pre-corrfix-20260901`, `oscaraiapp/correlator:pre-d1b-20260901` all present | CONFIRMED |
| "Correlator prefers startsAt (patterns/base.py:90-113)" | Read DEPLOYED file in container (`/home/splunk/app/patterns/base.py`): starts_at_ms → starts_at → now fallback with future-clamp; PLUS live proof A2 (scheduled_at = startsAt+30, not arrival+30) | CONFIRMED |
| "History API ignores size param (10/page)" | `GET /alerts/history?size=50` → `per_page:10, records:10` | CONFIRMED |
| "D1/D1-b keys deployed as described" | In-container grep: worker `nats_publisher.py:468` `Nats-Msg-Id: f"{fingerprint}:{status}:{starts_at}"`; correlator `consume.py:167` 4-part dedup key + `_lifecycle_token` called at :559 | CONFIRMED |

## End state
- correlation_rules total: **0** (API-verified after final delete).
- All injected episodes resolved (hygiene resolves sent for a2/a3b/c1).
- No config, env, container, or flag touched; no commits/pushes made.
- Artifacts of this review: audit row ids 40201–40224 in CorrelationAudit.
