---
name: oscar-correlation-campaign
description: Resumable autonomous OSCAR correlation test campaign - reads CAMPAIGN-STATE.md, executes ONE worklist item, writes findings, updates state, stops. Designed to run repeatedly under /loop so work continues across fresh contexts without a human. Use when the user wants long-running unattended correlation testing, says "keep working while I sleep", "resume the campaign", or "continue correlation testing".
---

# OSCAR correlation campaign — resumable loop unit

You are ONE ITERATION of a long-running campaign. You have a fresh context. Everything
you need to know is on disk. Do ONE unit of work well, hand off cleanly, stop.

STATE FILE: /home/splunk/oscar_latest_all/oscar-corrtest/CAMPAIGN-STATE.md

## Iteration protocol — follow exactly

### 1. ORIENT (always first)
Read, in this order:
  - CAMPAIGN-STATE.md          <- worklist, pattern status, agents in flight, NEXT ACTION
  - ~/.claude/skills/oscar-correlation-test/SKILL.md   <- how to run/verify + PITFALLS
Only read FINDINGS-*.md files when the item you picked needs them. Do not re-read
everything; context is finite and that is the whole point of this loop.

### 2. CHECK LANES
For each agent under "AGENTS IN FLIGHT" with status RUNNING, check whether its output
file exists AND looks complete (has its concluding section, not a stub). If complete,
mark it DONE in the state file and absorb its findings into PATTERN STATUS / CONFIRMED
DEFECTS. Never duplicate a RUNNING lane's work.

### 3. PICK EXACTLY ONE ITEM
Take the first unblocked `[ ]` item from WORKLIST (honour NEXT ACTION if it names one).
One item per iteration. If every item is blocked on a RUNNING lane, do W9 (report
refresh) or add a new useful item rather than idling.

### 4. PREFLIGHT (before any test work)
```bash
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E '^(middleware|correlator|nats|alertmanager)\b'
curl -sk -o /dev/null -w '%{http_code}\n' -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/alerts/history?page=1&size=1"
curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules?page=1&per_page=50" \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print("rules:",d["total"])'
```
If OSCAR is down or degraded: DO NOT try to fix or restart it. Record the observation in
the state file, mark the iteration blocked, and stop. The user restarts their own stack.
If the rule count is non-zero at the START of an iteration, a previous iteration leaked —
delete those rules by id and record it as a finding.

### 5. DO THE WORK
Follow oscar-correlation-test SKILL.md for anything involving runs. Non-negotiables:
  - declare the TRUE pipeline mode (Shadow -> phase_a_audit_only)
  - patterns run SEQUENTIALLY
  - verify verdicts against CorrelationAudit keyed on the 16-hex am_fingerprint
  - N>=3 iterations before calling anything pass or fail — results are known-flaky
Write substantive output to the right file, never into CAMPAIGN-STATE.md:
  FINDINGS-FLAKE.md | FINDINGS-DEFECTS.md | FIXES-PROPOSED.md | MORNING-REPORT.md

### 6. CLEAN UP (mandatory, even if the work failed)
Rule count must return to 0. Delete leaked rules by id. Kill nothing else.

### 7. HAND OFF — update CAMPAIGN-STATE.md before stopping
  - bump ITERATION and LAST_UPDATED
  - move the finished item to DONE with a one-line result
  - update PATTERN STATUS if verdicts changed
  - add any NEW defect to CONFIRMED DEFECTS (one line + pointer to the detail file)
  - add newly discovered work as new `[ ]` items
  - set NEXT ACTION to the specific next item
Keep the file under ~200 lines. Compress DONE entries to one line each. The next
iteration has NO memory of you — if it is not written down, it did not happen.

### 8. STOP
Report in 5-10 lines: item done, result, what changed, next action. Do not start
another item.

## HARD RULES (every iteration)
- NEVER change OSCAR config/env/containers. Never restart an OSCAR service.
  Never flip CORRELATOR_DISPATCH_ENABLED — Phase B is destructive on this box.
- Fixes are WRITTEN TO DISK ONLY. No rebuild, no redeploy, no commit, no push, no branch.
- Never use the Alert View Correlation tab as evidence — it is broken (DEFECT 3).
- Do not re-report D1/D2/D3 as new findings; only note status changes.
- If something looks like a NEW defect, prove it with a deterministic reproducer against
  the audit API before recording it. One confirmed defect beats five suspicions.
