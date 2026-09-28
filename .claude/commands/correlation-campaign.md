---
description: Run one iteration of the resumable OSCAR correlation campaign (reads CAMPAIGN-STATE.md, does one work item, hands off)
---

Invoke the `oscar-correlation-campaign` skill and execute EXACTLY ONE iteration.

$ARGUMENTS

Follow the iteration protocol in the skill precisely:
ORIENT (read CAMPAIGN-STATE.md) -> CHECK LANES -> PICK ONE ITEM -> PREFLIGHT ->
DO THE WORK -> CLEAN UP (rules back to 0) -> HAND OFF (update CAMPAIGN-STATE.md) -> STOP.

State file: /home/splunk/oscar_latest_all/oscar-corrtest/CAMPAIGN-STATE.md

Reminders that matter most:
- ONE work item per iteration. Hand off; do not continue into a second item.
- The next iteration has NO memory of this one. If you do not write it into
  CAMPAIGN-STATE.md, it is lost.
- NEVER restart or reconfigure OSCAR. Fixes are written to disk only, never deployed.
- If OSCAR is down, record it, mark blocked, and stop — do not attempt repair.
- Results are known-flaky: N>=3 before declaring pass or fail.
