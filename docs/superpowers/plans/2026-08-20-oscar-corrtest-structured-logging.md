# Structured Logging Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add durable, redacted structured application logs plus a bounded live stream suitable for CLI and UI consumption.

**Architecture:** `applog.System` is the only application logger. It writes JSON through a size-based rotating writer, mirrors a redacted bounded record into an in-memory ring, and publishes records to nonblocking subscribers. Main injects the logger into runtime, command, and web boundaries; bootstrap stderr remains available when the log system cannot open.

**Tech Stack:** Go `log/slog`, JSON Lines, standard-library synchronization and filesystem APIs, race-tested fakes.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- Rotate at 10 MiB and retain five application-log files.
- Keep at most 500 redacted records in memory.
- Never emit API keys, authorization headers, cookie values, CSRF tokens, scenario credential values, or values from keys whose names indicate secrets.
- Subscriber backpressure must not block request handling or the runner.
- Log records are operational diagnostics, not run evidence and not stored in SQLite.
- No third-party logging dependency.

---

### Task 1: Implement a bounded rotating writer

**Files:**
- Create: `internal/applog/rotate.go`
- Create: `internal/applog/rotate_test.go`

**Interfaces:**
- Produces: unexported `rotatingWriter{path string, maxBytes int64, backups int}` implementing `io.WriteCloser`.
- Guarantees: serialized complete writes, rotation before a write that crosses the limit, and files `application.jsonl.1` through `.5`.

- [ ] **Step 1: Write rotation and recovery tests**

```go
func TestRotatingWriterRetainsFiveBackups(t *testing.T) {
    // Use a 64-byte limit, write distinguishable complete JSON lines,
    // and assert current + five backups with newest at .1.
}
```

Cover existing oversized files, missing directories, partial rename failure,
permissions, concurrent writers, and closing twice.

- [ ] **Step 2: Run the tests and verify failure**

Run: `go test ./internal/applog -run Rotat`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement rotation**

Open with append/create and private user permissions, maintain the observed
size, close before rename, shift backups from high to low, reopen, then write.
Return errors without discarding the original error or panicking.

- [ ] **Step 4: Run focused race tests**

Run: `go test -race ./internal/applog -run Rotat -count=20`

Expected: PASS with no corrupt or interleaved lines.

- [ ] **Step 5: Commit**

```bash
git add internal/applog/rotate.go internal/applog/rotate_test.go
git commit -m "feat: add rotating application log"
```

### Task 2: Build the redacted ring and subscriber system

**Files:**
- Create: `internal/applog/system.go`
- Create: `internal/applog/redact.go`
- Create: `internal/applog/system_test.go`
- Create: `internal/applog/redact_test.go`

**Interfaces:**
- Produces: `Record{Sequence uint64; Time time.Time; Level, Source, Message string; Attributes map[string]string}`.
- Produces: `Source{Name, Path string; Downloadable bool}` and `Subscription{C <-chan Record; Cancel func()}`.
- Produces: `Options{MaxBytes int64; Backups, RingSize, SubscriberBuffer int; Now func() time.Time}`.
- Produces: `Open(logDir string, stderr io.Writer, options Options) (*System, error)` and `StderrOnly(stderr io.Writer) *System`.
- Produces: `System.Logger`, `Recent`, `Subscribe`, `Sources`, `OpenSource`, and `Close`.

- [ ] **Step 1: Write ring and redaction tests**

```go
func TestSystemRedactsBeforeEverySink(t *testing.T) {
    // Log sentinels as nested slog groups and attributes named api_key,
    // authorization, cookie, token, and credential. Assert no sentinel is
    // present in disk JSON, stderr, Recent, or subscriber records.
}
```

Test oldest-first ring snapshots, monotonic sequence numbers, subscription
cancellation, slow subscriber drops, source allowlisting, missing backups, and
concurrent close/log/subscribe operations.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/applog -run 'System|Redact|Source'`

Expected: FAIL because the system API does not exist.

- [ ] **Step 3: Implement redaction and structured fan-out**

Use one `slog.Handler` that normalizes attributes, redacts before encoding,
writes a single JSON line to disk, mirrors it to stderr, updates the ring, and
then attempts nonblocking subscriber sends. Drop attributes with unsafe keys
or replace values with `[REDACTED]`; never retain the original value in memory.

- [ ] **Step 4: Implement allowlisted source access**

Return only current application log, numbered retained backups, and bootstrap
log. `OpenSource` accepts an exact source name returned by `Sources`, rejects
paths and traversal, and opens only regular files under the resolved log root.

- [ ] **Step 5: Run package gate**

Run: `go test -race ./internal/applog -count=10`

Expected: PASS, including slow-subscriber and secret-sentinel tests.

- [ ] **Step 6: Commit**

```bash
git add internal/applog
git commit -m "feat: add redacted structured logging"
```

### Task 3: Wire structured lifecycle logging through the application

**Files:**
- Modify: `cmd/oscar-corrtest/main.go`
- Modify: `internal/command/app.go`
- Modify: `internal/command/app_test.go`
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/runtime_test.go`
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`

**Interfaces:**
- Extends: `runtime.Options` and `command.Dependencies` with `Logger *slog.Logger` and `Logs *applog.System`.
- Adds: web request records with request ID, method, route, status, duration, and remote IP; query strings and bodies are excluded.
- Adds: runtime records for run/case phase transitions, OSCAR call outcome class, cleanup state, and artifact persistence without request payloads.

- [ ] **Step 1: Write lifecycle and HTTP logging tests**

```go
func TestRequestLogExcludesSecretsAndQuery(t *testing.T) {
    // Request /settings?api_key=sentinel with Cookie and X-API-Key headers.
    // Assert route/status exist and sentinel/header values do not.
}
```

Add a runner test asserting transition fields are present while alert labels,
annotations, response bodies, and credential material are absent.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/runtime ./internal/web -run 'Log|Lifecycle'`

Expected: FAIL because structured logging is not injected.

- [ ] **Step 3: Open logs once in main and inject the system**

Open the resolved log directory before runtime, fall back to `StderrOnly` on
failure with a bootstrap-safe message, and close it after command completion.
Replace ad-hoc prints at the application boundary with structured records.

- [ ] **Step 4: Instrument runtime and web boundaries**

Use stable source names (`main`, `command`, `runtime`, `oscar`, `web`) and event
names. Log outcome categories, identifiers, counts, and durations only. Keep
human CLI output unchanged.

- [ ] **Step 5: Run the plan gate**

Run: `go test -race ./internal/applog ./internal/runtime ./internal/web ./internal/command ./cmd/oscar-corrtest`

Expected: PASS with the secret-sentinel suite green.

- [ ] **Step 6: Commit**

```bash
git add cmd/oscar-corrtest internal/command internal/runtime internal/web
git commit -m "feat: wire structured application logs"
```
