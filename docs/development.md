# Development

## Requirements

- Go 1.27.0 or newer
- GNU Make
- Git
- GNU tar for release packaging (`tar` on Linux, commonly `gtar` from Homebrew on macOS)
- `sha256sum` or `shasum`

The canonical module path is `github.com/cmetech/oscar-corrtest`. GitHub is the canonical source location; the same repository can be mirrored to GitLab without changing module imports or build commands.

No Python, Node, Docker, frontend toolchain, OSCAR source checkout, or CGO is required for a normal build. Race tests intentionally enable CGO because Go's race detector requires it.

## Make targets

| Target | Purpose |
|---|---|
| `tools` | Install pinned `gosec` and `govulncheck` binaries into `.tools/` |
| `fmt-check` | Fail when committed Go files are not formatted |
| `mod-check` | Verify modules, tidy them, and fail on module-file drift |
| `vet` | Run `go vet` over every package |
| `security` | Run the pinned static and vulnerability scanners |
| `test` | Run the complete test suite without cached results |
| `test-race` | Run the complete suite with the race detector |
| `plan2-gate` | Run focused configuration, migration/WAL/recovery, artifact/report, backup, runtime, and UI tests |
| `plan3-gate` … `plan7-gate` | Run focused qualification for each correlation delivery plan |
| `build` | Build the host binary in `bin/` with linker metadata |
| `cross` | Build CGO-free Linux AMD64 and ARM64 executables |
| `package` | Produce deterministic Linux archives using GNU tar and `gzip -n` |
| `checksums` | Write a deterministically ordered `dist/SHA256SUMS` |
| `archive-mod-check` | Verify and check module tidiness without requiring Git metadata |
| `standalone-check` | Build and test from `git archive` with isolated caches and no parent source dependency |
| `ci-core` | Run formatting, module, vet, security, test, race, and host-build gates |
| `ci` | Install tools, run core and standalone gates, package, and checksum sequentially |
| `release-gate` | Run CI plus every plan gate, archive-content checks, and package reproducibility |
| `live-qualification` | Explicitly opt in to all-eight-pattern testing against a disposable Phase-B OSCAR target; never runs from offline CI |
| `clean` | Remove generated files beneath `bin/` and `dist/` |

Both GitHub Actions and GitLab CI/CD call these Make targets rather than duplicating Go commands in YAML.

## Reproducible builds

Build metadata is derived from the current Git commit. `SOURCE_DATE_EPOCH` uses the commit timestamp. Release archives stage the executable, README, operator/built-in/schema documentation, systemd unit, and Containerfile beneath an `oscar-corrtest/` directory, normalize member order, ownership, and timestamps with GNU tar, and remove gzip timestamps with `gzip -n`.

```bash
make package checksums
```

Running the command again at the same commit must produce identical archive checksums.

## Local server

```bash
make build
./bin/oscar-corrtest serve --listen 127.0.0.1:8787 --data-dir /tmp/oscar-corrtest-state
```

Literal IPv4 or IPv6 loopback is the no-auth default. Non-loopback serving requires either TLS-protected bearer/session authentication or an explicit trusted-proxy policy with restricted peer CIDRs and an exact identity header/value. See `docs/operator.md`.

## SQLite and evidence development

The pinned `modernc.org/sqlite` driver is CGO-free and embeds a SQLite release newer than the required 3.51.3 floor. Its coupled `modernc.org/libc` version must remain exactly aligned with the driver's `go.mod`. Both CI systems cache from `go.sum`.

Runtime state has this layout:

```text
data-dir/
  corrtest.db
  corrtest.db-wal
  corrtest.db-shm
  runs/<run-id>/...
```

SQLite uses WAL, `foreign_keys=ON`, `busy_timeout=5000`, and `synchronous=FULL`. Keep it on a local filesystem. Migrations are embedded, ordered, transactionally applied, and checksum-verified. Do not edit an applied migration; add the next numbered file.

Artifact paths are application-generated, relative, traversal-checked, atomically published, and SHA-256 verified. Missing, pending, or changed files remain visible as integrity warnings. Portable exports contain canonical JSON, the immutable plan, events, offline HTML, JUnit, and a SHA-256 manifest.

At process startup, any active run is marked `INTERRUPTED` and receives one recovery event. Alert injection is never silently resumed. Cleanup retry reads a rule back by its returned ID and verifies the exact run-owned name/description before deletion.

The supported online backup command coordinates with WAL and refuses overwrite:

```bash
./bin/oscar-corrtest backup --data-dir /tmp/oscar-corrtest-state --output /tmp/corrtest-backup.db
```

The database backup excludes evidence directories. Back up the complete state directory or export the terminal runs that must remain portable.

## Service and release gates

The example `packaging/oscar-corrtest.service` requires operators to provision an `oscar-corrtest` system user and group. Its loopback listener is deliberate. `StateDirectory=` and `ConfigurationDirectory=` provide the only writable service locations while `ProtectSystem=strict` remains enabled.

Before creating the first real semantic tag on either remote, confirm GitHub release permissions and branch protection, then confirm the GitLab `CI_JOB_TOKEN` can upload package-registry assets and create release links. Local workflow validation cannot prove those remote settings.

Both publish-capable workflows run `make clean release-gate`; GitLab packaging
consumes the verified job's archives instead of rebuilding through a weaker
lane. The live qualification target is intentionally absent from both workflow
graphs.
