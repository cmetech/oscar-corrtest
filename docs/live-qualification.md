# Isolated live qualification

`make live-qualification` is the only target that intentionally contacts a
real OSCAR deployment. It is never a dependency of `ci` or `release-gate`.
Use a disposable target: the harness creates temporary correlation rules,
injects source alerts, emits synthetic alerts on a Phase-B deployment, and
resolves the exact run-owned alert history it observes.

Before running, independently verify that:

- the stored target uses OSCAR's external `/ext/mw` API and an API-key
  credential reference with correlation-rule and alert permissions;
- correlator publication and dispatch are Phase B, NATS/correlator workers are
  healthy, and correlation audit retention exceeds the test windows;
- configured notifier names used by parent-child cases exist;
- the OSCAR deployment and its alert history are disposable for this test.

Build the binary and set the explicit two-part acknowledgement:

```bash
make build
export OSCAR_CORRTEST_LIVE_TARGET_ID=tgt_...
export OSCAR_CORRTEST_LIVE_PHASE_B_ACK=I_ACKNOWLEDGE_PHASE_B_ON_A_DISPOSABLE_TARGET
export OSCAR_CORRTEST_LIVE_DISPOSABLE_ACK=I_ACKNOWLEDGE_PHASE_B_ON_A_DISPOSABLE_TARGET
export OSCAR_CORRTEST_LIVE_DATA_DIR=/var/lib/oscar-corrtest
export OSCAR_CORRTEST_LIVE_OUTPUT_DIR=$PWD/live-proof-$(date -u +%Y%m%dT%H%M%SZ)
make live-qualification
```

The gate first runs doctor, then executes all eight built-ins serially. Every
run must be `PASS` with `CLEAN` cleanup, export successfully, and pass offline
bundle verification. `INCONCLUSIVE`, `ERROR`, `FAIL`, `DIRTY`, `UNKNOWN`, a
pending/missing/changed artifact, or a cancellation stops the gate and prevents
an overall PASS. The summary contains target and run IDs but no credentials.

Bundle SHA-256 verifies integrity, not publisher identity. Retain the summary
and bundle hashes in an independently controlled system until signed bundles
are implemented.
