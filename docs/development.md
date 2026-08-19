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
| `build` | Build the host binary in `bin/` with linker metadata |
| `cross` | Build CGO-free Linux AMD64 and ARM64 executables |
| `package` | Produce deterministic Linux archives using GNU tar and `gzip -n` |
| `checksums` | Write a deterministically ordered `dist/SHA256SUMS` |
| `archive-mod-check` | Verify and check module tidiness without requiring Git metadata |
| `standalone-check` | Build and test from `git archive` with isolated caches and no parent source dependency |
| `ci-core` | Run formatting, module, vet, security, test, race, and host-build gates |
| `ci` | Install tools, run core and standalone gates, package, and checksum sequentially |
| `clean` | Remove generated files beneath `bin/` and `dist/` |

Both GitHub Actions and GitLab CI/CD call these Make targets rather than duplicating Go commands in YAML.

## Reproducible builds

Build metadata is derived from the current Git commit. `SOURCE_DATE_EPOCH` uses the commit timestamp. Release archives stage only the executable and README beneath an `oscar-corrtest/` directory, normalize member order, ownership, and timestamps with GNU tar, and remove gzip timestamps with `gzip -n`.

```bash
make package checksums
```

Running the command again at the same commit must produce identical archive checksums.

## Local server

```bash
make build
./bin/oscar-corrtest serve --listen 127.0.0.1:8787
```

Only literal IPv4 or IPv6 loopback addresses are accepted in the foundation. Hostnames, wildcard listeners, unspecified addresses, empty hosts, and non-loopback addresses are rejected because authenticated remote serving is not implemented yet.

## Service and release gates

The example `packaging/oscar-corrtest.service` requires operators to provision an `oscar-corrtest` system user and group. Its loopback listener is deliberate. The SQLite plan will add and document the writable state directory; do not weaken `ProtectSystem=strict` to invent one in this foundation.

Before creating the first real semantic tag on either remote, confirm GitHub release permissions and branch protection, then confirm the GitLab `CI_JOB_TOKEN` can upload package-registry assets and create release links. Local workflow validation cannot prove those remote settings.
