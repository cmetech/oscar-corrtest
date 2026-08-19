# Adversarial Plan Review — OSCAR Correlation Test Harness (Design + Plan 1)

**Review date:** 2026-08-19
**Reviewer:** independent adversarial reviewer (did not author either artifact)
**Verdict scope:** may implementation of Plan 1 begin from the currently reviewed plan?

---

## 1. Scope and evidence inspected

**Reviewed artifacts (digests verified before review):**

```
c401e5587a13c0aa9edc2474f2a5e291a8545481cd75e8da0cf34cf282022908  docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md   (1008 lines — read completely)
6ec8194f450a4e5b28100dc07fac94735842cf87d7b24e1947f41d87064e9dc7  docs/superpowers/plans/2026-08-19-oscar-corrtest-repository-foundation.md    (751 lines — read completely)
```

Both digests matched the review contract. **`oscar-corrtest` repository state at review time:** HEAD
`a68502ed87f9def32cb2db18980ba0f7f13db47e` (descendant of the initial docs-only commit
`74adce8…`, as the contract permits), containing exactly one file:
`docs/reviews/2026-08-19-oscar-corrtest-adversarial-plan-review-prompt.md`. Confirmed: **no**
implementation source, `go.mod`, `Makefile`, CI workflow, or packaging file exists. Nothing was
added, edited, committed, or pushed by this review other than this output file.

**Comparison / provenance sources read (all cited claims verified against these files):**

- `docs/superpowers/specs/2026-05-02-oscar-testkit-design.md` (466 lines, read completely)
- `oscar/oscar-correlator/src/app/routers/rules.py`, `routers/audit.py`, `routers/debug.py`,
  `schemas/rule.py`, `schemas/match_criteria/*.py` (all 8 patterns), `core/rule_store.py` (spot),
  `core/synthetic_emitter.py` (complete), `core/consume.py` (dispatch-gate region), `core/db.py`
  (CorrelationRules model), `src/app/main.py` (router mounts)
- `oscar/oscar-alertmanager/src/app/routers/alerts.py` (`insert_alert` complete; history endpoints),
  `core/fingerprint.py` (complete), `core/correlation_ingest.py` (complete), `core/settings.py` (spot),
  `core/db.py` (AlertHistory spot)
- `oscar/oscar-middleware/src/app/routers/alerts.py` (injection proxy), `routers/correlation_rules.py`
  (route map), `routers/notification_audit.py`, `core/route_permissions.py` (alerts + correlation domains),
  `schemas/correlation_rules.py` (via CLAUDE-verified summary + route file)
- `oscar/oscar-taskmanager/src/tm_notifier/handlers.py` (suppression-consumption region),
  `tm_notifier/fingerprint.py` (byte-parity diff)
- `oscar/oscar-util/scripts/send_alert.py`, `send_custom_alert.py`, `send_alert_performance.py`,
  `scripts/apf/models.py` (APF precedent), `oscar/oscar-util/migrations/add_correlation_rules_table.sql`
- `oscar/oscar-docs/docs/12-reference/rule-engine.md`, `oscar/oscar-docs/docs/07-api-reference.md`
- OTTO Gateway: `internal/admin/templates/base.html.tmpl`, `templates/dashboard.html.tmpl`,
  `static/css/admin.css` (token + theme-mechanism comparison)

**Network verification (read-only, all succeeded — none marked UNPROVEN for lack of network):**
GitHub API tag→SHA resolution for three actions; Docker Hub manifest for `golang:1.27.0-bookworm`;
GitLab registry manifest for `gitlab-org/cli:v1.109.0`; `go.dev/dl` release index; gosec/govulncheck
release existence; glab v1.109.0 `release create` documentation.

**Disposable experiments:** none were required; no temporary directory was created. All Plan 1
commands were statically audited (§8) — two were falsified by inspection against the repository's
actual current state and by git semantics, not by execution.

---

## 2. Executive verdict for beginning Plan 1

## **READY WITH REQUIRED CHANGES**

Plan 1 implementation may begin **after** the four required plan edits in §10 (items 1–4) are
applied. Two of its commands are confirmed-broken as written against current reality (HIGH-3,
HIGH-4), and its leak scanner will reject content the repository already contains (MED-1). None of
these are boundary or architecture defects — the repository boundary, Make-only CI contract,
loopback default, and interface layering are sound and Plan 1 freezes nothing that the two
design-level HIGH findings (which target Slices 2+) would need to change.

The **design**, however, must be amended before the first OSCAR-contact plan (Plan 3 / flood
end-to-end) is authored: it is silent about the single largest evidence-availability fact in
current OSCAR — the Phase A/Phase B dispatch gate (HIGH-1) — and it does not state how the harness
acquires the alert fingerprints that are the *only* key into the correlation-audit evidence
surface (HIGH-2).

---

## 3. Blocking findings

**None.** No finding meets the BLOCKER bar (wrong product boundary, unsafe mutation behavior,
unusable foundation, or unprovable product *from Plan 1*). The strongest candidate — HIGH-1 —
was evaluated for BLOCKER status and rejected: Plan 1 makes no OSCAR contact, freezes no evidence
interface, and the design's own compatibility-mode rule (§13.2: weak proof → `INCONCLUSIVE`,
design line 523) shows the correct posture already exists in principle; what is missing is the
specific fact it must be applied to.

---

## 4. High-severity findings

### HIGH-1 — The design is silent about OSCAR's dispatch gate (Phase A/Phase B); on a default deployment the synthetic-parent and notifier evidence surfaces do not exist, and capability discovery cannot detect that

- **Classification:** CONFIRMED
- **Evidence:**
  - `oscar/oscar-correlator/src/app/core/consume.py:542-560` — audit rows are buffered
    **unconditionally** ("Phase A still records what WOULD have notified"), but synthetic-parent
    emission and notifier dispatch are gated on
    `fail_open_state.correlator_dispatch_enabled`; `consume.py:565-572` shows `synthetic_emitter.emit()`
    runs only inside that gate.
  - `CORRELATOR_DISPATCH_ENABLED` defaults `false` and fresh installs deliberately stay Phase A
    (alertmanager Phase-99 cutover documentation; compose default `${CORRELATOR_DISPATCH_ENABLED:-false}`).
    Additionally, if `CORRELATOR_NATS_PUBLISH_ENABLED=false` the correlator receives **no alerts at
    all** — not even audit rows.
  - `grep -i 'dispatch|phase a|phase b|CORRELATOR_DISPATCH|NATS_PUBLISH|fail.open'` over the design
    returns **zero matches** (verified). Design §13.1 (lines 498–509) and §13.2 (lines 511–523) list
    seven required improvements; none covers pipeline/dispatch-mode discovery. No public endpoint
    exposes the mode: the middleware correlation surface (`route_permissions.py:278-324`) proxies rule
    CRUD, audit reads, guardrail config and debug only.
- **Concrete failure scenario:** an operator points the harness at a stock OSCAR install (Phase A).
  The flood positive case (design §10.2, lines 279–291) asserts both `audit-count parent_emitted == 1`
  **and** `synthetic-alert-count == 1`. The audit assertion passes (audit rows are written in
  Phase A); the synthetic-alert assertion finds no alert-history parent — verdict `FAIL` or
  `INCONCLUSIVE` against a *correctly functioning* correlator. Worse, the mirrored negative case
  (`synthetic-alert-count == 0` via alert history) passes **vacuously** on Phase A whatever the rule
  does — the exact "PASS without observing the intended OSCAR outcome" class the review contract rates
  at least HIGH. Parent-child per-notifier suppression (§11 row 6) has *no* observable effect
  surface at all under Phase A (dispatch is off; the Phase-94 notifier handlers only see
  correlator-dispatched traffic).
- **Why the design misses it:** §13.3's capability model is organized around API-profile/pattern
  support, not deployment pipeline state; dispatch mode is a runtime config flag, invisible to every
  surface the adapter is designed to probe.
- **Minimum specific remediation:** (1) add to §13.2 an item 0: *"a discoverable pipeline-mode
  signal (NATS publish enabled; correlator dispatch enabled) — until it exists, dispatch mode is an
  operator-declared target property, verified at qualification, never assumed"*; (2) require the
  capability snapshot (§13.3) to record the mode; (3) require the compiler to mark alert-history
  synthetic-parent assertions and all notifier-behavior assertions **unsupported → `SKIPPED`/
  `INCONCLUSIVE` (never `PASS`)** on Phase-A targets, keying positive *and negative* proof to
  correlation-audit rows (which exist in both phases — `consume.py:540`).
- **Closing test/gate:** a contract test against the fake OSCAR server in Phase-A profile proving a
  negative synthetic-parent scenario yields `INCONCLUSIVE`/`SKIPPED`, not `PASS`; a §21.5
  qualification checklist item pinning the target's two flags before any verdict is trusted.
- **Blocks:** design approval for Slice 2+ / Plan 3 authoring. **Does not block Plan 1** (no OSCAR
  contact; no interface frozen).

### HIGH-2 — The audit evidence surface is keyed only by exact alert fingerprint, injection returns no fingerprint, and the design never states how the harness acquires one

- **Classification:** CONFIRMED
- **Evidence:**
  - `oscar/oscar-correlator/src/app/routers/audit.py:63-67` — `GET /audit` takes
    `fingerprint` (required); `audit.py:104-108` — `GET /audit/children` takes `parent_fingerprint`
    (required). No filter by run, rule, label, outcome, or time range exists (design §13.2 item 3
    correctly requests one).
  - `oscar/oscar-alertmanager/src/app/routers/alerts.py:848` — the `POST /alerts` 201 response is
    `DBResponseSchema(id=<celery task id>, status="Alert group processing initiated in async mode")` —
    **no per-alert fingerprint is returned**. The `oscar_fingerprint` is stamped server-side at
    lines 790–815, *after* severity normalization mutates `labels["severity"]` (lines 761–765) and
    after `am_fingerprint` preservation (lines 740–742).
  - The fingerprint algorithm (`oscar-alertmanager/src/app/core/fingerprint.py:248-264`: SHA-256 of
    sorted-key JSON of labels minus two exclusion sets, first 12 hex chars) is an internal,
    thrice-copied module (byte-parity across alertmanager/taskmanager/correlator verified by diff)
    with an **env-configurable** dynamic exclusion input (`FINGERPRINT_EXCLUDE_PATTERNS`, SNMP-only).
- **Concrete failure scenario:** the harness computes a child fingerprint client-side in Go; a
  future OSCAR release adds one label name to `_LEGACY_STATIC_EXCLUSIONS` (it has grown twice —
  fingerprint.py:67-109 documents both waves). The harness's audit query is now scoped to a
  fingerprint no audit row carries. A negative case queries `/audit?fingerprint=<wrong>` and finds
  zero `parent_emitted` rows → **false PASS** — precisely review-contract invariant 16's "query
  scoped to the wrong fingerprint."
- **Why the design misses it:** §10.3 says assertions "use the strongest available identifier" and
  §12.1 puts audit rows at the top of the evidence hierarchy, but no section connects the two: the
  strongest identifier for the audit surface *is the fingerprint*, and nothing states where it comes
  from in compatibility mode.
- **Minimum specific remediation:** specify in §12/§13 the fingerprint acquisition contract:
  the harness **reads the fingerprint back** from alert history (rows are filterable by the exact,
  run-unique `alertname` column — `alerts.py:2048-2063` maps it; `AM_AlertHistory.alertname` is an
  indexed `String(255)`, `oscar-alertmanager/src/app/core/db.py:56,135`) and only then queries audit
  by that server-assigned value; client-side computation is at most a cross-check and never an
  assertion key. Add "injection response returns per-alert `oscar_fingerprint`" as an explicit
  §13.2 candidate improvement (items 3–4 subsume it, but the compat-mode bridge must be stated).
- **Closing test/gate:** contract test: inject against the fake server, resolve the fingerprint via
  history read-back, assert the audit query uses the read-back value; a mutation test that perturbs
  the client-side computation must not change any verdict.
- **Blocks:** Plan 3 (flood end-to-end) design/authoring. Not Plan 1.

### HIGH-3 — Plan 1 Task 1 Step 1 fails as written: the repository it creates already exists

- **Classification:** CONFIRMED
- **Evidence:** plan lines 89–98 (`mkdir oscar-corrtest; cd oscar-corrtest; git init -b main`) versus
  the actual repository at `oscar_app/oscar-corrtest` (HEAD `a68502e`, two commits, containing
  `docs/reviews/`). `mkdir` exits 1 with `File exists`; `git init -b main` in an existing repo is a
  reinitialization no-op that does **not** rename the current branch. The planned repository
  structure (lines 35–65) omits `docs/reviews/` entirely.
- **Concrete failure scenario:** an agentic executor (the plan mandates
  superpowers:subagent-driven-development, line 3) hits `mkdir: File exists` on its first command of
  its first task. Best case: it stops. Worst case: it "repairs" by removing or re-creating the
  directory, destroying the committed review-contract history — mutation of history the plan never
  authorized.
- **Why the plan misses it:** the plan was authored when `oscar-corrtest` was intentionally absent;
  the review-contract hardening commit (74adce8/a68502e) post-dates it.
- **Minimum specific remediation:** rewrite Step 1 to: *verify* the existing repo (top-level ends in
  `/oscar-corrtest`, branch `main`, HEAD descends from `74adce8…`, worktree clean, contains only
  `docs/reviews/`), and proceed without `mkdir`/`git init`. Add `docs/reviews/` to the planned
  structure listing.
- **Closing gate:** Task 1 Step 1's own expected-output check (already present) — once corrected it
  self-verifies.
- **Blocks:** **Plan 1, Task 1** — first command executed.

### HIGH-4 — `make mod-check` cannot succeed inside the Task 7 clean-checkout archive: `git diff` requires a repository the extracted archive does not have

- **Classification:** CONFIRMED
- **Evidence:** the Makefile contract (plan lines 443–446) defines
  `mod-check: go mod verify; go mod tidy; git diff --exit-code -- go.mod go.sum`.
  `scripts/test-standalone.sh` step 6 (plan line 653) runs `GOWORK=off make mod-check test build`
  **inside a `git archive HEAD` extraction in a fresh `mktemp -d` directory** (plan lines 650–652).
  `git archive` output contains no `.git`; a temp directory under `/tmp`/`$TMPDIR` has no ancestor
  repository; `git diff` there exits 128 (`fatal: not a git repository`), so `mod-check` fails, so
  `standalone-check` fails.
- **Concrete failure scenario:** Task 7 Step 4's acceptance command (`make standalone-check`,
  expected PASS, plan lines 700–708) is red on its first run. After Task 7 Step 1 wires
  `standalone-check` into both CI systems (plan line 663), **every GitHub and GitLab pipeline run
  fails**, violating plan invariant "every commit buildable and testable" for the final two commits.
- **Why the plan misses it:** `mod-check` was designed for the in-repo lane; Task 7 reuses it in the
  one environment where its git dependency is guaranteed absent, and no task step runs the archive
  lane before the acceptance gate.
- **Minimum specific remediation:** either (a) make the archive lane call an archive-safe module
  gate — `go mod verify && go mod tidy -diff` (exits non-zero when tidy would change anything; no
  git required) — or (b) guard the git half of `mod-check` with
  `git rev-parse --is-inside-work-tree >/dev/null 2>&1 &&` and rely on `go mod tidy -diff` for the
  archive. Keep the stricter git-diff form for the in-repo CI lane.
- **Closing gate:** Task 7 Step 4 itself, once the target is fixed — plus one negative fixture run
  proving a deliberately-untidy `go.mod` still fails the archive lane.
- **Blocks:** **Plan 1, Task 7/8** and both CI pipelines from Task 7 onward.

---

## 5. Medium- and low-severity findings

### Medium

**MED-1 — The standalone leak scanner will reject content the repository already contains, and its documentation exemption is unspecified.** CONFIRMED.
Plan line 648: the scan rejects `../oscar`, `/oscar_app/oscar`, and `github.com/cmetech/oscar/`
"outside documentation describing the prohibition." Two committed/planned files trip it:
(1) `docs/reviews/2026-08-19-…-prompt.md` (already committed) contains `/oscar_app/oscar…` paths
throughout (and this review file adds more); (2) Task 1 Step 2 copies the design into
`docs/specs/…`, and the design contains the literal `` `../oscar` `` (design line 908). Neither is
"documentation describing the prohibition," so a faithful implementation fails; a loose
"all markdown is exempt" implementation lets a real leak hide in a doc — the exact hole the review
contract's campaign 6 names. *Failure:* `make standalone-check` red on day one, or a scanner that
cannot detect doc-laundered references. *Remediation:* invert the model — scan **only** build/source
file classes (`*.go`, `go.mod`, `go.sum`, `Makefile`, `scripts/**`, `.github/**`, `.gitlab-ci.yml`,
`packaging/**`, `internal/web/templates/**`, `static/**`) and exempt `docs/**`/`README.md` by path;
keep the Task 7 Step 2 red-team fixture inside a scanned class. *Gate:* red-team fixture test (plan
line 667) plus a second fixture proving a forbidden string inside `docs/` does **not** fail the
scan (documented as out-of-scope) while one inside `internal/` does. Blocks Plan 1 Task 7 as
written (bundled with HIGH-4's fix window).

**MED-2 — The non-loopback bind guard is designed but unowned: no Plan 1 stub, no acceptance criterion, no named follow-on owner.** CONFIRMED.
Design line 785: binding non-loopback "requires an explicit flag and configured UI authentication or
… authenticated reverse proxy." Plan 1 Task 2 Step 5 (`--listen` with default `127.0.0.1:8787`)
accepts **any** address with no warning or refusal; design §24's 18 acceptance criteria (lines
977–994) contain **no** bind/auth criterion; the follow-on topic list (plan line 31) names no owner.
This is deferral-without-gate (review invariant 25) on the one item whose failure mode is an
unauthenticated mutation surface the moment a later slice adds POST routes. *Failure:* Plan 4-era
binary run with `--listen 0.0.0.0:8787` exposes rule-creation/alert-sending UI with no auth and no
warning, because nothing ever forced the guard to land before the mutations. *Remediation
(minimum):* in Plan 1, refuse a non-loopback `--listen` unless an explicit
`--allow-remote-unauthenticated` flag is passed and print a persistent warning (≈10 lines now,
enforced before any mutation surface exists); or add a §24 acceptance criterion + name the owning
plan. *Gate:* command test: `serve --listen 0.0.0.0:1` exits non-zero without the override flag.

**MED-3 — The design leaves the injection door ambiguous; one of the three real doors rewrites labels.** CONFIRMED.
Design §13.1 (line 505): "alert injection through the supported middleware or Alertmanager-facing
route." Reality: (a) middleware `POST /api/v1/alerts` is a **verbatim passthrough**
(`oscar-middleware/src/app/routers/alerts.py:159-176` — `model_dump_json()` forwarded unmodified;
label-safe); (b) middleware `POST /api/v1/alerts/webhook` runs the 15-stage operator-configured
mapping pipeline (`translate_alert` — `add~`/`replace~`/`remove~`/`keep~` can drop or rewrite any
`oscar_test_*` label); (c) upstream Prometheus Alertmanager `/api/v2/alerts` (the APF door,
`send_alert_performance.py:566`) adds AM grouping/dedup semantics between the harness and OSCAR.
*Failure:* an adapter profile that selects door (b) on a target with a `keep~`-style mapping loses
the entire reserved label contract silently; assertions then reject everything as ambiguous —
mass `INCONCLUSIVE` (or worse, name-fallback matching passes against distorted evidence).
*Remediation:* name door (a) as the supported injection route in §13.1 and add a preflight
label-survival probe (inject one alert, read history, verify every reserved label round-trips)
before any run mutates the target. *Gate:* qualification checklist + contract test.

**MED-4 — Injection acceptance is ambiguous in ways the design does not enumerate: three 2xx-with-drop paths and two rate limiters.** CONFIRMED.
`insert_alert` returns 2xx while dropping the payload for: ACL filter
(`alerts.py:663-666` — `status="Alert group filtered by ACL rule…"`), per-fingerprint rate limit
(`alerts.py:715-718` — keys on the AM `fingerprint` field / `am_fingerprint` label, lines 699–703),
and circuit-breaker fail-closed (`alerts.py:612-625` — 200 `"queued": true`). Success itself returns
only a Celery task id (line 848). A global request limiter (default 100 requests/60s,
`core/settings.py:36-37`) bounds burst pacing. Design §12.1 correctly demotes transport acceptance
to supporting evidence, but the adapter contract never says the *body* must be parsed to
distinguish accepted-and-dispatched from accepted-and-dropped. *Failure:* the flood case sets the
AM `fingerprint` field on its 5 identical repeats; repeats 2–5 are rate-limit-dropped with 2xx; the
positive case reads `parent_emitted == 0` and reports **product FAIL** for a harness-inflicted
condition. *Remediation:* adapter must (1) never set `fingerprint`/`am_fingerprint` on injected
alerts, (2) treat any non-"processing initiated" status string as injection failure, (3) respect
the target's global limiter in the §19.1 mutation budget. *Gate:* contract tests over recorded
fixtures for all three drop bodies.

**MED-5 — Correlator readiness is not reachable through any public surface; §13.1's "service health and readiness" is only partially true.** CONFIRMED.
The correlator's `/health`+`/ready` exist internally on :5400 (`main.py:103-137`), but the
middleware exposes no proxy for them — the correlation domain proxies only CRUD, audit reads,
guardrail config, and debug (`route_permissions.py:278-324`). Preflight can infer liveness from a
`GET /correlation_rules/guardrails/config` 200 but cannot distinguish "correlator degraded (NATS
consumer down — `/ready` 503)" from healthy. *Failure:* preflight passes on a correlator whose NATS
consumer is dead; all injected alerts are never evaluated; every negative case sees zero audit rows
and (absent HIGH-1's fix) risks vacuous PASS; positive cases burn full windows and report FAIL
against a healthy rule engine. *Remediation:* add to the §13.2 ledger: "externally reachable
correlator readiness (or readiness folded into the capabilities endpoint)"; until then, preflight
must classify inability-to-verify-readiness as a capability limitation recorded in the run.

**MED-6 — Rule creation semantics need pinning: import mutates by name; create collisions surface as unhandled 500s.** CONFIRMED.
`POST /rules/import` is upsert-by-name (`rules.py:414-430` — an existing `CorrelationRules.name`
match is UPDATEd). `CorrelationRules.name` is UNIQUE (`add_correlation_rules_table.sql:35`,
`uq_correlation_rules_name`), and `POST /rules` (`rules.py:179-214`) performs a plain INSERT with no
IntegrityError handling — a duplicate name propagates as an unhandled 500, and the rule may or may
not exist afterwards from the client's view. *Failure:* a batch-minded adapter "optimizes" rule
setup through the import endpoint; an operator rule named `corrtest-flood-p01-<short>` (manually
created lookalike — campaign 3's exact probe) is silently **overwritten**, violating "never updates
or deletes a rule it did not create" (§19.1). *Remediation:* design/adapter contract: temporary rules
are created **only** via `POST /rules` (never import/batch-upsert paths); a 5xx on create is an
unknown-outcome that must be reconciled by unique-name read-back (design §20 row 2 already requires
reconciliation — name this mechanism) before any retry. *Gate:* contract test: create-collision
fixture → adapter reconciles, never re-POSTs blindly, never deletes the pre-existing rule.

**MED-7 — "Deterministic Linux packages" is claimed but not specified; tar archives are not reproducible as planned, and macOS/Linux tar divergence is unaddressed.** CONFIRMED (as a claim/spec mismatch).
Plan goal (line 5) and Task 4 promise deterministic packaging; `scripts/package.sh` (lines 466–471)
stages and tars with no `--sort`, `--mtime`, `--owner/--group/--numeric-owner` normalization —
archive bytes then depend on filesystem mtimes and the local tar implementation (macOS ships bsdtar;
CI uses GNU tar), so `SHA256SUMS` differs across rebuilds of the same commit. The Go **binary** is
reproducible (`-trimpath -buildvcs=false`, commit-derived `BUILD_DATE` — lines 422–426 — a good
choice); the archive is not. *Failure:* release verification compares a locally rebuilt archive
hash to the published one; mismatch reads as tampering. *Remediation:* either normalize (GNU tar
`--sort=name --owner=0 --group=0 --numeric-owner --mtime=@<commit epoch>`; require `gtar` on macOS
or declare packaging Linux-CI-canonical) or reword the claim to "reproducible binaries, versioned
archives." *Gate:* CI step packaging twice and diffing checksums.

### Low

**LOW-1 — Theme toggle a11y: `aria-pressed` plus a next-action accessible name is a contradictory combination.** PREFERENCE (with a concrete confusion mode).
Plan Task 3 Step 4 requires the label to describe the next action ("Use light theme") *and*
`aria-pressed`. A screen reader announces "Use light theme, toggle button, pressed" — pressed
*what*? Pick one convention: state-named button + `aria-pressed`, or action-named button without
it. (OTTO, for comparison, uses a static "Toggle theme" label and no pressed state —
`base.html.tmpl:18` — the design already improves on it; just don't improve into a new ambiguity.)

**LOW-2 — Task 2's content-type table will fail against correct implementations if asserted by equality.** The table says `application/json` / `text/css`; Step 3 mandates
`application/json; charset=utf-8`. Specify prefix matching in the test contract.

**LOW-3 — Task 1 Step 2 wording:** "byte-for-byte … changing only its status" is self-contradictory,
and `apply_patch` is a tool-specific verb not all executors have. Also, copying the design into the
repo creates a second mutable copy of a document the review contract pins by checksum at the
workspace path — note the workspace original as canonical, or record the copy's provenance hash.

**LOW-4 — Design §22.2 / Plan Makefile drift:** the design's minimum target set names `ci`
(line 912); the plan ships `ci-core` plus `tools`/`mod-check`/`security`/`standalone-check` and no
`ci`. Reconcile the names (report-only: plan is the better contract; update the design).

**LOW-5 — systemd example gaps:** the unit references `User=oscar-corrtest` with no user-provisioning
note; consider `CapabilityBoundingSet=`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`,
`ProtectProc=invisible`, `MemoryDenyWriteExecute=yes` while the binary is this small. Fine as an
example otherwise; `ProtectSystem=strict` with no writable path is correct for Plan 1 and the plan
documents the SQLite-era change.

**LOW-6 — Relationship to the 2026-05-02 `oscar-testkit` proposal is unstated.** The design's §3
assesses scripts but never positions itself against the prior testkit brief, whose alert-flow track,
scheduled regression runs (testkit M1 ships cron scheduling — testkit lines 331, 344), and
multi-track plans overlap this product. One paragraph stating supersession/coexistence and that
recurring execution is delegated to CI closes it. Checked for silently lost requirements: scheduled
runs is the only material one; multi-target, JUnit, retention, capability snapshot, and
report-format requirements all survive in the new design.

**LOW-7 — Negative-proof anchor exists and should be named.** Verified: non-triggering evaluated
alerts produce audit rows (`released_no_trigger` / `pass_through` are buffered unconditionally —
`consume.py:540`; persistence's early-resolve cancel even writes a dedicated
`persistence_resolved_cancelled` reason — `patterns/persistence.py:250-277`). The design's negative
cases should key on these positive artifacts ("child audited with non-triggering outcome") rather
than only on absence-of-parent — this works in Phase A too and materially strengthens invariant 16.
Caveats to note in §12.2: audit rows arrive via a 5s/1000-row buffered flush, can be dropped under
extreme backpressure (buffer cap trims oldest), and are pruned at a 90-day default retention.

**LOW-8 — Preflight should read the guardrail config.** GRD-01 caps (window ≤3600s, matcher counts,
synthetic-rate hard cap) 400-reject rules at save time (`rules.py:190-193`); the public
`GET /correlation_rules/guardrails/config` exists (`route_permissions.py:316`). Add it to the §13.3
capability snapshot so the compiler validates timing budgets against target limits before mutating.

**LOW-9 — CI cache keyed by `go.mod`:** switch to `go.sum` when the first dependency arrives
(no-op today; noted so it doesn't fossilize).

**LOW-10 — `glab release create` via `CI_JOB_TOKEN`:** the flag and version are verified (below);
job-token release creation on the own project is documented GitLab behavior, but the first live tag
push should confirm the end-to-end (package-registry upload + release link) — listed in §12
residuals.

---

## 6. Invariant verdict matrix (all 25)

| # | Invariant | Verdict | Basis |
|---|---|---|---|
| 1 | Genuinely standalone repository | **PASS** (as planned) | Constraints lines 15–20 (`GOWORK=off`, `CGO_ENABLED=0`, no `../oscar` reads, no Python/Node/Docker); module `github.com/cmetech/oscar-corrtest` never matches the `cmetech/oscar/` scan pattern. Proof *mechanics* are broken until HIGH-4/MED-1 fixes land. |
| 2 | Every commit buildable/testable; CI never references missing targets | **FAIL** (as written) | HIGH-3 (Task 1 Step 1 unexecutable) + HIGH-4 (Task 7/8 gate red; both CIs red after wiring). Commit ordering otherwise verified sound: ci.yml (Task 5) references only Task 4 targets; standalone-check is wired to CI in the same Task 7 commit as its script (plan line 663). |
| 3 | Makefile is the single CI behavior contract | **PASS** (as planned) | Tasks 5/6 run only `make …`; Task 6 Step 3 (plan lines 615–617) asserts no raw `go test/build/vet` in either YAML. Residual: GitHub resolves Go via setup-go/go.mod while GitLab pins the image + `GOTOOLCHAIN=local` — equivalent for 1.27.0; re-check on version bumps. |
| 4 | Release tags/permissions/pins/credentials/artifacts valid, not plausible YAML | **PASS** (network-verified) | actions/checkout v6.0.2 = `de0fac2e…83dd`, setup-go v7.0.0 = `b7ad1dad…303e`, upload-artifact v7.0.1 = `043fb46d…6a0a` — all exact (GitHub API, commit-type refs). `gh release create --verify-tag --generate-notes` valid; `permissions: contents: write` + tag glob + shell regex re-check correct. GitLab: image digests exact (below); `--use-package-registry` exists in glab v1.109.0 (docs lines 130/135). Residual: LOW-10 live-run confirmation. |
| 5 | Versions real, compatible, pinned, available for AMD64+ARM64 | **PASS** (network-verified) | go1.27.0 is a released version (go.dev/dl). `golang:1.27.0-bookworm@sha256:d22fb682…c1cad` — digest exact on Docker Hub, manifest list includes amd64 + arm64. `registry.gitlab.com/gitlab-org/cli:v1.109.0@sha256:4dbd0934…1f456` — digest exact, manifest list. gosec v2.28.0 and golang.org/x/vuln v1.6.0 exist. Race lane: CGO_ENABLED=1 with gcc present on ubuntu-24.04 and golang:bookworm. |
| 6 | CGO-disabled binary deployable; runtime assumptions explicit | **PASS** (Plan 1 scope) | `cross` builds CGO-disabled linux/amd64+arm64; no writable state, cert, or tz need exists yet; the plan explicitly documents the state directory arriving with the SQLite plan (line 698). |
| 7 | Loopback default; pre-paint theme; accessible; no accidental unauthenticated mutation exposure | **PASS for Plan 1 / DEFERRED-WITHOUT-GATE for the bind guard** | Default `127.0.0.1:8787`; nonce-CSP pre-paint script; no mutation routes exist in Plan 1. But any `--listen` is accepted with no warning and no owner for the design-line-785 guard → MED-2. |
| 8 | CLI and UI share services; no divergent correlation implementations | **PASS** (design §7; trivially true in Plan 1) | Single `command.App` + `web.NewHandler`; design's dependency-inward rule (line 186). |
| 9 | SQLite durable ledger (migrations, WAL, retention, backup, crash) | **DEFERRED-WITH-GATE** | Design §14 is unusually complete (WAL+FULL sync, busy_timeout, no-NFS rule, online backup, read-only diagnostic mode on migration failure); §21.1/21.3 name the tests; acceptance criteria 11–12; owner: follow-on "SQLite ledger" (plan line 31). |
| 10 | Ownership = full run ID + OSCAR-returned IDs; names/short tokens never sufficient | **DEFERRED-WITH-GATE** | Design §10.3/§10.4 (line 353: short token "not an authority boundary"), §19.1 (delete by returned id, ownership verify, name-based only as recovery with full token match); §21.1 names collision/ownership tests. |
| 11 | Naming grammar + label set sufficient for all filter dimensions | **DEFERRED-WITH-GATE** | §10.4/§10.5 tables cover harness/run/suite/scenario/pattern/case/polarity/class/role/rule; verified against real surfaces: history filters on `alertname` (indexed, 255 chars — names fit), labels returned in history responses. |
| 12 | Grammar fits OSCAR/Prometheus label restrictions and fingerprint behavior | **PASS** (verified) | Fingerprint = SHA-256 over **labels only** (`fingerprint.py:248-264`); annotations never hashed → §10.6's event-id/index-as-annotations is exactly right. `oscar_test_*` labels are hashed (stable per case → stable fingerprints; distinct runs → distinct fingerprints — correct isolation). `oscar_correlation_*`/`oscar_synthetic_source` excluded (lines 84–109) so correlator stamping never mutates identity. `category` not special-cased at ingest. Alertname is a label value — no charset restriction; `oscar_test_*` are valid label names. |
| 13 | Reserved labels non-overridable; rule matchers compiled from the same physical names | **DEFERRED-WITH-GATE** | §10.5 (compiler owns `alertname`/`category`/`oscar_test_*`; line 387: compiler rewrites rule matches to the same value); §21.1 names reserved-label-rejection tests. Real matcher shape confirmed exact-alertname only (`schemas/match_criteria/*.py` — every `_MatchClause` is `{alertname}`, `extra="forbid"`). |
| 14 | Identity labels survive ingestion/synthetic/suppression/audit/history/notifier paths | **DEFERRED-WITH-GATE** (correctly declared as OSCAR prerequisites) | Verified today: source→history labels persist and return; synthetic parent carries `emit_spec.labels` (merged post-fingerprint, `synthetic_emitter.py:300-306`) so the harness can thread the full contract; suppression is carried as the `oscar_correlation_suppressed_notifiers` label consumed by handlers (`handlers.py:2605-2711`). **Audit rows carry no labels at all** (CorrelationAudit has only fingerprints/rule/outcome columns) — run-scoping of audit evidence is exactly §13.2 items 1/3, correctly listed. Notification-audit label carriage: UNPROVEN (filters exist for fingerprint/notifier/status — `notification_audit.py:19-35`). Gate: §23 line 971 (enhancements before/alongside Slice 2, else labeled compatibility mode). |
| 15 | 2xx never treated as correlation proof; each outcome class has terminal evidence | **PASS at design level, with MED-4 required precision** | §12.1 hierarchy demotes transport; §12.3 terminal records. The three 2xx-drop paths and body-parsing requirement must be made explicit (MED-4); Phase-A availability of outcome classes is HIGH-1. |
| 16 | Negative assertions observe the full bounded window; cannot pass via delay/skew/missing API/wrong fingerprint | **DEFERRED-WITH-GATE**, two live threats | §11 line 449 ("momentary zero never accepted") + §12.2 windows are right. Threats: wrong-fingerprint scoping (HIGH-2) and Phase-A vacuous absence (HIGH-1) — both must be closed in the gate; LOW-7's positive negative-anchor (audit rows for non-triggering alerts — verified to exist) is the strongest closure. |
| 17 | All eight patterns' stimuli match current implementations | **DEFERRED-WITH-GATE** | Suite table §11 is consistent with verified semantics: flood `min_count`/window (Lua trigger-once); threshold triggers on **PFCOUNT distinct** of `distinct_label`; sequence matches ordered by **event ts** with same-ms arrival tie-break (`sequence.py:130-141`) — stimuli need distinct timestamps; co-occurrence = N distinct alertnames; cross-source keys `oscar_source`→`source` fallback (`cross_source.py:8-9`) — harness must stamp it; persistence floor ≥30s + resolve-cancel with terminal audit reason; absence gates emission on backlog-drain + NATS liveness at fire time; parent-child never emits synthetic (D-19) with outcomes `suppressed_per_notifier`/`enriched`/`released_no_trigger` (`parent_child.py:11-26`). These timing/eligibility subtleties (esp. persistence's 60s effective grace and absence's fire-time gates) belong in the Slice 3–5 plans; gate: §21.5 qualification. |
| 18 | Rule CRUD/validate + evidence APIs exist with assumed semantics, or are explicit prerequisites | **PASS for what is assumed / DEFERRED-WITH-GATE for the declared gaps** | Verified existing: rule list (paginated ≤100, name-search only), validate (non-persisting), create (201), read, PUT partial, DELETE (404-idempotent — good cleanup semantics), import/export; middleware prefix `/api/v1/correlation_rules` with `read:`/`manage:correlation_rules` RBAC; audit reads (fingerprint-keyed, ≤100/page); notification-audit list/export with fingerprint/notifier/status/date filters; history list with column-filter DSL + time range. Declared gaps (§13.2 items 1–7) are genuine — confirmed each against source. Design's per-target prefix discovery matches the real prefix divergence (`/api/v1/correlator/rules` internal vs `/api/v1/correlation_rules` public). |
| 19 | Capability discovery versioned and fail-closed; an older OSCAR cannot run a weakened scenario and report success | **FAIL** (against current OSCAR + current design text) | No capabilities endpoint exists (improvement 6 correctly requested), **and** the one deployment property that most weakens scenarios — dispatch mode — is both undiscoverable and unmentioned (HIGH-1). §13.3's fail-closed intent is right; it cannot be honest until the mode is a declared/discovered target property. |
| 20 | Temp rules can't collide with/mutate operator rules or be prefix-deleted; cleanup retryable/idempotent/auditable | **PASS at design level, with MED-6 precision required** | Unique name constraint surfaces collisions (as a 500 — handle it); `POST /rules` is pure INSERT; DELETE by integer id with 404-as-conclusively-absent; §19.1's rules are correct. Must add: never use the upsert-by-name import path for temp rules; unknown-outcome reconcile by unique-name read-back. |
| 21 | Run serialization, queueing, cancellation, restart, abandoned-run recovery have explicit transitions | **DEFERRED-WITH-GATE** | Design §8 state machine (incl. `INTERRUPTED → RECOVERING`), §20 failure table, §21.3 crash-recovery tests, acceptance criterion 11. |
| 22 | Secrets never in logs/URLs/SQLite/exports/DOM/history; reports redact yet reproduce | **DEFERRED-WITH-GATE** | §12.4 (redact before persistence; no raw HTTP to disk), §18 (credential references only), §19.3; criterion 2 + §21.1 redaction tests. |
| 23 | Reports self-describing and tamper-evident; observed vs interpreted vs missing distinguishable | **DEFERRED-WITH-GATE** | §15 (canonical JSON, artifact SHA-256 manifest, capability-limitation warnings, evidence identifiers per assertion); §14.6 (hash-mismatch surfaced, never silently removed). |
| 24 | Plan 1 interfaces sufficient for later slices without foreseeable rewrites | **PASS** (judgment) | `command.App` with injected `ServeFunc` decouples CLI growth from HTTP; `web.Options`/`NewHandler(version.Info)` keeps assets/lifecycle contained; version package linker contract is final-shaped. Expected churn: `command.New`'s positional signature will need a deps/config struct when targets/DB arrive, and §18's flags>env>file precedence has no Plan 1 seam — both additive, neither a rewrite. |
| 25 | Every deferred obligation has a named follow-on plan or release gate | **FAIL (narrowly)** | Most obligations map to the six named follow-on topics (plan line 31) plus §24 criteria as release gates. Two exceptions with no owner and no criterion: the non-loopback auth guard (MED-2) and the create-vs-import rule-mutation rule (MED-6). Also note: follow-ons are named as topics, not numbered plan documents — acceptable pre-planning, but the topic list should be stamped into each future plan's header for traceability. |

---

## 7. Acceptance-criteria traceability matrix

Design §24, all 18 criteria (lines 977–994). Follow-on plan names use the plan's own
delivery-series topics (plan line 31): P2 = SQLite ledger/report history; P3 = flood end-to-end;
P4 = window/order patterns; P5 = timer patterns; P6 = parent-child evidence; P7 = custom
scenarios/operational hardening.

| # | Criterion (abridged) | Owner | Notes |
|---|---|---|---|
| 1 | One executable, CLI+UI, Linux, no Python/Node/CGO/external DB | **Plan 1** (established) + every later plan (preserved) | Plan 1 delivers and CI-enforces it |
| 2 | Target configured without secret value in SQLite | P2 | Credential-reference model §18 |
| 3 | Validate/create/read/delete uniquely owned rule via public APIs | P3 | APIs verified real (§6 inv. 18); MED-6 applies |
| 4 | Naming convention on every alert/parent/rule | P3 (compiler) | Grammar verified feasible (inv. 12) |
| 5 | Traffic isolation by run/pattern/scenario/case/polarity/role/name/rule | P3 + OSCAR prereqs 3–4 for server-side filters | Client-side filtering possible today via history labels |
| 6 | Fingerprint preservation across repeats unless pattern requires variation | P3 | Verified sound against `fingerprint.py` |
| 7 | Built-in positive+negative for all eight patterns | P3 (flood), P4 (co_occ/seq/cross/thresh), P5 (persist/absence), P6 (parent_child) | |
| 8 | Terminal evidence + documented verdict per case | P2 (verdict/report model) + P3 (first live evidence) | |
| 9 | Negative cases observe full decision windows | P3 onward | HIGH-1/HIGH-2 must be resolved in P3's design inputs |
| 10 | Exactly-one parent cardinality through stabilization | P3 | Emitter dedup verified (deterministic parent fingerprint) |
| 11 | Interrupted runs visible; owned resources cleanable | P2 (ledger) + P3 (cleanup against OSCAR) | |
| 12 | History/assertions/cleanup state survive restart | P2 | |
| 13 | Runs UI filters and reopens historical reports | P2 | |
| 14 | Portable ZIP with manifest hashes, JSON, HTML, JUnit, plan, scenario, evidence | P2 (format) + P3 (first real evidence) | |
| 15 | Light/dark contrast, keyboard, focus, reduced-motion contracts | **Plan 1** | Contract-tested in Task 3; LOW-1 nit |
| 16 | Stable documented CLI JSON envelopes and exit codes | Partially Plan 1 (version/serve codes); full table P2/P3 | §9.3's six-code table needs an owner note in P2 |
| 17 | Mutation/integrity/projection/recovery/backup/UI contract tests pass | Cumulative P1–P7 | |
| 18 | Full suite runs twice on non-prod OSCAR without leftovers/contamination | P7 + §21.5 qualification | |
| — | **ORPHANED items** | **Non-loopback bind guard (design line 785)** — in no criterion and no follow-on topic (MED-2). No other orphan found. | |

Circular prerequisites: none found. The seven-plan claim reconciles with the six design slices:
Slice 1 (line 947) = Plan 1 (executable shell) + Plan 2 (SQLite ledger) — consistent, not a
contradiction.

---

## 8. Plan 1 command and commit-order audit

Audited statically, in stated order. "OK" = internally consistent and consistent with verified
external facts.

| Task/Step | Command/artifact | Verdict |
|---|---|---|
| T1 S1 | `mkdir oscar-corrtest && git init -b main` | **FAILS — HIGH-3** (repo exists, 2 commits) |
| T1 S2 | `go.mod` with `go 1.27.0` | OK (three-part `go` directive valid; version exists) |
| T1 S2 | Design copy "byte-for-byte … change status" via `apply_patch` | Wording defects — LOW-3 |
| T1 S3–S6 | version/command tests → red → implement → `go test ./...` → commit list | OK; Task 2 correctly lists `app_test.go` for the `New(…, ServeFunc)` signature change |
| T2 S1–S6 | HTTP tests (healthz/readyz//, CSP nonce, 405, serve flag) → implement → commit | OK except content-type equality ambiguity (LOW-2). CSP `style-src 'self'` is compatible with the pre-paint script's `style.colorScheme` (CSSOM property assignment is not blocked by style-src). Nonce-per-page + no-store coherent. |
| T3 | Token/contract tests; exact `--ct-*` values | OK — matches design §16.3 exactly; palette verified against OTTO (§ below) |
| T4 S2 | Makefile constants (`git describe --match='v[0-9]*'`, commit-date BUILD_DATE, trimpath) | OK; VERSION falls back to short SHA pre-tag — satisfies Task 8's non-default check |
| T4 S2 | `fmt-check` find/gofmt | OK (repo always has .go files; `.tools` holds binaries, not source) |
| T4 S2 | `mod-check` (`go mod verify; go mod tidy; git diff --exit-code -- go.mod go.sum`) | OK **in-repo** (absent go.sum diffs clean); **FAILS in T7 archive — HIGH-4** |
| T4 S2 | `test-race` `CGO_ENABLED=1` | OK (gcc in ubuntu-24.04 and golang:bookworm) |
| T4 S3 | `package.sh` staging/trap/0755/naming | OK mechanically; determinism claim unmet — MED-7 |
| T4 S5 | tools/ci-core/cross/package/checksums, `tar -tzf` | OK (`sha256sum`/`shasum -a 256` output formats compatible) |
| T5 S1 | Action pins | **VERIFIED EXACT** — checkout v6.0.2=`de0fac2e4500dabe0009e67214ff5f5447ce83dd`; setup-go v7.0.0=`b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`; upload-artifact v7.0.1=`043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` (all commit-type refs) |
| T5 S2 | `release.yml`: `v*.*.*` glob + `^v[0-9]+\.[0-9]+\.[0-9]+$` re-check, `contents: write`, `gh release create --verify-tag --generate-notes` | OK (gh ≥2.31 on ubuntu-24.04) |
| T5 S3 | 40-hex `uses:` pin check inside ci.yml | OK |
| T6 S1 | `golang:1.27.0-bookworm@sha256:d22fb682…c1cad`, `GOTOOLCHAIN=local`, project-local caches | **DIGEST VERIFIED EXACT**; manifest list carries amd64+arm64 |
| T6 S2 | `registry.gitlab.com/gitlab-org/cli:v1.109.0@sha256:4dbd0934…1f456`; `glab release create … --use-package-registry` with `GITLAB_TOKEN=$CI_JOB_TOKEN` | **DIGEST VERIFIED EXACT**; `--use-package-registry` exists in v1.109.0 docs; job-token live confirmation is a §12 residual (LOW-10) |
| T6 S3 | Make-only parity assertion | OK — enforces invariant 3 |
| T7 S1 | `test-standalone.sh` (scan → clean-worktree → mktemp → `git archive` → symlink+scan → `make mod-check test build` → run binary) | Scan-before-clean ordering means the red-team edit **does** reach the scanner (contract satisfied); but **HIGH-4** (mod-check) and **MED-1** (docs/reviews + design copy trip the scan; exemption unspecified) |
| T7 S2 | Red-team fixture exercise | OK once MED-1's scope fix keeps fixtures in a scanned class |
| T7 S3 | systemd unit | OK for Plan 1 scope; LOW-5 |
| T8 | Full gate + `file` arch check + SIGTERM ≤5s + CI audit + no-empty-commit rule | OK — S5's "stage only named files, never `git add .`" is a good guard |

**Commit-order verification:** every commit N leaves `go test ./...` green and no CI file
references a target created after N (ci.yml lands Task 5 referencing Task 4 targets;
`standalone-check` enters CI in the same commit as its script, Task 7). The only violations of
invariant 2 are HIGH-3 (pre-execution) and HIGH-4 (Task 7 gate itself).

**OTTO comparison (campaign 7):** all twelve dark-theme tokens match OTTO exactly
(`admin.css:15-29`), light surfaces match (`admin.css:1400-1410`, header stays `#1E2128` — the
always-dark header claim is real). The design's light-mode **semantic** colors (`#087A49`,
`#A34D00`, `#B42318`, `#704099`) and split accent (`#8A6800` text) do **not** exist in OTTO — they
are harness improvements, correctly so: OTTO keeps `#FAD22D` as a light-mode accent, which fails
text contrast on `#F7F8FA`, and OTTO has no light-mode status colors at all. OTTO's theme
mechanism: hardcoded `data-theme="dark"` (`base.html.tmpl:2`), localStorage-only pre-paint script
with **no `prefers-color-scheme` fallback** (`base.html.tmpl:8`), fixed `color-scheme: dark`
(`admin.css:84`), static toggle label, no `aria-pressed` (`base.html.tmpl:18`). The design/plan
add system-preference fallback, per-theme `color-scheme`, and toggle state — all deliberate,
correctly scoped improvements, none a copied-assumption defect. Only LOW-1 (pressed+action-label
combo) survives as a finding. The plan's split asset files also correctly avoid OTTO's 2171-line
monolithic CSS.

---

## 9. OSCAR prerequisite / API gap ledger

Design §13.2's seven items were each verified against source; all seven are genuine. Three
additions are required.

| # | Prerequisite | Status in current OSCAR (evidence) | Design status |
|---|---|---|---|
| 1 | `oscar_test_run_id` preserved end-to-end incl. audit rows | **Needed.** `CorrelationAudit` carries no labels at all (model: fingerprints/rule/outcome/window only; audit responses mirror it 1:1) | §13.2 item 1 ✔ |
| 2 | Full reserved-label contract across history/synthetic/notification evidence | Partially available: history labels persist and return; synthetic parent carries `emit_spec.labels` (`synthetic_emitter.py:300-306`); notification-audit label carriage UNPROVEN | §13.2 item 2 ✔ |
| 3 | Audit filtering by run/pattern/rule/outcome/time | **Needed.** Only `fingerprint` / `parent_fingerprint` params exist (`audit.py:63-67,104-108`) | §13.2 item 3 ✔ |
| 4 | History filtering by exact label pairs / name prefix / created-at | Partially: column DSL filter incl. `alertname` (indexed) + `last_occurrence` range; **no server-side label filtering** (`apply_filters_to_select(…, [AlertHistory])`, `alerts.py:2119-2123`) | §13.2 item 4 ✔ |
| 5 | Idempotency keys for rule creation and alert injection | **Absent.** No header handling anywhere in `rules.py` / `insert_alert`; workaround = unique rule name read-back + fingerprint-stable alerts | §13.2 item 5 ✔ |
| 6 | Capabilities/version endpoint | **Absent** — and MUST also expose **pipeline mode** (`CORRELATOR_NATS_PUBLISH_ENABLED` / `CORRELATOR_DISPATCH_ENABLED`) per HIGH-1 | §13.2 item 6 ✔ — amend |
| 7 | Machine-readable error codes | **Absent** (detail strings/arrays; guardrail 400s are prose) | §13.2 item 7 ✔ |
| **8 (new)** | Externally reachable correlator readiness (or readiness in the capabilities endpoint) | `/ready` exists internally on :5400 only; no middleware proxy (`route_permissions.py:278-324`) | **Add** (MED-5) |
| **9 (new)** | Injection response returns per-alert `oscar_fingerprint` (or items 3–4 land first) | 201 returns Celery task id only (`alerts.py:848`) | **Add** (HIGH-2) |
| **10 (new)** | Public API documentation for the correlation surface | `07-api-reference.md` documents no correlation_rules / notification-audit / main `POST /alerts` endpoints | **Add** (doc-only) |

Ledger side-notes for the adapter's threat model (target-side, not harness defects):
`X-Internal-Service` is spoofable through `/ext/mw` (documented open M-16) — the adapter must never
rely on or send it; the middleware injection door `POST /api/v1/alerts` is the label-safe one
(MED-3); global injection limiter defaults 100 req/60s.

---

## 10. Required plan changes before implementation (ordered)

1. **Rewrite Task 1 Step 1** (HIGH-3): replace `mkdir`/`git init` with verification of the existing
   docs-only repository (top-level path, branch `main`, HEAD descends from `74adce8`, clean
   worktree); add `docs/reviews/` to the planned structure listing (lines 35–65).
2. **Make the archive lane module-gate archive-safe** (HIGH-4): in Task 7, run
   `go mod verify && go mod tidy -diff` inside the extraction (or guard `mod-check`'s `git diff`
   with `git rev-parse --is-inside-work-tree`); keep the git-diff form for the in-repo lane.
3. **Specify the leak scanner's scope as an explicit file-class allowlist** (MED-1): scan only
   `*.go`, `go.mod`/`go.sum`, `Makefile`, `scripts/**`, `.github/**`, `.gitlab-ci.yml`,
   `packaging/**`, `internal/web/**` assets; exempt `docs/**` and `README.md` by path; keep the
   red-team fixture in a scanned class; add the negative fixture (doc reference does NOT trip;
   source reference does).
4. **Add the non-loopback refusal stub to Task 2 Step 5** (MED-2): non-loopback `--listen` requires
   `--allow-remote-unauthenticated` and prints a persistent warning; add a command test. (Alternative
   accepted: a §24 acceptance criterion plus a named owning plan — but the 10-line stub is cheaper
   than the traceability.)
5. **Fix the packaging determinism claim** (MED-7): either add GNU-tar normalization flags (and pin
   the tar implementation) or reword "deterministic packages" to "reproducible binaries."
6. **Minor:** prefix-match content types in Task 2 tests (LOW-2); reword Task 1 Step 2's
   byte-for-byte/status sentence and drop the tool-specific `apply_patch` verb (LOW-3); reconcile
   `ci` vs `ci-core` naming with design §22.2 (LOW-4); resolve the toggle `aria-pressed`/label
   convention (LOW-1).

**Required design changes before Plan 3 (first OSCAR-contact plan) is authored** — not Plan 1
blockers, listed here because §13/§12 edits are cheapest now:
add dispatch-mode discovery/declaration + Phase-A assertion gating (HIGH-1); specify fingerprint
acquisition via history read-back (HIGH-2); name `POST /api/v1/alerts` as the injection door + add
the label-survival preflight probe (MED-3); enumerate the 2xx-drop response handling and the
no-AM-fingerprint rule (MED-4); add prerequisites 8–10 to §13.2 (MED-5/HIGH-2/docs); pin the
create-only rule-creation contract and 500-collision reconcile (MED-6); name the audit-row
negative-proof anchor and the audit flush/retention caveats (LOW-7); add guardrail-config reading
to the capability snapshot (LOW-8); add one paragraph positioning this design against the
2026-05-02 testkit proposal (LOW-6).

---

## 11. Deferred items with mandatory gates

| Deferred item | Owner | Mandatory gate |
|---|---|---|
| SQLite schema/migrations/WAL/backup/recovery | P2 | §21.1 repository+migration tests; §21.3 crash-after-rule-creation recovery test; criteria 11–12 |
| OSCAR adapter, capability snapshot, preflight | P3 | Contract tests over recorded fixtures for every assumed route incl. the three 2xx-drop bodies; **Phase-A profile test proving weakened assertions cannot PASS** (HIGH-1 gate) |
| Scenario compiler, reserved-label enforcement, naming | P3 | §21.1 reserved-label rejection + name normalization + collision tests |
| Fingerprint acquisition strategy | P3 | History read-back contract test (HIGH-2 gate) |
| Cleanup engine (create/verify/delete/reconcile) | P3 | Crash-between-create-and-record test; lost-response reconcile-by-unique-name test; MED-6's collision fixture |
| Window/order patterns + stimuli precision (event-ts ordering, distinct labels) | P4 | Per-pattern positive/negative fixture tests mirroring `schemas/match_criteria/*` + `patterns/*` semantics |
| Timer patterns (persistence 60s effective grace; absence fire-time gates) | P5 | Fake-clock tests + §21.5 live qualification |
| Parent-child + notifier evidence | P6 | Phase-B-only qualification lane; notification-audit label-carriage verification (prereq 2) |
| Custom scenarios, backup/retention controls, systemd/OCI, browser tests | P7 | §24 criteria 14–18 |
| Non-loopback auth guard | **currently unowned — must gain an owner via §10 item 4** | Command test refusing non-loopback without explicit override |

---

## 12. Residual operator-only or live-system verification

Cannot be closed by any amount of static review or fake-server testing:

1. **GitLab release lane, live:** first real `v*.*.*` tag on the actual GitLab project — confirms
   `CI_JOB_TOKEN` acceptance by `glab release create`, generic-package-registry upload, and release
   asset links (LOW-10). GitHub equivalent on the real repo (branch protection/permissions
   interplay).
2. **Phase-B qualification environment:** per §21.5 — per-notifier parent-child suppression, live
   synthetic-parent history evidence, `hold_until` pacing effects; requires an OSCAR with
   `CORRELATOR_NATS_PUBLISH_ENABLED=true` + `CORRELATOR_DISPATCH_ENABLED=true` and the
   `CORRELATION_EVENTS` notifier enabled.
3. **Label-survival probe on each real target** (MED-3) — operator mappings and ACL rules are
   deployment-specific and unknowable statically.
4. **Target Redis eviction policy** (`noeviction` check documented by the correlator team) — a
   `volatile-*`/`allkeys-*` target can evict trigger-once flags and make *any* black-box
   correlation test flaky; worth one line in the harness's target-diagnostics `doctor`.
5. **macOS developer lane for `package`/`checksums`** if MED-7 resolves toward GNU-tar
   normalization (gtar availability).

---

## 13. Final recommendation

**Begin Plan 1 after applying §10 items 1–4** (items 1–3 are confirmed command-level failures
against the repository's current state and git semantics; item 4 is a ten-line guard that closes
the only unowned deferral with a security consequence). The foundation itself is sound: the
repository boundary is real and enforced, the Make-only dual-CI contract is genuinely equivalent as
specified, every supply-chain pin in the plan resolved **exactly** to its claimed SHA/digest over
the network (a rarity worth noting — nothing in Tasks 5–6 is plausible-looking YAML), and the UI
foundation improves on its OTTO inspiration in precisely the places OTTO is weak.

**Do not author Plan 3 (the first OSCAR-contact plan) until the design absorbs HIGH-1 and
HIGH-2.** Both are facts about current OSCAR, verified in source, that determine what the harness's
oracle may legitimately claim: on a stock deployment the correlator writes audit rows but emits no
synthetic parents and dispatches nothing (`consume.py:542-572`), and the only key into the audit
evidence is a server-stamped fingerprint the injection API does not return
(`audit.py:63`, `alerts.py:848`). A harness built on the design as written would either fail
healthy Phase-A systems or — in its negative cases — pass vacuously against them, which is the
single failure mode this product exists to prevent. The design's own compatibility-mode principle
("must classify weak or ambiguous proof as INCONCLUSIVE", line 523) already contains the right
answer; it needs to be pointed at these two named facts.

The strongest attacks that **failed** (verified safe): the naming/label grammar against real
fingerprint semantics (labels-only hash, annotations free, correlator labels excluded — §10.6 is
exactly right); the emit-spec label carry-through onto synthetic parents; the trigger-once /
deterministic-parent-fingerprint dedup under redelivery; rule-delete idempotency for cleanup
(404 = conclusively absent); the negative-proof audit anchor (non-triggering alerts still produce
audit rows, including a dedicated resolve-cancel reason for persistence); the red-team
scanner-ordering attack (scan precedes the clean-worktree check as planned); and every version,
action SHA, and image digest pin in both CI definitions.
