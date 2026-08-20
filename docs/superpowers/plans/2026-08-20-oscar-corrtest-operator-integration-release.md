# Operator Integration and Release Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate the managed key, user service, structured logs, documentation, workbench, and Operations UI into reproducible cross-platform release artifacts with complete operator guidance.

**Architecture:** End-to-end tests exercise the real application composition against a fake OSCAR contract. Packaging explicitly includes operator documentation and service examples, while installer tests prove installations remain user-scoped and inert. One nonempty release gate is used locally and by every GitHub/GitLab publish lane.

**Tech Stack:** Go tests, POSIX shell and PowerShell installer tests, Make, GitHub Actions, GitLab CI, existing cross-compilation and packaging scripts.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- Installers place binaries and print exact next commands; they do not create `.env`, install a service, or start a process.
- Release archives remain cross-compiled for Linux and macOS amd64/arm64 and Windows amd64 where currently supported.
- Package checks reject stale, missing, duplicate, executable-bit, and unexpected files.
- Documentation states the all-interface listener is unauthenticated and intended for isolated test networks.
- CI executes the same `release-gate` used before a tag; no empty or wildcard-only test selection is accepted.
- Implementation ends with commits and verification; it does not tag, push, or publish.

---

### Task 1: Update installation and operator documentation

**Files:**
- Modify: `README.md`
- Create: `docs/operator-guide.md`
- Create: `docs/scenario-authoring.md`
- Create: `docs/service-management.md`
- Modify: `docs/installation.md`
- Modify: `docs/development.md`

**Interfaces:**
- Documents: managed `.env` locations on Linux/macOS/Windows and startup precedence.
- Documents: global key fallback and advanced per-target credential references.
- Documents: service install/start/stop/restart/status/logs/uninstall with exit codes.
- Documents: built-in preview, cloning, strict scenario authoring, P01/N01, naming/labels, artifacts, and log UI.

- [ ] **Step 1: Write a documentation contract test**

**Files:**
- Create: `internal/docs/operator_docs_test.go`

```go
func TestOperatorDocsContainPlatformAndLifecycleContracts(t *testing.T) {
    // Read committed docs and require POSIX/Windows .env paths, OSCAR_API_KEY,
    // all seven service subcommands, listener warning, scenario clone/preview,
    // P01/N01, oscar_test_run_id, category, logs, and no auto-start promise.
}
```

- [ ] **Step 2: Run the test and verify failure**

Run: `go test ./internal/docs -run Operator`

Expected: FAIL because the complete guide set does not exist.

- [ ] **Step 3: Write concise task-oriented documentation**

Lead with install, first explicit start, key configuration, opening the UI,
selecting a target, previewing/cloning a scenario, running it, inspecting OSCAR
by run/pattern labels, and exporting evidence. Include troubleshooting for
status exit 3, bootstrap logs, Phase-B qualification, and cleanup-safe behavior.

- [ ] **Step 4: Cross-link CLI and UI reference**

Add a command matrix and paths table. State that UI key entry is convenience
storage for a test tool but remains write-only, and that an external environment
value resumes after restart when it overrides the managed file.

- [ ] **Step 5: Run docs tests**

Run: `go test ./internal/docs`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add README.md docs internal/docs
git commit -m "docs: add operator and scenario guides"
```

### Task 2: Keep installers user-scoped, inert, and informative

**Files:**
- Modify: `scripts/install.sh`
- Modify: `scripts/install.ps1`
- Modify: `scripts/install_test.go`
- Modify: `packaging/README.txt`

**Interfaces:**
- POSIX default binary: `${XDG_BIN_HOME:-$HOME/.local/bin}/oscar-corrtest`.
- Windows default binary: `%LOCALAPPDATA%\Programs\oscar-corrtest\oscar-corrtest.exe`.
- Success output includes managed `.env` location, direct `serve`, and optional `service install` then `service start` commands.

- [ ] **Step 1: Extend installer behavior tests**

```go
func TestInstallersDoNotStartOrInstallService(t *testing.T) {
    // Run POSIX installer against a fake archive and statically inspect PowerShell.
    // Assert one binary is installed and no serve/service process is executed.
}
```

Cover spaces in user paths, architecture selection, checksum rejection, upgrades,
missing PATH notice, Windows path output, and exact explicit startup commands.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./scripts -run Install`

Expected: FAIL because the new output/path contracts are not present.

- [ ] **Step 3: Update installer messages and package README**

Resolve paths without printing secret values. Explain that configuration can be
completed in `/operations` after direct start or by writing `OSCAR_API_KEY` to
the displayed `.env`; starting remains an explicit user action.

- [ ] **Step 4: Run installer tests**

Run: `go test -race ./scripts`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts packaging/README.txt
git commit -m "feat: document explicit post-install startup"
```

### Task 3: Package all operator assets and strengthen CI gates

**Files:**
- Modify: `scripts/package.sh`
- Modify: `scripts/check-package.sh`
- Modify: `scripts/package_test.go`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `.gitlab-ci.yml`

**Interfaces:**
- Adds: nonempty `operator-experience-gate` Make target over exact packages.
- Extends: release archives with operator/scenario/service guides and service-definition examples where applicable.
- Preserves: current checksums, SBOM/provenance, static binaries, and tag-driven publication.

- [ ] **Step 1: Write package manifest tests**

```go
func TestReleaseArchivesContainOperatorDocumentation(t *testing.T) {
    // Build an archive and require binary, README, installation, operator,
    // scenario authoring, service management, license notices, and no secrets.
}
```

Require an exact manifest per target, correct executable bits, CRLF-tolerant
PowerShell, no source tree/config/database/log inclusion, and stale archive
cleanup.

- [ ] **Step 2: Run package tests and verify failure**

Run: `go test ./scripts -run Package`

Expected: FAIL because the manifest omits the new guides.

- [ ] **Step 3: Update explicit package/check manifests**

Avoid recursive globs. Teach the checker the exact platform-specific archive
names and file set. Include only static service examples; generated files must
not contain machine-specific paths.

- [ ] **Step 4: Add the nonempty Make gate**

The gate runs exact package tests for `platformpaths`, `envfile`, `service`,
`applog`, `operations`, `scenario`, `runtime`, `web`, `command`, `scripts`, and
`internal/docs`, with a preflight that fails if any selected package is absent.
Make `release-gate` depend on it.

- [ ] **Step 5: Pin every CI lane to `make release-gate`**

Require pull-request, branch, tag, GitHub publish, and GitLab publish jobs to run
the shared target before artifacts are uploaded. Preserve pinned action/image
digests and current least-privilege permissions.

- [ ] **Step 6: Run packaging and workflow checks**

Run: `go test ./scripts && make clean release-gate`

Expected: PASS with all target archives validated.

- [ ] **Step 7: Commit**

```bash
git add scripts Makefile .github/workflows .gitlab-ci.yml
git commit -m "build: gate operator experience releases"
```

### Task 4: Add end-to-end composition regressions and run the final gate

**Files:**
- Create: `internal/integration/operator_experience_test.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/runtime/runtime_test.go`
- Modify: `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

**Interfaces:**
- Proves: UI-managed global key reaches a newly constructed OSCAR client as `X-API-Key`.
- Proves: built-in preview/clone/run preserves naming, labels, assertions, evidence, and cleanup.
- Proves: structured logs reach the UI without secret, alert payload, or cookie leakage.
- Proves: platform service definitions invoke the same packaged binary and user paths.

- [ ] **Step 1: Write the global-key-to-OSCAR regression**

Start the real web server/runtime with temporary paths and a fake OSCAR server.
POST a sentinel key through Operations, create a target without credential ref,
execute a minimal qualified case, and assert the fake received the sentinel only
in `X-API-Key`. Assert no response, log, SQLite field, evidence artifact, or
report contains it.

- [ ] **Step 2: Write the built-in workbench-to-run regression**

Preview flood, clone it, execute P01/N01 against the semantic fake, and verify
distinct event fingerprints, reserved identity reuse, alertname/category/run
labels, declared assertions, terminal rows, immutable evidence, and cleanup.

- [ ] **Step 3: Write service and live-log composition regressions**

Use fake service runners for each GOOS and compare generated definitions. Open
the Operations event stream, emit lifecycle logs, and require ordered redacted
records plus clean cancellation and bounded buffering.

- [ ] **Step 4: Run focused integration tests repeatedly**

Run: `go test -race -count=20 ./internal/integration ./internal/web ./internal/runtime`

Expected: PASS with no flakes, races, leaked sentinel, or empty test selection.

- [ ] **Step 5: Run the complete release gate**

Run: `make clean release-gate`

Expected: PASS, including cross-compilation and archive validation.

- [ ] **Step 6: Run the complete race suite**

Run: `CGO_ENABLED=1 go test -race -count=1 ./...`

Expected: PASS.

- [ ] **Step 7: Record implementation status and commit**

Update the design document implementation-status appendix with exact gates and
remaining target-specific live qualification (if any). Do not claim a live
OSCAR PASS from fake-server tests.

```bash
git add internal/integration internal/web internal/runtime docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md
git commit -m "test: verify operator experience integration"
```
