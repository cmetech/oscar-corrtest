# Managed Environment and Cross-Platform Paths Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add deterministic Linux/macOS/Windows user paths, a managed `.env`, and global `OSCAR_API_KEY` fallback without persisting secrets in CorrTest data.

**Architecture:** `platformpaths` owns OS location rules. `envfile.Store` overlays the startup environment and managed file, supports atomic live replacement, and is injected into the command/runtime boundary. Runtime passes the store's getter to every new OSCAR client, so empty target references use the global key while explicit references retain priority.

**Tech Stack:** Go standard library, existing config/runtime/OSCAR packages, table-driven Go tests.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- Windows `.env` is `%LOCALAPPDATA%\oscar-corrtest\.env`.
- POSIX config uses XDG with `$HOME/.config` fallback; state uses XDG with `$HOME/.local/state` fallback.
- Environment precedence is command → external environment → managed `.env` → JSON → default.
- UI/live replacement may override the startup external value only until restart, and must report that fact later through `KeyStatus`.
- Secret values never enter SQLite, reports, artifacts metadata, errors, or logs.
- No database migration and no new third-party dependency.

---

### Task 1: Centralize platform paths

**Files:**
- Create: `internal/platformpaths/paths.go`
- Create: `internal/platformpaths/paths_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Paths{ConfigDir, EnvFile, ConfigFile, StateDir, LogDir, ApplicationLog, BootstrapLog string}`.
- Produces: `Resolve(goos string, lookup func(string) (string, bool)) (Paths, error)`.
- Produces: `config.LoadForOS(goos string, getenv func(string) string, overrides config.Overrides) (config.Settings, error)`; existing `config.Load` wraps it with `runtime.GOOS`.
- Extends: `config.Settings` with `EnvFile` and `LogDir`.

- [ ] **Step 1: Write path matrix tests**

```go
func TestResolvePlatformPaths(t *testing.T) {
    tests := []struct{ goos string; env map[string]string; envFile, state string }{
        {"linux", map[string]string{"HOME": "/home/alex"}, "/home/alex/.config/oscar-corrtest/.env", "/home/alex/.local/state/oscar-corrtest"},
        {"darwin", map[string]string{"HOME": "/Users/alex", "XDG_CONFIG_HOME": "/cfg", "XDG_STATE_HOME": "/state"}, "/cfg/oscar-corrtest/.env", "/state/oscar-corrtest"},
        {"windows", map[string]string{"LOCALAPPDATA": `C:\Users\alex\AppData\Local`}, `C:\Users\alex\AppData\Local\oscar-corrtest\.env`, `C:\Users\alex\AppData\Local\oscar-corrtest\data`},
    }
    // Resolve with a map lookup and compare cleaned values.
}
```

- [ ] **Step 2: Run the tests and verify the package is absent**

Run: `go test ./internal/platformpaths ./internal/config`

Expected: FAIL because `internal/platformpaths` and `LoadForOS` do not exist.

- [ ] **Step 3: Implement `Paths` and `Resolve`**

Use `filepath.Join`; reject unsupported `goos`, missing `LOCALAPPDATA` on
Windows, and missing `HOME` when the applicable XDG root is absent. Derive
`application.jsonl` and `service-bootstrap.log` under `LogDir`.

- [ ] **Step 4: Route config defaults through the resolver**

Implement `LoadForOS`; populate `EnvFile`/`LogDir`; retain the existing
absolute data-directory and listener validation. Keep `Load` as the production
wrapper so current callers compile.

- [ ] **Step 5: Run focused tests**

Run: `go test ./internal/platformpaths ./internal/config`

Expected: PASS, including Windows paths on a non-Windows host.

- [ ] **Step 6: Commit**

```bash
git add internal/platformpaths internal/config
git commit -m "feat: add cross-platform user paths"
```

### Task 2: Build the managed dotenv store

**Files:**
- Create: `internal/envfile/store.go`
- Create: `internal/envfile/store_test.go`

**Interfaces:**
- Consumes: `platformpaths.Paths.EnvFile`.
- Produces: `SourceUnset`, `SourceExternal`, `SourceManaged`, and `SourceLiveOverride`.
- Produces: `KeyStatus{Configured bool, Source Source, ExternalResumesOnRestart bool}`.
- Produces the locked `Open`, `Getenv`, `Status`, `Replace`, and `Clear` methods from the series index.

- [ ] **Step 1: Write parsing and precedence tests**

```go
func TestStorePrecedenceAndLiveOverride(t *testing.T) {
    // File has OSCAR_API_KEY=managed; lookup returns external.
    // Getenv initially returns external and Status is SourceExternal.
    // Replace("OSCAR_API_KEY", "replacement") returns replacement immediately,
    // persists it, and marks ExternalResumesOnRestart.
}
```

Cover blank/comment lines, `export KEY=`, single/double quotes, duplicate keys
(last managed assignment wins on read; replacement collapses the managed key
to one canonical assignment), CRLF, malformed quotes, NUL, and a 64 KiB file
limit.

- [ ] **Step 2: Run the tests and verify failure**

Run: `go test ./internal/envfile`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement strict read and overlay behavior**

`Open` snapshots relevant startup environment values through the injected
lookup and parses the file if present. `Getenv` returns live override, startup
external value, then managed value. A clear operation installs a live
tombstone so the key is absent until restart even when startup had an external
value.

- [ ] **Step 4: Implement atomic replacement**

Write a same-directory private temporary file, `Sync`, close, chmod `0600` on
POSIX where supported, and rename. Update in-memory state only after rename.
Preserve unrelated bytes and newline style. Reject keys outside
`^[A-Z_][A-Z0-9_]*$`, values containing CR/LF/NUL, and values over 16 KiB.

- [ ] **Step 5: Add rollback and concurrency tests**

Inject filesystem operations through unexported function fields in tests.
Prove a failed rename leaves file and `Getenv` unchanged. Run 50 concurrent
readers during replacement under the race detector.

- [ ] **Step 6: Run focused and race tests**

Run: `go test -race ./internal/envfile`

Expected: PASS with no races and no secret printed by failures.

- [ ] **Step 7: Commit**

```bash
git add internal/envfile
git commit -m "feat: add managed dotenv store"
```

### Task 3: Inject the environment and add global credential fallback

**Files:**
- Modify: `cmd/oscar-corrtest/main.go`
- Modify: `internal/command/app.go`
- Modify: `internal/command/app_test.go`
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/runtime_test.go`
- Modify: `internal/oscar/client.go`
- Modify: `internal/oscar/client_test.go`

**Interfaces:**
- Produces: `runtime.Options{Environment *envfile.Store}` and `runtime.OpenWithOptions`; existing `runtime.Open` remains a wrapper.
- Produces: `command.Dependencies{Serve, Open, Getenv}` and `command.NewApplication`; current constructors remain wrappers.
- Runtime stores `getenv func(string) string` and uses one `newOSCARClient(target)` helper for every client construction.

- [ ] **Step 1: Write OSCAR fallback tests**

```go
func TestEmptyCredentialUsesGlobalAPIKey(t *testing.T) {
    client := newTestClient(t, domain.Target{Credential: domain.CredentialRef{}},
        func(key string) string { if key == "OSCAR_API_KEY" { return "global-secret" }; return "" })
    // Execute a request and assert X-API-Key is global-secret.
}
```

Add a companion test proving explicit `CredentialEnvironment` wins over
`OSCAR_API_KEY` and absence of both sends no header.

- [ ] **Step 2: Run OSCAR tests and verify fallback failure**

Run: `go test ./internal/oscar -run 'Credential|APIKey'`

Expected: FAIL because an empty reference currently resolves to no credential.

- [ ] **Step 3: Implement fallback and runtime client helper**

Change only the empty-reference branch in `resolveCredential`. Refactor all
runtime `oscar.New` call sites through `newOSCARClient`, passing the injected
getter and harness version.

- [ ] **Step 4: Wire main through the store**

Resolve platform paths, open the managed store before command dispatch, pass
`store.Getenv` to command configuration, and capture the store in the runtime
open closure. Startup parse failures write a value-free error to stderr and
exit nonzero.

- [ ] **Step 5: Add live replacement integration test**

Open runtime with a temporary store, create a target with no credential,
replace the key, and prove the next fake-OSCAR request carries the replacement
without reconstructing runtime. Construct a client before replacement and
prove it retains its original header.

- [ ] **Step 6: Run plan gate**

Run: `go test -race ./internal/platformpaths ./internal/envfile ./internal/config ./internal/oscar ./internal/runtime ./internal/command`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/oscar-corrtest internal/command internal/runtime internal/oscar
git commit -m "feat: use managed global OSCAR API key"
```
