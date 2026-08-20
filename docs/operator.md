# Operator guide

The harness is a single CGO-free executable with embedded UI assets and a local SQLite database. Keep the state directory on a local filesystem. Back up both `corrtest.db` and any `runs/` evidence directories, or export individual terminal runs as portable ZIP bundles.

## Target and preflight

Store only a credential reference:

```bash
oscar-corrtest target add --name lab-a --url https://oscar.example --credential-env OSCAR_API_TOKEN
oscar-corrtest doctor --target <target-id> --pipeline-mode phase_b_dispatch
```

Until OSCAR exposes pipeline mode publicly, `publication_disabled`, `phase_a_audit_only`, and `phase_b_dispatch` are operator declarations captured in each run. Only Phase B can prove synthetic-parent and notifier outcomes. Doctor validates a real rule payload, injects one diagnostic alert, resolves its OSCAR fingerprint from history, and verifies every reserved label before any temporary rule is created.

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

Browser runs can be cancelled from their run-detail page. The process detaches a bounded cleanup context from the cancelled operation, persists terminal cleanup evidence, and waits for active cleanup before closing SQLite during shutdown.

Preview retention before applying it:

```bash
oscar-corrtest retention preview --before 2026-08-01T00:00:00Z
oscar-corrtest retention apply --before 2026-08-01T00:00:00Z --yes
```

Only terminal `CLEAN` or `NOT_REQUIRED` runs qualify. Each artifact is hash-verified again immediately before deletion; dirty, unknown, active, pending-artifact, missing, or changed runs remain preserved. Each invocation is capped at 500 candidates.

The Scenarios UI accepts bounded strict YAML/JSON source. Preview compiles through the same compiler without contacting OSCAR; import persists the original validated source by its SHA-256 digest.

## Serving modes

Loopback mode needs no UI authentication:

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

## systemd credentials

The included unit runs loopback-only by default. For bearer mode, provision a credential without placing its value in the unit or environment:

```bash
systemd-creds encrypt --name=corrtest-ui-token - /etc/credstore.encrypted/corrtest-ui-token
```

Add `LoadCredentialEncrypted=corrtest-ui-token` and `--auth-token-systemd corrtest-ui-token` to a local unit override, together with TLS certificate/key paths. OSCAR target credentials use the same `systemd` reference model.
