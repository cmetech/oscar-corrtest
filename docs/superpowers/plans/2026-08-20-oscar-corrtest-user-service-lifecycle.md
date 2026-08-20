# User-Level Service Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide explicit, user-scoped install/start/stop/restart/status/logs/uninstall commands on Linux, macOS, and Windows without starting a service during package installation.

**Architecture:** A platform-neutral `service.Manager` owns lifecycle semantics and delegates rendering and process execution to build-independent platform adapters selected from an injected GOOS. Generated units point at the current binary and resolved user paths. Commands translate service states into stable output and exit codes.

**Tech Stack:** Go standard library, launchd, systemd user units, Windows Task Scheduler, golden-file and command-runner tests.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- Services are user-scoped; elevated/root installation is neither required nor attempted.
- `service install` enables login startup but does not start the application immediately.
- Installers remain binary-only and never invoke a service lifecycle command.
- Runtime secrets remain in the managed `.env`; generated service definitions contain no API key.
- Status exits 0 when running, 3 when stopped or absent, and 1 on operational errors.
- Uninstall preserves binary, configuration, `.env`, SQLite, evidence, and logs.

---

### Task 1: Define the service contract and render deterministic definitions

**Files:**
- Create: `internal/service/service.go`
- Create: `internal/service/render.go`
- Create: `internal/service/render_test.go`
- Create: `internal/service/testdata/launchagent.golden`
- Create: `internal/service/testdata/systemd-user.golden`
- Create: `internal/service/testdata/task.xml.golden`

**Interfaces:**
- Produces: `StateUnknown`, `StateNotInstalled`, `StateStopped`, `StateRunning`.
- Produces: `Status{State State, PID int, Detail string}`.
- Produces: `Runner.Run(ctx context.Context, name string, args ...string) ([]byte, error)`.
- Produces: `Manager` methods `Install`, `Start`, `Stop`, `Restart`, `Status`, `Logs`, and `Uninstall`.
- Produces: `Options{GOOS, Executable string; Paths platformpaths.Paths; Runner Runner; Stdout, Stderr io.Writer}`.
- Produces the locked `NewManager(Options) (Manager, error)` constructor.

- [ ] **Step 1: Write golden rendering tests**

```go
func TestRenderDefinitionsContainOnlyResolvedPaths(t *testing.T) {
    // Render each platform with paths containing spaces.
    // Compare exact output to testdata goldens.
    // Reject OSCAR_API_KEY, shell interpolation, and a start-now directive.
}
```

- [ ] **Step 2: Run the tests and verify failure**

Run: `go test ./internal/service -run Render`

Expected: FAIL because the package and renderers do not exist.

- [ ] **Step 3: Implement value types and renderers**

Render a macOS LaunchAgent plist, a systemd user unit, and Task Scheduler XML.
Every definition executes `oscar-corrtest serve`, sets the working/state paths,
redirects bootstrap failures to `Paths.BootstrapLog`, and restarts only after
nonzero process exit. Escape XML and systemd arguments explicitly.

- [ ] **Step 4: Prove definitions are secret-free and deterministic**

Run: `go test ./internal/service -run Render -count=20`

Expected: PASS with byte-identical output on every run.

- [ ] **Step 5: Commit**

```bash
git add internal/service
git commit -m "feat: define user service lifecycle"
```

### Task 2: Implement platform adapters with injectable execution

**Files:**
- Create: `internal/service/manager.go`
- Create: `internal/service/darwin.go`
- Create: `internal/service/linux.go`
- Create: `internal/service/windows.go`
- Create: `internal/service/manager_test.go`

**Interfaces:**
- Consumes: renderers, `Options`, and `Runner` from Task 1.
- Guarantees: `Install` creates parent directories and atomically replaces only the CorrTest-owned definition.
- Guarantees: `Logs(ctx, lines, follow)` validates `1 <= lines <= 5000` and streams through the configured writers.

- [ ] **Step 1: Write command-sequence tests**

```go
func TestInstallEnablesWithoutStarting(t *testing.T) {
    // Linux expects daemon-reload then enable, never --now/start.
    // macOS expects bootstrap registration, never kickstart.
    // Windows expects /Create only, never /Run.
}
```

Cover installed/running/stopped/not-installed parsing, idempotent stop and
uninstall, command failure propagation, and preservation of data directories.

- [ ] **Step 2: Run the tests and verify failure**

Run: `go test ./internal/service -run 'Install|Start|Status|Uninstall'`

Expected: FAIL because adapters do not exist.

- [ ] **Step 3: Implement Linux and macOS adapters**

Use `systemctl --user daemon-reload`, `enable`, `start`, `stop`, `restart`,
`show`, `disable`, and `journalctl --user-unit`. Use `launchctl bootstrap`,
`bootout`, `kickstart -k`, `print`, and `kill TERM`. Treat platform-specific
"not found" output as a state, not an operational error.

- [ ] **Step 4: Implement Windows adapter**

Use `schtasks.exe /Create`, `/Run`, `/End`, `/Query /XML`, and `/Delete` with a
user-logon trigger. Store the generated XML under the resolved config directory
for auditability. Do not put secret values in task arguments or environment.

- [ ] **Step 5: Run all service tests under the race detector**

Run: `go test -race ./internal/service`

Expected: PASS without invoking the host service manager.

- [ ] **Step 6: Commit**

```bash
git add internal/service
git commit -m "feat: implement cross-platform user services"
```

### Task 3: Add lifecycle commands and stable exit behavior

**Files:**
- Create: `internal/command/service_commands.go`
- Create: `internal/command/service_commands_test.go`
- Modify: `internal/command/app.go`
- Modify: `internal/command/app_test.go`
- Modify: `cmd/oscar-corrtest/main.go`

**Interfaces:**
- Adds: `oscar-corrtest service install|start|stop|restart|status|logs|uninstall`.
- Adds: `service logs --lines N [--no-follow]`; default is 200 lines followed.
- Extends: `command.Dependencies` with a `Service func() (service.Manager, error)` factory.

- [ ] **Step 1: Write CLI contract tests**

```go
func TestServiceStatusExitCodes(t *testing.T) {
    // Running => 0 and "running"; stopped/not-installed => 3;
    // manager error => 1 with a value-free message on stderr.
}
```

Also cover command help, unknown actions, no implicit start after install,
bounded log-line parsing, and Ctrl-C cancellation of followed logs.

- [ ] **Step 2: Run command tests and verify failure**

Run: `go test ./internal/command -run Service`

Expected: FAIL because service commands are not registered.

- [ ] **Step 3: Implement commands using the injected manager**

Return status code 3 directly without wrapping it as an error. Print concise
human-readable state and the definition path; never print environment values.
Make log following respect command context cancellation.

- [ ] **Step 4: Wire the production manager**

Resolve the executable with `os.Executable`, pass resolved paths and an
`exec.CommandContext` runner, and preserve existing command constructors as
test-compatible wrappers.

- [ ] **Step 5: Run the plan gate**

Run: `go test -race ./internal/platformpaths ./internal/service ./internal/command ./cmd/oscar-corrtest`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/oscar-corrtest internal/command internal/service
git commit -m "feat: add user service commands"
```
