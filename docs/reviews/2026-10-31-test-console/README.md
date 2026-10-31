# OSCAR Test Console branding review

Implemented on `feat/oscar-test-console` in the separate `oscar-corrtest-console` worktree, based on `04b77c8`.

## Design and scope

The existing Go `html/template` shell, embedded assets and four shared CSS files remain the UI architecture. No frontend dependency was added. All application naming is now OSCAR Test Console; executable names, commands, environment variables, routes, schemas, evidence contracts and runtime behavior remain intact.

Source of truth: OSCAR Admin UI `src/lib/branding/templates/oscarModern.js`, `styles/branding-fonts.css`, and `src/views/components/branding/BrandLogo.js`, at commit `a8ac92b13c63d218fb8cf5acd888298e61fc1567`. Ericsson Dark Blue `#000082` anchors the header and light-theme primary actions; blue, teal, yellow and red semantic fills come from that recipe. Dark surfaces use navy/neutral tints. Hilda and Hilda Mono are served locally. The white Ericsson logo sits on the navy header in both themes with clearance, without cropping or recoloring. [Asset hashes](brand-assets.json) identify the exact copied originals.

Shared changes cover navigation, typography, 4px geometry/spacing, controls, cards, tables, labelled chips, focus visibility and responsive layouts. The dashboard describes correlation, ordinary alerts, receiver checks and load workflows without claiming an empty history. Run status, verdict and cleanup remain separate. Input and target chips expose existing metadata. Timestamps in the run list are displayed in UTC; stored timestamps are unchanged.

No execution, credential, cancellation, duplicate-submission, export or verdict logic was changed. Coverage boundaries and missing-artifact warnings remain visible. A completed lifecycle is neutral, not a passing verdict.

## Isolated preview

URL: http://127.0.0.1:8797

The preview uses `/tmp/oscar-console-preview/data/corrtest.db` and a separate fixture-only binary, `/tmp/oscar-console-preview/preview-ready`. It rejects all non-GET/HEAD requests. The fixture runtime has no service manager or managed credentials; the target is `http://127.0.0.1:1`. Displayed PASS/FAIL/INCONCLUSIVE and running rows are synthetic local fixtures, not OSCAR observations. One evidence artifact is deliberately absent to exercise the integrity warning.

Port 8787, the installed binary, launchd service, installed configuration and database were not modified. No live OSCAR scenario or SNMP traffic was sent. Preview files are temporary and are not a deployment package.

## Verification

- `go test ./...`: passed across all 29 tested packages. [Final output](regression-final.log); [uncached full run](regression-uncached.log).
- `go build -o /tmp/oscar-console-preview/oscar-corrtest ./cmd/oscar-corrtest`: passed.
- `git diff --check`: passed.
- Existing regressions cover target/credential handling, CSRF, receiver forms, cancellation, cleanup-safe deletion, evidence export/integrity and runtime behavior. The UI contract adds local asset delivery and consistent branded shell coverage.
- Chromium via agent-browser: eight pages × two themes × desktop 1440×1000 / mobile 390×844. Checked document width, shell, theme and loaded brand assets: [32 checks](visual-results.json).
- Representative screenshots visually inspected across dashboard, targets, workbench, run creation, history/evidence, settings and authoring. Receiver sections and missing evidence were also inspected below the initial viewport.
- Theme toggle persists its selection; confirmation dialog opens with Cancel focused and cancellation closes it without submission. No destructive action was confirmed.
- Semantic chip text contrast: [ratios](status-contrast.json), all at least 4.5:1. Status text is always present; color is supplementary.

Browser checks use local fixtures. Live OSCAR behavior, deployment and other browser engines were not exercised.

## Screenshots

| Page | Dark desktop | Light desktop | Dark mobile | Light mobile |
|---|---|---|---|---|
| dashboard | [dark desktop](screenshots/dashboard-dark-desktop.png) | [light desktop](screenshots/dashboard-light-desktop.png) | [dark mobile](screenshots/dashboard-dark-mobile.png) | [light mobile](screenshots/dashboard-light-mobile.png) |
| targets | [dark desktop](screenshots/targets-dark-desktop.png) | [light desktop](screenshots/targets-light-desktop.png) | [dark mobile](screenshots/targets-dark-mobile.png) | [light mobile](screenshots/targets-light-mobile.png) |
| scenarios | [dark desktop](screenshots/scenarios-dark-desktop.png) | [light desktop](screenshots/scenarios-light-desktop.png) | [dark mobile](screenshots/scenarios-dark-mobile.png) | [light mobile](screenshots/scenarios-light-mobile.png) |
| run-test | [dark desktop](screenshots/run-test-dark-desktop.png) | [light desktop](screenshots/run-test-light-desktop.png) | [dark mobile](screenshots/run-test-dark-mobile.png) | [light mobile](screenshots/run-test-light-mobile.png) |
| runs | [dark desktop](screenshots/runs-dark-desktop.png) | [light desktop](screenshots/runs-light-desktop.png) | [dark mobile](screenshots/runs-dark-mobile.png) | [light mobile](screenshots/runs-light-mobile.png) |
| evidence | [dark desktop](screenshots/evidence-dark-desktop.png) | [light desktop](screenshots/evidence-light-desktop.png) | [dark mobile](screenshots/evidence-dark-mobile.png) | [light mobile](screenshots/evidence-light-mobile.png) |
| settings | [dark desktop](screenshots/settings-dark-desktop.png) | [light desktop](screenshots/settings-light-desktop.png) | [dark mobile](screenshots/settings-dark-mobile.png) | [light mobile](screenshots/settings-light-mobile.png) |
| authoring | [dark desktop](screenshots/authoring-dark-desktop.png) | [light desktop](screenshots/authoring-light-desktop.png) | [dark mobile](screenshots/authoring-dark-mobile.png) | [light mobile](screenshots/authoring-light-mobile.png) |

Additional views: [receiver checks, dark mobile](screenshots/receivers-dark-mobile.png), [receiver checks, light desktop](screenshots/receivers-light-desktop.png), [missing evidence, light mobile](screenshots/integrity-light-mobile.png), [confirmation dialog](screenshots/confirmation-dark-mobile.png).
