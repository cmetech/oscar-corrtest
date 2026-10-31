# oscar-corrtest — Architecture Map

Module `github.com/cmetech/oscar-corrtest` (`go.mod:1`), Go 1.27, CGO-free.
Only two direct dependencies: `gopkg.in/yaml.v3` and `modernc.org/sqlite`
(`go.mod:5-8`). ~24k lines of Go, roughly half tests.

**What it is:** a self-contained, black-box qualification harness for OSCAR's
alarm-correlation engine. It compiles declarative scenarios into immutable
plans, creates temporary correlation rules on a live OSCAR, injects
deterministic alerts through the public API, reads back authoritative
server-side evidence (alert history / correlation audit / notification audit),
renders a verdict, then deletes everything it created and resolves every alert
it injected. It ships one binary carrying an embedded web UI, an embedded
SQLite schema, and its own service installer.

---

## 1. End-to-end lifecycle

```
target add ──► doctor ──► plan (compile, offline) ──► run ──► evidence ──► cleanup
```

1. **Target** — `oscar-corrtest target add --name … --url https://oscar/ext/mw
   --credential-env OSCAR_API_KEY` (`internal/command/commands.go:149`). Only a
   *credential reference* is persisted, never a secret
   (`internal/domain/target.go:25`, kinds `environment|file|systemd` at
   `internal/domain/target.go:16`). API profile is fixed to `public-v1`
   (`internal/oscar/client.go:43`).

2. **Doctor / preflight** — `Runtime.Doctor` (`internal/runtime/runtime.go:304`)
   compiles the `flood` builtin purely to obtain a real rule payload, then
   `diagnose` (`:325`):
   - `POST /api/v1/correlation_rules/validate` with the compiled P01 rule
     (`internal/oscar/client.go:117`);
   - injects **one** diagnostic probe alert
     `CORRTEST_PROBE_P00_SOURCE_<SHORT>` (`internal/oscar/request.go:120`) and
     polls `GET /api/v1/alerts/history` until exactly one record appears
     (`ProbeLabelSurvival`, `internal/oscar/client.go:355`), 20s budget
     (`runtime.go:336`);
   - compares every reserved label (except `severity`) between what was sent and
     what history returns; any drop lands in `MissingLabels`.
   `Compatible` = accepted ∧ history found ∧ non-empty server fingerprint ∧ no
   missing labels (`runtime.go:344`). Exit code 2 when incompatible
   (`commands.go:143`).

3. **Plan** — `oscar-corrtest plan builtin:flood --target … --pipeline-mode
   phase_b_dispatch`. `compiler.Compile` (`internal/compiler/compiler.go:73`)
   is completely offline: no OSCAR contact, no persistence
   (`runtime.go:156` / `:169`). It produces an immutable `Plan` with a SHA-256
   `Digest` over its own canonical JSON (`compiler.go:186-192`), a
   `MutationBudget{Rules, Alerts}`, per-case rule + alert payloads, and
   `Inspection` filters an operator can paste into OSCAR's UI (`compiler.go:181`).

4. **Run** — `Runtime.ExecuteBuiltin` / `ExecuteScenario`
   (`runtime.go:181` / `:211`) serialize under `runMu`, create a durable run
   row, compile, diagnose, then hand the plan to `runner.Runner.Execute`
   (`internal/runner/runner.go:125`). Status machine:
   `QUEUED → PREFLIGHT → SETTING_UP → INJECTING → OBSERVING → ASSERTING →
   CLEANING_UP → COMPLETED` (`internal/domain/run.go:12`, transitions gated by
   `CanTransition`, `run.go:148`). Hard gate at `runner.go:168`: unless
   `Ready ∧ LabelsSurvived ∧ PipelineMode == phase_b_dispatch`, the run
   terminates `INCONCLUSIVE` with **zero target mutation**
   (`finishWithoutMutation`, `runner.go:502`).

5. **Evidence** — per case the runner polls history + correlation audit
   (+ notification audit for `parent_child`) until assertions pass and hold
   through a stabilization re-read, or until the observation deadline
   (`internal/runner/observation.go:24`). Facts are normalized into
   `domain.ExecutionFacts`, written as a `normalized-oscar-evidence` artifact
   (`internal/runtime/evidence_writer.go:28`) and a canonical report JSON
   (`runner.go:531`).

6. **Cleanup** — `Runner.cleanup` (`runner.go:289`) deletes only rule IDs OSCAR
   returned, then resolves every observed alert using the **server** fingerprint
   read back from history (`ResolveHistory`, `client.go:198`;
   `BuildResolutionRequest`, `request.go:96`). Cleanup status is tracked
   independently of verdict (`CLEAN|DIRTY|NOT_REQUIRED|UNKNOWN`,
   `run.go:40`). A dirty run exits 4 and is retryable via
   `cleanup retry` → `Runtime.RetryCleanup` (`runtime.go:493`), which re-reads
   each rule and refuses look-alikes (`ownedRule`, `runtime.go:696` requires
   `run=<runID>` inside the rule description).

Exit-code contract (`internal/command/execution_commands.go:295`):
`0` pass/skipped · `1` fail · `2` inconclusive/usage · `3` other error ·
`4` cleanup dirty/unknown (dominates the verdict).

---

## 2. Packages

### `cmd/`
| Path | Role |
|---|---|
| `cmd/oscar-corrtest/main.go:23` | Entrypoint. Resolves platform paths, opens the managed `.env`, opens the rotating JSON log, builds the service-manager factory and the `open` runtime factory, then `command.NewApplication(...).Run` (`main.go:66`). |
| `cmd/generate-scenario-schema/main.go:10` | Regenerates `docs/schema/correlation-scenario.schema.json` from `scenario.GenerateJSONSchema()`. |
| `cmd/package-zip/main.go` | Deterministic zip build/list helper for Windows release archives (`internal/releasearchive`). |

### `internal/command` — CLI
`App` (`app.go:42`) with injected `Dependencies{Serve, Open, Getenv, Service,
Logger}` (`app.go:54`). `App.Run` dispatches on `args[0]` (`app.go:82-121`).
Runtime capabilities are discovered through *optional interfaces* asserted on
the opened runtime — `doctorRuntime` (`commands.go:100`), `retentionRuntime`
(`commands.go:20`), `evidenceRuntime` (`commands.go:391`), `cleanupRuntime`
(`commands.go:396`), `runDeletionRuntime` (`commands.go:254`),
`correlationRuntime` / `customCorrelationRuntime`
(`execution_commands.go:18`/`:23`), `scenarioCatalogRuntime`
(`execution_commands.go:85`). `help.go:23` holds the whole help corpus, which
`internal/docs/operator_docs_test.go` cross-checks against the Markdown docs.

### `internal/config` — effective settings
`Load`/`LoadForOS` (`config.go:43`/`:48`). Precedence: **defaults → config file
→ env → flags**. Default listen `0.0.0.0:8787` (`config.go:76`); env
`OSCAR_CORRTEST_DATA_DIR`, `OSCAR_CORRTEST_LISTEN` (`config.go:93-98`). The
JSON config file is strict: unknown fields rejected, trailing documents
rejected, `apiVersion` must equal `corrtest.oscar/v1alpha1`
(`config.go:134-148`). Data dir must be absolute (`config.go:105`).

### `internal/platformpaths` — on-disk contract
`Resolve(goos, lookup)` (`paths.go:174`) returns `Paths`
(`paths.go:162`): ConfigDir, EnvFile, ConfigFile, StateDir, LogDir,
ApplicationLog, BootstrapLog, ServiceDefinition. See §7 for values.

### `internal/envfile` — managed `.env`
`Store` (`store.go:47`) with `Open` (`:58`), `Getenv` (`:91`), `Status` (`:107`),
`Replace` (`:125`), `Clear` (`:134`). Write-only from the UI: values are
rewritten atomically (`atomicWrite`, `:252`), keys must match
`^[A-Z_][A-Z0-9_]*$` (`store.go:22`), and `Source` records whether a value came
from the process environment or the file (`store.go:27`).

### `internal/applog` — redacted structured logging
`System` (`system.go:52`), `Open(logDir, stderr, Options)` (`:66`),
`Logger(source)` (`:107`), ring buffer `Recent` (`:112`), live `Subscribe`
(`:128`), `Sources`/`OpenSource` for downloads (`:155`/`:177`). Custom
`slog.Handler` (`:243`) redacts secret-looking keys (`secretKey`, `:316`).
Primary file `application.jsonl`; rotation with 5 × 10 MiB backups
(`rotate.go`).

### `internal/domain` — pure types + invariants
`Run`/`RunStatus`/`Verdict`/`CleanupStatus` and the transition table
(`run.go:10-170`); `Target`, `TargetInput.Validate`, `CredentialRef`, `TLSPolicy`
(`target.go`); `ExecutionFacts`, `CaseFact`, `AssertionFact`,
`AlertAttemptFact`, `AlertResolutionFact` (`evidence.go`); `Artifact` +
integrity states (`artifact.go`); `Resource` lifecycle
`PROPOSED→CREATED→…/UNKNOWN` (`resource.go`); ULID-ish run IDs `crt_<26>`
(`id.go:45`, pattern enforced again at `internal/artifact/store.go:18`).

### `internal/scenario` — declarative input, closed by construction
- `model.go` — `Scenario`/`Case`/`Event`/`Assertion` (`model.go:7-46`).
- `contract.go` — every limit as a constant (`contract.go:5-26`:
  `RequiredCaseCount = 2`, `MaxScenarioDuration = 5m`, `MaxCaseWindow = 2m`,
  `MaxEvents = 100`, `MaxDocumentBytes = 1 MiB`, …), the 8 supported patterns
  (`:66`), the 21 **reserved labels** operators may not set (`:68-73`), and
  `PublicContract()` (`:98`) — the machine-readable field/pattern reference that
  drives the docs, the UI reference page, and the JSON Schema.
- `decode.go` — strict YAML/JSON decoding (`Decode`, `:94`; `validate`, `:190`),
  YAML alias/anchor rejection (`containsAlias`, `:357`), mutually-exclusive
  `role+repeat` vs `events` detection via raw-node presence
  (`findCaseFieldPresence`, `:320`).
- `schema.go` — `GenerateJSONSchema()` (`:61`) builds the checked-in
  `docs/schema/correlation-scenario.schema.json` from `PublicContract()`;
  `schema_contract_test.go` fails on drift.
- `builtin.go` — the eight built-in scenarios (`Builtin`, `:17`;
  `AllBuiltins`, `:8`).
- `examples.go` — the 16 basic/advanced authoring examples used by `/authoring`.

### `internal/compiler` — scenario → immutable plan
`Compile` (`compiler.go:73`). Key behaviors:
- Refuses any pipeline mode other than `phase_a_audit_only` / `phase_b_dispatch`
  (`:77`); refuses non-2-case scenarios (`:81`).
- Rule name: `corrtest-<pattern>-<casecode>-<shorttoken>` (`:113`).
- Alert name: `CORRTEST_<PATTERNCODE>_<CASECODE>_<ROLE>_<SHORT>`
  (`physicalName`, `:218`; pattern codes at `:20`).
- Stamps the full identity label set on every alert (`identityLabels`, `:276`)
  and per-event `oscar_test_event_id` / `oscar_test_event_index` (`:164`).
- Rejects reserved-label overrides at case *and* event level (`:100`, `:159`).
- `resolved` events must follow a firing event for the same role and may not
  change identity labels (`:142-151`).
- Per-pattern OSCAR `match_criteria` (`matchCriteria`, `:228`) — see §4.
- Observation window = case window + 15s, except `absence` = fixed 55s (`:132`).
- `parent_child` emits **no** synthetic alert (`EmitAlertName` cleared, `:129`).

### `internal/oscar` — the only network adapter
- `client.go:42` `New` — enforces `public-v1`, builds a TLS 1.2+ transport,
  optional per-target CA (`:61`) or `InsecureSkipVerify` (`:58`), 30s timeout.
- `resolveCredential` (`:85`) — env / file / systemd (`CREDENTIALS_DIRECTORY`),
  default env var `OSCAR_API_KEY`.
- `do` (`:414`) — sets `X-API-Key`, `User-Agent: oscar-corrtest/<ver>`, caps
  responses at 4 MiB (`:24`), converts non-2xx into `MachineError` with a
  512-char sanitized detail (`safeDetail`, `:456`) so credentials/bodies never
  leak.
- Paginated readers bounded to 100 pages (`maxListPages`, `:25`;
  `paginationLimitError`, `:349`) with exact-name post-filtering
  (`FindRules`, `:151`; `FindHistory`, `:257`).
- `sendAlert` (`:206`) classifies the response into
  `accepted|rejected|queued|partial|indeterminate` (`types.go:15`) from status,
  counts, and message text.
- `request.go` — exact wire bodies: `RuleRequest` (`:16`, hard-codes
  `priority=100`, `max_synthetic_per_minute=10`, `enabled=true`,
  `created_by=oscar-corrtest/<ver>`), `AlertGroupRequest` (`:37`),
  `BuildResolutionRequest` (`:96`), `BuildLabelProbeAlert` (`:120`).
  `alertmanagerTransportFingerprint` (`:148`) is a transport-only hash and
  explicitly refuses pre-stamped `oscar_fingerprint`/`am_fingerprint` labels.
- `preview.go` — renders the exact request sequence (stages
  `preflight.validate_rule`, `preflight.inject_label_probe`,
  `preflight.read_history`, `setup.validate_rule`, …) for the UI/authoring
  panes without touching the network.

### `internal/runner` — serial, recoverable execution
`Runner` (`runner.go:65`) over narrow `API` / `Store` / `EvidenceWriter`
interfaces (`:18`, `:31`, `:42`). `Execute` (`:125`) wraps everything in a
`context.WithTimeout(plan.MaxDuration)` and re-checks a wall-clock
`budgetDeadline` before every mutation (`budgetError`, `:137`). Rule creation
reconciles an ambiguous create by exact-name lookup and refuses anything that
does not round-trip name+pattern+description (`:190-204`).
`observation.go:24` `observeCase` — poll → evaluate → (optional stabilization
re-read) → verdict; `requiresFullWindow` (`assertions.go:59`) forces the whole
window whenever an assertion expects `equals: 0`, so negative controls cannot
pass early. `assertions.go:19` implements exactly three assertion kinds and
exact-count equality only. `discoverAttemptedHistory` (`runner.go:410`) recovers
server identities for alerts that were sent before a failure so cleanup can
still resolve them.

### `internal/runtime` — composition root
`Runtime` (`runtime.go:57`) owns settings, SQLite, artifact store, history
service, log system, operations controller, and the active-run cancel map.
`OpenWithOptions` (`:89`) creates a `0700` data dir, opens
`<data>/corrtest.db`, and recovers interrupted runs on start (`:128`).
Public surface: Preview/Execute/Start builtin+scenario, `CancelRun` (`:285`),
`Doctor` (`:304`), `ExportRun` (`:452`), `VerifyBundle` (`:488`),
`RetryCleanup` (`:493`), `ImportScenario` (`:590`, dedupes by SHA-256 and
re-decodes the raw bytes so callers cannot pair arbitrary source with a parsed
document), `PreviewRetention`/`ApplyRetention` (`:624`/`:645`, cap 500),
`DeleteRun` (`:661`, artifact-verified, staged, rollback-safe).
`StartBuiltin` (`:241`) is the UI path: it validates first, then runs detached
on the *process root* context so a closed browser cannot orphan a run.

### `internal/persistence/sqlite` — durable ledger
`Open` (`database.go:28`) requires an absolute path, chmods `0600`, requires
SQLite ≥ `3.51.3` (`database.go:18`), runs `PRAGMA integrity_check` and degrades
to a not-ready state rather than crashing. `migrations.go:30` applies embedded
`migrations/*.sql` inside transactions, verifying each applied migration's
recorded SHA-256 (`:49`) and refusing a database newer than the binary (`:80`).
Repositories: `target_repository.go`, `run_repository.go` (the biggest, 502
lines — transitions, finalization, resource ledger, retention), `artifact_repository.go`,
`scenario_repository.go`, `execution_repository.go`, `resource_repository.go`,
plus online `backup.go`.

### `internal/artifact` — traversal-safe local file store
`Store` (`store.go:38`) rooted at the data dir; run IDs must match
`^crt_[0-9A-HJKMNP-TV-Z]{26}$` (`:18`). `Write` (`:71`) hashes and fsyncs;
`Verify` (`:140`) returns `valid|missing|hash-mismatch`; `StageRunDeletion`
(`:176`) + `Rollback`/`Finalize` (`:231`/`:239`) make run deletion atomic.
Path validation at `:246`/`:262`, parent-directory verification at `:298`.

### `internal/evidence` — portable bundles
`WriteWithArtifacts` (`bundle.go:90`) builds a deterministic ZIP containing
`events.json`, `junit.xml`, `plan.json`, `report.html`, `report.json`,
attachments (normalized evidence renamed `normalized-evidence.json`,
`runtime.go:479`), and finally `manifest.json` with a SHA-256 per entry
(`bundle.go:74-83`). Bundle apiVersion `corrtest.oscar/bundle-v1` (`:23`).
`Verify` (`:214`) re-hashes every member without trusting filenames. Only
`COMPLETED` runs with a valid verdict are exportable (`:91`).

### `internal/history` — thin durable service
`Service` (`service.go:35`) — `CreateRun` (`:53`), `GetRun`, `ListRuns`,
`ListRunEvents`, `RecoverInterruptedRuns` (`:92`), target CRUD (`:97`-`:133`).

### `internal/operations` — operator control plane
`Controller` (`controller.go:33`) binds config paths + `.env` store + service
manager + log system. `Snapshot` (`:46`) exposes effective paths, API-key
presence/source, and service state; `ReplaceAPIKey`/`ClearAPIKey` (`:67`/`:81`)
are write-only; `ServiceAction` (`:92`); `RecentLogs`/`SubscribeLogs`/
`OpenLogSource` (`:118`/`:129`/`:139`).

### `internal/service` — user-scoped service manager
`Manager` interface (`service.go`), `manager` impl (`manager.go:16`) with
`Install/Start/Stop/Restart/Status/Logs/Uninstall` and `DefinitionPath`.
`render.go` templates the unit/plist/Task-XML; `runner.go` is the exec seam;
`parseStatus` (`manager.go:245`) normalizes systemd/launchctl/schtasks output.
`Install` never starts; `Uninstall` removes only the definition.

### `internal/authoring` + `internal/web/help.go` — guided authoring
`authoring.Service.Build(Selection)` (`service.go:76`) assembles the
Quickstart / Schema / Patterns / Assertions / Validation guide from
`scenario.PublicContract()` and `scenario.Examples`; `Inspect` (`:102`)
compiles pasted source and returns the OSCAR-request preview.
`catalog.go` merges builtins + examples + imported records.

### `internal/report`, `internal/version`, `internal/releasearchive`, `internal/testoscar`
`report.Build` (`report.go:73`) produces the `corrtest.oscar/v1alpha1` report
document embedded in bundles. `version.Current()` is `-ldflags`-injected
(`Makefile:12`). `releasearchive` writes reproducible zips.
`testoscar` (`server.go`, `model.go`) is a deterministic fake OSCAR used by the
adapter and runner tests — no live target needed for `make test`.

### `internal/integration`
`operator_experience_test.go` — full in-process end-to-end: CLI → runtime →
web UI → fake OSCAR, including SSE, service actions, and log redaction.

---

## 3. CLI surface (derived from `app.go:82` + flag sets)

Global: `oscar-corrtest help [command]`, `--help`, `-h` (`HandleHelp`,
`help.go`). Two output modes everywhere: `--output human|json`; JSON is always
wrapped as `{"apiVersion":"corrtest.oscar/v1alpha1","data":…}`
(`commands.go:550`). `--config` and `--data-dir` are accepted by every
runtime-backed command (`commonConfigFlags`, `commands.go:508`).

| Command | Flags / args | Source |
|---|---|---|
| `version` | — | `app.go:88` |
| `serve` | `--listen`, `--config`, `--data-dir`, `--remote-mode bearer\|trusted-proxy`, `--auth-token-env`, `--auth-token-file`, `--auth-token-systemd`, `--proxy-header`, `--proxy-value`, `--trusted-proxy <CIDRs>`, `--tls-cert`, `--tls-key` | `app.go:123-181` |
| `service install\|start\|stop\|restart\|status\|uninstall` | no args | `service_commands.go:11` |
| `service logs` | `--lines N` (default 200), `--no-follow` | `service_commands.go:54` |
| `target add` | `--name`, `--url`, `--api-profile` (default `public-v1`), `--credential-env`, `--credential-file`, `--credential-systemd` (at most one), `--ca-file`, `--insecure`, `--output` | `commands.go:149` |
| `target list` | `--output` | `commands.go:204` |
| `doctor` | `--target` (req), `--pipeline-mode` (req), `--output` | `commands.go:104` |
| `scenario list` | `--output` (builtins + imported) | `execution_commands.go:41` |
| `scenario validate <file>` | `--output` | `execution_commands.go:141` |
| `scenario import <file>` | `--output` (≤1 MiB, regular file) | `execution_commands.go:90` |
| `plan <builtin:pattern\|file>` | `--target` (req), `--pipeline-mode` (req), `--output` | `execution_commands.go:177` |
| `run <builtin:pattern\|file>` | same as `plan` | `execution_commands.go:181` |
| `runs list` | `--target`, `--status`, `--verdict`, `--cleanup`, `--pattern`, `--output` | `commands.go:289` |
| `runs show <id>` | `--output` | `commands.go:331` |
| `runs delete <id>` | `--yes` (required) | `commands.go:258` |
| `cleanup retry <run-id>` | — | `commands.go:400` |
| `retention preview` | `--before <RFC3339>` (req) — `--yes` forbidden | `commands.go:25` |
| `retention apply` | `--before <RFC3339>` (req), `--yes` (req) | `commands.go:25` |
| `export <run-id>` | `--output <new-zip>` (req) | `commands.go:441` |
| `verify-bundle <zip>` | — | `commands.go:478` |
| `backup` | `--output <path>` (req) | `commands.go:361` |

`--pipeline-mode` accepts exactly `publication_disabled`, `phase_a_audit_only`,
`phase_b_dispatch`, `unknown` (`execution_commands.go:286`).

Serve-flag safety rules (`app.go:185-229`): remote/TLS flags require
`--remote-mode`; `bearer` requires TLS **and** exactly one credential reference
≥16 bytes (`app.go:231-271`); `trusted-proxy` requires a canonical header, an
exact value, and ≥1 CIDR, and rejects bearer flags. Serving unauthenticated on a
non-loopback address prints an explicit warning (`app.go:160`).

---

## 4. Correlation patterns, builtins, and the scenario schema

Eight patterns (`scenario/contract.go:66`), each compiled to fixed OSCAR
`match_criteria` (`compiler/compiler.go:228`):

| Pattern | Compiled criteria | P01 (positive) | N01 (negative) |
|---|---|---|---|
| `flood` | `{match:{alertname}, min_count:5}` | role `interface_down` × **5** | × **4** |
| `co_occurrence` | `{required_matches:[…], min_matches:n}` | `disk_full` + `cpu_high` (+1s) | `disk_full` only |
| `sequence` | `{sequence:[login_failure, privileged_command]}` | correct order | reversed order |
| `persistence` | `{match, unresolved_for_seconds:30}` | `service_down` stays firing | resolved at +10s |
| `absence` | `{expected_match, expected_every_seconds:10, absent_for_seconds:30}` | one heartbeat then silence | heartbeats every 8s to +48s |
| `parent_child` | `{parent_match, child_matches, suppress_children_for_notifiers, tag_children_for_notifiers}` | parent then child (+1s), suppress `email` | child alone |
| `cross_source` | `{required_sources:[snmp, api]}` | `oscar_source` snmp + api | snmp twice |
| `threshold` | `{match, distinct_label:"device", min_distinct_count:3}` | devices d1/d2/d3 | d1 twice |

Builtins live in `scenario/builtin.go:17`: name `<pattern>-basic`, suite
`builtin-all`, `maxDuration 90s`, case window `30s`, `groupBy: [site]`, site
label `corrtest-p01` / `corrtest-n01`. Default assertions are
`audit-count outcome=parent_emitted equals=1` + `synthetic-alert-count equals=1`
for P01 and `equals: 0` for N01 (`builtin.go:20-27`); `parent_child` instead
asserts `parent-link-count outcome=suppressed_per_notifier equals=1` (P01) and
`audit-count outcome=released_no_trigger equals=1` (N01) (`builtin.go:57-58`).

**Schema** — `docs/schema/correlation-scenario.schema.json` (494 lines, JSON
Schema draft 2020-12, `$schema` L2, root `additionalProperties:false` L7),
generated from `scenario.PublicContract()` (`scenario/schema.go:61`) and
drift-tested by `schema_contract_test.go`. Regenerate with
`go run ./cmd/generate-scenario-schema && git diff --exit-code docs/schema/…`.

- Scenario: `apiVersion` (const `corrtest.oscar/v1alpha1`), `kind` (const
  `CorrelationScenario`), `name`, `suite` (1–100 chars), `pattern` (enum of 8),
  `maxDuration` (Go duration, >0, ≤5m), `cases` (**exactly two**: `P01` and
  `N01`). All required; `additionalProperties: false`.
- Case: `name` (unique, ≤120), `code` (`P01|N01`), `polarity`
  (`positive|negative`, must match the code), `window` (>0, ≤2m), optional
  `groupBy` (≤16 unique), optional `labels` (≤64, non-reserved), conditional
  `role`+`repeat` (1–100) **XOR** `events` (1–100), `parent_child`-only
  `suppressForNotifiers`/`tagForNotifiers` (≤16, disjoint), required
  `assertions` (1–32).
- Event: `role`, `status` (`firing|resolved`), optional `labels`, optional
  `delay` (absolute non-decreasing offset within budget; a `resolved` event must
  follow a `firing` one for the same role and may not change labels).
- Assertion: `kind` ∈ {`synthetic-alert-count`, `audit-count`,
  `parent-link-count`}, `outcome` (required for the two audit kinds, forbidden
  for `synthetic-alert-count`), `equals` (0–100, exact count).

Key schema line refs: root `required` L8-16; `cases` `minItems/maxItems: 2` with
an `allOf` requiring exactly one `P01/positive` and one `N01/negative` L21-86;
`pattern` enum L103-114; `maxDuration` `x-corrtest-format: go-duration`, max
`5m0s` L90-96; `$defs.assertion` L123-191 (outcome forbidden for
`synthetic-alert-count` L162-168, required for the audit kinds L184-188);
`$defs.case` L192-359 with the `role`+`repeat` **XOR** `events` `oneOf` at
L327-358; `$defs.event` L360-448 with "resolved ⇒ no labels" at L427-447; the
root `allOf` L450-493 forbidding notifier fields on the seven non-`parent_child`
patterns.

21 **reserved labels** are compiler-owned and rejected in scenarios
(`contract.go:68-73`, mirrored in the schema at L250-272 and L389-411):
`alertname`, `am_fingerprint`, `category`, `oscar_fingerprint`, `severity`, and
the full `oscar_test_*` family.

Several rules are **not expressible in JSON Schema** and live only in the strict
Go decoder (`scenario/decode.go`, documented at `docs/scenario-authoring.md:249-256`):
suppress/tag disjointness, declaration-order and prior-firing identity checks,
duration budget arithmetic, `cases[].name` uniqueness, and YAML
duplicate-key / alias / multi-document rejection.

---

## 5. How it talks to OSCAR

Base URL is the **external middleware root**, normally `https://host/ext/mw`
(`docs/operator.md`); pointing at an internal service URL bypasses OSCAR RBAC
and is documented as not a valid qualification. Auth is a single
`X-API-Key` header (`client.go:436`) resolved from an env/file/systemd
reference; default env var `OSCAR_API_KEY` (`client.go:91`).

Endpoints (`internal/oscar/preview.go:14-19`):

| Path | Method | Use |
|---|---|---|
| `/api/v1/correlation_rules/validate` | POST | preflight + per-case rule validation |
| `/api/v1/correlation_rules` | POST / GET / GET `{id}` / DELETE `{id}` | create, paginated exact-name search, read-back, delete |
| `/api/v1/alerts` | POST | inject source alerts, probe alert, and cleanup resolutions |
| `/api/v1/alerts/history` | GET | authoritative fingerprint + label read-back |
| `/api/v1/correlation_rules/audit` | GET | correlation outcomes per fingerprint |
| `/api/v1/notification-audit/` | GET | notifier evidence (parent_child only) |

**Pipeline-mode semantics.** OSCAR does not expose its correlator pipeline mode
publicly, so the mode is an *operator declaration* recorded in every run
(`docs/operator.md`). `publication_disabled` and `unknown` are accepted by the
CLI but rejected by the compiler (`compiler.go:77`). `phase_a_audit_only`
compiles but the runner still refuses to mutate unless the mode is
`phase_b_dispatch` (`runner.go:168`) — only Phase B can produce synthetic
parents and notifier outcomes.

**Fingerprint discipline.** CorrTest never trusts the transport fingerprint it
computes for the Alertmanager envelope (`request.go:146-148`); every assertion,
audit lookup, and resolution keys off the fingerprint OSCAR returns in history.
Alerts carrying pre-stamped `oscar_fingerprint`/`am_fingerprint` are refused
(`request.go:154`).

**Rule lifecycle.** Exactly two rules per run (P01/N01), named
`corrtest-<pattern>-<case>-<short>`, described with `run=<runID> scenario=… case=…`
(`compiler.go:137`). Every rule is recorded in the local `resources` ledger
before creation (`runner.go:181`) so an interrupted create is still reconcilable.
Deletion only ever uses an ID OSCAR returned, or an exact-name match whose
description proves run ownership (`runtime.go:696`). A 404 on delete is success
(`client.go:180`).

**Evidence collection.** Per poll: history for every expected `alertname`
filtered to `oscar_test_run_id == runID` and expected `oscar_test_event_id`
(`observation.go:94-121`); correlation audit per source fingerprint filtered to
the run's own rule name (`observation.go:122-138`); synthetic ("parent") history
when the rule has an `emit_spec` (`observation.go:153`); notification audit for
`parent_child` (`observation.go:139`). Readiness flags `SourceReady`/`AuditReady`
must both be true or the case is `INCONCLUSIVE` rather than `FAIL`
(`observation.go:85`).

---

## 6. Embedded web UI

Started by `web.Run` (`server.go:1091`); handler built in
`NewHandlerWithOptions` (`server.go:163`). Templates are Go `html/template`
files embedded from `internal/web/templates/*.tmpl` (17 templates: dashboard,
targets, runs, run_detail, run_test, scenarios, authoring + 4 authoring
sub-templates, operations, settings, reference, help_drawer, confirm_dialog,
base). Static assets embedded via `internal/web/assets.go`.

Routes (`server.go:182-798`), Go 1.22 method-prefixed patterns:

| Route | Purpose |
|---|---|
| `GET /healthz`, `GET /readyz` | liveness / readiness (`:182`, `:185`) |
| `GET /static/` | embedded assets, no-cache (`:193`) |
| `GET /{$}` | dashboard (`:194`) |
| `GET /targets`, `POST /targets` | list / create target (`:197`, `:213`) |
| `GET /runs`, `POST /runs` | filtered history / start a run (`:246`, `:470`) |
| `GET /run-test` | run launcher (`:268`) |
| `GET /runs/{id}` | run detail (`:497`) |
| `POST /runs/{id}/cancel`, `POST /runs/{id}/delete` | (`:528`, `:549`) |
| `GET /runs/{id}/export` | streams an evidence bundle (`:570`) |
| `GET /runs/{id}/events` | **SSE** live timeline (`:599`) |
| `GET /authoring` | authoring guide (`:283`) |
| `GET /scenarios`, `POST /scenarios`, `POST /scenarios/clone`, `POST /scenarios/{id}/delete` | workbench (`:296`, `:349`, `:323`, `:445`) |
| `GET /operations` | operator console (`:641`) |
| `POST /operations/api-key`, `POST /operations/api-key/clear` | write-only key (`:654`, `:677`) |
| `POST /operations/service/{action}` | service control (`:694`) |
| `GET /operations/logs/{source}` | download `application`/`bootstrap` log (`:725`) |
| `GET /operations/events` | **SSE** live log stream (`:743`) |
| `GET /settings`, `GET /reference` | (`:790`, `:793`) |

**Default port 8787**, bound to `0.0.0.0` (`config.go:76`; `Containerfile:14`
exposes 8787).

**Auth model** (`internal/web/auth.go`): three modes (`auth.go:20`) —
`SecurityNone` (default; guarded by a strict Host allowlist when the listener is
loopback, `server.go:1049`/`:1067`), `bearer` (TLS mandatory, token ≥16 bytes,
8-hour HMAC session in an `HttpOnly; Secure; SameSite=Strict` cookie named
`corrtest_session`, `auth.go:65`/`:150`), and `trusted-proxy` (canonical header
+ exact value + peer-IP CIDR allowlist, `auth.go:30-33`). All mutating routes
require a CSRF token (`csrfToken`/`validCSRF`, `server.go:995`/`:1011`) **and**
same-origin (`sameOrigin`, `:1023`). Every HTML response carries a nonce-based
CSP with `frame-ancestors 'none'` and `base-uri 'none'` (`server.go:971`).
Unauthenticated mode is deliberate — the docs treat the network/firewall as the
boundary (`docs/operator.md`, "Serving modes").

---

## 7. Persistence and on-disk layout

**SQLite** at `<StateDir>/corrtest.db`, mode `0600`, directory `0700`
(`runtime.go:93-100`, `database.go:49`). Schema from two embedded migrations:

- `migrations/0001_initial.sql` — `targets`, `scenarios`, `runs`, `run_cases`,
  `run_events`, `alert_attempts`, `assertions`, `resources`, `artifacts`, plus
  7 indexes (lines 1–137). All tables `STRICT`.
- `migrations/0002_attempt_results.sql` — attempt outcome columns.
- Ledger table `schema_migrations(version, name, sha256, applied_at)`
  (`migrations.go:35`) with tamper and forward-version detection (`:49`, `:80`).

Pragmas: WAL, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=FULL`
(`database.go`, `docs/development.md`). Local filesystem only; one process per
data directory. Data-dir layout:

```
<data-dir>/
  corrtest.db  corrtest.db-wal  corrtest.db-shm
  runs/<run-id>/…            # artifacts; NOT included in `backup`
```

On startup any run left active is marked `INTERRUPTED` with a recovery event;
alert injection is never silently resumed (`history/service.go:92`).

`resources` is the **safety ledger**: a row exists before a rule is created, so
an interrupted create is reconcilable (`runner.go:181`, lifecycle in
`domain/resource.go`).

**Artifacts** live under the data dir, written and verified by
`internal/artifact` (SHA-256 + byte size per file; run-scoped paths keyed by the
`crt_…` run ID).

**Evidence bundles** are portable ZIPs produced by `export`
(`evidence/bundle.go:90`) containing `manifest.json`, `report.json`,
`report.html`, `plan.json`, `events.json`, `junit.xml`, and
`normalized-evidence.json`; `verify-bundle` re-hashes every entry offline
(`bundle.go:214`). SHA-256 proves integrity, not publisher identity
(`docs/live-qualification.md`).

**Paths** (`platformpaths/paths.go:174`):

| | Linux | macOS | Windows |
|---|---|---|---|
| Config dir | `$XDG_CONFIG_HOME/oscar-corrtest` (default `~/.config/oscar-corrtest`) | same | `%LOCALAPPDATA%\oscar-corrtest` |
| `.env` | `<config>/.env` | same | `%LOCALAPPDATA%\oscar-corrtest\.env` |
| `config.json` | `<config>/config.json` | same | `…\oscar-corrtest\config.json` |
| State dir | `$XDG_STATE_HOME/oscar-corrtest` (default `~/.local/state/oscar-corrtest`) | same | `…\oscar-corrtest\data` |
| Logs | `<state>/logs/application.jsonl`, `service-bootstrap.log` | same | `…\data\logs\…` |
| Service definition | `~/.config/systemd/user/oscar-corrtest.service` | `~/Library/LaunchAgents/io.cmetech.oscar-corrtest.plist` | `…\oscar-corrtest\oscar-corrtest-task.xml` (Task Scheduler entry `OSCAR CorrTest`) |

**Retention:** `retention preview/apply --before` selects only `COMPLETED`/
`INTERRUPTED` runs with `CLEAN`/`NOT_REQUIRED` cleanup, capped at 500 per
invocation (`runtime.go:624`). `backup --output` takes a coordinated online
SQLite snapshot (`persistence/sqlite/backup.go`).

---

## 8. Build, test, run

**No Go toolchain on this box** → use the container path for anything that
compiles.

Container (`Containerfile`): pinned `golang:1.27.3-bookworm` digest builder →
`FROM scratch` runtime with CA bundle, the binary, a `65532`-owned
`/var/lib/oscar-corrtest`, `EXPOSE 8787`, `ENTRYPOINT ["/oscar-corrtest"]`,
`CMD ["help"]` (never starts a listener implicitly).

```sh
# build + run entirely in containers
podman build -t oscar-corrtest -f Containerfile .
podman run --rm -p 8787:8787 -v corrtest-data:/var/lib/oscar-corrtest \
  oscar-corrtest serve --data-dir /var/lib/oscar-corrtest

# run the Go test suite without installing Go
podman run --rm -v "$PWD":/src -w /src golang:1.27.3-bookworm \
  sh -c 'GOWORK=off go test -count=1 ./...'
```

Makefile targets (`Makefile`):

| Target | Effect |
|---|---|
| `make build` | `bin/oscar-corrtest`, `CGO_ENABLED=0`, `-trimpath`, version ldflags (`Makefile:12`) |
| `make test` / `test-race` | `go test -count=1 ./...` / `CGO_ENABLED=1 -race` |
| `make fmt-check`, `mod-check`, `vet`, `security` | gofmt, `go mod verify/tidy` diff, `go vet`, gosec 2.28.0 + govulncheck 1.6.0 (`make tools` installs both into `.tools/`) |
| `make plan2-gate`…`plan7-gate`, `operator-experience-gate` | per-feature-series package gates |
| `make ci-core` / `ci` | fmt→mod→vet→security→plan2→test→race→build (+ tools, standalone, package, checksums) |
| `make cross`, `package`, `checksums` | 5 targets (linux/darwin amd64+arm64, windows amd64) → `dist/` + `SHA256SUMS` |
| `make container-check` | asserts `FROM scratch` and no `:latest` tags |
| `make reproducible-check`, `package-content-check`, `installer-posix-check`, `release-script-check`, `standalone-check` | release-contract scripts under `scripts/` |
| `make release-gate` | `ci` + every plan gate + container + installer + reproducibility |
| `make live-qualification` | **the only target that contacts a real OSCAR** |

`make live-qualification` (`scripts/live-qualification.sh`, `docs/live-qualification.md`)
requires a two-part explicit acknowledgement:

```sh
export OSCAR_CORRTEST_LIVE_TARGET_ID=tgt_...
export OSCAR_CORRTEST_LIVE_PHASE_B_ACK=I_ACKNOWLEDGE_PHASE_B_ON_A_DISPOSABLE_TARGET
export OSCAR_CORRTEST_LIVE_DISPOSABLE_ACK=I_ACKNOWLEDGE_PHASE_B_ON_A_DISPOSABLE_TARGET
export OSCAR_CORRTEST_LIVE_DATA_DIR=/var/lib/oscar-corrtest
export OSCAR_CORRTEST_LIVE_OUTPUT_DIR=$PWD/live-proof-$(date -u +%Y%m%dT%H%M%SZ)
make live-qualification
```

It runs `doctor`, then all eight builtins serially; every run must be `PASS` +
`CLEAN`, export cleanly, and pass offline bundle verification.

Typical operator session (`docs/operator.md`):

```sh
export OSCAR_API_KEY='...'
oscar-corrtest target add --name lab-a --url https://oscar.example/ext/mw --credential-env OSCAR_API_KEY
oscar-corrtest doctor --target <id> --pipeline-mode phase_b_dispatch
oscar-corrtest plan builtin:flood --target <id> --pipeline-mode phase_b_dispatch
oscar-corrtest run  builtin:flood --target <id> --pipeline-mode phase_b_dispatch
oscar-corrtest export <run-id> --output ./evidence.zip
oscar-corrtest verify-bundle ./evidence.zip
```

Installers: `scripts/install.sh` (POSIX, default `$HOME/.local/bin`, honours
`XDG_BIN_HOME`, `OSCAR_CORRTEST_VERSION`, `OSCAR_CORRTEST_INSTALL_DIR`,
`OSCAR_CORRTEST_RELEASE_BASE_URL`, `OSCAR_CORRTEST_RELEASE_API_URL`) and
`scripts/install.ps1` (default `%LOCALAPPDATA%\Programs\oscar-corrtest`).
Neither starts the app nor installs the service. `packaging/oscar-corrtest.service`
is a sample **system** unit (`Type=simple`, `User/Group=oscar-corrtest`,
`ExecStart=… serve --listen 0.0.0.0:8787 --config /etc/oscar-corrtest/config.json
--data-dir /var/lib/oscar-corrtest`, `StateDirectory`/`ConfigurationDirectory`,
`Restart=on-failure`, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`,
…) — **not** installed by either user-scoped installer and unrelated to
`oscar-corrtest service install`.

---

## 9. Known issues and open items

### Review chronology (`docs/reviews/`, 11 files)

| Date | File | Verdict |
|---|---|---|
| 08-19 | `…-adversarial-plan-review.md:59` | READY WITH REQUIRED CHANGES |
| 08-19 | `…-adversarial-plan-review-resolution.md` | 4 HIGH / 7 MED / 10 LOW dispositioned |
| 08-19 | `…-remediation-review.md:73,186` | CLEARED FOR PLAN 1 |
| 08-19 | `…-adversarial-code-review.md:412` | **BLOCK LIVE QUALIFICATION** — 3 BLOCKER, 9 HIGH, 26 MED, ~20 LOW at `e8ab6d0` |
| 08-20 | `…-adversarial-code-review-resolution.md:23` | every finding confirmed |
| 08-20 | `…-adversarial-remediation-re-review.md:296` | READY FOR CONTROLLED LIVE QUALIFICATION — 0 BLOCKER, 0 HIGH, 3 MEDIUM, 8 LOW |
| 08-20 | `…-adversarial-remediation-re-review-resolution.md:49` | 3 MEDIUMs fixed at `dbe4864`; nothing ≥ MEDIUM open |
| 08-20 | `…-remediation-status.md:20-80` | **authoritative open/closed ledger** |

Three files are review *prompts*, not findings.

### Closed
All 3 BLOCKERs and 9 HIGHs from 08-19 are closed with named regressions
(`remediation-status.md:22-41`): wrong `Authorization: Bearer` → `X-API-Key`;
flood could not reach cardinality; the timing oracle was unsound in both
directions; assertions were inert; normalized rows stuck at `PLANNED`; raw
evidence discarded; `UNKNOWN` resources unreconcilable (rule leak);
non-atomic terminal write; one-row-per-alertname invalid for
threshold/cross_source; impossible absence timing; injected alerts never
resolved; the fake OSCAR scripted off `_P01_`/`_N01_` case codes.
The three re-review MEDIUMs (RC-1 unresolved alerts after an ERROR run,
MED-FIX inert `internal/oscar/testdata` fixtures, NF-1 unpinned history route
and filter DSL) were closed at `dbe4864` (`re-review-resolution.md:22-26`).

### Open / deferred (`remediation-status.md:69-80`, `re-review.md:266-276`)
- **Live qualification is still `UNPROVEN`** until an operator runs the isolated
  all-eight-pattern gate against a disposable Phase-B target
  (`remediation-status.md:16-18`).
- Dispatch mode stays operator-declared; OSCAR exposes no discovery endpoint.
- Bundle SHA-256 proves integrity against its own manifest, not publisher
  identity; signing is future work.
- Retention capped at 500 candidates per invocation; no pagination.
- **RC-ORACLE-NEW-1** (`re-review.md:117`): a wrongful emit *after* a negative
  case's deadline escapes to PASS — inherent to finite negative windows.
- Parent-child P01 proves suppression at the correlator **audit** layer
  (`suppressed_per_notifier`), not end-to-end at the notifier
  (`re-review.md:282-286`).
- Deferred without a gate (`re-review.md:276`): UI parity for CLI-only
  maintenance actions, remote-login rate limiting, executing an imported
  scenario by stored ID, generalized custom rule criteria. Also: notifier-name
  discovery (`email` remains a live prerequisite).
- Remaining LOWs (`re-review.md:116-123`): `Host: localhost:PORT` rejected with
  421 because `net.ParseIP("localhost")` fails; only history-observed alerts are
  resolved at cleanup (accepted-but-not-yet-visible alerts are skipped);
  evidence `redaction_state` is asserted rather than computed;
  `live-qualification.sh`'s `"verdict":"PASS"` grep can match a per-case verdict.
- Mutation campaign stands at **16 KILLED / 14 SURVIVED** (`re-review.md:192`);
  survivors are declared coverage gaps with no false-PASS consequence.
- ⚠️ **Security-posture reversal:** the 08-19 review hardened a loopback-only
  default, but `docs/superpowers/specs/2026-08-20-…-distribution-and-open-listener-design.md`
  explicitly supersedes it with an unauthenticated `0.0.0.0:8787` default. The
  earlier DNS-rebinding and session findings were reviewed against the old
  default.
- OSCAR-side prerequisites the harness cannot fix (`plan-review.md:509-525`):
  audit rows carry no labels and are filterable only by fingerprint; no
  server-side label filtering on history; no idempotency keys; no
  capabilities/version endpoint; injection returns a Celery task id, not a
  fingerprint. Operator-only checks (`plan-review.md:583-601`) include the
  target's Redis eviction policy — anything but `noeviction` can evict
  trigger-once flags and make *any* correlation test flaky.

### `.planning/` and `.superpowers/`
`.planning/sketches/MANIFEST.md` records three UI sketches and their chosen
winners (documentation drawer, three-pane scenario workbench, unified
operations). `.superpowers/sdd/2026-08-20-…-scenario-authoring-guide/final-fix-report.md`
is complete (all five findings fixed, full `release-gate` green), with one
residual: previously deferred UX-only minors were left unchanged (line 238).

`docs/superpowers/` holds 4 specs and 18 plans: the original 7-plan harness
series (repository-foundation → durable-ledger → flood-vertical-slice →
window-order-patterns → timer-patterns → parent-child-notifier →
custom-operational-hardening), the `trust-remediation` plan, the 8-plan
Operator Experience series (managed-environment, user-service-lifecycle,
structured-logging, console-reference, scenario-workbench,
operations-workspace, operator-integration-release, scenario-authoring-guide),
and `distribution-and-open-listener`.

### Design limitations explicit in the code and docs

- **Pipeline mode is unverifiable.** OSCAR exposes no public pipeline-mode
  endpoint, so `--pipeline-mode` is an operator assertion recorded in the run,
  not a proven fact (`docs/operator.md`; `compiler.go:77`, `runner.go:168`).
- **Bundle signatures are not implemented.** SHA-256 proves integrity only;
  retain hashes out-of-band (`docs/live-qualification.md`).
- **Unauthenticated default listener.** `serve` binds `0.0.0.0:8787` with no
  auth; the firewall is the boundary (warning emitted at `app.go:160`).
- **Cross-process cancellation is impossible.** `CancelRun` only works for runs
  active in the current process (`runtime.go:297`).
- **Runs are serialized process-wide** by `runMu` (`runtime.go:182`) — no
  concurrent executions.
- **Assertions are exact-count only** across three kinds; no ranges, no
  predicates (`runner/assertions.go:19`).
- **Notifier names are never inferred.** The builtin `parent_child` case
  hard-codes notifier `email`; other deployments must import a custom scenario
  (`docs/builtins.md`).
- **Pagination is bounded at 100 pages**; exceeding it is a hard
  `pagination_limit` error rather than a partial result (`client.go:349`).
- **Windows arm64 is not in the release set** (`docs/install.md`).
