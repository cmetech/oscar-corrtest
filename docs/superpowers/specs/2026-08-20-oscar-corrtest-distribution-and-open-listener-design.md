# OSCAR Corrtest Distribution and Open Listener Design

**Date:** 2026-08-20  
**Status:** Approved in conversation; implementation contract  
**Supersedes:** The loopback-only default and unauthenticated non-loopback prohibition in `2026-08-19-oscar-correlation-test-harness-design.md`

## 1. Goal

Make `oscar-corrtest` simple to publish and install as a user-scoped,
standalone application:

- one guarded command prepares and initiates a GitHub release;
- anonymous one-line installers download the latest release on Linux, macOS,
  and Windows;
- release binaries are cross-compiled and checksum-protected;
- installation never starts a hidden background process;
- `oscar-corrtest serve` listens on all interfaces by default so the UI is
  directly reachable from another machine;
- the OSCAR external API key is the only credential needed to execute tests.

The application remains a test harness, not a hardened multi-tenant service.
Optional bearer/TLS and trusted-proxy modes remain available for operators who
want to protect the UI.

## 2. Approved product decisions

| Decision | Contract |
|---|---|
| Installation scope | User-scoped by default; no `sudo`, systemd, launchd, or Windows service installation. |
| Post-install behavior | Install only. Print exact configuration and startup commands; never auto-start. |
| Default listener | `0.0.0.0:8787`, HTTP, unauthenticated. |
| Required runtime credential | Only an OSCAR external API key reference is required to run doctor/tests. No credential is required to start or view the UI. |
| Optional security | Existing bearer mode still requires TLS; trusted-proxy mode still requires an exact identity header and trusted CIDRs. Literal loopback remains selectable. |
| Distribution source | Public GitHub Releases in `cmetech/oscar-corrtest`. |
| Release trigger | An exact semantic tag `vX.Y.Z` pushed by the guarded release script; the existing GitHub workflow publishes assets. |
| Installer behavior | Resolve latest or pinned version, select the exact platform asset, verify SHA-256, then atomically replace only the executable. |
| Persistent state | Installers never create, move, overwrite, or delete configuration, SQLite, evidence, or target credentials. |

Anonymous `curl`/PowerShell installation requires both the repository and its
release assets to be public. The local repository currently has no Git remote;
publishing is operationally blocked until `origin` points to
`https://github.com/cmetech/oscar-corrtest` (or its SSH equivalent).

## 3. Approaches considered

### 3.1 GitHub Release assets with checksum-verifying installers — selected

The installers resolve a release tag from GitHub, download an immutable asset
and `SHA256SUMS`, verify it, and install the executable. This keeps Go and the
source repository off destination machines, supports version pinning, and
reuses the existing tag-triggered workflow.

### 3.2 `go install` from source — rejected

This would require Go 1.27 and a compiler toolchain on every destination,
couple installation to module availability, and provide a worse operator
experience than the requested standalone executable.

### 3.3 Download a branch-head binary — rejected

Branch artifacts are mutable, lack a durable version identity, and weaken the
checksum/reproducibility proof already enforced by the release gate.

## 4. Release asset contract

The initial release set is:

| OS | Architecture | Required asset |
|---|---|---|
| Linux | amd64 | `oscar-corrtest_<version>_linux_amd64.tar.gz` |
| Linux | arm64 | `oscar-corrtest_<version>_linux_arm64.tar.gz` |
| macOS | amd64 | `oscar-corrtest_<version>_darwin_amd64.tar.gz` |
| macOS | arm64 | `oscar-corrtest_<version>_darwin_arm64.tar.gz` |
| Windows | amd64 | `oscar-corrtest_<version>_windows_amd64.zip` |
| All | All | `SHA256SUMS` |

Windows arm64 is not a release requirement. It may be added only after its
installer path has the same checksum and smoke-test coverage as Windows amd64.

All binaries use `CGO_ENABLED=0`, `-trimpath`, disabled VCS auto-stamping, and
the existing explicit version/commit/build-date linker metadata. The Go code,
embedded UI, and pure-Go SQLite dependency permit cross-compilation from the
Linux CI runner.

Each archive contains a single `oscar-corrtest/` root with:

- `bin/oscar-corrtest` or `bin/oscar-corrtest.exe`;
- `README.md`;
- operator, installation, built-in, live-qualification, and scenario-schema
  documentation;
- the optional Linux systemd unit and scratch `Containerfile`.

The Windows ZIP is created by a small Go standard-library packaging command,
not an unpinned external ZIP tool. ZIP member timestamps, modes, and ordering
derive from `SOURCE_DATE_EPOCH` so the complete release set remains
reproducible.

`make package checksums` produces every required asset. Package-content and
reproducibility checks cover all five platform archives. The GitHub release
workflow names every expected asset explicitly; an absent platform build fails
before publication.

## 5. Guarded release script

`scripts/release.sh vX.Y.Z` is a POSIX shell developer tool. It does not call
the GitHub Release API directly and does not duplicate workflow publishing.
Its contract is:

1. Require exactly one `vMAJOR.MINOR.PATCH` argument.
2. Require Git, `curl`, a clean worktree, branch `main`, and a configured
   release remote (`OSCAR_CORRTEST_RELEASE_REMOTE`, default `origin`).
3. Fetch the remote `main` and tag namespace, then require local `HEAD` to equal
   the remote `main` tip. The script never pushes application commits or
   bypasses branch protection.
4. Reject an existing local or remote tag and an existing GitHub release tag.
5. Run `make clean release-gate VERSION=<tag>`.
6. Verify the complete six-file release set, including `SHA256SUMS`, locally.
7. Create an annotated tag on the verified `HEAD`.
8. Push only `refs/tags/<tag>` to the configured remote.
9. Print the workflow/release URL and the exact recovery command if the tag
   push fails. A failed push leaves the local annotated tag intact and never
   deletes remote state automatically.

The GitHub Actions release workflow remains the publisher of record. On the
tag event it independently runs `make clean release-gate`, then creates the
GitHub Release and uploads the exact release set. GitLab continues to consume
the same verified archives and may mirror the semantic release.

The release script is tested with isolated fake `git` and `make` commands. Its
tests prove validation and command ordering without creating a real tag,
pushing a remote, or publishing a release.

## 6. POSIX installer

The supported one-liner is:

```sh
curl -fsSL https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.sh | sh
```

`scripts/install.sh` is POSIX `sh` with `set -eu` and no Bash-only syntax.

### 6.1 Inputs

| Variable | Default | Purpose |
|---|---|---|
| `OSCAR_CORRTEST_VERSION` | latest GitHub release | Pin an exact `vX.Y.Z` release. |
| `OSCAR_CORRTEST_INSTALL_DIR` | `$HOME/.local/bin` | Destination directory for the executable. |
| `OSCAR_CORRTEST_RELEASE_BASE_URL` | GitHub release-download base | Override for mirrors and isolated tests. |
| `OSCAR_CORRTEST_RELEASE_API_URL` | GitHub latest-release API | Override latest-version discovery. |

### 6.2 Flow

1. Require `HOME`, `uname`, `tar`, and either `curl` or `wget`.
2. Map `uname -s` to `linux` or `darwin`; map `x86_64`/`amd64` to `amd64`
   and `arm64`/`aarch64` to `arm64`. Reject every other pair before download.
3. Use `OSCAR_CORRTEST_VERSION`, or parse `tag_name` from the latest-release
   API without requiring `jq`.
4. Require an exact `vX.Y.Z` resolved tag.
5. Download the matching archive and `SHA256SUMS` into a trap-cleaned private
   temporary directory.
6. Extract the exact checksum row by filename and calculate SHA-256 with
   `sha256sum` or `shasum -a 256`. Missing tools, missing rows, or mismatches
   abort before the install directory is touched.
7. Extract into the private staging directory and require exactly one regular,
   executable binary at `oscar-corrtest/bin/oscar-corrtest`. Reject archive
   symlinks and unexpected path traversal.
8. Create the user-owned install directory, copy to a same-directory private
   temporary file, set mode `0755`, and rename atomically over the destination.
9. On macOS, best-effort remove `com.apple.quarantine` from the installed
   executable.
10. Print the installed version, absolute binary path, a PATH note when the
    directory is absent from `PATH`, and the explicit first-use commands.

The installer never invokes `serve`, touches the corrtest data directory, or
requests an OSCAR API key.

## 7. Windows installer

The supported PowerShell command is:

```powershell
irm https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.ps1 | iex
```

`scripts/install.ps1` mirrors the POSIX contract:

- latest or `OSCAR_CORRTEST_VERSION` selection;
- GitHub or mirror URL overrides;
- Windows amd64 validation;
- download ZIP plus `SHA256SUMS` into a temporary directory;
- `Get-FileHash -Algorithm SHA256` verification before installation;
- staged `Expand-Archive`, exact regular executable validation, and atomic
  replacement;
- default destination
  `%LOCALAPPDATA%\oscar-corrtest\bin\oscar-corrtest.exe`;
- add the destination to the current user's PATH only when absent, without
  changing machine-wide configuration;
- print an explicit PowerShell startup command and remote UI URL;
- never install or start a Windows service.

The PowerShell installer supports an explicit local/mirror asset root so a
Windows CI smoke test can exercise the real ZIP without contacting GitHub.

## 8. Default listener and security contract

Configuration defaults change from `127.0.0.1:8787` to `0.0.0.0:8787`.

With no serving flags:

```sh
oscar-corrtest serve
```

the application starts an unauthenticated HTTP UI reachable at
`http://<server-ip>:8787`. The startup output includes a warning that every
network peer able to reach the port can view evidence, create temporary rules,
and inject alerts through the configured OSCAR target.

This is an intentional reversal of the prior adversarial-review invariant.
The user has accepted the trade-off because corrtest is a local test tool and
direct UI access is a primary deployment requirement.

The existing hardening choices remain:

- `--listen 127.0.0.1:8787` restores local-only serving and applies the exact
  loopback Host allowlist that protects against DNS rebinding;
- `--remote-mode bearer` still requires exactly one UI-token reference plus a
  TLS certificate and key;
- `--remote-mode trusted-proxy` still requires trusted peer CIDRs and an exact
  authenticated identity header/value;
- CSRF, same-origin checks, CSP, session expiry, and output escaping remain in
  every mode.

The loopback Host guard is applied only when the actual listener IP is
loopback. Applying it to the wildcard default would reject every legitimate
remote Host header. Explicit bearer and proxy modes continue to wrap all UI,
mutation, SSE, and download routes.

The example systemd unit and documentation use the wildcard default. The
installer itself remains user-scoped and does not install that unit.

## 9. OSCAR API-key flow

No OSCAR credential is needed to install the binary, start the UI, browse
persisted history, preview scenarios, or view reports. Doctor and live runs
require a target whose credential is an external OSCAR API-key reference.

The first-use flow printed by installers is equivalent to:

```sh
export OSCAR_API_KEY='...'
oscar-corrtest target add \
  --name lab-a \
  --url https://oscar.example/ext/mw \
  --credential-env OSCAR_API_KEY
oscar-corrtest doctor --target tgt_... --pipeline-mode phase_b_dispatch
oscar-corrtest serve
```

For persistence across shells, the operator may provision a protected file and
use `--credential-file /absolute/path`. Only the reference is stored. A custom
`--ca-file` is optional for an OSCAR endpoint signed by a private CA;
`--insecure` remains an explicit diagnostic escape hatch. UI certificates and
UI tokens are optional hardening inputs, not default requirements.

## 10. Documentation and packaging

Create `docs/install.md` as the complete user-scoped installation guide. It
covers one-line and manual installation, version pinning, PATH behavior,
OSCAR API-key references, startup, direct UI access, optional hardening,
upgrade-by-reinstall, backup, and uninstall.

Update README quick start, operator guidance, development/release instructions,
the example systemd unit, and the original architecture document's superseded
listener statements. Release archives include `docs/install.md`.

The documented default startup is only:

```sh
oscar-corrtest serve
```

No certificate, UI token, remote-mode flag, SSH tunnel, or OSCAR API key is
required for that command. The API key becomes necessary only when doctor or a
test run contacts OSCAR.

## 11. Verification and acceptance criteria

The release gate must prove:

1. Linux amd64/arm64, Darwin amd64/arm64, and Windows amd64 binaries compile
   with `CGO_ENABLED=0`.
2. Every named archive exists, contains the expected executable and packaged
   documentation, and has exactly one matching `SHA256SUMS` row.
3. Rebuilding the same commit produces byte-identical archives and checksums.
4. The POSIX installer installs a real host-compatible package from a local
   release-shaped endpoint, reports the correct version, upgrades atomically,
   and leaves an independently created state directory byte-for-byte unchanged.
5. Corrupt archives, altered checksums, missing checksum rows, invalid release
   tags, unsupported platforms, and incomplete archives fail before replacing
   an existing binary.
6. PowerShell syntax is validated; the Windows installer smoke test runs on a
   Windows GitHub Actions worker against the real Windows ZIP.
7. The guarded release script cannot tag or push with a dirty tree, a
   non-semantic version, a non-main branch, a local/remote tag collision, a
   non-origin-equivalent HEAD, or a failed release gate.
8. Default configuration is `0.0.0.0:8787`; `serve` with no flags reaches the
   server without UI credentials and emits the unauthenticated-network warning.
9. Explicit loopback serving retains the Host allowlist, while wildcard serving
   accepts a normal remote Host header.
10. Bearer/TLS and trusted-proxy tests remain green and continue protecting
    every route when selected.
11. The standalone archive has no dependency on the OSCAR source repository,
    Go, Node, Python, an external database, or a frontend toolchain at runtime.
12. `make clean release-gate` covers cross-platform packaging, installer smoke,
    release-script contract tests, package content, and reproducibility.

## 12. Out of scope

- Automatic process startup or background-service registration by installers.
- Homebrew, apt, yum, winget, Scoop, MSI, PKG, RPM, or DEB distribution.
- Windows arm64 until equivalent installer verification exists.
- Code signing, Apple notarization, and publisher-identity signatures.
- A vanity install URL.
- Automatically creating the GitHub repository, changing its visibility, or
  configuring branch protection/release permissions.
- Treating unauthenticated wildcard serving as safe for untrusted networks.
