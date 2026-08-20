# OSCAR CorrTest Operator Experience and Service Design

**Date:** 2026-08-20  
**Status:** Draft implementation contract; all product decisions approved in conversation  
**Primary reader:** A fresh engineer implementing this release  
**Post-read action:** Produce and execute a complete implementation plan without relying on the design conversation  
**Related contracts:** The original correlation-harness design and the distribution/open-listener amendment remain authoritative except where this document explicitly extends them.

## 1. Purpose

OSCAR CorrTest already provides the correlation runner, evidence ledger, reports,
CLI, embedded web UI, cross-platform release archives, and user-scoped installers.
The next release makes those capabilities easier to operate as a long-running
local test appliance and makes scenario authoring understandable from the UI.

This release adds four connected capabilities:

1. A managed, user-scoped `.env` containing the default OSCAR API key.
2. Explicit user-level background-service commands on Linux, macOS, and Windows.
3. Technical reference material, built-in scenario inspection, and guided scenario authoring inside the web UI.
4. Structured application logs with bounded retention and an OTTO-style live log viewer.

The product remains one CGO-free Go executable with embedded server-rendered
templates and small vanilla-JavaScript enhancements. It does not acquire a
frontend build tool, a second server, or another persistent database.

## 2. Goals and non-goals

### 2.1 Goals

- Let an operator install the binary, configure one global OSCAR API key, and run tests without constructing credential-reference commands.
- Let the operator install, start, stop, restart, inspect, and uninstall a user-level background service through one stable CLI.
- Preserve the prior rule that binary installation does not silently start a process.
- Explain every UI page in precise OSCAR terms without sending the operator to external documentation for normal use.
- Make all built-in examples directly inspectable as canonical YAML.
- Let an operator clone an example into an editable custom scenario, validate it, and inspect its P01/N01 execution and evidence contract before a run.
- Put application configuration, service health, storage paths, and live logs in one dense Operations workspace.
- Preserve all existing scenario, run, evidence, cleanup, release, and compatibility invariants.

### 2.2 Non-goals

- Multi-user configuration ownership or per-user authorization inside one CorrTest process.
- A general OSCAR rule editor.
- Arbitrary code, templates, or executable assertions in scenario documents.
- A React, Vue, or other single-page application.
- Automatic service installation or startup from `install.sh` or `install.ps1`.
- Machine-wide services requiring root or Administrator access.
- Displaying a saved API key back to the browser.
- Persisting logs in SQLite or adding full-text log indexing.
- Treating the unauthenticated wildcard listener as safe on an untrusted network.

## 3. Approved experience direction

The interface is a dense engineering console for technical OSCAR operators.
It favors compact tables, explicit state, monospaced identifiers, and direct
access to evidence over onboarding tours or simplified wizard language.

Three approved sketches establish the visual contract:

| Area | Selected direction | Contract |
|---|---|---|
| Application shell | Documentation drawer | Preserve maximum working width. A compact `? Page reference` action opens precise technical help from the right. |
| Scenario authoring | Three-pane workbench | Keep example catalog, exact source, and compiled P01/N01 contract visible together at desktop widths. |
| Operations | Unified workspace | Show global credential state and service/storage health beside the live application log. |

The existing yellow-accented OSCAR/OTTO visual vocabulary, compact radii,
status colors, monospaced technical fields, and dark/light themes remain.
Responsive layouts may stack or hide secondary panes, but no capability may
become desktop-only.

## 4. Platform path contract

All persistent files are user-scoped. Platform resolution is centralized and
injectable in tests rather than scattered across commands.

| Purpose | Linux and macOS | Windows |
|---|---|---|
| Configuration root | `$XDG_CONFIG_HOME/oscar-corrtest`, falling back to `$HOME/.config/oscar-corrtest` | `%LOCALAPPDATA%\oscar-corrtest` |
| Managed environment | `<config-root>/.env` | `%LOCALAPPDATA%\oscar-corrtest\.env` |
| JSON configuration | `<config-root>/config.json` | `%LOCALAPPDATA%\oscar-corrtest\config.json` |
| State root | `$XDG_STATE_HOME/oscar-corrtest`, falling back to `$HOME/.local/state/oscar-corrtest` | `%LOCALAPPDATA%\oscar-corrtest\data` |
| Structured logs | `<state-root>/logs` | `%LOCALAPPDATA%\oscar-corrtest\data\logs` |

The service definition locations are:

- macOS: the current user's LaunchAgents directory, label `io.cmetech.oscar-corrtest`;
- Linux: the current user's systemd unit directory, unit `oscar-corrtest.service`;
- Windows: a current-user Task Scheduler entry named `OSCAR CorrTest`.

The platform resolver fails with an actionable error if required home or local
application directories cannot be determined. It never falls back to the
current directory.

## 5. Managed `.env` and configuration precedence

### 5.1 Managed key

The Settings surface manages one key:

```dotenv
OSCAR_API_KEY=<value>
```

The file may contain comments and unrelated settings. Replacing or clearing
`OSCAR_API_KEY` preserves every unrelated line and preserves the existing
newline style where practical. Writes use a same-directory temporary file,
flush, restrictive permissions where supported, and atomic rename. A failed
write leaves the prior file intact.

The parser accepts ordinary unquoted and quoted dotenv values. A value written
by the UI is bounded, cannot contain NUL or line breaks, and is escaped using a
single canonical form. Outer whitespace is not silently made part of the key.

### 5.2 Startup precedence

Effective configuration precedence remains:

```text
command-line option
→ process environment
→ managed .env
→ config.json
→ platform default
```

The managed file is loaded before the existing configuration loader runs. It
fills variables that are absent from the process environment; it does not
erase an explicitly supplied environment value.

The Operations page identifies the effective credential source as one of:

- managed `.env`;
- external process environment;
- not configured.

It never returns any part of the value. The UI shows only `Configured` or
`Not configured`; it does not show a suffix, hash, length, or other secret-derived
identifier.

### 5.3 Live replacement

Saving through the UI performs one logical operation:

1. Validate the submitted value.
2. Atomically update `.env`.
3. Update `OSCAR_API_KEY` in the running process.
4. Return a secret-free success state.

Subsequent OSCAR clients use the new value without restarting the process.
A test already in progress retains the client and credential it began with.
If an external startup environment originally overrode `.env`, the UI warns
that the external value will regain precedence on the next process start.

Clearing follows the same atomic order. The running variable is removed only
after the file update succeeds.

## 6. Global and per-target credential behavior

A target without an explicit credential reference resolves `OSCAR_API_KEY` at
the moment a new OSCAR client is created. This is the normal UI path.

An explicit per-target environment, file, or systemd credential reference
continues to override the global key. Existing stored target records and the
database schema do not change.

The Targets UI presents:

- `Global OSCAR_API_KEY` as the default credential choice;
- an expandable advanced section for the existing reference kinds;
- a direct link to Operations when the global key is absent.

No target, report, artifact, log record, HTML response, or SQLite row contains
the resolved credential value.

## 7. User-level background service

### 7.1 CLI

The binary adds this command surface:

```text
oscar-corrtest service install
oscar-corrtest service start
oscar-corrtest service stop
oscar-corrtest service restart
oscar-corrtest service status
oscar-corrtest service logs
oscar-corrtest service uninstall
```

`service install` creates or updates the current-user definition and enables
login startup, but it does not start the process immediately. `service start`
is the explicit start-now action. This extends, but does not weaken, the prior
installer contract: one-line binary installers still do not install or start
a service.

The generated service always:

- uses the absolute path to the current executable;
- invokes `serve` with no certificate or authentication requirement;
- relies on the managed `.env` and platform defaults;
- writes bootstrap stdout/stderr under the state log directory;
- runs as the current user;
- uses safely quoted, platform-native arguments;
- never embeds the OSCAR API key.

Lifecycle operations are idempotent. Starting an already-running service and
stopping an already-stopped service succeed without creating a duplicate
process. `service status` exits zero only when running, exits three for a known
stopped or not-installed state, and exits one for an operational/unknown error.
`service logs` prints the latest 200 application records and follows until
interrupted. `service uninstall` stops a running managed instance, removes only
the service definition, and preserves the binary, `.env`, configuration,
SQLite database, artifacts, reports, and logs.

### 7.2 Platform adapters

The service package exposes one platform-neutral manager and uses OS-specific
implementations behind it.

- macOS uses modern `launchctl bootstrap`, `bootout`, `kickstart`, and `print`
  operations against the per-user GUI domain.
- Linux uses `systemctl --user`, including daemon reload and enable/disable.
- Windows uses current-user Task Scheduler commands and a logon trigger.

All external-command execution is injected for deterministic tests. Status is
normalized to `unsupported`, `not-installed`, `stopped`, `starting`, `running`,
`failed`, or `unknown` with a secret-free diagnostic message.

### 7.3 UI controls

Operations shows normalized service status, platform mechanism, listener,
process identifier when available, uptime when available, and log location.

While the process is running, Stop and Restart actions may use the same service
adapter after the HTTP response has been committed. Starting a stopped service
remains a CLI or desktop action because the web UI is unavailable when the
process is stopped. Unsupported or foreground-only launches show the exact CLI
commands instead of presenting ineffective buttons.

## 8. Page documentation

Every primary page gets a compact `? Page reference` action. The drawer content
is technical and page-specific. It explains:

- what the page reads or mutates;
- required inputs and allowed values;
- the corresponding OSCAR resources and public API effects;
- naming and reserved-label behavior;
- evidence and verdict semantics;
- common failure states and cleanup implications;
- relevant CLI equivalents.

The full Reference page contains:

1. Installation and first start.
2. Target and global API-key configuration.
3. Running one pattern or the full built-in suite.
4. Runs, evidence, cleanup, reports, and exports.
5. Scenario schema and authoring reference.
6. All eight built-in correlation patterns.
7. Assertion kinds and negative-case deadline semantics.
8. Alert naming, categories, and reserved filter labels.
9. Service commands and platform paths.
10. Logs and troubleshooting.

Reference content lives with the embedded UI and ships in release archives.
Tests compare pattern documentation against the built-in pattern catalog so a
new pattern cannot silently omit its reference section.

## 9. Scenario workbench

### 9.1 Three-pane contract

At desktop widths, Scenarios contains:

1. A searchable catalog of all eight built-ins and every imported scenario.
2. The exact canonical YAML source for the selected definition.
3. A structured inspection of P01/N01 stimuli, expected evidence, generated
   rule/alert identity, and relevant filter labels.

Built-ins are read-only. `Clone as custom` creates an editable browser copy;
it does not change or shadow the built-in. Imported records reopen their
persisted original source and remain content-addressed.

### 9.2 Canonical built-in source

The scenario package gains one canonical encoder that uses the same wire model
as the strict decoder. Durations are emitted as Go duration strings, fields use
the public schema names, output order is stable, and protected labels are never
introduced into the editable source.

Every built-in must satisfy:

```text
Decode(Encode(Builtin(pattern))) == Builtin(pattern)
```

The encoder is also used by packaged examples and UI previews so three copies
of the schema cannot drift.

### 9.3 Validation and compilation

Source syntax validation remains local and non-mutating. Errors name the
scenario, case where applicable, and invalid field or budget without exposing
an internal stack trace.

Structural inspection works without an OSCAR target. A target-aware immutable
plan preview still requires a selected target and pipeline mode and uses the
existing compiler path. The UI distinguishes these two states instead of
claiming that target compatibility has been proved by structural inspection.

The compiled pane exposes, at minimum:

- pattern, rule name, group-by labels, and timing window;
- P01 and N01 event roles, order/delay or repeat count;
- every declared assertion and expected value;
- generated alert-name grammar;
- generated category;
- `oscar_test_run_id`, pattern, scenario, case, polarity, role, and rule labels;
- whether the selected mode is Phase A audit-only or Phase B dispatch.

Import remains explicit and persists only a validated source document.

## 10. Unified Operations workspace

Operations replaces the current minimal Settings page and contains three
logical areas in one responsive workspace.

### 10.1 Configuration

- Global API-key state and replacement/clear actions.
- Managed `.env` location and effective source.
- Current listener and unauthenticated-network warning where applicable.
- Database, artifact, configuration, state, and log paths.

The API-key input is a password field with autocomplete disabled. A blank
submission means no change. Clear is a separate explicit action.

### 10.2 Service state

- Installed/running state and platform manager.
- Process and uptime information when observable.
- Stop/Restart controls when supported.
- Exact CLI fallback commands.

### 10.3 Live logs

The selected log source appears beside configuration at desktop widths and
stacks below it at narrower widths. Controls include:

- allowlisted source selection;
- level filter;
- plain-text or regular-expression filter;
- pause/resume with buffered-record count;
- download of the selected current log;
- transport and source health.

## 11. Structured logging

### 11.1 Records and sinks

The application adopts `log/slog` and emits JSON records. A process-level
handler fans each sanitized record to:

- stderr for foreground use and service bootstrap capture;
- a bounded application log file;
- a 500-record in-memory ring used for immediate UI backfill and live delivery.

The default application log rotates at 10 MiB and retains five backups. The
rotation implementation is pure Go and safe for concurrent writers. Failure to
open the file degrades to stderr and a browser-visible logging-health warning;
it does not prevent correlation tests from running.

Important records include run ID, target ID, scenario, pattern, lifecycle
stage, verdict, cleanup state, status code, attempt, duration, and safe error
classification. Alert bodies and full OSCAR responses remain evidence
artifacts rather than ordinary application logs.

### 11.2 Secret exclusion

The logging boundary rejects or redacts fields whose keys represent API keys,
authorization headers, credentials, cookies, or submitted secret values.
Callers also follow a no-secret logging contract. Tests send sentinel values
through startup, settings, target resolution, OSCAR failures, and run failures
and assert absence from every sink and UI response.

### 11.3 SSE and source status

The web endpoint accepts only an allowlisted source identifier. A new
connection receives current safe source status, up to 500 recent complete
records, and then live records. Keepalives preserve idle connections.

Source states are `opening`, `missing`, `unreadable`, `empty`, and `watching`.
The browser separately renders transport state (`connecting`, `connected`, or
`reconnecting`) and file/source state. Missing or unreadable files remain
recoverable without closing the SSE connection.

Slow subscribers cannot block the logger. They may drop intermediate records,
but the stream reports a drop count and reconnect backfill restores the latest
bounded view. Source switching clears client-side rows and filter state before
opening the next allowlisted stream.

## 12. Web routes and mutation rules

The server-rendered route family is extended conceptually as follows:

| Method and route | Purpose |
|---|---|
| `GET /operations` | Render credential, service, storage, and log workspace. |
| `POST /operations/api-key` | Atomically replace the managed key. |
| `POST /operations/api-key/clear` | Atomically remove the managed key. |
| `POST /operations/service/{action}` | Stop or restart through the user service adapter. |
| `GET /operations/logs/stream` | SSE backfill, health, and live records for an allowlisted source. |
| `GET /operations/logs/download` | Download the selected current allowlisted log. |
| `GET /reference` | Render full embedded technical documentation. |
| `GET /scenarios` | Render catalog, selected source, and inspection contract. |
| `POST /scenarios/clone` | Populate an editable copy without persistence. |
| Existing scenario preview/import routes | Validate, compile, and persist custom source. |

All mutations retain request-size limits, same-origin verification, CSRF
validation, and output escaping. Existing bearer or trusted-proxy mode wraps
every new route. Downloads use fixed server-owned paths and never accept a
filesystem path from the request.

## 13. Network posture

The default listener remains unauthenticated HTTP on `0.0.0.0:8787` by explicit
product decision. The startup warning and Operations warning are expanded to
state that any reachable peer can view evidence and logs, replace the OSCAR API
key, create rules, inject alerts, and request service stop/restart.

This document does not reintroduce a tunnel, TLS, or UI token requirement.
Operators may still select loopback, bearer/TLS, or trusted-proxy modes using
the existing advanced flags.

Ease of use does not remove the existing CSRF, same-origin, CSP, secure-session,
trusted-proxy, or loopback Host protections where those modes apply.

## 14. Failure behavior

| Failure | Required outcome |
|---|---|
| `.env` is malformed at startup | Fail with the file and variable class, never the value. Do not partially apply it. |
| API-key replacement cannot be persisted | Keep both the file and active key unchanged; show an actionable error. |
| External environment overrides `.env` | Identify the override without exposing either value. |
| Service manager is unavailable | Report `unsupported` or `unknown` with the exact CLI/platform remedy. |
| Stop/restart request loses the response race | The action remains idempotent; status on the next start explains the last known transition. |
| Built-in encoding fails | Treat as an application defect and refuse the affected preview; never emit invalid YAML. |
| Custom scenario is invalid | Preserve the submitted source in the editor, show the validation error, and perform no target mutation. |
| Log file is unavailable | Continue on stderr, show degraded logging health, and keep run/evidence persistence functional. |
| SSE disconnects | Reconnect with bounded backfill and explicit transport state. |
| A log subscriber is slow | Do not block run execution; report dropped records. |

## 15. Backward compatibility

- Existing `serve` behavior, wildcard listener, advanced authentication flags,
  targets, runs, reports, artifacts, and SQLite data remain valid.
- No database migration is required.
- Targets with explicit credential references behave exactly as before.
- Targets without a reference gain the global `OSCAR_API_KEY` fallback.
- Existing manual foreground startup remains supported.
- Existing one-line installers still install only the binary and never start a process.
- Release archives add updated documentation and any service-definition examples
  but retain their current asset names and checksums contract.

## 16. Testing strategy

### 16.1 Platform and environment tests

- Resolve every documented path for Linux, macOS, and Windows with injected
  platform/environment inputs.
- Prove missing required roots fail rather than using a relative path.
- Cover dotenv comments, quoting, duplicates, line endings, malformed input,
  atomic replacement, clear, permissions, preservation, and rollback.
- Prove process environment precedence and immediate in-process replacement.

### 16.2 Credential tests

- A target with no reference uses global `OSCAR_API_KEY`.
- Every explicit reference kind overrides the global key.
- A saved key is used by the next doctor/run without process restart.
- An in-progress client retains its initial value.
- Secret sentinels never appear in SQLite, reports, artifacts metadata, HTML,
  JSON, logs, errors, or downloads.

### 16.3 Service tests

- Golden definitions for LaunchAgent, user systemd, and Windows Task Scheduler.
- Safe quoting for executable and data paths containing spaces.
- Install does not start now; start, stop, restart, status, logs, and uninstall
  call only the expected platform operations.
- Repeated install/uninstall and stop of an already-stopped service are safe.
- Cross-platform builds compile without executing the host's service manager.

### 16.4 Scenario and documentation tests

- Every built-in canonical YAML round-trips through the strict decoder.
- Catalog includes all built-ins and imported records with source available.
- Built-ins cannot be mutated; cloning creates an independent editable copy.
- Structural inspection requires no target and target-aware preview still uses
  the existing compiler.
- Compiled inspection exposes P01/N01 assertions and reserved filter labels.
- Every primary page has a reference drawer and every built-in pattern has a
  full Reference section.

### 16.5 Logging and browser tests

- Concurrent writes, rotation, retention, partial failures, and restart behavior.
- Ring backfill ordering, bounded memory, slow subscribers, and cancellation.
- SSE status, backfill-before-live ordering, keepalives, reconnect, source
  allowlisting, and download allowlisting.
- Level/text filtering, invalid regex handling, pause/resume, buffered count,
  source switching, dark/light themes, and responsive layout.
- Settings and service POST routes reject missing CSRF or wrong-origin requests.

### 16.6 Repository gates

- Existing unit, race, integration, release-gate, installer, reproducibility,
  and cross-compilation checks remain green.
- Release archives include updated operator/reference documentation.
- A clean installed binary can load `.env`, start in the foreground, install a
  user service definition, and execute a fake-OSCAR run using the global key.

## 17. Acceptance criteria

1. Windows uses `%LOCALAPPDATA%\oscar-corrtest\.env`; POSIX uses the documented XDG/fallback path.
2. Saving a global API key through Operations persists it atomically and the next OSCAR request uses it without restart.
3. No response or durable artifact exposes the key.
4. Existing targets with advanced references remain unchanged; empty references use the global key.
5. All seven service subcommands work through user-level platform adapters; install does not start the process immediately.
6. Every built-in scenario can be selected and viewed as canonical valid YAML.
7. The scenario workbench shows catalog, source, and P01/N01 contract together at desktop widths.
8. Operators can clone, edit, validate, preview, and import a custom scenario without mutating a built-in.
9. Every page has technical reference help and the full Reference page documents all eight patterns, labels, services, and troubleshooting.
10. Operations shows configuration/service health and a live structured log with backfill, filters, pause/resume, status, and download.
11. Log files are bounded and no log sink contains credential sentinels.
12. The wildcard unauthenticated posture remains explicit, functional, and loudly disclosed.
13. `make clean release-gate` and race-enabled tests pass after the change.

## 18. Implementation slices

The work is planned and executed in this dependency order:

1. **Platform paths and managed environment** — cross-platform locations,
   dotenv store, precedence, global credential fallback, and tests.
2. **User service lifecycle** — platform-neutral CLI, three OS adapters,
   generated definitions, status normalization, and documentation.
3. **Structured logging foundation** — sanitized slog handler, rotation, ring,
   subscribers, source status, and CLI log access.
4. **Console shell and embedded reference** — selected documentation drawer,
   Reference page, navigation organization, and page help content.
5. **Scenario workbench** — canonical encoder, built-in/imported catalog,
   three-pane inspection, cloning, validation, and compiler preview.
6. **Unified Operations workspace** — global API-key mutations, service status
   and actions, paths, SSE log viewer, filtering, pause, and download.
7. **Integration, packaging, and operator docs** — installed-service smoke,
   archive content, platform guides, README updates, and release-gate coverage.

Each slice is independently testable. The Operations UI depends on the path,
service, and logging foundations; it must not create alternate implementations
of those behaviors in the web package.

## 19. Implementation status (2026-08-20)

All seven slices are implemented on `feature/operator-experience`. The shipped
surface includes managed user paths and `.env`, global-key fallback, Linux,
macOS, and Windows user-service adapters, bounded redacted logs, page-level and
full reference guidance, canonical scenario inspection and cloning, the
Operations workspace, user-scoped inert installers, and five cross-compiled
release archives.

Verification completed against commit `7d67b14`:

- `go test -race -count=20 ./internal/integration ./internal/web ./internal/runtime`
- `make clean release-gate`, including format/module/vet/security/vulnerability
  checks, the full unit and race suites, standalone archive validation,
  installer smoke, exact archive manifests, checksums, and reproducibility
- Linux amd64/arm64, macOS amd64/arm64, and Windows amd64 compilation and
  packaging

Composition regressions prove that the write-only UI-managed key reaches a new
OSCAR client only as `X-API-Key`, canonical flood P01/N01 executes through the
independent semantic oracle with clean resource teardown, generated service
definitions use the packaged binary, and Operations log streaming preserves
ordered redacted backfill/live records. The final gate also caught and closed
a Windows Task Scheduler XML encoding mismatch and unbounded detached service
actions.

No live OSCAR qualification is claimed by these fake-server or semantic-model
tests. A controlled run against the intended disposable Phase-B OSCAR target
remains the target-specific release qualification step.
