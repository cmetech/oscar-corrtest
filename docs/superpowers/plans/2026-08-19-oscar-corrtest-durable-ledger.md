# OSCAR Correlation Test Harness Durable Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configuration, a crash-recoverable SQLite ledger, secret-safe target metadata, hashed run artifacts, canonical report history, coordinated online backup, CLI history commands, and durable server-rendered Targets/Runs pages without contacting OSCAR.

**Architecture:** This is Plan 2 of a seven-plan delivery series and completes Slice 1B. Application services depend on domain-facing repository and artifact interfaces; `internal/persistence/sqlite` contains the only SQLite- and driver-specific code; the CLI and web UI use the same services. SQLite is the authoritative searchable ledger while large evidence remains in atomically written, hash-addressed run directories.

**Tech Stack:** Go 1.27.0, `database/sql`, pinned CGO-free `modernc.org/sqlite` v1.55.0 with its coupled `modernc.org/libc` v1.74.1, embedded forward-only SQL migrations, `html/template`, vanilla JavaScript/CSS, and Go test subprocesses for crash recovery.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`
**Prior plan:** `docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md`
**Plan-2 gate:** Migration/WAL/recovery/backup tests and durable interrupted-run evidence must pass before completion.

## Global Constraints

- Work only in the standalone `oscar-corrtest` repository and preserve its existing history.
- Use Go 1.27.0 or newer and keep `GOWORK=off`, `CGO_ENABLED=0` standalone Linux AMD64/ARM64 builds.
- Pin `modernc.org/sqlite` to v1.55.0 and the exact `modernc.org/libc` version selected by that module; no unpinned or floating dependency is permitted.
- Require SQLite 3.51.3 or newer at runtime; the selected driver embeds SQLite 3.53.3 for supported Linux AMD64/ARM64 targets.
- Keep SQLite and local artifact files behind inward-facing Go interfaces; no SQLite types may leak into domain, command, or web packages.
- Enable `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`, and `synchronous=FULL` on every pooled connection and verify effective settings during startup tests.
- Keep network calls out of SQLite transactions. Plan 2 performs no OSCAR network call or target mutation.
- Never persist, log, render, report, or artifact credential values. Store only typed environment, file, or systemd credential references.
- Use restrictive local permissions: directories `0700`, SQLite/backup/artifact files `0600` subject to the process umask.
- Write artifact files to a sibling temporary file, sync, atomically rename, and sync the parent directory before recording them available.
- Store only validated data-directory-relative artifact paths. Reject absolute paths, traversal, empty path components, and symlink escapes.
- Treat migration/integrity failure as diagnostic read-only mode: `/healthz` remains 200, `/readyz` returns 503, and mutation commands fail closed.
- At startup atomically mark every active run `INTERRUPTED`, append a monotonic recovery event, and never resume injection.
- Preserve verdict and cleanup status as separate concepts.
- Canonical report JSON is versioned and durable in Plan 2. Portable HTML, JUnit, evidence ZIP, live SSE, and OSCAR deep links remain Plan 3.
- The online backup operation must coordinate with SQLite WAL, refuse overwrite, install the destination atomically, and prove the result opens and passes integrity checks.
- Keep the default listener `127.0.0.1:8787`; do not introduce remote serving.
- Update both CI cache dependency paths from `go.mod` to `go.sum` in the first dependency-bearing commit.
- Use TDD: every behavior starts with a failing test that is observed before implementation. Commit after each completed task.

## Delivery Boundary

Plan 2 provides durable foundations that can be exercised through services, CLI history commands, tests, and server-rendered pages. It does not provide `run`, `plan`, scenario import, OSCAR diagnostics, rule CRUD, alert injection, audit/history polling, cleanup against OSCAR, SSE, HTML/JUnit/ZIP export, retention deletion, or remote authentication. Those remain owned by Plans 3–7.

## Planned Repository Structure

```text
internal/
  artifact/store.go
  artifact/store_test.go
  config/config.go
  config/config_test.go
  domain/id.go
  domain/id_test.go
  domain/run.go
  domain/target.go
  history/service.go
  history/service_test.go
  persistence/sqlite/
    backup.go
    backup_test.go
    database.go
    database_test.go
    migrations.go
    migrations_test.go
    migrations/0001_initial.sql
    run_repository.go
    run_repository_test.go
    target_repository.go
    target_repository_test.go
  report/report.go
  report/report_test.go
  runtime/runtime.go
  runtime/runtime_test.go
  testoscar/server.go
  testoscar/server_test.go
internal/web/
  templates/runs.html.tmpl
  templates/run_detail.html.tmpl
  templates/targets.html.tmpl
  templates/settings.html.tmpl
```

---

### Task 1: Add configuration precedence and stable run identifiers

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/domain/id.go`
- Create: `internal/domain/id_test.go`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `config.Settings`, `config.Overrides`, `config.Load(func(string) string, Overrides) (Settings, error)`, `domain.NewRunID(io.Reader) (RunID, error)`, and `RunID.Short() string`.

- [ ] **Step 1: Write failing configuration tests**

Test defaults, a versioned JSON config file, environment overrides, CLI overrides, unknown JSON fields, invalid listener/data paths, and XDG resolution. Use an injected `getenv` and temporary HOME/XDG values; do not mutate process-global environment in parallel tests.

Required settings:

```go
type Settings struct {
    ConfigPath    string
    DataDir       string
    ListenAddress string
}

type Overrides struct {
    ConfigPath    string
    DataDir       string
    ListenAddress string
}
```

Precedence must be override, `OSCAR_CORRTEST_DATA_DIR`/`OSCAR_CORRTEST_LISTEN`, JSON config, then XDG/default. The JSON document requires `"apiVersion":"corrtest.oscar/v1alpha1"`.

- [ ] **Step 2: Run the tests and observe RED**

Run `go test ./internal/config -v`; expect failure because the package does not exist.

- [ ] **Step 3: Implement minimal configuration loading**

Decode with `json.Decoder.DisallowUnknownFields`, reject trailing documents, normalize paths without requiring them to exist, and default to `127.0.0.1:8787`. Use `$XDG_CONFIG_HOME/oscar-corrtest/config.json` or `$HOME/.config/oscar-corrtest/config.json`; use `$XDG_STATE_HOME/oscar-corrtest` or `$HOME/.local/state/oscar-corrtest` for data.

- [ ] **Step 4: Write failing identifier tests and observe RED**

Require full IDs matching `^crt_[0-9A-HJKMNP-TV-Z]{26}$`, deterministic output for an injected 16-byte reader, an eight-character uppercase Crockford short token, collision-independent full entropy, and failure when entropy cannot be read. Run `go test ./internal/domain -run RunID -v`; expect failure.

- [ ] **Step 5: Implement run IDs**

Encode 128 random bits as fixed-width Crockford Base32 without external packages. The short token is the first eight encoded characters after `crt_`; it is display-only and never the authority key.

- [ ] **Step 6: Verify and commit**

Run `go test ./internal/config ./internal/domain -v` and `go test ./...`; expect PASS.

Commit:

```bash
git add .gitignore internal/config internal/domain
git commit -m "feat: add configuration and run identity contracts"
```

---

### Task 2: Pin SQLite and implement checksum-verified migrations

**Files:**
- Modify: `go.mod`
- Create: `go.sum`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `.gitlab-ci.yml`
- Create: `internal/persistence/sqlite/database.go`
- Create: `internal/persistence/sqlite/database_test.go`
- Create: `internal/persistence/sqlite/migrations.go`
- Create: `internal/persistence/sqlite/migrations_test.go`
- Create: `internal/persistence/sqlite/migrations/0001_initial.sql`

**Interfaces:**
- Produces: `sqlite.Open(context.Context, string) (*Database, error)`, `(*Database).Close() error`, `(*Database).Ready() error`, and internal `migrate(context.Context, *sql.DB, fs.FS) error`. The concrete `*sql.DB` remains an unexported field used only by files in this package.

- [ ] **Step 1: Add pinned module requirements and CI cache tests**

Run:

```bash
go get modernc.org/sqlite@v1.55.0
go mod tidy
go list -m modernc.org/sqlite modernc.org/libc
```

Expected versions are `modernc.org/sqlite v1.55.0` and `modernc.org/libc v1.74.1`. Update GitHub `cache-dependency-path` and GitLab cache `files` to `go.sum`. Add/extend static workflow tests so the old `go.mod` cache key fails.

- [ ] **Step 2: Write failing database-setting tests and observe RED**

Open a temp database and assert `sqlite_version() >= 3.51.3`, `journal_mode=wal`, `foreign_keys=1`, `busy_timeout=5000`, `synchronous=2`, a local `corrtest.db-wal` appears during writes, and multiple pooled connections preserve connection-local settings. Run `go test ./internal/persistence/sqlite -run 'Open|Pragma|WAL' -v`; expect failure.

- [ ] **Step 3: Implement database opening**

Create parent directories with `0700`, reject a non-regular existing database, build a `file:` URI with DSN pragmas, set a bounded pool of four open/four idle connections, ping, verify the embedded SQLite version and effective pragmas, and close on fatal open/path failures. `Open` returns a live `Database` whose `Ready()` reports integrity or migration failures so the runtime can serve diagnostics while every repository mutation fails closed. Never infer or permit a network filesystem path; document local-filesystem responsibility in the returned error/help text.

- [ ] **Step 4: Write failing migration tests and observe RED**

Test fresh apply, no-op reapply, ordered upgrade with a test `fs.FS`, rollback of a deliberately failing migration, checksum mismatch after an applied migration changes, and a complete schema/index/foreign-key inventory. Run `go test ./internal/persistence/sqlite -run Migration -v`; expect failure.

- [ ] **Step 5: Implement migrations and initial schema**

Embed `migrations/*.sql`. Create `schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL)`. Each migration runs in one immediate transaction and records the lowercase SHA-256 of its exact embedded bytes. Reject gaps, duplicate versions, modified applied content, and a database schema newer than the binary.

`0001_initial.sql` creates the spec tables with restrictive foreign keys, status/verdict checks, canonical JSON text columns, UTC RFC3339 timestamp text, uniqueness for `(run_id, sequence)` and `(run_id, stable_key)`, and indexes for target name, run recency/status/verdict/cleanup, case pattern, event sequence, and artifact run/kind.

- [ ] **Step 6: Verify standalone CGO-free cross-build and commit**

Run:

```bash
go mod verify
go test ./internal/persistence/sqlite -v
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/oscar-corrtest
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/oscar-corrtest
make mod-check
```

Commit:

```bash
git add go.mod go.sum .github .gitlab-ci.yml internal/persistence/sqlite
git commit -m "feat: add sqlite migrations and durable schema"
```

---

### Task 3: Add secret-safe target metadata

**Files:**
- Create: `internal/domain/target.go`
- Create: `internal/persistence/sqlite/target_repository.go`
- Create: `internal/persistence/sqlite/target_repository_test.go`
- Create: `internal/history/service.go`
- Create: `internal/history/service_test.go`

**Interfaces:**
- Produces: `domain.Target`, `domain.CredentialRef`, `domain.TLSPolicy`, `sqlite.CreateTarget`, `sqlite.ListTargets`, `sqlite.GetTarget`, and `history.Service` target methods.

- [ ] **Step 1: Write failing target validation and repository tests**

Require unique case-insensitive display names, absolute HTTP(S) base URLs without userinfo/query/fragment, TLS verification by default, mutually valid custom-CA/insecure settings, and typed credential references `env`, `file`, or `systemd`. Reject any API accepting a credential value field.

Persist and reopen a target, then inspect every SQLite text value and captured log/output buffer to prove a sentinel secret is absent. Run `go test ./internal/domain ./internal/persistence/sqlite ./internal/history -run Target -v`; expect failure.

- [ ] **Step 2: Implement target types and repository**

Store only credential type and reference name/path. File credential references must be absolute; environment/systemd names must match `[A-Za-z_][A-Za-z0-9_]*`. Return sanitized target structures only. Use UTC timestamps supplied by an injected clock in services.

- [ ] **Step 3: Verify and commit**

Run `go test ./internal/domain ./internal/persistence/sqlite ./internal/history -v` and `go test ./...`; expect PASS.

Commit:

```bash
git add internal/domain/target.go internal/persistence/sqlite/target_repository* internal/history
git commit -m "feat: persist secret-safe target metadata"
```

---

### Task 4: Implement the run ledger and crash recovery

**Files:**
- Create: `internal/domain/run.go`
- Create: `internal/persistence/sqlite/run_repository.go`
- Create: `internal/persistence/sqlite/run_repository_test.go`
- Modify: `internal/history/service.go`
- Modify: `internal/history/service_test.go`

**Interfaces:**
- Produces: run/case/verdict/cleanup/event types; `CreateRun`, `GetRun`, `ListRuns`, `TransitionRun`, `AppendRunEvent`, `RecoverInterruptedRuns`; and history list/detail/filter services.

- [ ] **Step 1: Write failing state-machine tests and observe RED**

Test allowed lifecycle transitions, terminal-state immutability, verdict/cleanup independence, monotonic event sequences under concurrent append attempts, run filters, and stable newest-first pagination. Run `go test ./internal/domain ./internal/persistence/sqlite -run 'Run|Event|Transition' -v`; expect failure.

- [ ] **Step 2: Implement domain and repository behavior**

Use the exact lifecycle and verdict enums in the spec. Transition state and append its event in the same short transaction. List filters accept target, status, verdict, cleanup status, pattern, and created-at bounds; values are bound parameters, never SQL fragments.

- [ ] **Step 3: Write a failing durable interruption test**

Use a helper subprocess controlled by `GO_WANT_CORRTEST_CRASH_HELPER=1`: it opens a temp data directory, creates a run in `OBSERVING`, prints `READY`, then blocks. The parent kills it without graceful shutdown, starts a second process/runtime, and asserts the run is `INTERRUPTED`, has exactly one recovery event, retains its previous state in structured event detail, and remains interrupted after a third restart.

Run `go test ./internal/persistence/sqlite -run InterruptedProcess -v`; expect failure before recovery is implemented.

- [ ] **Step 4: Implement idempotent startup recovery**

In one transaction, select active runs, set status `INTERRUPTED`, set cleanup `UNKNOWN` only when owned resources exist (otherwise `NOT_REQUIRED`), and append one `run.interrupted` event per run. Never create behavioral verdicts or resume work.

- [ ] **Step 5: Verify and commit**

Run `go test ./internal/domain ./internal/persistence/sqlite ./internal/history -count=1 -v` and `go test -race ./internal/persistence/sqlite ./internal/history`; expect PASS.

Commit:

```bash
git add internal/domain/run.go internal/persistence/sqlite/run_repository* internal/history
git commit -m "feat: add crash-recoverable run ledger"
```

---

### Task 5: Add atomic artifacts and canonical report history

**Files:**
- Create: `internal/artifact/store.go`
- Create: `internal/artifact/store_test.go`
- Create: `internal/report/report.go`
- Create: `internal/report/report_test.go`
- Modify: `internal/persistence/sqlite/run_repository.go`
- Modify: `internal/persistence/sqlite/run_repository_test.go`

**Interfaces:**
- Produces: `artifact.Store`, `artifact.Manifest`, `artifact.Write`, `artifact.Verify`, `report.Document`, `report.Build`, and repository methods for pending/available artifact state and canonical reports.

- [ ] **Step 1: Write failing artifact safety tests and observe RED**

Test directory/file permissions, sibling temporary writes, final atomic visibility, SHA-256/size/MIME metadata, no-overwrite behavior, path traversal/absolute/backslash/empty-component rejection, symlink escape rejection, missing file, changed file, and cleanup after injected write/sync/rename failures. Run `go test ./internal/artifact -v`; expect failure.

- [ ] **Step 2: Implement the artifact store**

Only accept application-generated run IDs and basename components. Create `runs/<run-id>/evidence`; write with `os.CreateTemp` and mode `0600`; call `Sync`, close, rename without overwrite, then sync the parent. Verify by reopening with a traversal-safe resolved path and streaming SHA-256. Return explicit `missing`, `hash_mismatch`, or `valid` integrity results.

- [ ] **Step 3: Write failing report tests and observe RED**

Build a report from stored run/case/assertion/event/resource/artifact facts. Require `apiVersion: corrtest.oscar/v1alpha1`, deterministic JSON bytes, sanitized target URL, separate run/verdict/cleanup fields, artifact warnings, and no secret sentinel. Persist it in `runs.canonical_report_json`, write identical `runs/<id>/report.json`, and prove restart/reopen equality.

- [ ] **Step 4: Implement report building and two-phase artifact registration**

Insert a `PENDING` artifact row before filesystem publication, then mark it `AVAILABLE` with size/hash after successful rename. A pending, missing, or mismatched artifact stays queryable as an integrity warning. Store only canonical JSON generated from typed structures; reject invalid raw JSON at repository boundaries.

- [ ] **Step 5: Verify and commit**

Run `go test ./internal/artifact ./internal/report ./internal/persistence/sqlite -count=1 -v` and `go test -race ./internal/artifact ./internal/report`; expect PASS.

Commit:

```bash
git add internal/artifact internal/report internal/persistence/sqlite
git commit -m "feat: add hashed artifacts and canonical reports"
```

---

### Task 6: Implement coordinated online backup

**Files:**
- Create: `internal/persistence/sqlite/backup.go`
- Create: `internal/persistence/sqlite/backup_test.go`

**Interfaces:**
- Produces: `(*Database).Backup(context.Context, string) error`.

- [ ] **Step 1: Write failing backup tests and observe RED**

Continuously append run events on one connection while backing up another. Require the backup destination not to exist, verify refusal to overwrite, cancellation cleanup, mode `0600`, no leaked temp file, `PRAGMA integrity_check='ok'`, matching applied migrations, and a self-consistent prefix of committed events after opening the backup as a new database.

Run `go test ./internal/persistence/sqlite -run Backup -count=1 -v`; expect failure.

- [ ] **Step 2: Implement online backup behind the SQLite package boundary**

Hold a dedicated `*sql.Conn`, use `Conn.Raw` and the modernc online-backup interface, copy in bounded page steps while checking context, finish/close correctly on every path, fsync the temporary database, and atomically install it without overwriting the destination. Immediately open the installed snapshot read-only and run integrity/migration verification; remove an invalid destination and return the verification error.

- [ ] **Step 3: Verify and commit**

Run `go test ./internal/persistence/sqlite -run Backup -count=10 -v` and `go test -race ./internal/persistence/sqlite -run Backup -count=1`; expect PASS.

Commit:

```bash
git add internal/persistence/sqlite/backup*
git commit -m "feat: add coordinated sqlite backup"
```

---

### Task 7: Compose the runtime and add target/run/backup CLI commands

**Files:**
- Create: `internal/runtime/runtime.go`
- Create: `internal/runtime/runtime_test.go`
- Modify: `internal/command/app.go`
- Modify: `internal/command/app_test.go`
- Modify: `cmd/oscar-corrtest/main.go`
- Modify: `packaging/oscar-corrtest.service`

**Interfaces:**
- Produces: `runtime.Open(context.Context, config.Settings, version.Info) (*Runtime, error)`, `Runtime.History`, `Runtime.Artifacts`, `Runtime.Readiness`, and CLI commands `target add/list`, `runs list/show`, and `backup`.

- [ ] **Step 1: Write failing runtime tests and observe RED**

Require one initialization path for CLI and web, restrictive directory creation, migrations before readiness, startup recovery before serving, close ordering, and diagnostic mode on a deliberately broken migration/integrity check. Run `go test ./internal/runtime -v`; expect failure.

- [ ] **Step 2: Implement runtime composition**

Composition may import concrete SQLite/artifact packages; domain/services/web may not. Represent readiness as a concurrency-safe snapshot with component, status, and sanitized error. Fatal path/permission failures stop startup; migration/integrity failures retain a diagnostic runtime with mutations disabled.

- [ ] **Step 3: Write failing CLI tests and observe RED**

Test exact usage, JSON/human output, filtering, missing IDs, duplicate targets, and exit codes. Commands accept shared `--config` and `--data-dir`; `serve` also accepts `--listen`. `target add` accepts `--name`, `--url`, `--credential-env|--credential-file|--credential-systemd`, `--ca-file`, and `--insecure`; exactly zero or one credential reference is allowed. `backup --output` requires a path and refuses overwrite.

- [ ] **Step 4: Implement CLI commands using the runtime**

Add:

```text
oscar-corrtest target add ...
oscar-corrtest target list [--output json]
oscar-corrtest runs list [--target ... --status ... --verdict ... --cleanup ... --pattern ... --output json]
oscar-corrtest runs show <run-id> [--output json]
oscar-corrtest backup --output <path>
```

Use versioned JSON envelopes. Never print credential contents. Keep `version` independent of database startup.

- [ ] **Step 5: Update systemd paths and verify restart behavior**

Pass explicit `--config /etc/oscar-corrtest/config.json` and `--data-dir /var/lib/oscar-corrtest`; retain loopback binding and hardening. Test that two sequential CLI invocations see identical target/run history.

- [ ] **Step 6: Verify and commit**

Run `go test ./internal/runtime ./internal/command ./cmd/oscar-corrtest -count=1 -v` and `go test ./...`; expect PASS.

Commit:

```bash
git add internal/runtime internal/command cmd packaging
git commit -m "feat: expose durable target and run history commands"
```

---

### Task 8: Activate durable Targets, Runs, Run Detail, and diagnostic UI

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/assets.go`
- Modify: `internal/web/templates/base.html.tmpl`
- Modify: `internal/web/templates/dashboard.html.tmpl`
- Create: `internal/web/templates/runs.html.tmpl`
- Create: `internal/web/templates/run_detail.html.tmpl`
- Create: `internal/web/templates/targets.html.tmpl`
- Create: `internal/web/templates/settings.html.tmpl`
- Modify: `internal/web/static/css/components.css`
- Create: `internal/web/static/css/pages.css`
- Modify: `cmd/oscar-corrtest/main.go`

**Interfaces:**
- Consumes: runtime history/readiness services.
- Produces: `GET /targets`, `GET /runs`, `GET /runs/{id}`, `GET /settings`, database-aware `/readyz`, and POST-only target creation protected by same-origin and CSRF controls.

- [ ] **Step 1: Write failing handler contract tests and observe RED**

Use real temp SQLite/artifact stores, not SQL mocks. Require navigation, target form labels/errors, filtered run table, persisted detail report, separate verdict/cleanup pills, artifact integrity warnings, empty states, 404, HTML escaping, security headers, and 503 JSON readiness details when migrations are unavailable. Verify restart by closing/reopening before the second request.

Run `go test ./internal/web -run 'Target|Run|Ready|Diagnostic' -v`; expect failure.

- [ ] **Step 2: Generalize template rendering and handler dependencies**

Define narrow web interfaces for target creation/list, run list/detail, and readiness. Parse templates once. Render every page into a buffer before writing. Navigation uses ordinary links and accurate `aria-current`; Run test and Scenarios remain disabled.

- [ ] **Step 3: Implement mutation protection**

Target creation is POST-only. Require `Origin` to match the request scheme/host when present, reject cross-site `Sec-Fetch-Site`, and require an HMAC-authenticated session-bound CSRF token emitted in the form. Use `303 See Other` after success. Do not add target delete/edit yet.

- [ ] **Step 4: Implement accessible pages**

Runs supports query filters for target/status/verdict/cleanup/pattern and renders table caption/headers with narrow-screen overflow. Run Detail renders summary, timeline, cases, report JSON metadata, and artifact warnings as escaped text. Settings shows sanitized paths, SQLite version/mode/readiness, and diagnostics but no credential values. Preserve theme/focus/reduced-motion contracts.

- [ ] **Step 5: Verify and commit**

Run `go test ./internal/web -count=1 -v`, `go test -race ./internal/web`, and `go test ./...`; expect PASS.

Commit:

```bash
git add internal/web cmd/oscar-corrtest
git commit -m "feat: add durable target and run history UI"
```

---

### Task 9: Add the Plan-3 fake OSCAR server seam and complete qualification

**Files:**
- Create: `internal/testoscar/server.go`
- Create: `internal/testoscar/server_test.go`
- Modify: `README.md`
- Modify: `docs/development.md`
- Modify: `scripts/test-standalone.sh`
- Modify: `Makefile`

**Interfaces:**
- Produces: test-only `testoscar.New(t testing.TB) *Server`, scripted response queues, request recording, and deterministic latency/failure controls for future OSCAR adapters.

- [ ] **Step 1: Write failing fake-server tests and observe RED**

Require recorded method/path/query/headers/body, ordered scripted responses, unexpected-request failure, optional blocked response released by the test, and automatic shutdown. Run `go test ./internal/testoscar -v`; expect failure.

- [ ] **Step 2: Implement the minimal fake server**

Use `httptest.Server`; never import OSCAR source or reproduce endpoint semantics in Plan 2. Keep secrets redacted from failure output.

- [ ] **Step 3: Extend standalone and mandatory-gate tests**

Make `standalone-check` archive the repository, build with no parent OSCAR tree, run fresh migration/config/backup smoke tests in a temporary data directory, verify no sentinel secret appears, and terminate cleanly. Add a `plan2-gate` Make target that runs focused migration/WAL/recovery/artifact/report/backup/UI tests; make `ci-core` invoke it before the full suite.

- [ ] **Step 4: Document the durable foundation**

README and development docs must cover XDG/system paths, config precedence and schema, credential references, local-filesystem-only SQLite, WAL files, target/history/backup commands, backup limitations (database snapshot excludes run evidence directories), diagnostic readiness, and the explicit no-OSCAR-mutation Plan 2 boundary.

- [ ] **Step 5: Run fresh Plan 2 verification**

Run:

```bash
make clean
make plan2-gate
make ci
test -z "$(git status --porcelain --untracked-files=no)" || git status --short
```

Then inspect Linux packages:

```bash
file bin/linux_amd64/oscar-corrtest bin/linux_arm64/oscar-corrtest
tar -tzf dist/*linux_amd64.tar.gz
tar -tzf dist/*linux_arm64.tar.gz
```

Expected: every Plan 2 gate and the full Plan 1 CI contract pass; both binaries are static Linux ELF for the requested architecture; archives contain no database, WAL, secret, config, or evidence file.

- [ ] **Step 6: Commit**

```bash
git add internal/testoscar README.md docs/development.md scripts/test-standalone.sh Makefile
git commit -m "test: qualify the durable ledger foundation"
```

---

## Plan 2 Completion Evidence

Plan 2 is complete only when the final report records all of the following from fresh commands:

- exact `modernc.org/sqlite` and coupled `modernc.org/libc` versions;
- SQLite runtime version and effective WAL/foreign-key/busy-timeout/synchronous values;
- fresh and upgrade migration success plus checksum/failure rejection;
- subprocess process-loss proof of durable `INTERRUPTED` state and one recovery event;
- concurrent WAL writer/UI reader proof;
- artifact traversal, symlink, atomic-write, missing, and hash-mismatch proof;
- canonical report restart/reopen and secret-absence proof;
- online backup under active writes, restore, and integrity proof;
- target and run CLI/UI restart persistence;
- `/readyz` 503 diagnostic behavior;
- CGO-free Linux AMD64/ARM64 builds, standalone archive build, security scans, race tests, and full `make ci` success;
- clean worktree after all Plan 2 commits.
