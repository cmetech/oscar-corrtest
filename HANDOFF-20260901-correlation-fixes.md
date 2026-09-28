# HANDOFF — OSCAR Correlation: campaign complete, fix set 1 DEPLOYED & PROVEN (2026-09-01)

AUDIENCE: any AI session or engineer resuming correlation work. Everything here is
verifiable from the referenced files; nothing requires the original session's context.

## RESULT
Post-fix regression sweep 2026-09-01: **8/8 patterns PASS, both cases each, cleanup CLEAN**
(sweep-postfix/*.json). First fully green sweep of the campaign. persistence flipped from
0-for-8 FAIL to PASS — direct confirmation D1/D1-b are closed at pattern level.
No-rule baseline re-proven on fixed code: normal alerting undisturbed; with 0 rules the
correlator observes firing AND resolve (2 audit rows, released_no_trigger/no_matching_rules)
and adds nothing.

## FIX SET 1 — deployed 2026-09-01, all live-proven
| Defect | Fix | Where | Proof |
|---|---|---|---|
| D1 clears dropped (transport) | Nats-Msg-Id = f"{fp}:{status}:{starts_at}" | oscar-alertmanager core/nats_publisher.py | fire+clear@10s -> persistence_resolved_cancelled |
| D1-b clears dropped (internal) | _dedup_key + _lifecycle_token(alert) | oscar-correlator core/consume.py | same-window flap: all 4 audit rows incl. cancel |
| D2 rejected rule persisted; validate lied; import ungated | shared emit_spec gate pre-insert, all persist paths | oscar-correlator core/rule_store.py + routers/rules.py | 422 + row count unchanged; validate+import reject |
| D3 Correlation tab always empty | correlationFingerprintOf() -> am_fingerprint label | oscar-adminui correlationTabLib.js + CorrelationTab.js | in served bundle; browser-verified |
Rollback tags: oscaraiapp/alertmanager_middleware:pre-corrfix-20260901,
oscaraiapp/correlator:pre-d1b-20260901. `./oscar compile <svc>` = build AND recreate
(correlator has 45s stop grace).

## OPEN — decided in principle, NOT built (fix set 2)
1. EVENT TIME (D4+D6, HIGH): propagate startsAt end-to-end; correlator ALREADY prefers it
   (patterns/base.py:90-113) and windows/ordering honour it when present. Gap is per-source
   stamping: trapreceiver code APPEARS to stamp (session.py:910-918 — VERIFY LIVE FIRST,
   this decides most remaining scope); webhookreceiver handlers = audit+patch; corrtest
   harness never sends it. Trailers: ms precision in trapreceiver strftime; same-second ties.
   Closes: 60% sequence miss under concurrency, false wrong-order fires, window membership
   drift (28s pair correlated by 20s rule / 2s pair missed), D1-b residual for startsAt-less
   alerts.
2. D5 (MED): threshold distinct HLL is tumbling (ts_ms//window_ms) inside a sliding window;
   decision = make sliding. Also prerequisite for threshold clear condition.
3. SYNTHETIC CLEARING (design final, build before Phase B): a synthetic clears when its
   claim stops being true — negation-of-birth per pattern (threshold: below N still-firing;
   co_occurrence/cross_source: any required member clears; persistence: source resolves;
   absence: heartbeat returns; flood: quiet window; sequence: TTL only) + per-rule
   hold_seconds. Real alerts clear ONLY on their own authority; children never clear a real
   parent (failover counter-example) — all-children-cleared triggers a VERIFICATION PROBE;
   parent clear LIFTS suppression, never cascades. Follow-ups: UI badge for suppressed
   children; probe automation.
## PHASE B REMAINS BLOCKED (independent of everything above)
AlertHistory dict-vs-list label defect (correlator dispatch_planner passes alerts[] labels
as dict; AlertHistoryHandler expects [{Label,Value}]) + scheduler lag-observer 500s. Plus
set 2 items. Do NOT flip CORRELATOR_DISPATCH_ENABLED.

## EVIDENCE MAP (this repo)
FINDINGS-DEFECTS.md (all defects + reproducers) | FINDINGS-FLAKE.md (8-sweep flake analysis)
| FINDINGS-COVERAGE.md (coverage matrix) | FINDINGS-OSCAR-BACKEND.md (correlator internals,
rule schema all 8 patterns) | REVIEW-FIXES-ADVERSARIAL.md (hostile review; found the flap
hole + import bypass) | FIXES-PROPOSED.md (diffs, blast radius, deploy+verify per fix) |
MORNING-REPORT.md | CAMPAIGN-STATE.md (iteration log) | sweep*/ scen2/ flake/ (raw JSON)
| CODE_CONTEXT.md (harness architecture). Shareable summary artifact:
https://claude.ai/code/artifact/6eba1789-16bd-4072-9bc2-70d539afe17c

## HOW TO RE-VERIFY (any session)
Harness: bin/oscar-corrtest (LOCALLY PATCHED — see TEST_CAMPAIGN_CONTEXT.md; fresh clone
needs internal/oscar/flextime.go + alertname fold + am_fingerprint audit keying, all in this
repo's diff). Target tgt_6KYCH7ESVS65T87RNBPQ97SSFK -> https://192.168.29.195/ext/mw
(--insecure; key = webhookreceiver test key, env OSCAR_API_KEY). Box is SHADOW: always
--pipeline-mode phase_a_audit_only; assert on CorrelationAudit keyed by the 16-hex
am_fingerprint LABEL (never the 12-hex history fingerprint); history API ignores `size`
(10/page — PAGE IT). Full protocol + 8 pitfalls: .claude/skills/oscar-correlation-test/
Slash commands: /test-correlation (full suite), /correlation-campaign (resumable iteration).
Run everything SEQUENTIALLY; rules must return to 0 after every block.
