# FINDINGS — OSCAR correlator backend (2026-08-29)
No OSCAR config, env, container or service was changed. Read-only throughout.

## 0. HEADLINE
The correlator was NOT broken. It correlated run crt_48E25NV13XH51S3E0PC1M8HFX2
correctly and wrote a `parent_emitted` audit row for the flood rule at 20:39:33.
The harness saw nothing for TWO independent reasons:

1. FINGERPRINT DOMAIN MISMATCH (harness bug, not OSCAR).
   corrtest polls audit?fingerprint=<12-hex> using the AlertHistory `fingerprint`
   field. CorrelationAudit.alert_fingerprint is a DIFFERENT 16-hex value, carried
   on the alert as the `am_fingerprint` LABEL. Zero rows can ever match.
   FIX: poll with the `am_fingerprint` label value. Never `fingerprint` /
   `oscar_fingerprint`.
2. CORRELATOR_DISPATCH_ENABLED=false (Shadow) suppresses only the synthetic
   parent EMIT. syntheticCount:0 is correct and expected. Audit rows are still
   written UNCONDITIONALLY.

Evidence (audit rows for the run):
  26158  rule 3  5964b28730884e38  parent_emitted   <-- FLOOD FIRED
  26154/26160/26162/26163 rule 3   enriched
  26156  rule 4  71af00e9d3f878cc  enriched
Alert whose history fingerprint is 31038f7b3e62 carries:
  oscar_fingerprint 31038f7b3e62      (12 hex, AlertHistory)
  am_fingerprint    0be9385a228040e7  (16 hex, CorrelationAudit key -> row 26162)

## 1. Ingest path (proven)
JetStream durable PULL consumer. AM celery worker publishes each stored alert to
subject `oscar.alerts.stored` (oscar-alertmanager/src/app/core/settings.py:167,
core/nats_publisher.py). Correlator binds durable `correlator-main` on stream
OSCAR_ALERTS (oscar-correlator/src/app/core/nats_client.py:75-76, subscribe_main
:335). Stream created by eventmesh-api streams_bootstrap.py (nats_client.py:228-232).
  /ready -> {"status":"ready","nats":true,"consumer":true,"redis":true,
             "lua_registered":true,"timer_store":true}
  AM worker: CORRELATOR_NATS_PUBLISH_ENABLED=true
NATS_CONNECTION_ERROR / redis-stack gaierror bursts were historic (08-27 19:47-19:48
restart window). Both resolve now (redis-stack 172.18.0.59, nats 172.18.0.25).

## 2. What CORRELATOR_DISPATCH_ENABLED=false disables
oscar-correlator/src/app/core/consume.py:
  :539  await audit_writer.buffer(decision)   <- BEFORE any gate (D-20 buffer-first)
  :544-546 comment: buffering is UNCONDITIONAL (Phase A records what WOULD have
           notified); only emit/dispatch SIDE EFFECTS are gated
  :556-561 dispatch_eligible = dispatcher is not None and fail_open_state is not None
           and fail_open_state.correlator_dispatch_enabled and is_dispatchable(decision)
  :565-571 only inside the gate: synthetic_emitter.emit() + _accumulate_held_child
  :242-244 _resolve_child_hold_until returns None when gated off
Shadow gives: windows, patterns, parent_emitted/enriched/released_no_trigger audit
rows. Withholds: synthetic alert, correlator->notifier dispatch.

## 3. CORRELATOR_ENABLED absent from container = correct
It is an OSCAR CLI compose-profile toggle, not a container env var
(oscar/oscar:460 _is_flag_true "${CORRELATOR_ENABLED:-false}"; default listed
oscar/oscar:6337). Correlator compose passes an explicit whitelist excluding it
(oscar-correlator/docker-compose.yml:19-104). Effective value true from
oscar/overrides.env:208.

## 4. Env-file chain (oscar/oscar:273-277, :1070-1095)
versions.env -> all conf/env/*.env alphabetically -> oscar/.env ->
conf/env/feature-overrides.env -> oscar/overrides.env (LAST, final say)

  CORRELATOR_ENABLED              conf default unset->false | overrides.env:208 'true'  | n/a (profile flag)
  CORRELATOR_NATS_PUBLISH_ENABLED conf 'false' (alertmanager.env:305) | overrides.env:209 'true' | AM worker true
  CORRELATOR_DISPATCH_ENABLED     conf 'false' (correlator.env:90, alertmanager.env:306) | overrides.env:211 'false' | false

overrides.env:203-211 operator note: "enable in SHADOW mode (2026-07-29)... SAFETY:
dispatch stays OFF (Phase A). Do NOT flip without the cutover preflight."

## 5. CAMPAIGN BLOCKER — do NOT flip dispatch on this box
memory/correlator_phaseb_no_alerthistory.md (root-caused 2026-08-12 live A/B):
with CORRELATOR_DISPATCH_ENABLED=true, NO AM_AlertHistory row is written for ANY
alert — correlator POSTs alerts[].labels as a DICT while AlertHistoryHandler reads
the alertmanager v1 LIST form [{"Label":k,"Value":v}]. Notifier returns 200 and
writes nothing, silently.
VERIFIED STILL PRESENT IN THE RUNNING CONTAINER:
  consume.py:768 alert["labels"] = _normalize_labels(alert.get("labels"))  -> dict
  dispatch_planner._build_alert_group() (core/dispatch_planner.py:402-428) coerces
  only commonLabels/commonAnnotations; docstring: "The alerts[] entry is passed
  through untouched"
The ~10-line fix has not landed. Phase B would break the very AlertHistory the
harness reads.

## 6. Debug endpoints work; our key HAS manage:correlator:debug
/stats and /status 404 because they do not exist. Real routes (openapi.json):
/metrics /health /ready and under /api/v1/correlator/: rules/reload, rules (GET/POST),
rules/validate, rules/batch, rules/export, rules/{id}/export, rules/import,
rules/{id} (GET/PUT/DELETE), audit, audit/children, debug/windows, debug/timers,
debug/simulate, debug/rules/{id}, guardrails/config.
Via middleware prefix /api/v1/correlation_rules/... (route_permissions.py:325-328).
LIVE:
  GET /debug/windows -> 200 {"active_windows_total":2,"sample":[
      {"window_key":"oscar:correlator:cooccur:7:369b242a9db8",...}]}
  GET /debug/timers  -> 200 {"pending_timers":0,"backlog_drained":true,...}
  POST /debug/simulate {"labels":{"alertname":"..."}} ->
      {"dry_run":true,"alertname":...,"matched_rule_count":N,"decisions":[...]}
      read-only projection: no window write/timer/emit/dispatch/audit
      (routers/debug.py:118-145)

## 7. Rule schema — empirically confirmed, all 8 patterns
Top-level (schemas/rule.py RuleCreateRequest, extra="forbid"):
  REQUIRED name, pattern, window_seconds (int>0, cap 3600), match_criteria
  OPTIONAL group_by_labels (List[str]/null; a DICT IS REJECTED), emit_spec,
           for_notifiers, priority (default 100), max_synthetic_per_minute
           (default 100), enabled (default true), description (PUT run=<runId> HERE),
           created_by, estimated_alerts_per_window, estimated_group_cardinality

EVERY match_criteria schema is extra="forbid", INCLUDING the inner match clause —
the ONLY key allowed in a match clause is `alertname` (plain string; no regex, no
operators, no extra label matchers).

All 8 returned {"valid":true,"errors":[]}:
  flood         {"match":{"alertname":"A"},"min_count":3}                       min_count>=1
  co_occurrence {"required_matches":[{"alertname":"A"},{"alertname":"B"}],"min_matches":2}
                required_matches>=2; 1<=min_matches<=len(required_matches)
  sequence      {"sequence":[{"alertname":"A"},{"alertname":"B"}]}              >=2, ordered
  persistence   {"match":{"alertname":"A"},"unresolved_for_seconds":60}         >=30 HARD FLOOR
  absence       {"expected_match":{"alertname":"A"},"expected_every_seconds":60,
                 "absent_for_seconds":180}   both >=1; absent_for >= expected_every
  parent_child  {"parent_match":{"alertname":"P"},"child_matches":[{"alertname":"C"}],
                 "suppress_children_for_notifiers":[],"tag_children_for_notifiers":[]}
                child_matches>=1; the two notifier lists optional, default []
  cross_source  {"required_sources":[{"source":"snmp","match":{"alertname":"A"}},
                 {"source":"splunk","match":{"alertname":"B"}}]}                >=2
  threshold     {"match":{"alertname":"A"},"distinct_label":"host",
                 "min_distinct_count":5}     >=1 (HyperLogLog)

Error contract (live negatives):
  match:{"alertname":"A","severity":"crit"} ->
    {"valid":false,"errors":[{"field":"match_criteria.match.severity",
                              "message":"Extra inputs are not permitted"}]}
  match_criteria:{} ->
    {"valid":false,"errors":[{"field":"match_criteria.match","message":"Field required"},
                             {"field":"match_criteria.min_count","message":"Field required"}]}
group_by_labels genuinely optional (flood validated without it).

SCENARIO-DESIGN CONSEQUENCE: isolation must come from a UNIQUE alertname per run
(corrtest already does this), not from label matchers. group_by_labels (e.g. ["site"])
is what partitions windows.

Live guardrails that can silently reject at load: MAX_WINDOW_DURATION=3600,
MAX_ALERTS_PER_WINDOW=1000, MAX_GROUP_CARDINALITY=10000, MAX_MATCHERS_PER_RULE=10,
MAX_SYNTHETIC_PER_MINUTE_HARD_CAP=1000, MAX_ACTIVE_WINDOWS=10000.
WINDOW_GRACE_SECONDS=5, TIMER_POLL_INTERVAL_MS=100 — persistence/absence fire off the
timer subsystem, so allow >= unresolved_for_seconds + ~10s.

## 8. Change set (for the USER to decide — NOT applied)
OPTION A (RECOMMENDED) — change NOTHING in OSCAR. Every assertion is available in Shadow:
  synthetic parent emitted -> CorrelationAudit.outcome == "parent_emitted"
  child enrichment         -> outcome == "enriched"
  negative / no-fire       -> released_no_trigger, or no rule-keyed row
  window state mid-run     -> GET /debug/windows
  timer patterns           -> GET /debug/timers
  rule/index resolution    -> POST /debug/simulate
Only harness change: key the audit poll on am_fingerprint. BLAST RADIUS: ZERO.

OPTION B — Authoritative (Phase B). CURRENTLY UNSAFE.
  CORRELATOR_DISPATCH_ENABLED 'false' -> 'true' in oscar/overrides.env:211
  read by correlator settings.py:227 via FailOpenState (fail_open_poller.py:54)
  AND AM settings.py:166 via _is_legacy_dispatch_enabled (tm/tasks.py:82-140)
  RESTARTS (baked at compose up; editing the file alone does nothing):
    correlator, alertmanager, alertmanager_middleware,
    oscar-alertmanager-alertmanager_worker-1  (correlator stop_grace_period 45s)
  WHAT TRIGGERS IT:
    1. consume.py:556-561 dispatch_eligible true -> synthetic_emitter.emit() POSTs
       synthetic parent to ALERTMANAGER_BASE_URL/alerts; dispatcher.dispatch() POSTs
       to NOTIFIER_URL/api/v1/notifiers/send
    2. AM legacy broadcast stands down — _is_legacy_dispatch_enabled (tm/tasks.py:117)
       requires Redis key oscar:alertmanager:correlator_dispatch_enabled == "true"
       (SETEX TTL 300s from the scheduler lag observer) to suppress legacy.
       Fail-open: absent/"false"/Redis unreadable -> legacy still fires (tm/tasks.py:121-140)
  BLAST RADIUS (OSCAR-wide, not test-scoped):
    AlertHistory BREAKS (section 5, proven, unfixed). Alert View dark.
    Notifiers: correlator becomes sole notification path for EVERY alert.
      Enabled in NTF_Notifiers: ALERTHISTORY, LOGGER, SMTP, CORRELATION_EVENTS
    Ticketing/ESB rides the same notifier fan-out.
    Scheduler dependency: lag observer erroring here — scheduler log 08-27 17:08
      "Correlator lag observer trigger HTTP error: 500". A wedged observer means the
      Redis flag never goes true, system fails open to legacy, Phase B never engages
      — the test would prove nothing.
    Stale windows for deleted rules (cooccur:7, cooccur:8) would dispatch on expiry.
  PREREQS: (1) land _build_alert_group label-shape fix + AlertHistoryHandler error
  guard, re-prove AlertHistory in Phase B; (2) phase99_cutover_preflight.sh --live PASS
  (check #5 CONFIG-COHERENCE demands both flags true in oscar/conf/env, conflicts with
  this box keeping them in overrides.env — expect a false FAIL); (3) fix scheduler
  lag-observer 500.
  THERE IS NO PER-RULE OR PER-TEST SCOPING. THE FLAG IS GLOBAL.

## 9. Blockers
 1 HIGH   harness polls audit with wrong fingerprint domain (12-hex oscar_fingerprint
          vs 16-hex am_fingerprint)                        -> harness agent
 2 HIGH   syntheticCount can never be >0 in Shadow; assert on CorrelationAudit.outcome
                                                            -> harness agent
 3 BLOCKS-B Phase B destructive here (AlertHistory label-shape defect, unfixed 08-12)
 4 BLOCKS-B scheduler lag observer 500s -> Redis dispatch flag never true
 5 MEDIUM match clauses accept only alertname; isolate by unique alertname, partition
          by group_by_labels                                -> harness agent
 6 LOW    stale windows for deleted rule ids linger; /debug/rules/{id} 404s for them
