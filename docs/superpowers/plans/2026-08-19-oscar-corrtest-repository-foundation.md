# OSCAR Correlation Test Harness Repository Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the existing independent `oscar-corrtest` Git repository with a working Go CLI, embedded light/dark web shell, health endpoints, reproducible Linux packages, standalone-build proof, and equivalent GitHub and GitLab pipelines.

**Architecture:** This is Plan 1 of a seven-plan delivery series. It establishes the repository and executable boundary without contacting OSCAR or creating the SQLite schema. The binary uses a small command layer shared by `version` and `serve`; the HTTP package owns embedded templates/assets and graceful shutdown; Make targets are the single build contract consumed by both CI systems.

**Tech Stack:** Go 1.27.0, standard-library `flag`/`net/http`/`html/template`/`embed`, vanilla JavaScript, CSS custom properties, POSIX shell packaging, GitHub Actions, GitLab CI/CD.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`
**Adversarial review:** `docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md`
**Execution gate:** focused remediation review reports no open `BLOCKER` or Plan-1 `HIGH` finding.

## Global Constraints

- Work only in the existing standalone repository at `oscar_app/oscar-corrtest`; it is a sibling of `oscar`, never a directory inside the OSCAR repository.
- Preserve the existing `.git` history on branch `main`, including the adversarial review commits rooted at `74adce897694313918136fda23fb386b782e0a78`; never reinitialize or replace it.
- Use the canonical Go module `github.com/cmetech/oscar-corrtest`.
- Use Go 1.27 or newer; pin this plan, `go.mod`, and CI to Go 1.27.0.
- A clean checkout must build with `GOWORK=off`, `CGO_ENABLED=0`, and no readable `../oscar` tree.
- Do not import OSCAR source packages, shell out to OSCAR scripts, or copy generated code from OSCAR.
- Do not require Python, Node, Docker, or a frontend build to compile or run the binary.
- Bind the UI to `127.0.0.1:8787` by default, reject non-loopback listeners until authenticated remote serving is implemented, and embed every template, stylesheet, and script.
- Preserve the approved OTTO-inspired token palette, always-dark header, pre-paint theme selection, dynamic accessible toggle, focus-visible treatment, and reduced-motion behavior.
- Use `make` as the only CI build interface; CI YAML configures caching/artifacts but does not reimplement the gates.
- Build Linux AMD64 and Linux ARM64 release archives in this plan.
- Pin third-party GitHub Actions to immutable commit SHAs and GitLab images to version plus manifest digest.
- Do not provision remote repositories in this plan; external repository creation and visibility require explicit owner authorization.
- Use TDD and commit after every completed task.

## Delivery-series boundary

This plan ends at a deployable UI/CLI shell. Follow-on plans cover: SQLite ledger/report history; flood end-to-end; window/order patterns; timer patterns; parent-child evidence; and custom scenarios/operational hardening. Those plans consume the executable, HTTP, version, build, and CI interfaces defined here.

## Planned repository structure

```text
oscar-corrtest/
  .git/
  .github/workflows/ci.yml
  .github/workflows/release.yml
  .editorconfig
  .gitignore
  .gitlab-ci.yml
  Makefile
  README.md
  go.mod
  cmd/oscar-corrtest/main.go
  docs/development.md
  docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-prompt.md
  docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review.md
  docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-resolution.md
  docs/reviews/2026-08-19-oscar-corrtest-remediation-review-prompt.md
  docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md
  docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md
  internal/command/app.go
  internal/command/app_test.go
  internal/version/version.go
  internal/version/version_test.go
  internal/web/assets.go
  internal/web/server.go
  internal/web/server_test.go
  internal/web/templates/base.html.tmpl
  internal/web/templates/dashboard.html.tmpl
  internal/web/static/css/tokens.css
  internal/web/static/css/base.css
  internal/web/static/css/components.css
  internal/web/static/js/theme.js
  packaging/oscar-corrtest.service
  scripts/package.sh
  scripts/test-standalone.sh
```

---

### Task 1: Verify the repository and add the version command

**Files:**
- Create: `oscar-corrtest/.editorconfig`
- Create: `oscar-corrtest/.gitignore`
- Create: `oscar-corrtest/go.mod`
- Create: `oscar-corrtest/internal/version/version.go`
- Create: `oscar-corrtest/internal/version/version_test.go`
- Create: `oscar-corrtest/internal/command/app.go`
- Create: `oscar-corrtest/internal/command/app_test.go`
- Create: `oscar-corrtest/cmd/oscar-corrtest/main.go`

**Interfaces:**
- Consumes: the approved canonical design already committed at `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`.
- Produces: `version.Info`, `version.Current()`, `command.App`, `command.New(...)`, and `(*command.App).Run(context.Context, []string) int`.

- [ ] **Step 1: Verify the existing docs-only repository**

Run from `oscar_app/oscar-corrtest`:

```bash
test "$(git rev-parse --show-toplevel)" = "$PWD"
test "$(git branch --show-current)" = "main"
git merge-base --is-ancestor 74adce897694313918136fda23fb386b782e0a78 HEAD
test -z "$(git status --porcelain)"
test ! -e go.mod
test ! -e Makefile
test ! -e cmd
test ! -e internal
test ! -e .github
test ! -e .gitlab-ci.yml
git ls-files docs/reviews docs/superpowers
```

Expected: every check passes; the top level ends in `/oscar_app/oscar-corrtest`; the repository contains the committed review, design, and plan but no implementation or build files. If any check fails, stop without deleting, moving, or reinitializing anything.

- [ ] **Step 2: Create module and repository metadata**

Create `go.mod`:

```go
module github.com/cmetech/oscar-corrtest

go 1.27.0
```

Create `.gitignore`:

```gitignore
/bin/
/dist/
/.tools/
/.cache/
*.db
*.db-shm
*.db-wal
coverage.out
```

Create `.editorconfig` with UTF-8, LF, a final newline, two-space YAML indentation, and tab-indented Go/Make files. Do not copy or rewrite the design: the committed `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md` is canonical and its adversarial-review provenance remains in `docs/reviews/`.

- [ ] **Step 3: Write failing version and command tests**

Create `internal/version/version_test.go`:

```go
package version

import "testing"

func TestCurrentReturnsLinkerValues(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldBuildDate })
	Version, Commit, BuildDate = "v1.2.3", "abc123", "2026-08-19T20:00:00Z"
	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildDate != BuildDate {
		t.Fatalf("Current() = %#v", got)
	}
}
```

Create `internal/command/app_test.go`:

```go
package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cmetech/oscar-corrtest/internal/version"
)

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{Version: "v1.2.3", Commit: "abc", BuildDate: "now"})
	if code := app.Run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != "oscar-corrtest v1.2.3 commit=abc built=now\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version.Info{})
	if code := app.Run(context.Background(), []string{"bogus"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
```

- [ ] **Step 4: Confirm the red state**

Run `go test ./internal/version ./internal/command`.

Expected: FAIL because `Info`, `Current`, and `New` are undefined.

- [ ] **Step 5: Implement minimal version and command packages**

Create `internal/version/version.go`:

```go
package version

type Info struct {
	Version string `json:"version"`
	Commit string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

var Version = "0.0.0-dev"
var Commit = "unknown"
var BuildDate = "unknown"

func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}
```

Create `internal/command/app.go` with this exact public shape:

```go
type App struct { stdout, stderr io.Writer; info version.Info }
func New(stdout, stderr io.Writer, info version.Info) *App
func (a *App) Run(ctx context.Context, args []string) int
```

`Run` prints `usage: oscar-corrtest <version|serve>` and returns 2 for no/unknown command. `version` prints the exact tested line and returns 0. Create `main.go` using `signal.NotifyContext` for `os.Interrupt` and `syscall.SIGTERM`, then exit with the returned code.

- [ ] **Step 6: Verify and commit**

Run:

```bash
go test ./...
go run ./cmd/oscar-corrtest version
```

Expected: PASS and `oscar-corrtest 0.0.0-dev commit=unknown built=unknown`.

Commit:

```bash
git add .editorconfig .gitignore go.mod internal cmd
git commit -m "feat: add corrtest command foundation"
```

---

### Task 2: Add the embedded HTTP shell and graceful `serve`

**Files:**
- Create: `oscar-corrtest/internal/web/assets.go`
- Create: `oscar-corrtest/internal/web/server.go`
- Create: `oscar-corrtest/internal/web/server_test.go`
- Create: `oscar-corrtest/internal/web/templates/base.html.tmpl`
- Create: `oscar-corrtest/internal/web/templates/dashboard.html.tmpl`
- Create: `oscar-corrtest/internal/web/static/css/tokens.css`
- Create: `oscar-corrtest/internal/web/static/css/base.css`
- Create: `oscar-corrtest/internal/web/static/css/components.css`
- Create: `oscar-corrtest/internal/web/static/js/theme.js`
- Modify: `oscar-corrtest/internal/command/app.go`
- Modify: `oscar-corrtest/internal/command/app_test.go`
- Modify: `oscar-corrtest/cmd/oscar-corrtest/main.go`

**Interfaces:**
- Consumes: `version.Info`.
- Produces: `web.Options`, `web.NewHandler(version.Info) http.Handler`, `web.Run(context.Context, Options) error`, and injected `command.ServeFunc`.

- [ ] **Step 1: Write failing HTTP and serve-command tests**

Create table-driven tests for `/healthz`, `/readyz`, `/`, and `/static/css/tokens.css`. Require 200, a content type whose media-type prefix matches the table, and expected body fragments. Also assert POST `/healthz` returns 405; the dashboard CSP contains a nonce matching the inline script; HTML renders before status 200; and `serve --listen 127.0.0.1:9999` passes that address to a fake `ServeFunc`.

Use this table:

```go
tests := []struct{ path, contentType, body string }{
	{"/healthz", "application/json", `"status":"ok"`},
	{"/readyz", "application/json", `"status":"ready"`},
	{"/", "text/html", "OSCAR Correlation Test Harness"},
	{"/static/css/tokens.css", "text/css", "--ct-bg"},
}
```

- [ ] **Step 2: Confirm the red state**

Run `go test ./internal/web ./internal/command`.

Expected: FAIL because the web package and `serve` command do not exist.

- [ ] **Step 3: Implement assets and handler**

In `assets.go`, embed `templates/*.html.tmpl` and `static`, derive a `staticFS` with `fs.Sub`, and parse templates once.

In `server.go` define:

```go
type Options struct { ListenAddress string; Version version.Info }
func NewHandler(info version.Info) http.Handler
func Run(ctx context.Context, opts Options) error
```

Register method-aware `http.ServeMux` routes. JSON endpoints use `application/json; charset=utf-8`; static files use `Cache-Control: no-cache`. Generate an 18-byte random Base64 nonce for each page and send:

```text
default-src 'self'; script-src 'self' 'nonce-<nonce>'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'
```

Render HTML into `bytes.Buffer` before sending status 200. Add `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, and `Cache-Control: no-store`. `Run` uses explicit server timeouts and a five-second graceful shutdown timeout; `http.ErrServerClosed` is success.

- [ ] **Step 4: Create the templates and empty state**

Place this nonce-bearing pre-paint script before CSS links:

```html
<script nonce="{{.Nonce}}">(function(){try{var k='corrtest-theme';var s=localStorage.getItem(k);var t=(s==='light'||s==='dark')?s:(matchMedia('(prefers-color-scheme: light)').matches?'light':'dark');document.documentElement.dataset.theme=t;document.documentElement.style.colorScheme=t;}catch(e){document.documentElement.dataset.theme='dark';}})();</script>
```

Render version/build metadata and tabs for Dashboard, Run test, Runs, Scenarios, Targets, and Settings. Only Dashboard is active; other tabs use `aria-disabled="true"`. The empty state says: `No correlation runs yet` and `Configure an OSCAR target, then start a built-in test suite.`

- [ ] **Step 5: Wire `serve` into the command layer**

Change the constructor to:

```go
type ServeFunc func(context.Context, web.Options) error
func New(stdout, stderr io.Writer, info version.Info, serve ServeFunc) *App
```

Use a `flag.FlagSet` with `ContinueOnError`. Default `--listen` to `127.0.0.1:8787`, reject positional arguments, print `listening on http://<address>`, return 1 for server failure, and 2 for flag errors. Parse the listener with `net.SplitHostPort`, require a literal IP from `net.ParseIP`, and permit it only when `IsLoopback()` is true; reject hostnames, wildcard, unspecified, empty-host, and non-loopback listeners before calling `ServeFunc`, with an error explaining that authenticated remote serving is not implemented. Do not add an unauthenticated override flag. Pass `web.Run` from `main.go`.

Add table-driven command tests proving `127.0.0.1`, `127.0.0.2`, and `[::1]` are accepted while `localhost`, `0.0.0.0`, `[::]`, an empty host such as `:8787`, and `192.0.2.10` return exit 2 without invoking `ServeFunc`.

- [ ] **Step 6: Verify and commit**

Run `go test ./internal/web ./internal/command` and `go test ./...`; expect PASS.

Commit:

```bash
git add internal/web internal/command cmd/oscar-corrtest
git commit -m "feat: add embedded web shell and health endpoints"
```

---

### Task 3: Implement and contract-test light/dark presentation

**Files:**
- Modify: `oscar-corrtest/internal/web/static/css/tokens.css`
- Modify: `oscar-corrtest/internal/web/static/css/base.css`
- Modify: `oscar-corrtest/internal/web/static/css/components.css`
- Modify: `oscar-corrtest/internal/web/static/js/theme.js`
- Modify: `oscar-corrtest/internal/web/templates/base.html.tmpl`
- Modify: `oscar-corrtest/internal/web/server_test.go`

**Interfaces:**
- Consumes: Task 2 embedded assets.
- Produces: stable `--ct-*`, `data-theme`, `data-theme-toggle`, and `corrtest-theme` contracts.

- [ ] **Step 1: Add failing presentation-contract tests**

Read embedded CSS/JS/templates and assert both themes define all approved tokens; `color-scheme` changes with theme; focus outline is at least 3px; reduced motion exists; and the toggle/JS update `data-theme`, native color scheme, `aria-pressed`, a stable accessible label, and `corrtest-theme` storage.

- [ ] **Step 2: Confirm the red state**

Run `go test ./internal/web -run Theme -v` and expect FAIL.

- [ ] **Step 3: Implement exact tokens**

```css
:root {
  --ct-bg: #1A1D24; --ct-card: #242832; --ct-card-hover: #2C3140;
  --ct-header: #1E2128; --ct-border: #2D3340; --ct-fg: #FAFAFA;
  --ct-fg-muted: #9CA3AF; --ct-accent: #FAD22D; --ct-accent-fg: #1A1D24;
  --ct-pass: #0FC373; --ct-warning: #FF8C0A; --ct-fail: #FF3232;
  --ct-activity: #AF78D2; color-scheme: dark;
}
[data-theme="light"] {
  --ct-bg: #F7F8FA; --ct-card: #FFFFFF; --ct-card-hover: #F1F3F7;
  --ct-header: #1E2128; --ct-border: #E5E7EB; --ct-fg: #1A1D24;
  --ct-fg-muted: #4B5563; --ct-accent: #FAD22D; --ct-accent-fg: #1A1D24;
  --ct-pass: #087A49; --ct-warning: #A34D00; --ct-fail: #B42318;
  --ct-activity: #704099; color-scheme: light;
}
```

Add the 4/8/12/16/24/32 spacing scale, 4px input radius, 6px card radius, system/monospace fonts, responsive grid/tables, always-dark header, 3px focus outlines, and reduced-motion override.

- [ ] **Step 4: Implement accessible toggle behavior**

Use the stable accessible label `Light theme` and set `aria-pressed="true"` exactly when light theme is active. The visible icon and optional `title` may describe the next action, but the accessible name must not change meaning while `aria-pressed` changes state. On click update document theme, native color scheme, pressed state, icon/title, and local storage; storage failures must not break toggling.

- [ ] **Step 5: Verify and commit**

Run `go test ./internal/web -v`, launch `go run ./cmd/oscar-corrtest serve`, toggle/reload both themes, then Ctrl-C and confirm clean shutdown.

Commit:

```bash
git add internal/web
git commit -m "feat: add accessible light and dark themes"
```

---

### Task 4: Add reproducible build, verification, and packaging

**Files:**
- Create: `oscar-corrtest/Makefile`
- Create: `oscar-corrtest/scripts/package.sh`
- Create: `oscar-corrtest/README.md`
- Create: `oscar-corrtest/docs/development.md`
- Modify: `oscar-corrtest/internal/version/version_test.go`

**Interfaces:**
- Consumes: Task 1 linker variables and the CLI package.
- Produces: `tools`, `fmt-check`, `mod-check`, `vet`, `security`, `test`, `test-race`, `build`, `cross`, `package`, `checksums`, `ci-core`, and `ci` Make targets plus reproducible `dist/*.tar.gz` archives.

- [ ] **Step 1: Strengthen the version test**

Add a test asserting every source default is non-empty. Run `go test ./internal/version -v`; expect PASS. Linker overrides are verified after the Makefile exists.

- [ ] **Step 2: Create the Make build contract**

Define these exact constants:

```make
BINARY := oscar-corrtest
PKG := ./cmd/oscar-corrtest
BUILD_DIR := bin
DIST_DIR := dist
TOOLS_DIR := $(CURDIR)/.tools
VERSION ?= $(shell git describe --tags --always --dirty --match='v[0-9]*' 2>/dev/null || echo 0.0.0-dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo unknown)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct HEAD 2>/dev/null || echo 0)
LDFLAGS := -s -w -X github.com/cmetech/oscar-corrtest/internal/version.Version=$(VERSION) -X github.com/cmetech/oscar-corrtest/internal/version.Commit=$(COMMIT) -X github.com/cmetech/oscar-corrtest/internal/version.BuildDate=$(BUILD_DATE)
GO_BUILD := GOWORK=off CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="$(LDFLAGS)"
GOSEC_VERSION := v2.28.0
GOVULNCHECK_VERSION := v1.6.0
```

Implement targets with these commands and invariants:

```make
tools:
	GOBIN=$(TOOLS_DIR) go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	GOBIN=$(TOOLS_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

fmt-check:
	@files="$$(find . -type f -name '*.go' -not -path './.tools/*')"; \
	unformatted="$$(gofmt -l $$files)"; \
	test -z "$$unformatted" || { printf '%s\n' "$$unformatted"; exit 1; }

mod-check:
	go mod verify
	go mod tidy
	git diff --exit-code -- go.mod go.sum

vet:
	go vet ./...

security:
	$(TOOLS_DIR)/gosec ./...
	$(TOOLS_DIR)/govulncheck ./...

test:
	go test -count=1 ./...

test-race:
	CGO_ENABLED=1 go test -race -count=1 ./...
```

`build` writes `bin/oscar-corrtest`; `cross` writes CGO-disabled Linux AMD64 and ARM64 binaries; `package` depends on `cross` and calls `scripts/package.sh` for both with `$(SOURCE_DATE_EPOCH)`; `checksums` creates a deterministically ordered `dist/SHA256SUMS` using `sha256sum` or `shasum -a 256`; and `ci-core` composes formatting, modules, vet, security, tests, race, and host build. Define `ci` as a sequential recipe—not only parallelizable prerequisites—so even `make -j ci` executes `$(MAKE) tools`, `$(MAKE) ci-core`, `$(MAKE) package`, then `$(MAKE) checksums` in order. Task 7 inserts `$(MAKE) standalone-check` between `ci-core` and `package` after that target exists.

- [ ] **Step 3: Implement safe archive creation**

`scripts/package.sh` accepts exactly `<version> <amd64|arm64> <binary-path> <source-date-epoch>`. It verifies the binary and numeric epoch, creates a staging directory via `mktemp -d`, traps cleanup of only that directory, installs the executable as mode 0755 under `oscar-corrtest/bin/`, includes README, and writes:

```text
dist/oscar-corrtest_<version>_linux_<arch>.tar.gz
```

Reject unsupported architectures and never include `.env`, database, credential, `.git`, or workspace-parent content. Require GNU tar: select `gtar` when available or accept `tar` only when `tar --version` identifies GNU tar; otherwise fail with an actionable message. Create archives with GNU format, a sorted member order, numeric owner/group zero, mode-preserving staged files, and `--mtime=@<source-date-epoch>`, then pipe the tar stream through `gzip -n`. Packaging is therefore byte-reproducible across supported Linux builders; macOS developers need GNU tar (`gtar`) for the package target.

- [ ] **Step 4: Document operator/developer entry points**

README quick start:

```bash
make build
./bin/oscar-corrtest version
./bin/oscar-corrtest serve
```

Document loopback binding, the current foundation-only scope, the future OSCAR/SQLite slices, and standalone builds. `docs/development.md` documents Go 1.27.0, tool installation, every Make target, GitHub as canonical module path, and GitLab mirror compatibility.

- [ ] **Step 5: Verify and commit**

Run:

```bash
make ci
./bin/oscar-corrtest version
tar -tzf dist/oscar-corrtest_*_linux_amd64.tar.gz
corrtest_repro_dir=$(mktemp -d "${TMPDIR:-/tmp}/oscar-corrtest-repro.XXXXXX")
test -d "$corrtest_repro_dir"
case "$(basename "$corrtest_repro_dir")" in oscar-corrtest-repro.*) ;; *) exit 1 ;; esac
trap 'rm -rf "$corrtest_repro_dir"' EXIT
cp dist/SHA256SUMS "$corrtest_repro_dir/first.sha256"
make package checksums
cp dist/SHA256SUMS "$corrtest_repro_dir/second.sha256"
diff -u "$corrtest_repro_dir/first.sha256" "$corrtest_repro_dir/second.sha256"
```

Expect all gates to pass, both archives/checksums to exist, archive paths to begin `oscar-corrtest/`, and the two package checksum snapshots to be identical. The trap removes only the validated directory returned by `mktemp -d`.

Commit:

```bash
git add Makefile scripts/package.sh README.md docs/development.md internal/version
git commit -m "build: add standalone verification and linux packages"
```

---

### Task 5: Add GitHub Actions verification and releases

**Files:**
- Create: `oscar-corrtest/.github/workflows/ci.yml`
- Create: `oscar-corrtest/.github/workflows/release.yml`

**Interfaces:**
- Consumes: Task 4 Make targets.
- Produces: PR/push verification artifacts and semantic-tag GitHub Releases.

- [ ] **Step 1: Create `ci.yml` with immutable action pins**

Use:

```text
actions/checkout v6.0.2 = de0fac2e4500dabe0009e67214ff5f5447ce83dd
actions/setup-go v7.0.0 = b7ad1dad31e06c5925ef5d2fc7ad053ef454303e
actions/upload-artifact v7.0.1 = 043fb46d1a93c77aae656e7c1c64a875d1fc6a0a
```

Trigger pushes, pull requests, and manual dispatch. Use `permissions: contents: read`, branch concurrency cancellation, and `ubuntu-24.04`. Read Go from `go.mod`, cache modules, and run only these project commands:

```yaml
- run: make ci
```

Upload `dist/*.tar.gz` and `dist/SHA256SUMS` as `oscar-corrtest-${{ github.sha }}` with 14-day retention. Put a version comment beside each SHA-pinned `uses:` line.

- [ ] **Step 2: Create `release.yml`**

Trigger tags matching the workflow glob `v*.*.*`. Use `contents: write`; reject any tag that does not match the shell regular expression `^v[0-9]+\.[0-9]+\.[0-9]+$`, run `make ci`, and publish with:

```yaml
- name: Publish GitHub release
  env:
    GH_TOKEN: ${{ github.token }}
  run: >-
    gh release create "$GITHUB_REF_NAME"
    dist/*.tar.gz dist/SHA256SUMS
    --verify-tag --generate-notes
```

- [ ] **Step 3: Add a static pin test and run local gates**

Add a shell check to `ci.yml` that fails if a `uses:` value does not contain a 40-character hexadecimal revision. Run `make ci`; expect PASS.

- [ ] **Step 4: Commit**

```bash
git add .github
git commit -m "ci: add github verification and release workflows"
```

---

### Task 6: Add equivalent GitLab CI/CD

**Files:**
- Create: `oscar-corrtest/.gitlab-ci.yml`

**Interfaces:**
- Consumes: the same Task 4 Make targets as GitHub.
- Produces: GitLab verify, package, and semantic-tag release jobs.

- [ ] **Step 1: Create verify/package jobs**

Pin the build image:

```text
golang:1.27.0-bookworm@sha256:d22fb682b72b6ebf58365871c437cf75794131831a6b8e6f6ebc5302c67c1cad
```

Define `verify`, `package`, `release` stages; set `GOTOOLCHAIN=local` plus project-local `GOCACHE`/`GOMODCACHE`; cache by `go.mod` while the standard-library-only module has no `go.sum`. Require the first dependency-adding commit to change both GitHub and GitLab cache dependency paths to `go.sum`. Verify runs:

```yaml
script:
  - make tools
  - make ci-core
```

Package needs verify, runs `make package checksums`, and preserves tarballs/checksums for 14 days.

- [ ] **Step 2: Add the tag release job**

Pin:

```text
registry.gitlab.com/gitlab-org/cli:v1.109.0@sha256:4dbd09345a1d8b2a14a1e655ce992ad3fb9138c1d4bec75b04232efa0701f456
```

The job needs package artifacts, runs only for semantic-version tags, sets `GITLAB_TOKEN=$CI_JOB_TOKEN`, and executes:

```yaml
script:
  - >-
    glab release create "$CI_COMMIT_TAG"
    --name "OSCAR Correlation Test Harness $CI_COMMIT_TAG"
    --notes "Automated release $CI_COMMIT_TAG"
    --use-package-registry
    dist/*.tar.gz dist/SHA256SUMS
```

Document the first real semantic tag on each eventual remote as an operator release gate: confirm GitHub permissions/branch protection and GitLab `CI_JOB_TOKEN` package-registry upload plus release links. Do not claim those external settings are proven by local YAML validation.

- [ ] **Step 3: Verify CI parity**

Assert GitHub calls `make ci`; assert GitLab's verify and package jobs collectively call `make tools`, `make ci-core`, `make package`, and `make checksums`. Neither YAML may contain direct `go test`, `go build`, or `go vet` commands. Task 7 adds the standalone gate to the `ci` target and the GitLab verify job immediately after its script exists.

- [ ] **Step 4: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: add gitlab verification and release pipeline"
```

---

### Task 7: Prove clean-checkout independence and add systemd packaging

**Files:**
- Create: `oscar-corrtest/scripts/test-standalone.sh`
- Create: `oscar-corrtest/packaging/oscar-corrtest.service`
- Modify: `oscar-corrtest/Makefile`
- Modify: `oscar-corrtest/README.md`
- Modify: `oscar-corrtest/docs/development.md`
- Modify: `oscar-corrtest/.gitlab-ci.yml`

**Interfaces:**
- Consumes: a committed repository and the Task 4 module/test/build targets.
- Produces: `make archive-mod-check`, `make standalone-check`, the final `make ci` gate, and a hardened Linux service example.

- [ ] **Step 1: Implement the clean-checkout proof**

`scripts/test-standalone.sh` must:

1. expose an internal `--scan-only <root>` mode used by its scanner contract tests;
2. scan only source/build inputs: `*.go`, `go.mod`, `go.sum`, `Makefile`, `scripts/**`, `.github/workflows/**`, `.gitlab-ci.yml`, `packaging/**`, `internal/web/templates/**`, and `internal/web/static/**`; construct the three forbidden scanner patterns from non-contiguous shell fragments so `scripts/test-standalone.sh` does not match its own policy literals;
3. reject `../oscar`, `/oscar_app/oscar`, and `github.com/cmetech/oscar/` in those inputs; explicitly exclude `docs/**` and `README.md`, which legitimately record the boundary and review provenance;
4. require a clean worktree for the full standalone mode, but not for `--scan-only`;
5. create a validated `mktemp -d` path and trap removal of only that path;
6. extract `git archive HEAD` into it;
7. reject every symlink and repeat the same file-class scan in the archive;
8. run `GOWORK=off make archive-mod-check test build` inside the archive;
9. execute the archived binary's `version` command.

Add:

```make
standalone-check:
	bash scripts/test-standalone.sh

archive-mod-check:
	go mod verify
	go mod tidy -diff
```

Insert `$(MAKE) standalone-check` in the Task 4 `ci` recipe after `$(MAKE) ci-core` and before packaging; the existing GitHub workflows already call `make ci` and therefore pick up the new gate without a YAML change. Add `make standalone-check` to the GitLab verify job after `make ci-core`. This ordering keeps every prior task's commit green while establishing final CI parity as soon as the target exists.

- [ ] **Step 2: Prove the check detects leakage**

Exercise the scanner before the full clean-worktree gate:

1. create `docs/reviews/scanner-exemption-fixture.md` containing the forbidden sibling path and run `bash scripts/test-standalone.sh --scan-only .`; expect PASS because documentation is deliberately outside the dependency scan;
2. add the same forbidden path in a comment to `internal/command/app_test.go`, rerun scan-only, and require its explicit forbidden-reference failure;
3. remove only the temporary documentation file and source comment, rerun scan-only, and expect PASS.

These two controls prove the allowlist does not reject review/design prose and cannot be bypassed by putting an OSCAR dependency in source.

- [ ] **Step 3: Create the systemd example**

```ini
[Unit]
Description=OSCAR Correlation Test Harness
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=oscar-corrtest
Group=oscar-corrtest
ExecStart=/usr/local/bin/oscar-corrtest serve --listen 127.0.0.1:8787
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
```

Document that Plan 1 refuses non-loopback listeners, that a future remote mode requires authenticated serving or an authenticated reverse proxy, that operators must provision the `oscar-corrtest` system user/group before installing the example unit, and that the writable state directory arrives with the SQLite plan.

- [ ] **Step 4: Commit, then verify the archive**

```bash
git add scripts/test-standalone.sh packaging Makefile README.md docs/development.md .gitlab-ci.yml
git commit -m "test: prove standalone builds and add systemd unit"
make ci
```

Expected: PASS from the extracted checkout, including `go mod tidy -diff` without any `.git` directory.

---

### Task 8: Run the repository-foundation acceptance gate

**Files:**
- Modify only if verification exposes a defect in Tasks 1–7.

**Interfaces:**
- Consumes: all Plan 1 outputs.
- Produces: a clean repository ready for the SQLite-ledger plan.

- [ ] **Step 1: Run every gate**

```bash
make ci
git diff --check
git status --short
```

Expected: success and a clean worktree; ignored build/tool/cache directories do not appear.

- [ ] **Step 2: Verify target architectures**

Run `file` on both Linux binaries. Expect x86-64 and aarch64 ELF executables. Run the host-compatible binary's `version` and require non-default linker metadata.

- [ ] **Step 3: Verify HTTP and shutdown behavior**

Start the binary, request `/`, `/healthz`, `/readyz`, and CSS, then send SIGTERM. Require correct status/content types/security headers, theme persistence, and exit within five seconds.

- [ ] **Step 4: Audit both CI definitions**

Confirm least-privilege GitHub permissions; SHA-pinned actions; digest-pinned GitLab images; Make-only gates; tag-only releases; packaged archive artifacts; and no cross-repository checkout.

- [ ] **Step 5: Commit only necessary verification corrections**

If Steps 1–4 required corrections, rerun the failing acceptance command, stage only the explicitly named files changed for those corrections (never `git add .`), and commit them as `fix: close repository foundation verification gaps`. If no correction was required, do not create an empty commit.

Do not tag or create remote repositories. Report the local path, commit, verification evidence, archive names, and the remote URLs/visibility decisions that still require owner authorization.
