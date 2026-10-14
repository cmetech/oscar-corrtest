# History severity correspondence

Scope: fix the actual ordinary receiver probe/cleanup wire mismatch found while assessing managed-cohort integration. No cohort startup, new routes, grants, schema migration or sender changes.

1. RED: serve actual History JSON with top-level `severity` and no severity label; exercise the real client ordinary probe/read-back/clear. Add missing, conflicting and changed severity negatives, including exact named source episode checks.
2. Retain optional top-level severity on HistoryRecord. A single observed-label comparison handles the promoted severity field without adding it to Labels: if both forms exist they must agree with expected; absence fails. Other labels keep presence plus equality. Reuse in ordinary probe/clear and exact source episode binding. Outbound clear always uses frozen original native labels/start.
3. Focused checks, full Go suite, one fresh whole-cut review and one fix pass. Archive initial failure/pass evidence with deterministic gzip, commit feature-only and leave clean. Do not conflate severity correspondence with immutable source lifecycle or physical-clear authority.
