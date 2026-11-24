# OSCAR Test Console

**OSCAR Test Console** (`oscar-corrtest`) is a standalone Go application for correlation scenarios, ordinary alerts, webhook and SNMP receiver checks, and load testing. It supports all eight OSCAR alarm-correlation patterns. It creates isolated temporary rules through OSCAR's public API, sends deterministic alerts, resolves authoritative fingerprints from alert history, collects audit/notifier evidence, cleans up owned rules, and preserves run reports in SQLite. The same executable provides a CLI and an embedded light/dark web UI.

## Quick start

Install the latest Linux or macOS release into `$HOME/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.sh | sh
```

Then start the UI explicitly—no certificate, UI token, SSH tunnel, or OSCAR
API key is required to start it:

```bash
oscar-corrtest serve
```

Open `http://<server-ip>:8787`. The default listener is directly reachable and
unauthenticated; use it only where network access is appropriate for a test
tool that can create temporary rules and inject alerts. The installer never
starts a background service.

Explore the CLI from the binary itself. Global, command-group, and individual
action help are available without opening the database or contacting OSCAR:

```bash
oscar-corrtest --help
oscar-corrtest service --help
oscar-corrtest service start --help
oscar-corrtest help run
```

For persistent user-scoped operation, explicitly install and start it:

```bash
oscar-corrtest service install
oscar-corrtest service start
oscar-corrtest service status
```

The install step enables future login startup but does not start the process
immediately. See [service management](docs/service-management.md).

Configure an OSCAR target when you are ready to run tests:

For a separately installed correlation test pack, use the dedicated
[installed test-pack workflow](docs/installed-test-pack.md). The generic built-in
commands below create their own test rules.

```bash
export OSCAR_API_KEY='...'
oscar-corrtest target add \
  --name lab-a --url https://oscar.example/ext/mw \
  --credential-env OSCAR_API_KEY --output json

oscar-corrtest doctor \
  --target <target-id> --pipeline-mode phase_b_dispatch

oscar-corrtest plan builtin:flood \
  --target <target-id> --pipeline-mode phase_b_dispatch

oscar-corrtest run builtin:flood \
  --target <target-id> --pipeline-mode phase_b_dispatch
```

The easiest persistent configuration is the Operations page: paste the global
key once and OSCAR Test Console writes `OSCAR_API_KEY` to its managed user `.env`. Linux
and macOS use `$HOME/.config/oscar-corrtest/.env` unless
`XDG_CONFIG_HOME` is set; Windows uses
`%LOCALAPPDATA%\oscar-corrtest\.env`. The UI never reads the value back.
Advanced targets may still use explicit environment/file/systemd references,
which override the global key.
Runs started from the browser continue if the browser disconnects; reconnecting replays persisted events.
The **Run test** page also offers manual webhook receiver and SNMP trap receiver checks, plus a paced SNMP mixed-load mode with migrated problem/repeat/clear fixtures; see [receiver checks](docs/receiver-checks.md) for their required mapping, credentials, and evidence boundaries.
Open `/authoring` for the target-free Authoring guide: 16 basic/advanced
examples across all eight patterns, plus exact YAML, compiled-contract, OSCAR
API, and lifecycle previews. Opening an example in Scenarios creates an
unsaved draft; **Save custom scenario** explicitly persists a validated custom
version. See the full [scenario authoring guide](docs/scenario-authoring.md).
Active run pages provide cancellation with bounded cleanup; cleanup-safe
terminal runs can be deleted only after local artifact verification.

Windows amd64 and version-pinned installation are also supported. See the
[installation guide](docs/install.md) for Windows PowerShell, upgrades,
configuration, backup, and uninstall.

## Native alert qualification campaigns

| Selector | Workload |
|---|---|
| `alerting-day-control@v1` | 150 minutes of identical ordinary load on pre-change and post-change builds, then up to 30 minutes of drain observation. Includes direct no-policy and canonical no-policy paths, with first identity enrollment measured separately from recurring delivery. |
| `alerting-day@v3` | 150 minutes of baseline traffic, standing repeats, correlation and tickets, with ordinary resend coverage and additional managed TL1 and configured-policy cohorts. Drain observation follows load. |
| `alerting-day-smoke@v3` | Short mixed-workload smoke. |
| `managed-alerts-smoke@v1` | Dedicated managed TL1 functional smoke for lifecycle, resend, escalation, acknowledgement and ticket flows. |
| `alerting-day@v4` | The single-node mixed-load gate: 150 minutes with every lane at once (correlation cycling with ticketed parents, tickets, fingerprint policy rounds, R1-R4 resend plus an escalation cohort, front office, a homelab task run every 5 minutes, alert reports at T+60 and T+120). The total offered firing rate over all lanes is 5/s, 10/s in 5-minute spikes at T+45 and T+105; resolves come on top. 30-minute drain. Needs `RESEND_SCHEDULE_ENABLED=true`, commissioned fingerprint policies, an approved `core-alarm-history-dump` report and front-office user fo-01 allowed to run reports and the homelab task. |
| `alerting-day-smoke@v4` | The v4 lanes in about 15 minutes, non-comparable. |

Both smoke selectors are non-comparable. The mixed V3 selectors accept
`--managed-share 0.10` (10% of baseline plus mixed-rate PDUs during load).
The dedicated smoke uses the controlled probe, signed operator session and
independent provider receipts. Full performance runs also use the commissioned
per-run observer capture. See [managed qualification](docs/managed-native-campaign.md)
and [controlled fixtures](test-packs/native-alert-actions-v1/README.md).

A full qualification run records an operator-declared OSCAR build manifest ID,
CorrTest binary ID and frozen workload configuration digest at start. The
manifest must identify the actual deployed builds, including the native provider.
Keep the same CorrTest binary and exact workload configuration across reference
and candidate runs. Save the manifest and configuration with the exported run
bundles. Include ingress and routes, labels and expected outcomes, ordinary and
configured policies, traffic rates and the managed traffic share where used.
The declaration identifies those artifacts. The comparison checks stored
metadata and does not verify deployments or discover hashes from OSCAR.

- `control_before_after` compares a `pre_change_control` reference on the
  pre-change build with a later `post_change_control` candidate on the changed
  build. Both use `alerting-day-control@v1`. Keep timed-policy semantic changes
  in the configured campaign.
- `configured_policy` compares explicitly recorded
  `configured_policy_reference` runs with later `configured_policy_candidate`
  runs on the same `alerting-day@v3` workload. Track added configured sends and
  managed source cost separately from the ordinary before/after control.

Declare full-run provenance with
`--comparison-role`, `--target-build`, `--corrtest-build` and
`--workload-config`. The API accepts these as `provenance.role`,
`provenance.targetBuild`, `provenance.corrTestBuild` and
`provenance.workloadConfig` on `POST /api/v1/campaign/runs`. Campaign status
returns the start declaration in `options.provenance` and the frozen hash in
`campaign.ScenarioHash`.

```bash
oscar-corrtest campaign compare "$CANDIDATE_ID" \
  --reference-run "$REFERENCE_ID" --mode control_before_after \
  --scenario-hash "$SCENARIO_HASH" --server "$CORRTEST_SERVER" \
  --token-file "$CORRTEST_TOKEN_FILE" --output json
```

Use one to three explicit references. The comparison page accepts the same
inputs. `GET /api/v1/campaign/compare` takes comma-separated `ids` and
`referenceIds` plus `mode` and `scenarioHash`. All selected runs must have
complete, clean, comparable evidence. References must also pass. A missing
reference is reported as a missing baseline. The result shows run IDs, build
provenance, the reference median and its minimum and maximum per headline. A
single reference is provisional.

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

One OSCAR Test Console runtime owns each data directory. While `serve` or a CLI run is using it, another stateful CLI command (including `runs show`, `runs list`, backup or export) refuses with “state directory already in use.” Use the running web UI for status, or wait for the owner to exit before using the CLI. Independent data directories remain separate. The persistent `.corrtest.lock` file is normal; never delete it to force access. The OS releases ownership after process exit or a crash, and the next owner performs existing interrupted-run recovery without resuming injection. Stop older OSCAR Test Console binaries before upgrading: they do not honor this lock.

SQLite uses WAL, foreign keys, a busy timeout, and full synchronous writes. Keep the data directory on a local filesystem. The online database backup does not include separate run artifact directories; preserve the whole state directory or export the required runs.

## Serving securely

Default serving uses unauthenticated HTTP on `0.0.0.0:8787`. Startup prints a
warning because any reachable peer can use the harness's configured mutation
authority. Bind `127.0.0.1:8787` for local-only use. Optional direct bearer
mode requires TLS and a separate UI credential reference; trusted-proxy mode
requires explicit proxy CIDRs plus an exact identity header/value.

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

`make package checksums` writes deterministic Linux amd64/arm64, macOS
amd64/arm64, and Windows amd64 archives plus `SHA256SUMS` to `dist/`. Archives
include the executable, installation/operator docs, scenario schema, systemd
unit, and scratch-based `Containerfile`. Both CI systems use immutable
action/image pins and publish packaged archives rather than raw binaries.

Configuration precedence is flags, `OSCAR_CORRTEST_*` environment variables, a versioned JSON file, then XDG defaults:

```json
{
  "apiVersion": "corrtest.oscar/v1alpha1",
  "dataDir": "/var/lib/oscar-corrtest",
  "listenAddress": "0.0.0.0:8787"
}
```

Targets persist credential references only. Secret values are resolved in memory for a request and are excluded from SQLite, reports, HTML, SSE, and evidence bundles.

Go 1.27.4 or newer is required only to build from source; released binaries
are CGO-free and need no Go, Python, Node, external database, or OSCAR source
checkout.

## Creating a release

Pushing an annotated `vMAJOR.MINOR.PATCH` tag starts the GitHub Actions release
pipeline; no local build or packaging script is required:

```bash
git tag -a v1.2.3 -m "OSCAR Test Console v1.2.3"
git push origin refs/tags/v1.2.3
```

The pipeline runs the complete release gate, cross-compiles Linux, macOS, and
Windows assets, tests the Windows installer, verifies checksums, and publishes
the GitHub Release. The guarded `scripts/release.sh v1.2.3` command remains an
optional local preflight. See [the development guide](docs/development.md#creating-a-github-release)
for the complete operator procedure.

For the current selective-admission work, see [reliability E2E coverage](docs/reliability-e2e.md): named child recovery/refire stimuli, explicit downstream injection limits, and the backend contracts still required for full E2E scenarios.
