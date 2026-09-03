---
description: Run the full OSCAR correlation test campaign (8 patterns, N iterations, flake rate, audit verification, no-rule baseline)
---

Invoke the `oscar-correlation-test` skill and run the FULL campaign.

Arguments: $ARGUMENTS
  (optional: a pattern name to test just one, e.g. `flood`; or `N=5` to set iterations.
   Default: all 8 patterns, 3 iterations.)

Follow the skill exactly. Non-negotiables:

1. Run the PREFLIGHT first. Abort and report if middleware/correlator/nats are not up,
   if /ready is not fully true, or if the rule count is non-zero before starting.
2. Read CORRELATOR_DISPATCH_ENABLED and declare the TRUE pipeline mode. Declaring the
   wrong mode produces false failures on every positive case.
3. Run patterns SEQUENTIALLY, never in parallel.
4. Run N>=3 iterations and report a FLAKE RATE per pattern. Results are known to vary
   run to run on an idle box — a single green run proves nothing.
5. Verify every non-trivial verdict against the CorrelationAudit API directly, keyed on
   the 16-hex `am_fingerprint` label (NOT the 12-hex AlertHistory fingerprint).
6. Run the no-rule negative baseline: correlation must be additive and must not alter
   normal alert flow.
7. Clean up. Rule count must return to 0. Report any leaked rules as a defect.
8. NEVER change OSCAR config, env, or containers. Never flip CORRELATOR_DISPATCH_ENABLED.

Report: a per-pattern table (verdict, flake rate over N, audit outcomes observed),
the no-rule baseline result, any NEW defect with reproduction steps, and explicit
confirmation that cleanup returned to 0.

Known defects — do NOT re-report these as new; only note if status changed:
- DEFECT 1: JetStream Nats-Msg-Id dedup drops a resolve arriving within ~2 min of its
  firing (correlator-only; persistence emits a false parent). Intermittent.
- DEFECT 2: 422 create still persists the rule row ("rule persisted but rejected by loader").
- DEFECT 3: Alert View Correlation tab queries the wrong fingerprint domain and always
  shows "not correlated". Never use the UI as evidence.
Full detail: oscar-corrtest/FINDINGS-DEFECTS.md
