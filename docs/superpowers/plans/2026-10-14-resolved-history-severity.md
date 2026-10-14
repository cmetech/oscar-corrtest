# Resolved History severity lifecycle binding

Reproduce real `status=resolved`, current `severity=cleared`, absent severity label using the existing staged Flood recovery/refire HTTP fixture. Retain the actual `original_severity` response field as separate evidence. Only the source episode observation may use this field: resolved + cleared + matching original severity, with exact episode start/fingerprint/other labels and no conflicting retained severity label. Firing checks and native cleanup keep strict current-severity matching.

No new endpoint, source capability, physical clear, permission, schema or fixture pattern. Test missing/wrong original, arbitrary current severity and changed source labels as inconclusive; verify canonical partial recovery, refire and subsequent recovery pass only their source History observations. Preserve setup failures, baseline behavioral RED, focused GREEN, full suite and one fresh review before feature commit.
