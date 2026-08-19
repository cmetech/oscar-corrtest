# OSCAR Correlation Test Harness

`oscar-corrtest` is a standalone Go application for proving OSCAR alarm-correlation behavior with reproducible evidence. This foundation release provides the executable, embedded web shell, health endpoints, packaging, and CI boundary. OSCAR connectivity, SQLite-backed run history, rule orchestration, simulated alerts, and correlation reports arrive in subsequent delivery plans.

## Quick start

Go 1.27.0 or newer is required.

```bash
make build
./bin/oscar-corrtest version
./bin/oscar-corrtest serve
```

Open <http://127.0.0.1:8787>. The foundation accepts literal loopback listeners only. Remote serving will not be enabled until it has authentication or is explicitly deployed behind an authenticated reverse proxy.

## Build and package

The application has no frontend build and no runtime dependency on Python, Node, Docker, CGO, OSCAR source, or an external database. Templates, CSS, and JavaScript are embedded in the executable.

```bash
make test
make ci-core
make package checksums
```

Linux AMD64 and ARM64 archives are written to `dist/`. Packaging requires GNU tar; macOS developers can install it as `gtar` with Homebrew. A clean checkout builds with `GOWORK=off` and `CGO_ENABLED=0`.

## Delivery scope

The next delivery slices add:

- target configuration, SQLite migrations, durable run/artifact history, and report export;
- safe OSCAR rule lifecycle, simulated alert injection, and flood-pattern evidence;
- window, ordering, timer, parent-child, and notifier correlation scenarios;
- imported custom scenarios and authenticated operational deployment.

The approved architecture and naming contracts are maintained under `docs/superpowers/`.
