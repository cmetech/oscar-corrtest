# OSCAR Correlation Test Campaign — shared context (2026-08-29)

## Environment (verified working)
- Patched harness binary: `/home/splunk/oscar_latest_all/oscar-corrtest/bin/oscar-corrtest`
  (v1.0.2 + 2 local fixes, NOT the released binary; build via podman, see below)
- OSCAR external API: `https://192.168.29.195/ext/mw`  (IP only; `localhost` 404s)
- TLS: self-signed `O=demo, CN=reverseproxy.ericsson.com`; addressed by IP -> SAN mismatch.
  Targets MUST be registered with `--insecure`. `--ca-file` alone does NOT work.
- API key: env var `OSCAR_API_KEY` = `<OSCAR_API_KEY - webhookreceiver test key, see team secrets>`
  (never written to disk; export it in each shell)
- Working target id: `tgt_6KYCH7ESVS65T87RNBPQ97SSFK` (insecure, credential-env OSCAR_API_KEY)
- `doctor --pipeline-mode phase_b_dispatch` => Compatible: true, EXIT 0.

## Build (no host Go toolchain; use podman)
cd /home/splunk/oscar_latest_all/oscar-corrtest
podman run --rm -v "$PWD":/src:z -w /src -e CGO_ENABLED=0 \
  docker.io/library/golang:1.27.0-bookworm \
  go build -o /src/bin/oscar-corrtest ./cmd/oscar-corrtest

## Two local fixes already applied (uncommitted)
1. `internal/oscar/flextime.go` (new): `flexTime` type accepting OSCAR's zone-less
   timestamps (`2026-08-27T17:10:36.326000`). Go's time.Time requires RFC3339, so all
   three decode sites (historyWire.createdAt, auditWire.created_at, notificationWire.created_at)
   previously failed with "OSCAR returned malformed JSON" (client.go:409).
2. `internal/oscar/client.go` historyWire.normalized(): OSCAR returns `alertname` as a
   TOP-LEVEL field, not inside the labels array. Fold it into the label map, otherwise
   doctor reports missingLabels:["alertname"] and labelsSurvived:false.
These are harness-side contract adapters. Do NOT "fix" OSCAR for them.

## OSCAR API shapes (confirmed live)
- history: `{total_records,total_pages,page,per_page,records:[{ID,fingerprint,alertname,
  status,createdAt,labels:[{Label,Value}],annotations:[...]}]}`
- correlation rules list: `{rows:[],total,page,perPage}`  (currently total=0)
- rule validate: POST /api/v1/correlation_rules/validate -> `{valid,errors:[{field,message}]}`
  required fields: name, pattern, window_seconds, match_criteria
- Permissions needed by the key (oscar-middleware/src/app/core/route_permissions.py):
  manage:correlation_rules (validate/create/delete, :295 — NO granular per-verb perm),
  read:correlation_rules (audit, :304), create:alerts (POST /api/v1/alerts, :195),
  read:alerts (history, :187)

## CURRENT BLOCKER (the thing to resolve)
`run builtin:flood` => INCONCLUSIVE. Evidence from run crt_48E25NV13XH51S3E0PC1M8HFX2:
- 5/5 source alerts DID reach alert history (sourceFingerprints has 5, min_count=5)
- syntheticCount: 0, auditOutcomes: null
- corrtest correctly created rules 3+4, polled audit per fingerprint, deleted both (cleanup CLEAN)
=> The correlator never emitted. Container `correlator` is Up+healthy on oscar_network,
   resolves `nats` fine (172.18.0.25). NATS_CONNECTION_ERRORs in its log STOPPED at
   19:48; the run was 20:39, so NATS was connected.
=> Prime suspect: `CORRELATOR_DISPATCH_ENABLED=false` in the running container
   (Shadow, not Authoritative). Set in oscar/conf/env/*.env.
   NOTE: /api/v1/correlator/stats and /status are 404 on :5400 — find the real
   introspection endpoints.

## Rules of engagement
- Do NOT change any OSCAR config/env/container without explicit user approval.
  The user was previously burned by an unapproved change and has an explicit hard rule.
  Report what should change + blast radius; let the user decide.
- No commits. No pushes. No branches.
- Alert injection is fine (that is the point), but every rule must carry
  `run=<runId>` in its description so cleanup can find it.
