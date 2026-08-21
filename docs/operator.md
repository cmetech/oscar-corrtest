# Operator guide

The harness is a single CGO-free executable with embedded UI assets and a local SQLite database. Keep the state directory on a local filesystem. Back up both `corrtest.db` and any `runs/` evidence directories, or export individual terminal runs as portable ZIP bundles.

## Target and preflight

Store only a credential reference:

```bash
export OSCAR_API_KEY='...'
oscar-corrtest target add --name lab-a --url https://oscar.example/ext/mw --credential-env OSCAR_API_KEY
oscar-corrtest doctor --target <target-id> --pipeline-mode phase_b_dispatch
```

Until OSCAR exposes pipeline mode publicly, `publication_disabled`, `phase_a_audit_only`, and `phase_b_dispatch` are operator declarations captured in each run. Only Phase B can prove synthetic-parent and notifier outcomes. Doctor validates a real rule payload, injects one diagnostic alert, resolves its OSCAR fingerprint from history, and verifies every reserved label before any temporary rule is created.

The `public-v1` profile targets OSCAR's external middleware root (normally
`/ext/mw`) and sends the referenced credential using OSCAR's `X-API-Key`
contract. Pointing the harness at an internal service URL bypasses the external
authentication/RBAC surface and is not a valid production qualification.

## Run and evidence

```bash
oscar-corrtest scenario list
oscar-corrtest plan builtin:flood --target <target-id> --pipeline-mode phase_b_dispatch
oscar-corrtest run builtin:flood --target <target-id> --pipeline-mode phase_b_dispatch
oscar-corrtest runs list --pattern flood
oscar-corrtest export <run-id> --output ./evidence.zip
oscar-corrtest verify-bundle ./evidence.zip
```

`PASS` and cleanup status are independent. A cleanup-dirty run exits with code 4 and remains available for `oscar-corrtest cleanup retry <run-id>`. Retry reads the exact returned rule ID and verifies full run ownership before deletion. Manual deletion requires an exact terminal run ID, verified artifacts, clean/not-required cleanup, and `--yes`.

For a release qualification against a disposable Phase-B OSCAR deployment,
use the separately gated procedure in [live-qualification.md](live-qualification.md).

Browser runs can be cancelled from their run-detail page. The process detaches a bounded cleanup context from the cancelled operation, persists terminal cleanup evidence, and waits for active cleanup before closing SQLite during shutdown.

Preview retention before applying it:

```bash
oscar-corrtest retention preview --before 2026-08-01T00:00:00Z
oscar-corrtest retention apply --before 2026-08-01T00:00:00Z --yes
```

Only terminal `CLEAN` or `NOT_REQUIRED` runs qualify. Each artifact is hash-verified again immediately before deletion; dirty, unknown, active, pending-artifact, missing, or changed runs remain preserved. Each invocation is capped at 500 candidates.

The Scenarios UI accepts bounded strict YAML/JSON source. Preview compiles through the same compiler without contacting OSCAR; import persists the original validated source by its SHA-256 digest.

Use the catalog to preview any built-in, compare P01 with N01, and select
**Clone as custom** before editing. The compiled pane exposes alertname,
`category=corrtest_<pattern>`, `oscar_test_run_id`, rule criteria, assertion
values, and manual OSCAR filters. See [scenario-authoring.md](scenario-authoring.md).

## Scenario authoring and live lifecycle

Use `/authoring` for the target-free Authoring guide. Its Quickstart, Schema,
Patterns, Assertions, and Validation sections provide 16 basic/advanced
examples, each with YAML, compiled-contract, OSCAR API JSON, and lifecycle
views. Opening an example in Scenarios creates an unsaved editable draft;
**Save custom scenario** is the explicit persistence action.

The preview does not contact OSCAR or prove live target compatibility. A live
run first validates a real rule payload and reserved-label survival, then
creates exactly two temporary correlation rules for P01 and N01. It does not
create ordinary OSCAR alert rules. Source stimuli use `POST /api/v1/alerts`;
CorrTest resolves server identities from authoritative history, records
evidence, resolves its injected alerts, and deletes only returned rule IDs.
Phase A is audit-only. Use Phase B when a synthetic parent or notifier outcome
is part of the required evidence. The full authoring workflow, field reference,
and generated schema drift command are in [scenario-authoring.md](scenario-authoring.md).

## Operations and application logs

The Operations page manages the write-only global API key, shows the effective
configuration/state/log paths, exposes current-user service state, and follows
redacted structured records. The primary file is `application.jsonl`; rotation
keeps five 10 MiB backups. Downloads are limited to the displayed application
and bootstrap sources. Operational logs help diagnose the harness but are not
correlation verdict evidence.

Linux/macOS defaults include `$HOME/.config/oscar-corrtest/.env`; Windows uses
`%LOCALAPPDATA%\oscar-corrtest\.env`. Use `oscar-corrtest service install`,
`service start`, `service stop`, `service restart`, `service status`,
`service logs`, and `service uninstall` for the equivalent lifecycle CLI.

## Serving modes

The default starts an unauthenticated HTTP listener on every interface:

```bash
oscar-corrtest serve
```

Open `http://<server-ip>:8787`. No OSCAR API key, UI token, certificate, proxy,
or tunnel is needed to start or browse the UI. Because any reachable peer can
view evidence and initiate mutations through configured targets, constrain
port 8787 with the lab network/firewall boundary.

Loopback mode remains available for local-only access and applies the strict
loopback Host allowlist:

```bash
oscar-corrtest serve --listen 127.0.0.1:8787
```

Direct remote bearer mode requires TLS and a reference-only credential:

```bash
oscar-corrtest serve --listen 0.0.0.0:8787 --remote-mode bearer \
  --auth-token-file /run/credentials/corrtest-ui-token \
  --tls-cert /etc/oscar-corrtest/tls.crt --tls-key /etc/oscar-corrtest/tls.key
```

Behind an authenticated reverse proxy, restrict accepted peers and require the exact identity header/value the proxy overwrites:

```bash
oscar-corrtest serve --listen 0.0.0.0:8787 --remote-mode trusted-proxy \
  --proxy-header X-Forwarded-User --proxy-value corrtest-operators \
  --trusted-proxy 10.20.0.0/16
```

Never expose trusted-proxy mode directly to untrusted networks. Firewall the listener so only the declared proxy networks can connect.

The scratch container defaults to `help`, so it never starts a network service
implicitly. Its `/var/lib/oscar-corrtest` directory is owned by UID/GID 65532.
For the directly reachable default:

```bash
docker run --rm -p 8787:8787 -v corrtest-data:/var/lib/oscar-corrtest \
  oscar-corrtest serve --data-dir /var/lib/oscar-corrtest
```

The same firewall warning applies. Add bearer/TLS or restricted trusted-proxy
mode when the UI needs its own authentication boundary. Do not publish a
loopback-bound container port and assume it is reachable through Docker's port
forwarding.

## systemd credentials

The included example unit listens on `0.0.0.0:8787`, matching the application
default. It is not installed by either user-scoped installer. For optional
bearer mode, provision a credential without placing its value in the unit or
environment:

```bash
systemd-creds encrypt --name=corrtest-ui-token - /etc/credstore.encrypted/corrtest-ui-token
```

Add `LoadCredentialEncrypted=corrtest-ui-token` and `--auth-token-systemd corrtest-ui-token` to a local unit override, together with TLS certificate/key paths. OSCAR target credentials use the same `systemd` reference model.
