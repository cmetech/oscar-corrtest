# OSCAR Correlation Test Harness

`oscar-corrtest` is a standalone Go application for proving OSCAR alarm-correlation behavior with reproducible evidence. The current durable-foundation release provides configuration, secret-safe target metadata, SQLite-backed run history, hashed artifact storage, canonical report JSON, online database backup, CLI history commands, and an embedded light/dark web UI. OSCAR rule orchestration and simulated alert injection begin in Plan 3.

## Quick start

Go 1.27.0 or newer is required.

```bash
make build
./bin/oscar-corrtest version
./bin/oscar-corrtest target add --name lab-a --url https://oscar.example --credential-env OSCAR_API_TOKEN
./bin/oscar-corrtest serve
```

Open <http://127.0.0.1:8787>. The foundation accepts literal loopback listeners only. Remote serving will not be enabled until it has authentication or is explicitly deployed behind an authenticated reverse proxy.

## Build and package

The application has no frontend build and no runtime dependency on Python, Node, Docker, CGO, OSCAR source, or an external database. SQLite, templates, CSS, and JavaScript are embedded in the executable.

```bash
make test
make plan2-gate
make ci-core
make standalone-check
make package checksums
```

Linux AMD64 and ARM64 archives are written to `dist/`. Packaging requires GNU tar; macOS developers can install it as `gtar` with Homebrew. A clean checkout builds with `GOWORK=off` and `CGO_ENABLED=0`.

## Durable configuration and history

Configuration precedence is command flags, `OSCAR_CORRTEST_*` environment variables, a versioned JSON file, then defaults. Interactive defaults follow XDG config/state paths. The systemd unit explicitly uses `/etc/oscar-corrtest/config.json` and `/var/lib/oscar-corrtest`.

```json
{
  "apiVersion": "corrtest.oscar/v1alpha1",
  "dataDir": "/var/lib/oscar-corrtest",
  "listenAddress": "127.0.0.1:8787"
}
```

Targets store only an `env`, `file`, or `systemd` credential reference. Credential values are resolved only by future OSCAR requests and are never stored in SQLite, logs, reports, HTML, or artifacts. TLS verification is the default; insecure mode is explicit per target.

Useful durable commands:

```bash
oscar-corrtest target list --output json
oscar-corrtest runs list --verdict FAIL --output json
oscar-corrtest runs show <run-id> --output json
oscar-corrtest backup --output ./corrtest-backup.db
```

The SQLite database must remain on a local filesystem, not NFS or another network filesystem. It runs in WAL mode with full synchronous writes, foreign keys, and startup migration/integrity checks. A failed check leaves `/healthz` alive and `/readyz` unavailable for diagnostic inspection.

The backup command creates a coordinated SQLite snapshot and refuses overwrite. It does not include `runs/` evidence files; preserve those directories separately until portable per-run evidence bundles arrive in Plan 3.

## Delivery scope

The next delivery slices add:

- safe OSCAR rule lifecycle, simulated alert injection, and flood-pattern evidence;
- window, ordering, timer, parent-child, and notifier correlation scenarios;
- imported custom scenarios and authenticated operational deployment.

The approved architecture and naming contracts are maintained under `docs/superpowers/`.

## Linux service example

`packaging/oscar-corrtest.service` is a hardened systemd example that keeps the listener on `127.0.0.1`. Provision the `oscar-corrtest` system user and group before installing the unit; systemd creates the restricted configuration and state directories.

Plan 1 refuses wildcard and non-loopback listeners. A future remote mode must add authentication or be explicitly deployed behind an authenticated reverse proxy.
