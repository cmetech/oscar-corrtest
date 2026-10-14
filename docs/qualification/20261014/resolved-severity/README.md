# Resolved History severity — 2026-10-14

Backend History returns current `severity=cleared` after resolution and separately retains `original_severity`. AM `routers/alerts.py` `_canonical_history_record` returns both; new row creation saves original severity, and AM/Middleware schemas document it as episode-start severity never updated. The previous strict current-severity match made staged recovery inconclusive.

## Evidence

- `initial-fixture-setup.log.gz` and `initial-adjacent-fixture-setup.log.gz`: first test attempted to override reserved scenario severity and therefore failed validation. These are setup failures, not behavioral proof. The fixture was corrected to use its unchanged compiler-owned `warning` severity.
- `red.log.gz`: rerun against committed `4fafc90` product files reproduces the canonical resolved wire failure at the partial recovery checkpoint. Negative missing/wrong original, arbitrary changed current and changed source label controls already remain inconclusive. Only this cut's own in-progress product edits were temporarily saved/restored for that baseline reproduction; no other work was reverted.
- `green.log.gz`: actual client/runner adjacent suites pass after retaining optional original severity and using a narrow observation-only comparison.
- `full-suite.jsonl.gz`: **889 tests/subtests passed, 27 passing packages, zero failures**. Two package-level skip records are the no-test-file `cmd/generate-scenario-schema` and `cmd/package-zip`; no individual tests skipped.

`TestStagedRecoveryBindsOriginalSeverityFromActualHistoryWire` runs the existing Flood lifecycle YAML through the real client and independent HTTP fixture, promoting severity to actual History fields. Canonical partial recovery, refire in a new episode, and later recovery pass all five checkpoints. Each bad-evidence control stops after seven stimuli with INCONCLUSIVE. The canonical report retains `originalSeverity`; physicalClear remains NOT_PROVEN.

`TestResolvedSeverityObservationDoesNotRelaxNativeInputIdentity` checks canonical/retained-label evidence, missing or wrong original, changed label, non-resolved status and arbitrary current severity. The strict comparison used by ordinary probes and native cleanup still rejects every resolved/cleared example; this exception belongs only to source History observations. Exact episode start, fingerprint, remaining labels and competing-row checks are unchanged. Legacy matching current/label evidence remains supported, but a present contradictory original severity now prevents source binding.

## Review and limits

Fresh reviewer `resolved_severity_review` (GPT-6 Astra/high, isolated context) reported no actionable findings. It checked field decoding/report serialization, observation callers, strict ordinary comparison and regression scope; no tests or backend execution were rerun by the reviewer. No post-review product fix was needed. `git diff --check` passed.

No joined cohort, live endpoint, production permission, sender payload, receiver capability or selected admission changed. This proves local source History status handling, not physical recovery, trusted source recovery, actual live grouping or UAT acceptance. Logs use deterministic gzip (mtime=0); all failures and passes are retained.
