# OSCAR CorrTest Operator Experience Delivery Series

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

This index orders seven independently testable implementation plans. Execute
them in order because each later plan consumes interfaces created earlier.

| Plan | Deliverable | Depends on |
|---|---|---|
| 1 | Managed environment and cross-platform paths | Existing application |
| 2 | User-level service lifecycle | Plan 1 paths |
| 3 | Structured logging foundation | Plan 1 paths; Plan 2 log contract |
| 4 | Console shell and embedded reference | Existing web shell |
| 5 | Three-pane scenario workbench | Plan 4 shell |
| 6 | Unified Operations workspace | Plans 1–4 |
| 7 | Integration, packaging, and operator documentation | Plans 1–6 |

Plan files:

1. `2026-08-20-oscar-corrtest-managed-environment.md`
2. `2026-08-20-oscar-corrtest-user-service-lifecycle.md`
3. `2026-08-20-oscar-corrtest-structured-logging.md`
4. `2026-08-20-oscar-corrtest-console-reference.md`
5. `2026-08-20-oscar-corrtest-scenario-workbench.md`
6. `2026-08-20-oscar-corrtest-operations-workspace.md`
7. `2026-08-20-oscar-corrtest-operator-integration-release.md`

## Locked cross-plan interfaces

The plans define and then preserve these names:

```go
platformpaths.Resolve(goos string, lookup func(string) (string, bool)) (platformpaths.Paths, error)

envfile.Open(path string, lookup func(string) (string, bool)) (*envfile.Store, error)
(*envfile.Store).Getenv(key string) string
(*envfile.Store).Status(key string) envfile.KeyStatus
(*envfile.Store).Replace(key, value string) error
(*envfile.Store).Clear(key string) error

service.NewManager(service.Options) (service.Manager, error)
applog.Open(logDir string, stderr io.Writer, options applog.Options) (*applog.System, error)
operations.New(config.Settings, *envfile.Store, service.Manager, *applog.System) *operations.Controller
```

The command and runtime constructors retain compatibility wrappers for current
tests while the production main uses dependency structs introduced by the
series. No plan adds a database migration.

## Series verification

After every plan, run its targeted package tests and commit. After Plan 7 run:

```bash
make clean release-gate
CGO_ENABLED=1 go test -race -count=1 ./...
```

Do not tag or publish a release as part of implementation.
