# ADVERSARIAL REVIEW — three proposed correlation fixes (2026-09-01)

Reviewer stance: assume broken until proven. Every attack below was executed
(code trace, isolated unit test, or read-only live probe against
https://192.168.29.195/ext/mw). No rule was created, no alert injected, no fix
code modified, nothing rebuilt/restarted/committed.

## VERDICTS

| Fix | Verdict |
|-----|---------|
| 1 — `Nats-Msg-Id = f"{fingerprint}:{status}"` | **DEPLOY-WITH-CHANGE** — append `starts_at` to the key (`f"{fp}:{status}:{starts_at or ''}"`). The flap hole is CONFIRMED by an executable attack test, and under Phase B it black-holes a re-fire from ALL delivery, not just the correlator. |
| 2 — pre-insert loader gate in `rules.py` | **DEPLOY-WITH-CHANGE** — (i) de-duplicate the mirrored predicate into `rule_store` (shared function) or accept documented drift risk; (ii) the "deliberate" import carve-out is reachable from the UI (`CorrelationImportDialog.js`) and silently recreates D2 for normal users — at minimum surface loader rejections in the import result. Neither change blocks deploying the current diff; both are required follow-ups before Phase B. |
| 3 — `correlationFingerprintOf()` in adminui | **DEPLOY** — every attack failed to break it, including live probes of the oldest history rows and a real audit row with `parent_fingerprint` set. |

---

## FIX 1 — dedup key `{fingerprint}:{status}`

### CONFIRMED — 1a THE FLAP HOLE (severity: HIGH; HIGH-CRITICAL once Phase B is on)

**Failure scenario:** fire(t0) → resolve(t+10s) → RE-FIRE(t+20s), same
fingerprint. Keys: `fp:firing`, `fp:resolved`, `fp:firing`. The re-fire's key
equals the first firing's key and lands inside the OSCAR_ALERTS dedup window
(no explicit `duplicate_window` → JetStream default 2 min). JetStream ACKs the
publish with `duplicate=true` and DISCARDS it. Proven by executable test
`test_attack_flap_hole.py::test_flap_refire_collides_with_original_firing`
(scratchpad; PASSES against the fixed `nats_publisher.py`, i.e. the collision
exists).

**What is lost, precisely:**

- **Correlator (Shadow and Phase B):** the NEW firing never arrives.
  - *persistence* (`patterns/persistence.py:253`): the resolve (now correctly
    delivered by this fix) CANCELLED the timer; the lost re-fire never re-arms
    it → a genuinely persistent re-fired condition emits NO synthetic parent →
    **MISSED PAGE**. Note the irony: the fix converts D1 from a false-page
    defect into a missed-page defect for flapping alerts.
  - *parent_child* (`parent_child.py:117`): the parent's resolve deleted the
    suppression key; the lost parent re-fire never re-registers it → children
    of an ACTIVE incident are released → **noise pages** (suppression fails to
    re-establish).
- **Phase B TOTAL LOSS (worse than the correlator missing it):**
  `oscar-alertmanager/src/tm/tasks.py:570-586` skips the legacy HTTP fan-out
  whenever the correlator is authoritative AND `nats_publish_failed` is False.
  A JetStream dedup drop is a *successful* publish (PubAck, no exception), so
  `nats_publish_failed` stays False → legacy POST skipped → the re-fire reaches
  **neither** the correlator **nor** AlertHistory/ESB/SMTP. In Phase B a flap
  re-fire within 2 min is delivered NOWHERE. (This hole exists pre-fix too —
  for resolves — but the fix's own rationale claims completeness it does not
  have, and the deploy decision is being made now.)

**Which is worse — clears lost (pre-fix) vs re-fires lost (post-fix)?**
Pre-fix loses the clear → false synthetic parents (noise) + parent_child
suppression key overstays TTL (missed pages, proven live in FINDINGS ARM A).
Post-fix loses the re-fire → persistence missed pages + Phase B total loss of
the re-fire. The fix is still a strict improvement for the resolve direction
and does not make the firing direction worse than it already was (a re-fire
was already deduped by the bare-fingerprint key). So: deployable, but the
claim "firing and resolve now pass while redelivery still dedupes" is only
half the story — the re-fire is a genuinely NEW logical message that still
dedupes wrongly.

**Minimal correction (evaluated against D-11's redelivery purpose):**
`Nats-Msg-Id = f"{fingerprint}:{status}:{alert.get('starts_at') or ''}"`.
- Redelivery (celery retry, AM webhook retry) re-sends the SAME payload dict →
  same `starts_at` → still dedupes. D-11 preserved.
- AM `group_interval` re-notification of a continuing firing: AM keeps
  `startsAt` stable for the episode → same key → still deduped (attack 1d
  requirement holds).
- Re-fire after resolve: AM mints a NEW `startsAt` for the new episode → new
  key → delivered. Hole closed whenever `startsAt` is populated.
- When `starts_at` is None (trap sources per D4 — `tasks.py:723` publishes
  `starts_at: None`), the key degrades to exactly the proposed fix: no worse,
  and closing THAT residue is D4's `startsAt` end-to-end work, not this fix.
- Rejected alternatives: an occurrence counter (no such monotonic field exists
  in the published dict; inventing one adds state), event timestamp/`endsAt`
  (resolve redelivery could carry a re-serialized differing value; `starts_at`
  is the stable episode identity).
- Also recommended (observability, not blocking): log `PubAck.duplicate` in
  `_publish()` so a dedup drop is visible instead of silent.
Proven by `test_startsat_suffixed_key_would_close_the_hole` in the same attack
file (re-fire passes, redelivery dedupes).

### Attacks that FAILED to break the fix
- **1b resolve→resolve:** both key `fp:resolved` → deduped inside the window.
  Correct — duplicate clears are idempotent redeliveries; the correlator needs
  the clear once. Beyond 120 s they pass, same as pre-fix. No break.
- **1c status domain:** `status` is a plain `str` in the schema
  (`schemas/alerts.py:268`), sourced verbatim from the AM webhook
  (firing/resolved in practice); the fix lowercases and strips, so `"Firing"`
  → `firing`. `None`/empty → `"unknown"`: two different lifecycle states can
  only both map to `"unknown"` if both LACK a status, in which case the
  correlator's own `is_resolved()` (reads the same field) cannot classify them
  either — collision is moot. Distinct real statuses (e.g. a hypothetical
  `"cleared"`) get distinct keys. No break.
- **1d re-notification double-count:** repeated firing notifications keep the
  key `fp:firing` → still deduped inside the window, so flood/threshold counts
  are not inflated by this fix. Past 120 s a re-notification re-counts — but
  that is byte-identical to pre-fix behaviour (bare fingerprint had the same
  window). No regression.
- **Header consumers:** grepped correlator/eventmesh — nothing parses
  `Nats-Msg-Id` content; only JetStream uses it. No break.
- **Fingerprint-absent path:** header omitted exactly as before (pinned by
  `test_no_header_when_fingerprint_absent`). No break.
- Ran the author's suite: `PYTHONPATH=src pytest tests/test_nats_publisher.py`
  → 17 passed.

---

## FIX 2 — pre-insert loader gate in `routers/rules.py`

### CONFIRMED — 2a mirror drift is structural (severity: MEDIUM, latent)

`_emit_spec_alertname_error()` (`rules.py:149-170`) is today a faithful
logical mirror of `rule_store.py:189-206` — same `parent_child` carve-out,
same `isinstance(dict)` coercion, same
`str`+`.strip()` test, same reason string. **Verified by side-by-side diff of
the two predicates: no divergence today.** But it is a COPY, not a shared
function, so the moment the loader gains a new rejection reason:
- `create_rule` still catches it via the kept post-load backstop (422 +
  compensating DELETE) — degraded but safe;
- **`validate_rule` lies again** (valid:true, create 422) — the exact D2
  symptom, resurrected for the new reason;
- **`batch_create_rules` has NO post-load backstop** (`rules.py:379-392`):
  all rows insert in one transaction, then a single `rule_store.load()` whose
  report is never inspected → a drifted rejection leaks the whole batch's rows
  with a 200 response;
- **`update_rule` has NO post-load backstop** either → a drifted rejection
  turns a loadable rule unloadable with a 200.

**Minimal correction:** move the predicate INTO `rule_store` as a module-level
function (e.g. `def emit_spec_alertname_error(pattern, emit_spec)`) and call
it from both the loader loop and the router — one source of truth, drift
impossible for this gate. (Match-criteria drift is already impossible: both
sides call `PATTERN_TO_SCHEMA[pattern].model_validate` — checked
`rules.py:112` vs `rule_store.py` dispatch.) Optionally: have batch/update
inspect the load report the way create does.

### CONFIRMED — 2e import path recreates D2 from the UI (severity: MEDIUM)

The fix's carve-out says import is deliberate — but
`oscar-adminui/src/views/pages/rules/CorrelationImportDialog.js:75` POSTs to
`/api/correlation-rules/import`, which proxies to the ungated
`POST /rules/import`. So a normal UI user importing a historical export with
`emit_spec: null` on a flood rule gets: HTTP success, row persisted, loader
silently rejects it (`_import_one_yaml_doc` records only parse/schema errors;
the post-import `rule_store.load()` report is never folded into the import
result). Rule appears in the list, never evaluates — the "201-then-broken
rule" the fix claims to have eliminated, minus even the 422.
**Minimal correction:** after import's `rule_store.load()`, map
`report.rules[status=rejected]` back onto the imported ids and surface them in
`RuleImportResult` (per-file warning), OR apply the gate to import with an
explicit `emit_spec`-missing error per document. Follow-up decision, as the
author flagged — but "cannot be reached from the UI" is FALSE, so it cannot be
waved off.

### CONFIRMED (minor) — 2d compensating-DELETE failure

If the DELETE at `rules.py:252-257` itself raises (DB down mid-request), the
HTTPException(422) is never reached: the caller gets a 500 and the orphan row
remains — the original D2 leak state, now minus the misleading message. The
index is still consistent (the first `load()` already excluded the rejected
rule). Acceptable residual: the path is already "should be unreachable"
belt-and-suspenders, and a DB that fails the DELETE would likely have failed
the INSERT. No change demanded; noted for the record.

### Attacks that FAILED to break the fix
- **2b batch partial failure:** both gates (`_validate_match_criteria`,
  `_validate_emit_spec`) run for EVERY item before any insert
  (`rules.py:362-377`), and inserts share one `session.begin()` transaction →
  all-or-nothing for every currently-known rejection reason; no row leak, no
  index churn (single reload after commit). Contract is documented in the
  docstring ("whole batch is rejected atomically"). No break (drift caveat
  above notwithstanding).
- **2c update leak:** traced the merge — `values.get("pattern",
  existing.pattern)` + `values["emit_spec"] if present else existing` covers
  all four combinations, including `PUT {emit_spec: null}` on a flood rule
  (caught) and `pattern: flood` on an emit_spec-less parent_child rule
  (caught). Gate runs before the UPDATE statement. No leak found for the known
  reason.
- **2e concurrent same-name creates:** `uq_correlation_rules_name`
  (`db.py:105`) makes the second insert fail at the DB — a 500 rather than a
  friendly 409, but no duplicate row and PRE-EXISTING behaviour untouched by
  this diff. No break attributable to the fix.
- **validate stays DB-free:** the emit_spec branch in `validate_rule` touches
  no session (existing test asserting `get_async_session_maker` is never
  called still passes). No break.
- Ran the author's suite: `pytest tests/test_rules_router.py` → 24 passed.

---

## FIX 3 — `correlationFingerprintOf()` (adminui)

### Attacks that FAILED to break the fix (all verified, several live)

- **3a pre-label-era history rows:** pulled the OLDEST rows live (page 327 of
  327, read-only `GET /api/v1/alerts/history`): rows back to **2026-07-02**
  ALL carry an `am_fingerprint` label (e.g. `1e9096b217e9` →
  `['236348b9a51e38b2']`). No row in the 3265-record retention lacks it — the
  stamping (`alerts.py:741`) predates retention. For a hypothetical row
  without it, the fix returns null → fetch disabled → the new explicit
  "cannot be looked up" empty state renders (no crash, no misleading query).
  No break.
- **3b parent_fingerprint domain (live-verified, not assumed):** real audit
  row id 37867 (`fingerprint=106f21bede4aee8f`, rule 85) carries
  `parent_fingerprint: "5a450322c3bc"` — 12 hex, i.e. the correlator's
  CANONICAL fingerprint from `generate_fingerprint`
  (`synthetic_emitter.py:141`), NOT a 16-hex AM value. Attack question: does
  the synthetic PARENT's history record carry that same value as its
  `am_fingerprint` label (the key the fixed children query sends)? Code chain
  says yes: the emitter POSTs the parent with `fingerPrint: fp`
  (`synthetic_emitter.py:342`) straight to alertmanager-middleware
  `POST /alerts`, whose ingest stamps `labels["am_fingerprint"] =
  alert.fingerprint` (`alerts.py:739-741`) — no Prometheus-AM hop, no
  refingerprinting. So parent's `am_fingerprint` label == emitted fp ==
  `audit.parent_fingerprint`; the children lookup joins correctly. The
  read-only children endpoint with that live value returns the 2 expected
  children. **Caveat I could not close live:** zero synthetic parents exist in
  the entire alert history (swept all 327 pages for
  `oscar_synthetic_source`) — Shadow has never let one land — so the parent
  view has never been exercised against a real history record. The chain is
  code-proven, not live-proven; the author's verify-live step 4 MUST be run at
  deploy time.
- **3c label shape:** traced the real data path: `CorrelationTab` is rendered
  only by `AlertDetailPanel.js:803`, whose consumers are
  `AlertHistoryList.js` / `AlertHistorySearch.js` (rows straight from the
  history API, whose live-confirmed shape is `labels:[{Label,Value}]` — see
  TEST_CAMPAIGN_CONTEXT §shapes, re-confirmed by my page-1 probe) and
  `RuleQueueDrawer.js`. `AlertDetailPanel` itself reads labels with array
  `.find` (lines 214/267/309), so the array shape is the component's working
  contract. If any caller ever passes object-shaped labels,
  `correlationFingerprintOf` returns null → honest empty state, no D3-style
  wrong-domain query and no crash. No break.
- **3d injection:** the fingerprint flows into the URL via
  `encodeURIComponent` (`CorrelationTab.js:111`) for the path form and via
  axios `params` (auto-encoded) for the children form; rendering is React
  (auto-escaped). Label values are server-stamped fingerprints, not
  user-controlled markup. No break.
- **Duplicate labels:** two `am_fingerprint` labels → `.find` takes the
  first; the stamp site writes exactly one. No break.
- Ran the author's suite: `npx vitest run CorrelationTab.test.js` → 62 passed.

**One wording nit (non-blocking):** the fix's comments say the audit key is
"the 16-hex am_fingerprint". True for real alerts (AM webhook fingerprints);
for synthetic parents the same label carries a 12-hex canonical fingerprint.
The CODE is agnostic (it copies whatever the label holds — correct); only the
comment over-specifies. Do not "fix" the code to enforce 16-hex.

---

## Attack artifacts
- `/tmp/claude-1001/.../scratchpad/test_attack_flap_hole.py` — 2 tests, both
  pass against the fixed publisher: (1) proves the re-fire/first-firing key
  collision; (2) proves the `starts_at`-suffixed key closes it while keeping
  redelivery dedup.
- Live read-only probes: history page 1 + page 327 (oldest rows), full 327-page
  sweep for `oscar_synthetic_source` (0 hits), audit
  `?fingerprint=106f21bede4aee8f`, audit
  `/children?parent_fingerprint=5a450322c3bc`.
- Author suites re-run in isolation: 17 + 24 + 62 passed.
