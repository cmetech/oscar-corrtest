# OSCAR Corrtest Distribution and Open Listener Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver guarded GitHub release automation, checksum-verifying user-scoped installers, cross-platform release archives, and directly reachable default UI serving.

**Architecture:** Keep GitHub Actions as the publisher of record: a guarded local script verifies and pushes an annotated semantic tag, then CI independently rebuilds and publishes deterministic assets. POSIX and PowerShell installers resolve a latest or pinned release, verify `SHA256SUMS`, and atomically replace only the user-local executable. Serving defaults to unauthenticated `0.0.0.0:8787`, while explicit loopback, bearer/TLS, and trusted-proxy modes retain their existing protections.

**Tech Stack:** Go 1.27, POSIX shell, PowerShell, GNU Make, GitHub Actions, GitLab CI, Go standard-library `archive/zip`, embedded SQLite/UI.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-distribution-and-open-listener-design.md`

## Global Constraints

- User-scoped installation is the default and never uses `sudo` or registers a background service.
- Installers install only; they never invoke `serve` or touch corrtest state/configuration.
- Required platforms are Linux amd64/arm64, Darwin amd64/arm64, and Windows amd64.
- Every downloaded executable is verified against `SHA256SUMS` before replacement.
- The release script never pushes application commits, deletes tags, or bypasses branch protection.
- The default UI is intentionally unauthenticated on `0.0.0.0:8787`; startup warns about network-visible mutation authority.
- Existing bearer/TLS and trusted-proxy behavior remains available and tested.
- The OSCAR API-key value remains reference-only.
- Use TDD red-green-refactor for every behavior change.

## File and interface map

| File | Responsibility |
|---|---|
| `internal/config/config.go` | Default listen address. |
| `internal/command/app.go` | Serving validation and public-listener warning. |
| `internal/web/server.go` | Apply the Host guard only to loopback listeners. |
| `internal/releasearchive/zip.go` | Deterministic Windows ZIP creation. |
| `cmd/package-zip/main.go` | Narrow CLI for the ZIP writer. |
| `scripts/package.sh` | Package one GOOS/GOARCH artifact. |
| `scripts/install.sh` | POSIX latest/pinned installer. |
| `scripts/install.ps1` | PowerShell latest/pinned installer. |
| `scripts/release.sh` | Guarded semantic tag orchestration. |
| `scripts/test-install-posix.sh` | POSIX real-archive installer smoke. |
| `scripts/test-install-windows.ps1` | Windows real-ZIP installer smoke. |
| `scripts/test-release.sh` | Offline release-script contract. |
| `Makefile` | Cross-build and distribution gates. |
| `.github/workflows/release.yml` | Verify, Windows smoke, and publish. |
| `docs/install.md` | Complete installation guide. |

---

### Task 1: Directly reachable default listener

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/command/app_test.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/command/app.go`
- Modify: `internal/web/server.go`
- Modify: `packaging/oscar-corrtest.service`

**Interfaces:**
- Produces: no-flag `serve` on `0.0.0.0:8787`, an unauthenticated-network warning, and loopback-only Host guarding.

- [ ] **Step 1: Write failing configuration and command tests**

Change the default assertion in `internal/config/config_test.go` to the literal
`0.0.0.0:8787`. Add `TestServeDefaultsToUnauthenticatedAllInterfaces` that
invokes `serve` without flags and asserts:

```go
if got.ListenAddress != "0.0.0.0:8787" || got.Security.Mode != web.SecurityNone {
    t.Fatalf("options=%+v", got)
}
if !strings.Contains(stderr.String(), "WARNING") || !strings.Contains(stderr.String(), "unauthenticated") {
    t.Fatalf("stderr=%q", stderr.String())
}
```

Replace the loopback-only address table with acceptance tests for
`0.0.0.0`, `[::]`, a literal non-loopback IP, `127.0.0.1`, and `[::1]`.
Require the warning only for non-loopback `SecurityNone` listeners. Preserve
the existing bearer-without-TLS rejection and authenticated-mode tests.

- [ ] **Step 2: Write a failing Host-guard selector test**

Add `TestHostGuardSelectionMatchesActualListener`. Wrap a handler through a
wished-for `hostGuardForListener`; prove a foreign Host receives 421 for
`127.0.0.1:8787` but 200 for `0.0.0.0:8787`.

- [ ] **Step 3: Run RED tests**

```bash
go test ./internal/config ./internal/command ./internal/web -count=1
```

Expected: the default and wildcard tests fail and `hostGuardForListener` is undefined.

- [ ] **Step 4: Implement the minimal listener change**

Set the config default to `0.0.0.0:8787`. When `--remote-mode` is empty,
reject supplied authentication/TLS flags but accept a syntactically valid
listener as `SecurityNone`. Print this warning for non-loopback listeners:

```go
fmt.Fprintln(a.stderr, "WARNING: corrtest UI is unauthenticated; every network peer that can reach this listener can create rules and inject alerts")
```

Add `hostGuardForListener(next, listenerAddress, security)` and return the
unwrapped handler unless mode is `SecurityNone` and the actual listener host is
a literal loopback IP. Use it from `web.Run`. Change the systemd listener to
`0.0.0.0:8787`.

- [ ] **Step 5: Run GREEN and commit**

```bash
gofmt -w internal/config internal/command internal/web
go test ./internal/config ./internal/command ./internal/web -count=1
git add internal/config internal/command internal/web packaging/oscar-corrtest.service
git commit -m "feat: expose corrtest UI by default"
```

---

### Task 2: Deterministic cross-platform release set

**Files:**
- Create: `internal/releasearchive/zip.go`
- Create: `internal/releasearchive/zip_test.go`
- Create: `cmd/package-zip/main.go`
- Modify: `scripts/package.sh`
- Modify: `scripts/check-package.sh`
- Modify: `scripts/check-reproducible.sh`
- Modify: `Makefile`

**Interfaces:**
- Produces: `releasearchive.WriteZip(outputPath, rootDir string, epoch time.Time) error`.
- Produces: `releasearchive.ListZip(path string) ([]string, error)` for dependency-free content checks.
- Produces: five platform archives plus `SHA256SUMS` through `make package checksums`.

- [ ] **Step 1: Write failing deterministic ZIP tests**

Create tests that call `WriteZip` twice on the same staged tree and assert
byte-identical SHA-256 results, lexical entry order, the fixed epoch timestamp,
and executable mode on `bin/oscar-corrtest.exe`. Add a second test that creates
a symlink and requires an error containing `symlink`.

- [ ] **Step 2: Observe RED**

```bash
go test ./internal/releasearchive -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement ZIP creation and its CLI**

Implement `WriteZip` with `filepath.WalkDir`, explicit symlink rejection,
sorted relative paths, `zip.FileHeader.SetMode`, fixed `Modified`, Deflate, and
same-directory atomic publication. Implement `ListZip` with `archive/zip`,
lexically sorted returned names, and no extraction. `cmd/package-zip` accepts:

```text
package-zip <output.zip> <staged-root> <source-date-epoch>
package-zip --list <archive.zip>
```

- [ ] **Step 4: Generalize the package matrix**

Change `scripts/package.sh` to:

```text
package.sh <version> <linux|darwin|windows> <amd64|arm64> <binary> <epoch>
```

Use normalized GNU tar/gzip for Linux/Darwin and the Go ZIP command for
Windows. Add CGO-free builds for Linux amd64/arm64, Darwin amd64/arm64, and
Windows amd64. Extend checksums to sorted `.tar.gz` and `.zip` assets.

- [ ] **Step 5: Expand package checks**

Require exactly these assets:

```text
oscar-corrtest_<version>_linux_amd64.tar.gz
oscar-corrtest_<version>_linux_arm64.tar.gz
oscar-corrtest_<version>_darwin_amd64.tar.gz
oscar-corrtest_<version>_darwin_arm64.tar.gz
oscar-corrtest_<version>_windows_amd64.zip
SHA256SUMS
```

Assert required archive members for all five packages and update the
reproducibility script to compare the complete checksum file after two builds.

- [ ] **Step 6: Run GREEN and commit**

```bash
gofmt -w internal/releasearchive cmd/package-zip
go test ./internal/releasearchive ./cmd/package-zip -count=1
make clean package checksums package-content-check reproducible-check
git add internal/releasearchive cmd/package-zip scripts Makefile
git commit -m "feat: package cross-platform releases"
```

---

### Task 3: POSIX one-line installer

**Files:**
- Create: `scripts/install.sh`
- Create: `scripts/test-install-posix.sh`
- Modify: `Makefile`

**Interfaces:**
- Consumes: Task 2 asset names.
- Produces: user-local executable and `installer-posix-check`.

- [ ] **Step 1: Write the failing real-package smoke**

Create `scripts/test-install-posix.sh` that detects the host OS/architecture,
stages the matching real archive beneath a temporary release-shaped `file://`
tree, and runs the installer with temporary `HOME` and install directory.
Assert installed `version` output, successful rerun, and unchanged hashes for a
separate sentinel state directory. Corrupt the archive and remove its checksum
row in separate cases; both must fail without changing the installed binary.

- [ ] **Step 2: Add the Make gate and observe RED**

```make
installer-posix-check: package checksums
	sh scripts/test-install-posix.sh "$(VERSION)"
```

Run `make installer-posix-check`; expect failure because `install.sh` is absent.

- [ ] **Step 3: Implement `install.sh`**

Use POSIX `sh`, `set -eu`, exact semantic tags, OS/architecture selection,
latest/pinned release resolution, curl/wget download, exact checksum-row
verification, archive traversal/symlink rejection, staged extraction, and
same-directory atomic replacement. Support:

```text
OSCAR_CORRTEST_VERSION
OSCAR_CORRTEST_INSTALL_DIR
OSCAR_CORRTEST_RELEASE_BASE_URL
OSCAR_CORRTEST_RELEASE_API_URL
```

Default to `$HOME/.local/bin`; never invoke `serve` or touch state. Print the
absolute binary path, PATH note, `serve` command, remote URL form, and target-add
example using `OSCAR_API_KEY`.

- [ ] **Step 4: Run GREEN and commit**

```bash
sh -n scripts/install.sh scripts/test-install-posix.sh
make installer-posix-check
git add scripts/install.sh scripts/test-install-posix.sh Makefile
git commit -m "feat: add checksum verified POSIX installer"
```

---

### Task 4: PowerShell installer and Windows release smoke

**Files:**
- Create: `scripts/install.ps1`
- Create: `scripts/test-install-windows.ps1`
- Modify: `.github/workflows/release.yml`
- Modify: `scripts/check-release-contract.sh`

**Interfaces:**
- Consumes: Windows ZIP from Task 2.
- Produces: user-local Windows executable and a pre-publication Windows smoke job.

- [ ] **Step 1: Write the PowerShell smoke first**

Create a script with `-Version` and `-AssetDirectory`. It isolates
`LOCALAPPDATA`, invokes `install.ps1` against local assets, executes the real
Windows binary's `version`, reruns for upgrade, then corrupts a ZIP and proves
the checksum failure leaves the installed executable hash unchanged.

- [ ] **Step 2: Observe RED on a Windows/PowerShell environment**

```powershell
pwsh -NoProfile -File scripts/test-install-windows.ps1 -Version v0.0.0-test -AssetDirectory dist
```

Expected: FAIL because `install.ps1` is absent.

- [ ] **Step 3: Implement `install.ps1`**

Mirror the POSIX variables and behavior, using `Get-FileHash`, staged
`Expand-Archive`, exact executable validation, and atomic `Move-Item`. Default
to `%LOCALAPPDATA%\oscar-corrtest\bin`, update only the current-user PATH when
absent, print startup commands, and never create/start a service.

- [ ] **Step 4: Refactor GitHub release workflow**

Using current official GitHub syntax and immutable action SHAs, create:

1. Ubuntu `verify`: semantic tag check, `make clean release-gate`, upload `dist`.
2. `windows-2025` smoke: download verified `dist`, run the PowerShell smoke.
3. Ubuntu `publish`: depend on both, download the same assets, publish all five
   archives and `SHA256SUMS`.

Extend `check-release-contract.sh` to prove those dependencies and exact assets.

- [ ] **Step 5: Run offline GREEN gates and commit**

```bash
make release-contract-check package-content-check
git add scripts/install.ps1 scripts/test-install-windows.ps1 scripts/check-release-contract.sh .github/workflows/release.yml
git commit -m "feat: add Windows installer release gate"
```

---

### Task 5: Guarded release orchestration

**Files:**
- Create: `scripts/release.sh`
- Create: `scripts/test-release.sh`
- Modify: `Makefile`

**Interfaces:**
- Produces: `scripts/release.sh vX.Y.Z` and `release-script-check`.

- [ ] **Step 1: Write isolated release-script tests**

Construct a temporary Git repository and bare `origin`, with fake `make` and
GitHub API responses first on PATH. Test invalid version, dirty tree, non-main
branch, HEAD differing from origin/main, existing local tag, existing remote
tag, release-gate failure, and success. Success must prove `make clean
release-gate VERSION=v1.2.3` ran before an annotated tag was pushed, and no
branch ref changed.

- [ ] **Step 2: Observe RED**

```bash
sh scripts/test-release.sh
```

Expected: FAIL because `release.sh` is absent.

- [ ] **Step 3: Implement `release.sh`**

Require exact `vMAJOR.MINOR.PATCH`, clean `main`, configured remote (default
`origin`), GitHub repository `cmetech/oscar-corrtest`, fetched main/tag refs,
exact `HEAD == remote/main`, no local/remote/release collision, successful
`make clean release-gate VERSION=<tag>`, complete assets, and valid checksums.
Create the annotated tag only after all gates, then push only that tag. Never
delete tags automatically; print a precise retry command on push failure.

- [ ] **Step 4: Run GREEN and commit**

```bash
sh -n scripts/release.sh scripts/test-release.sh
sh scripts/test-release.sh
git add scripts/release.sh scripts/test-release.sh Makefile
git commit -m "feat: automate guarded GitHub releases"
```

---

### Task 6: Installation and release documentation

**Files:**
- Create: `docs/install.md`
- Modify: `README.md`
- Modify: `docs/operator.md`
- Modify: `docs/development.md`
- Modify: `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`
- Modify: `scripts/package.sh`
- Modify: `scripts/check-package.sh`

**Interfaces:**
- Produces: one complete install/upgrade/start/backup/uninstall guide in every archive.

- [ ] **Step 1: Write `docs/install.md` from actual behavior**

Document the POSIX and PowerShell one-liners, version pinning, paths, manual
archive/checksum installation, PATH refresh, API-key environment/file
references, doctor, `oscar-corrtest serve`, `http://<server-ip>:8787`, explicit
loopback, optional bearer/proxy hardening, upgrade by rerunning the installer,
complete state backup, online DB backup limits, and uninstall that preserves
state unless separately requested.

- [ ] **Step 2: Remove conflicting current guidance**

Put one-line installation first in README. Update operator/development docs and
the systemd discussion for the public default. Mark original loopback-only
architecture clauses as superseded by the new spec rather than deleting their
historical rationale.

- [ ] **Step 3: Package the guide and run consistency checks**

Add `docs/install.md` to every archive and required-member list. Run:

```bash
rg -n "loopback-only|requires --remote-mode|127\.0\.0\.1:8787.*default" README.md docs packaging
make package-content-check
```

Only historical reviews and explicitly superseded statements may retain the old contract.

- [ ] **Step 4: Commit docs**

```bash
git add README.md docs packaging scripts/package.sh scripts/check-package.sh
git commit -m "docs: add cross-platform installation guide"
```

---

### Task 7: Gate integration and final verification

**Files:**
- Modify: `Makefile`
- Modify: `scripts/check-release-contract.sh`
- Modify: `.github/workflows/ci.yml` only if `release-gate` does not reach installer gates
- Modify: `.gitlab-ci.yml` only if new asset globs are omitted

**Interfaces:**
- Produces: one offline `make clean release-gate` covering the complete slice.

- [ ] **Step 1: Wire all offline gates**

Require POSIX installer smoke, release-script contract, five-platform package
content, full checksums, and full reproducibility from `release-gate`. Keep live
qualification excluded.

- [ ] **Step 2: Prove mutation sensitivity**

One at a time, temporarily change and restore: default listener to loopback;
enable Host guard on wildcard; omit Darwin arm64; bypass installer checksum;
tag before release gate; omit Windows ZIP from publisher. Run the named test for
each and require failure before restoring.

- [ ] **Step 3: Run full verification from a clean tree**

```bash
git diff --check
go test -shuffle=on -count=20 ./internal/config ./internal/command ./internal/web ./internal/releasearchive
make clean release-gate
go test -race ./...
git status --short
```

Expected: all exit 0 and status is clean apart from ignored generated outputs.

- [ ] **Step 4: Validate release rejection without publishing**

```bash
./scripts/release.sh 1.2.3
```

Expected: exit 2 before network access. Never invoke a valid release version
against a real remote during implementation verification.

- [ ] **Step 5: Commit final gates and report**

```bash
git add Makefile scripts/check-release-contract.sh .github/workflows/ci.yml .gitlab-ci.yml
git commit -m "test: gate cross-platform distribution"
```

Report all slice commits, the exact five archives plus checksum file, complete
gate/race results, that no real tag/release was created, and that a public
`origin` is still required before `scripts/release.sh vX.Y.Z` can publish.
