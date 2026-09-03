---
name: oscar-correlation-test
description: Test the OSCAR correlator end-to-end — all 8 patterns, positive + negative cases, no-rule baseline, flake detection, and direct CorrelationAudit verification. Use when the user says "test correlation", "run the correlation suite", "is the correlator working", "test flood/parent_child/persistence", "correlation regression", or after changing correlator code, rules, or NATS/eventmesh config.
---

# OSCAR correlation testing

Drives the `oscar-corrtest` Go harness AND verifies against OSCAR's own
CorrelationAudit API, because the harness and the UI can both lie (see PITFALLS).

## 0. Environment (verify FIRST, every time)

```bash
export PATH="$HOME/.local/bin:$PATH"
export OSCAR_API_KEY='<external API key>'
export B=https://192.168.29.195/ext/mw          # host IP; localhost 404s
export CT=/home/splunk/oscar_latest_all/oscar-corrtest/bin/oscar-corrtest
export TGT=tgt_6KYCH7ESVS65T87RNBPQ97SSFK       # registered with --insecure
```

Preflight — ALL must pass before any run:
```bash
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E '^(middleware|correlator|nats|alertmanager)\b'
curl -sk -o /dev/null -w '%{http_code}\n' -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/alerts/history?page=1&size=1"   # want 200
docker exec correlator curl -s localhost:5400/ready    # want nats:true consumer:true redis:true
curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules?page=1&per_page=5" | python3 -c 'import sys,json;print("rules:",json.load(sys.stdin)["total"])'
$CT doctor --target $TGT --pipeline-mode phase_a_audit_only   # want exit 0
```
A non-zero rule count before a run means a previous run leaked rules — clean up first.

## 1. DECLARE THE TRUE PIPELINE MODE

`--pipeline-mode` is an OPERATOR DECLARATION. OSCAR exposes no discovery endpoint.
Declaring the wrong mode produces false FAILs.

```bash
docker exec correlator env | grep CORRELATOR_DISPATCH_ENABLED
```
- `false` (Shadow, the normal state) -> use `--pipeline-mode phase_a_audit_only`
- `true`  (Authoritative)             -> use `--pipeline-mode phase_b_dispatch`

In Shadow the correlator evaluates and writes audit rows but emits NO synthetic alert.
`syntheticCount: 0` is CORRECT there — assert on CorrelationAudit outcomes instead.
Declaring `phase_b_dispatch` on a Shadow box fails every positive case on
`synthetic-alert-count` while `audit-count` passes. That is the tell.

## 2. Run the suite

```bash
mkdir -p sweepN
for p in flood co_occurrence sequence persistence absence parent_child cross_source threshold; do
  $CT run builtin:$p --target $TGT --pipeline-mode phase_a_audit_only --output json \
     > sweepN/$p.json 2> sweepN/$p.log
  echo "$p exit=$?"
done
```
SEQUENTIALLY — never in parallel. All runs share one OSCAR; concurrent injection
cross-contaminates windows.

Extra scenarios in `scenarios/` (flood boundary/window-expiry/group-partition/
wrong-groupby, parent_child multi-child/child-before-parent/group-isolation/tag-only,
threshold boundary, persistence long-window):
```bash
$CT run scenarios/flood-boundary-above.yaml --target $TGT --pipeline-mode phase_a_audit_only
```

Scoreboard:
```bash
for f in sweepN/*.json; do python3 -c "
import json,sys
d=json.load(open('$f')); d=d.get('data',d); cr=d.get('canonicalReport',{})
rows=[f\"{c.get('code')}={c.get('verdict')}\" for c in cr.get('cases',[])]
ao=[]
for c in cr.get('cases',[]): ao += (c.get('auditOutcomes') or [])
print(f\"{'$f':28s} {cr.get('verdict','?'):13s} {d.get('cleanupStatus','?'):7s} {' '.join(rows):22s} {','.join(sorted(set(ao)))}\")"; done
```

## 3. RESULTS ARE FLAKY — always run N>=3

Confirmed 2026-08-30: identical rules on an idle box gave different verdicts across
sweeps (parent_child and sequence passed in one sweep, failed the next; absence went
INCONCLUSIVE). A single green run proves nothing; a single red run is not yet a defect.
Run the suite 3+ times and report a FLAKE RATE per pattern. Only a pattern failing
consistently across runs is a candidate defect, and only after step 4.

## 4. VERIFY AGAINST OSCAR DIRECTLY (never trust one layer)

CorrelationAudit is keyed on the 16-hex `am_fingerprint` LABEL — NOT the 12-hex
AlertHistory `fingerprint` field. Using the wrong one silently returns 0 rows.

```bash
# get am_fingerprint from the history record, then:
curl -sk -H "X-API-Key: $OSCAR_API_KEY" \
  "$B/api/v1/correlation_rules/audit?fingerprint=<am_fingerprint>&page=1&perPage=20" \
  | python3 -m json.tool
```
Outcomes: `parent_emitted` (rule fired) | `enriched` (matched, window advanced) |
`suppressed_per_notifier` (parent_child suppression) | `released_no_trigger` (no fire).

Read-only debug endpoints (key needs `manage:correlator:debug`):
```
GET  $B/api/v1/correlation_rules/debug/windows     # active windows + keys
GET  $B/api/v1/correlation_rules/debug/timers      # pending timers, backlog_drained
POST $B/api/v1/correlation_rules/debug/simulate    # {"labels":{"alertname":"X"}} dry run
```
`simulate` has NO side effects — ideal for proving rule/index resolution.

## 5. No-rule negative baseline (correlation must be ADDITIVE)

With `correlation_rules` total = 0, inject firing then resolved and assert:
alert reaches history, labels intact, NO `oscar_correlation_*` labels added,
audit shows `released_no_trigger`, and status goes `firing` -> `resolved`.
Alert envelope (a bare list is 422 — it needs the full AM group envelope):
```json
{"receiver":"oscar-corrtest","status":"firing","groupKey":"k",
 "groupLabels":{"alertname":"X"},"commonLabels":{"alertname":"X","severity":"critical"},
 "commonAnnotations":{"summary":"s"},
 "alerts":[{"fingerprint":"<16hex>","status":"firing",
            "labels":{"alertname":"X","severity":"critical"},"annotations":{"summary":"s"}}]}
```

## 6. Cleanup — ALWAYS

```bash
curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules?page=1&per_page=50" \
 | python3 -c 'import sys,json;d=json.load(sys.stdin);print("total:",d["total"]);[print(" ",r["id"],r["name"]) for r in d["rows"]]'
curl -sk -X DELETE -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules/<id>"
```
Must return to 0. corrtest deletes only rules whose description contains `run=<runId>`;
anything created by hand or leaked by a 422 must be removed manually.

## PITFALLS (all confirmed live — do not rediscover)

1. **Wrong fingerprint domain.** Audit is keyed on 16-hex `am_fingerprint`; AlertHistory
   exposes a 12-hex `fingerprint`. Querying with the 12-hex ALWAYS returns 0 rows. This
   bit the harness AND the product UI (CorrelationTab.js:90).
2. **The Alert View Correlation tab is broken** (DEFECT 3) — it queries with the 12-hex
   fingerprint, so it shows "This alert was not correlated" for every alert, in every
   mode. Its empty-state text blames Shadow, which is misleading. NEVER use the UI as
   evidence that correlation did or did not happen. Use the audit API.
3. **Resolves are dropped inside ~2 min** (DEFECT 1) — JetStream `Nats-Msg-Id` dedup is
   keyed on the alert fingerprint, which a resolve shares with its firing, so the resolve
   never reaches the correlator. Persistence then emits a false parent. Correlator-only:
   AlertHistory/ESB/SMTP take the HTTP fan-out and are unaffected. Reproduce with a
   delay-controlled A/B (resolve at 8s vs 150s), NOT the builtin scenario (flaky).
4. **422 create still persists the row** (DEFECT 2) — `/validate` accepts a flood rule
   without `emit_spec`; create returns 422 "rule persisted but rejected by loader" and
   leaves the row. Always re-check the rule count after a failed create.
5. **Match clauses accept ONLY `alertname`.** Every match_criteria schema is
   `extra="forbid"`, including the inner match clause. Isolate by unique alertname;
   partition with `group_by_labels` (a List[str] — a dict is rejected).
6. **`persistence.unresolved_for_seconds` has a hard floor of 30.** Timer-driven patterns
   (persistence, absence) need `>= unresolved_for + ~10s` of observation
   (WINDOW_GRACE_SECONDS=5, TIMER_POLL_INTERVAL_MS=100).
7. **TLS**: the reverseproxy cert is self-signed (`CN=reverseproxy.ericsson.com`) and we
   address it by IP, so targets need `--insecure`. `--ca-file` alone fails on SAN mismatch.
8. **Stale windows** for deleted rule ids linger; `/debug/rules/{id}` 404s for them.

## HARD RULES

- NEVER flip `CORRELATOR_DISPATCH_ENABLED`. Phase B is destructive on this box: the
  correlator posts `alerts[].labels` as a dict while AlertHistoryHandler expects the
  alertmanager v1 list form, so NO AlertHistory row is written for ANY alert. Plus the
  scheduler lag-observer 500s mean the Redis flag never goes true and Phase B never
  actually engages. Everything below can be proven in Shadow.
- No OSCAR config/env/container changes without explicit user approval.
- Report audit outcomes as the evidence. `syntheticCount` is meaningless in Shadow.
