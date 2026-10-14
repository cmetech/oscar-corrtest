# History severity correspondence — 2026-10-14

The [managed cohort assessment](../../../ordinary-webhook-managed-cohort-assessment-20261014.md) found that actual History promotes severity to a top-level field and removes it from the label array. CorrTest's ordinary probe includes `severity=warning`, so the previous client refused real valid read-back; a contradictory top-level value could also be ignored when a matching label existed.

`red.log.gz` reproduces both defects using real HTTP client decoding and exact named source episode checks. `green.log.gz` passes adjacent client/runner/runtime suites after retaining optional `HistoryRecord.severity` separately and using one observed-input comparison in ordinary probe, native clear and named episode binding. No severity is fabricated into `Labels`, and native clears still use frozen original labels/start. All present severity forms must equal the expected input value; absent evidence and conflicts fail. Other labels still require presence and equality, including empty values.

The wire tests cover promoted-only, matching dual forms, legacy label-only, missing, changed promoted value and both directions of conflict. Probe output retains the observed top-level severity separately. An observed changed severity (including ordinary recovery's `cleared` value) does not reconstruct an original severity label or grant cleanup authority. Existing direct doctor behavior remains unchanged.

This is a local wire-contract correction, not a joined backend proof. No services, permissions or listeners were changed. See [the plan](../../../superpowers/plans/2026-10-14-history-severity-correspondence.md). Full `go test ./... -count=1 -json` passed **875 tests/subtests across 27 packages, zero failures**. Two package-level skip records are no-test-file commands `cmd/generate-scenario-schema` and `cmd/package-zip`; no individual tests skipped. Fresh [whole-cut review](review.md) found no actionable defects. `git diff --check` passed. No post-review product changes or repeated suite were needed.

Raw final suite evidence is `full-suite.jsonl.gz`. All logs use deterministic gzip (mtime=0), preserving failure and pass output.
