# OSCAR Correlation Test Harness

`oscar-corrtest` is a standalone Go application for black-box testing all eight OSCAR alarm-correlation patterns. It creates isolated temporary rules through OSCAR's public API, sends deterministic alerts, resolves authoritative fingerprints from alert history, collects audit/notifier evidence, cleans up owned rules, and preserves run reports in SQLite. The same executable provides a CLI and an embedded light/dark web UI.

## Quick start

Go 1.27.0 or newer is required to build. Released Linux binaries are CGO-free and need no Go, Python, Node, external database, or OSCAR source checkout.

```bash
make build
export OSCAR_API_TOKEN='...'
./bin/oscar-corrtest target add \
  --name lab-a --url https://oscar.example/ext/mw \
  --credential-env OSCAR_API_TOKEN --output json

./bin/oscar-corrtest doctor \
  --target <target-id> --pipeline-mode phase_b_dispatch

./bin/oscar-corrtest plan builtin:flood \
  --target <target-id> --pipeline-mode phase_b_dispatch

./bin/oscar-corrtest run builtin:flood \
  --target <target-id> --pipeline-mode phase_b_dispatch

./bin/oscar-corrtest serve
```

Open <http://127.0.0.1:8787>. Runs started from the browser continue if the browser disconnects; reconnecting replays persisted events.
The Scenarios page validates, previews, and imports strict YAML/JSON. Active run pages provide cancellation with bounded cleanup; cleanup-safe terminal runs can be deleted only after local artifact verification.

## What is tested

Built-in positive and negative cases cover:

- `flood`, `co_occurrence`, `sequence`, and `threshold` window/order/cardinality behavior;
- `persistence` and `absence` timer behavior;
- `cross_source` distinct-source behavior;
- `parent_child` linkage and per-notifier suppression evidence.

Physical alert names use `CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>`. Every source and expected synthetic alert carries `category=corrtest_<pattern>`, the full `oscar_test_run_id`, scenario, pattern, case, polarity, class, role, and temporary-rule labels. These values are included in the compiled plan, run UI's “Inspect in OSCAR” panel, canonical report, and evidence bundle.

See [docs/builtins.md](docs/builtins.md) for the case catalog and [docs/operator.md](docs/operator.md) for deployment and recovery.
Live target qualification is deliberately separate from offline CI; see
[docs/live-qualification.md](docs/live-qualification.md).

## Safety and proof model

- The harness uses correlation rule validate/create/read/delete only. It never calls rule import/upsert or update for temporary resources.
- Rule creation is recorded before the network call. Lost responses are reconciled by exact run-owned name and description before adoption.
- Alert injection uses `POST /api/v1/alerts`. Its required Alertmanager transport fingerprint is never used as OSCAR evidence; the server-assigned fingerprint read back from history is authoritative.
- Target doctor and every run automatically validate a rule and prove reserved-label survival before creating rules.
- Unknown pipeline mode, Phase A side-effect gating, missing labels, ambiguous history, incomplete negative windows, or unavailable evidence cannot produce `PASS`.
- Behavioral verdict and cleanup status are independent. Cleanup retry reads back exact ownership before deleting anything.
- Only one mutation run executes at a time.

Pipeline mode is currently operator-declared because OSCAR does not expose it publicly. Use `phase_b_dispatch` only after verifying correlator publication and dispatch are enabled on the target.

## History, custom scenarios, and evidence

```bash
oscar-corrtest scenario list
oscar-corrtest scenario validate ./scenario.yaml
oscar-corrtest scenario import ./scenario.yaml
oscar-corrtest runs list --pattern flood --verdict PASS --output json
oscar-corrtest runs show <run-id> --output json
oscar-corrtest cleanup retry <run-id>
oscar-corrtest export <run-id> --output ./run-evidence.zip
oscar-corrtest verify-bundle ./run-evidence.zip
oscar-corrtest backup --output ./corrtest-backup.db
oscar-corrtest retention preview --before 2026-08-01T00:00:00Z
oscar-corrtest retention apply --before 2026-08-01T00:00:00Z --yes
```

Custom YAML/JSON is strict and bounded: unknown/duplicate keys, aliases, multiple documents, reserved-label overrides, unsafe durations, and oversized inputs are rejected. The release archive includes the JSON Schema at `docs/schema/correlation-scenario.schema.json`.

Evidence ZIPs are atomic and non-overwriting. They contain the canonical JSON report, immutable plan, timeline, offline HTML, JUnit XML, and a SHA-256 manifest. Run deletion requires an exact ID, `--yes`, a terminal clean/not-required cleanup state, and verified local artifacts. Retention uses the same gate, provides a separate preview, and processes no more than 500 exact candidates per invocation.

SQLite uses WAL, foreign keys, a busy timeout, and full synchronous writes. Keep the data directory on a local filesystem. The online database backup does not include separate run artifact directories; preserve the whole state directory or export the required runs.

## Serving securely

Default serving accepts only literal loopback IPs. Direct non-loopback bearer mode requires TLS and an environment/file/systemd credential reference. Trusted-proxy mode requires explicit proxy CIDRs plus an exact identity header/value and rejects direct or spoofed requests.

```bash
oscar-corrtest serve --listen 0.0.0.0:8787 \
  --remote-mode bearer --auth-token-file /run/credentials/corrtest-ui-token \
  --tls-cert /etc/oscar-corrtest/tls.crt --tls-key /etc/oscar-corrtest/tls.key
```

## Build, test, and package

The Makefile is the only build interface used by developers, GitHub Actions, and GitLab CI.

```bash
make test
make test-race
make plan7-gate
make ci
make release-gate
# Explicit opt-in only; contacts a disposable OSCAR target:
make live-qualification
```

`make package checksums` writes deterministic Linux AMD64 and ARM64 archives to `dist/`. Archives include the executable, operator docs, scenario schema, systemd unit, and scratch-based `Containerfile`. Both CI systems use immutable action/image pins and publish packaged archives rather than raw binaries.

Configuration precedence is flags, `OSCAR_CORRTEST_*` environment variables, a versioned JSON file, then XDG defaults:

```json
{
  "apiVersion": "corrtest.oscar/v1alpha1",
  "dataDir": "/var/lib/oscar-corrtest",
  "listenAddress": "127.0.0.1:8787"
}
```

Targets persist credential references only. Secret values are resolved in memory for a request and are excluded from SQLite, reports, HTML, SSE, and evidence bundles.
