# Console Shell and Embedded Reference Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the current web shell into a dense OSCAR engineering console with contextual documentation on every page and a complete embedded Reference page.

**Architecture:** A static typed help catalog is rendered both into a reusable right-side drawer and the full Reference page. Server templates remain the source of truth; a small progressive-enhancement script controls drawer state, keyboard behavior, and focus without requiring a frontend framework.

**Tech Stack:** Go `html/template`, embedded CSS/JavaScript, semantic HTML, existing web server and security middleware.

**Spec:** `docs/superpowers/specs/2026-08-20-oscar-corrtest-operator-experience-and-service-design.md`

## Global Constraints

- Preserve server-rendered operation when JavaScript is unavailable.
- Use the approved console Variant C: maximum workspace plus a contextual right drawer.
- Every existing page exposes concise purpose, workflow, OSCAR effect, evidence, and CLI-equivalent guidance.
- No external fonts, scripts, icon CDNs, trackers, or network-loaded assets.
- Preserve CSP, CSRF, Host validation, theme persistence, and keyboard navigation.
- Documentation describes current behavior only; unsupported operations are labeled unavailable.

---

### Task 1: Create a typed, coverage-tested help catalog

**Files:**
- Create: `internal/web/help.go`
- Create: `internal/web/help_test.go`
- Modify: `internal/web/view.go`
- Modify: `internal/web/view_test.go`

**Interfaces:**
- Produces: `HelpTopic{ID, Title, Summary string; Sections []HelpSection; Links []HelpLink}`.
- Produces: `HelpSection{Heading string; Paragraphs, Bullets []string; Code string}`.
- Produces: `HelpCatalog` with `Topic(id string) (HelpTopic, bool)` and `All() []HelpTopic`.
- Extends: page view data with `HelpTopicID` and active navigation state.

- [ ] **Step 1: Write catalog coverage tests**

```go
func TestEveryConsolePageHasCompleteHelp(t *testing.T) {
    for _, page := range []string{"dashboard", "targets", "scenarios", "runs", "artifacts", "operations", "reference"} {
        topic, ok := defaultHelpCatalog().Topic(page)
        // Require purpose, workflow, OSCAR effect, evidence, and CLI sections.
    }
}
```

Also require entries for all eight correlation pattern families, naming and
label conventions, P01/N01 polarity, cleanup, verdict semantics, and service
commands.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/web -run Help`

Expected: FAIL because the help catalog does not exist.

- [ ] **Step 3: Implement static catalog and stable ordering**

Keep copy in Go structures so template rendering is escaped. Do not render
Markdown or accept stored/user HTML. Return copies from `All` to prevent
request-time mutation.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/web -run 'Help|View'`

Expected: PASS with no missing console or pattern topic.

- [ ] **Step 5: Commit**

```bash
git add internal/web/help.go internal/web/help_test.go internal/web/view.go internal/web/view_test.go
git commit -m "feat: add embedded help catalog"
```

### Task 2: Implement the dense shell and accessible reference drawer

**Files:**
- Create: `internal/web/templates/help_drawer.html.tmpl`
- Create: `internal/web/static/js/help.js`
- Modify: `internal/web/templates/base.html.tmpl`
- Modify: `internal/web/static/css/app.css`
- Modify: `internal/web/assets.go`
- Modify: `internal/web/assets_test.go`
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`

**Interfaces:**
- Adds: a page-level `Help` button targeting `#page-help-drawer`.
- Adds: drawer close button, overlay, `Escape` close, focus return, and focus containment.
- Adds: static asset `/static/js/help.js` with a CSP-compatible external script.

- [ ] **Step 1: Write shell rendering and accessibility tests**

```go
func TestEveryPageRendersContextualHelpTrigger(t *testing.T) {
    // GET each page; require aria-controls, dialog label, topic title,
    // fallback link to /reference#topic, and no inline event handlers.
}
```

Test the JS asset route, content type, cache policy, CSP script-src, active nav,
and dark/light theme controls.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/web -run 'Help|Shell|Asset|CSP'`

Expected: FAIL because the drawer and script are absent.

- [ ] **Step 3: Build the console shell**

Use the approved dense structure: fixed utility header, compact navigation,
wide content area, status chips, and a 400–480 px right drawer at desktop.
Below the desktop breakpoint, use an inset full-height dialog. Preserve strong
focus rings, minimum 44 px controls, readable monospace data, and light/dark
tokens.

- [ ] **Step 4: Add progressive drawer behavior**

Set `aria-expanded`, use `hidden` as the closed source of truth, focus the close
button on open, close on overlay or Escape, cycle Tab within the drawer, and
return focus to the invoking button. With JavaScript disabled, the fallback
Reference link remains usable.

- [ ] **Step 5: Run shell tests and manual asset verification**

Run: `go test -race ./internal/web && go test ./internal/web -run TestEveryPageRendersContextualHelpTrigger -count=20`

Expected: PASS with stable escaping and CSP.

- [ ] **Step 6: Commit**

```bash
git add internal/web/templates internal/web/static internal/web/assets.go internal/web/assets_test.go internal/web/server.go internal/web/server_test.go
git commit -m "feat: add dense console help drawer"
```

### Task 3: Add the full Reference page and route

**Files:**
- Create: `internal/web/templates/reference.html.tmpl`
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `internal/web/help.go`
- Modify: `internal/web/static/css/app.css`

**Interfaces:**
- Adds: `GET /reference` and stable topic anchors `#dashboard`, `#scenarios`, `#pattern-flood`, and related catalog IDs.
- Adds: Reference navigation entry and cross-links back to relevant application pages.

- [ ] **Step 1: Write route and content tests**

```go
func TestReferenceContainsOperationalContracts(t *testing.T) {
    // Require eight patterns, correlation labels, test-run naming grammar,
    // P01/N01, verdict states, cleanup policy, service commands, and API-key setup.
}
```

- [ ] **Step 2: Run the test and verify failure**

Run: `go test ./internal/web -run Reference`

Expected: FAIL because `/reference` is not registered.

- [ ] **Step 3: Implement the Reference page**

Render catalog sections with a sticky table of contents, stable escaped IDs,
CLI snippets, and direct navigation links. Include a clear warning that the
default all-interface listener is unauthenticated and intended for isolated
test networks.

- [ ] **Step 4: Run the plan gate**

Run: `go test -race ./internal/web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web
git commit -m "feat: add in-app operator reference"
```
