# Unified Operations Workspace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Combine API-key configuration, effective paths, service lifecycle, live status, and safe application logs into one technical Operations page.

**Architecture:** `operations.Controller` composes the managed environment store, service manager, structured logs, and resolved settings behind value-safe operations. The web layer depends on that narrow controller contract, serves ordinary actions with CSRF, and exposes a same-origin SSE stream for redacted records and service status.

**Tech Stack:** Go standard library, existing web middleware/templates, Server-Sent Events, managed environment/service/log packages from Plans 1–3.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- The API key is write-only in the UI; responses expose only configured state, source category, and whether an external value resumes after restart.
- No suffix, hash, length, validation echo, or other secret-derived identifier is returned.
- Key updates are atomic and apply to newly created OSCAR clients immediately.
- Service stop/restart responses must be flushed before the process can terminate.
- Log streams and downloads use only allowlisted redacted application sources.
- All state-changing routes require current CSRF protection and request-size limits.

---

### Task 1: Compose a value-safe operations controller

**Files:**
- Create: `internal/operations/controller.go`
- Create: `internal/operations/controller_test.go`
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/runtime_test.go`

**Interfaces:**
- Produces: `Snapshot{Key envfile.KeyStatus; Paths PathSnapshot; Service service.Status; LogSources []applog.Source}`.
- Produces: `PathSnapshot{ConfigFile, EnvFile, DataDir, LogDir string}`.
- Produces locked: `operations.New(config.Settings, *envfile.Store, service.Manager, *applog.System) *operations.Controller`.
- Produces: `Snapshot`, `ReplaceAPIKey`, `ClearAPIKey`, `ServiceAction`, `RecentLogs`, `SubscribeLogs`, `OpenLogSource`.
- Extends: runtime options/state with the controller and a read-only `Operations() *operations.Controller` accessor.

- [ ] **Step 1: Write controller contract tests**

```go
func TestReplaceAPIKeyReturnsNoSecretDerivedData(t *testing.T) {
    snapshot, err := controller.ReplaceAPIKey(context.Background(), "unique-sentinel-secret")
    encoded, _ := json.Marshal(snapshot)
    // Require configured state and reject sentinel, suffix, length, digest,
    // file contents, and any map containing a credential value.
}
```

Cover empty/oversized/multiline values, clear behavior, persistence failure
rollback, external-resumes-on-restart state, unavailable manager, bounded logs,
and source traversal attempts.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/operations`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement controller composition**

Return purpose-built snapshots rather than exposing underlying stores. Accept
only service actions `install`, `start`, `stop`, `restart`, and `uninstall`.
Use fixed maximums for recent-log count and keep log records already redacted.

- [ ] **Step 4: Wire runtime without creating dependency cycles**

Construct the controller after environment, service, and logs are available in
main/runtime options. Keep runtime execution unaware of UI semantics; only its
OSCAR client getter depends on the environment store.

- [ ] **Step 5: Run focused race tests**

Run: `go test -race ./internal/operations ./internal/runtime`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/operations internal/runtime
git commit -m "feat: compose operations controller"
```

### Task 2: Add secure Operations HTTP routes

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/security_test.go`
- Modify: `internal/web/view.go`
- Modify: `internal/web/view_test.go`

**Interfaces:**
- Adds: `GET /operations` and redirects `GET /settings` to `/operations`.
- Adds: `POST /operations/api-key`, `POST /operations/api-key/clear`, and `POST /operations/service/{action}`.
- Adds: `GET /operations/events` SSE and `GET /operations/logs/{source}` download.
- Consumes: narrow web-local operations interface implemented by `*operations.Controller`.

- [ ] **Step 1: Write API-key and service route tests**

```go
func TestOperationsAPIKeyIsWriteOnly(t *testing.T) {
    // POST a sentinel with valid CSRF, follow redirect, inspect all headers/body,
    // then GET operations and events. Sentinel must never appear anywhere.
}
```

Cover missing/invalid CSRF, oversized forms, unsupported service actions,
method rejection, controller failures, configured flash messages, route-level
request logging, and `/settings` compatibility redirect.

- [ ] **Step 2: Write SSE and download tests**

Require `text/event-stream`, `Cache-Control: no-store`, connection cancellation,
initial status, `log`, `service`, and `ping` event shapes, bounded event data,
allowlisted source names, attachment headers, and traversal rejection.

- [ ] **Step 3: Run tests and verify failure**

Run: `go test ./internal/web -run 'Operations|APIKey|Service|Event|LogDownload'`

Expected: FAIL because Operations routes do not exist.

- [ ] **Step 4: Implement ordinary routes**

Parse at most the configured small form-body limit. Immediately discard form
storage after key replacement. Use Post/Redirect/Get and generic success/error
messages. For stop/restart, render/flush a response and schedule the action on
the server-owned context so the initiating request is not cut off.

- [ ] **Step 5: Implement SSE and safe downloads**

Send an initial status snapshot, forward redacted records from a bounded
subscription, poll service status at a modest interval, send heartbeat comments,
and exit on request cancellation. Never allow client paths to reach `os.Open`.

- [ ] **Step 6: Run route and security tests**

Run: `go test -race ./internal/web -run 'Operations|APIKey|Service|Event|Log|CSRF|Host|CSP'`

Expected: PASS with no leaked sentinel.

- [ ] **Step 7: Commit**

```bash
git add internal/web
git commit -m "feat: add operations endpoints"
```

### Task 3: Render the unified settings, service, and log console

**Files:**
- Create: `internal/web/templates/operations.html.tmpl`
- Create: `internal/web/static/js/operations.js`
- Modify: `internal/web/static/css/app.css`
- Modify: `internal/web/assets.go`
- Modify: `internal/web/assets_test.go`
- Modify: `internal/web/server_test.go`

**Interfaces:**
- Adds: API-key status/form, effective path table, service status/actions, live log viewport, source selector, pause/resume, clear-view, and download controls.
- Adds: JavaScript reconnection with capped backoff and a visible disconnected state; no client persistence of key values.

- [ ] **Step 1: Write Operations template tests**

```go
func TestOperationsPageRendersTechnicalWorkspaceWithoutSecret(t *testing.T) {
    // Require configured/source status, effective paths, service buttons,
    // log controls, event endpoint, help topic, and CSRF tokens.
}
```

Require password input with autocomplete disabled, no prefilled value, clear
confirmation copy, disabled unavailable actions, and semantic live-region labels.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/web -run OperationsPage`

Expected: FAIL because the template and asset do not exist.

- [ ] **Step 3: Build the approved unified Operations surface**

Use Variant B: configuration and paths in the upper workspace, service state and
actions adjacent, and a wide dense log console below. Provide source/level/text
filters over the bounded client view, timestamps, connection status, and a link
to contextual documentation.

- [ ] **Step 4: Implement live log enhancement**

Append records with `textContent`, cap the DOM to 500 rows, preserve paused
records in a bounded client queue, reconnect with 1–30 second backoff, and stop
when the document is hidden only after recording the disconnected state. Never
write event payloads to localStorage/sessionStorage.

- [ ] **Step 5: Run the plan gate**

Run: `go test -race ./internal/envfile ./internal/service ./internal/applog ./internal/operations ./internal/runtime ./internal/web`

Expected: PASS with all secret-sentinel and security tests.

- [ ] **Step 6: Commit**

```bash
git add internal/web
git commit -m "feat: add unified operations workspace"
```
