# FIXES PROPOSED — OSCAR correlation defects 1 / 2 / 3

> ## Post-adversarial-review (2026-09-01)
>
> The two corrections demanded by `REVIEW-FIXES-ADVERSARIAL.md` are now APPLIED
> on disk (still undeployed):
>
> 1. **D1 — flap hole closed.** `Nats-Msg-Id` is now
>    `f"{fingerprint}:{status}:{starts_at or ''}"`. The status-only key made a
>    re-fire after a resolve collide with the first firing's key inside the
>    ~2 min dedup window — JetStream silently discarded a genuinely NEW episode
>    (persistence missed page; parent_child suppression never re-registered;
>    Phase B total loss because a deduped publish counts as success and skips
>    legacy fan-out). `starts_at` is stable per episode (group_interval
>    re-notifications keep it) and new per re-fire, so redelivery still dedupes
>    while a new episode always delivers. `starts_at: None` degrades to exactly
>    the status-only key. Field verified at `tm/tasks.py:724`
>    (`create_notification_payloads` builds the published inner alert dict).
>    Tests +3 (re-fire distinct key / re-notification same key / None degrades);
>    the review's attack test in scratchpad was updated to assert the hole is
>    now CLOSED and passes against the fixed publisher.
> 2. **D2 — mirror eliminated + import gated.** The emit_spec.alertname
>    predicate is HOISTED into `rule_store.emit_spec_alertname_error()` (module
>    level, next to the loader gate that consumes it); the loader AND every
>    router gate (create, batch, update, validate, import) call that ONE
>    function — the router's `_emit_spec_alertname_error` mirror no longer
>    exists (a drift-probe test pins this). The `/rules/import` carve-out is
>    REMOVED: the review proved `CorrelationImportDialog.js:75` posts to the
>    ungated import endpoint, silently recreating D2 from the UI. Import now
>    runs the same shared gate per document, reporting rejections in
>    `RuleImportResult.errors[{filename, message}]` with the loader's reason
>    string — the existing per-file partial-error contract is honoured, import
>    stays partial (never all-or-nothing). Two stale import fixtures that
>    encoded the ungated behaviour were updated; tests +3.
> 3. **D3 — unchanged.** Review verdict: **DEPLOY** (every attack failed). See
>    the deploy-time caveat added to the D3 section.
>
> Not applied (review marked non-blocking / out of scope): logging
> `PubAck.duplicate` in `_publish()` (observability suggestion), post-load
> report inspection in batch/update (the shared pre-persist gate already covers
> every known rejection reason; drift is now structurally impossible for this
> gate), the 2d compensating-DELETE-failure residual (review: "no change
> demanded"), and the D3 16-hex comment wording nit (review: do not change).

Authored 2026-08-30. **Source edits ONLY.** Nothing was rebuilt, restarted,
committed, pushed or branched; no env/config file was touched; no `./oscar`
command was run. The live stack was not disturbed (a concurrent correlation-test
lane is running against it).

Unit tests were run in-process only (`pytest` on single files with `PYTHONPATH=src`,
`vitest run` on one file) — none of them opens a socket to the live stack.

Files changed:

| # | File | Kind |
|---|------|------|
| 1 | `oscar/oscar-alertmanager/src/app/core/nats_publisher.py` | fix (corrected 09-01: `:starts_at` suffix) |
| 1 | `oscar/oscar-alertmanager/tests/test_nats_publisher.py` | tests (+4, then +3 post-review) |
| 2 | `oscar/oscar-correlator/src/app/core/rule_store.py` | fix (post-review: shared `emit_spec_alertname_error()`) |
| 2 | `oscar/oscar-correlator/src/app/routers/rules.py` | fix (post-review: mirror deleted, import gated) |
| 2 | `oscar/oscar-correlator/tests/test_rules_router.py` | tests (+4, 6 fixtures updated; +1 post-review drift probe) |
| 2 | `oscar/oscar-correlator/tests/test_rules_router_import.py` | tests (post-review: +2, 2 fixtures updated) |
| 3 | `oscar-adminui/src/views/pages/alerts/correlationTabLib.js` | fix |
| 3 | `oscar-adminui/src/views/pages/alerts/CorrelationTab.js` | fix |
| 3 | `oscar-adminui/src/views/pages/alerts/CorrelationTab.test.js` | tests (+3) |

> `oscar-adminui/src/lib/exportLimits.js(.test.js)` also show as modified in
> `git diff`. Those were **already dirty before this session** and are not mine.

---

## DEFECT 1 (HIGH) — a resolve within ~2 min of its firing never reaches the correlator

**File:line:** `oscar/oscar-alertmanager/src/app/core/nats_publisher.py:431-432`
(inside `publish_stored_sync`)

### Diff

```diff
diff --git a/oscar-alertmanager/src/app/core/nats_publisher.py b/oscar-alertmanager/src/app/core/nats_publisher.py
index 3ba99b031..54dd35b56 100644
--- a/oscar-alertmanager/src/app/core/nats_publisher.py
+++ b/oscar-alertmanager/src/app/core/nats_publisher.py
@@ -428,8 +428,25 @@ class _NatsPublisher:
         # fingerprint so a redelivered publish is deduped inside the stream's
         # dedup window. Omit the header when no fingerprint is present rather
         # than sending an empty dedup key.
+        #
+        # DEFECT-1 (2026-08-30): the key must ALSO carry the lifecycle state.
+        # A resolve carries the SAME fingerprint as its firing (that is what
+        # makes it the same alert), so a fingerprint-only key made JetStream
+        # treat the resolve as a duplicate of the firing and silently discard
+        # it inside the stream's dedup window (OSCAR_ALERTS has no explicit
+        # duplicate_window -> JetStream default 2 minutes). The correlator
+        # therefore never observed clears that happened within ~2 minutes, and
+        # persistence emitted synthetic parents for conditions that had already
+        # cleared. Adding the status keeps genuine redelivery dedup PER
+        # LIFECYCLE STATE while letting firing and resolve through as distinct
+        # messages. `status` is the top-level string set in
+        # tm/tasks.py create_notification_payloads() ("firing"/"resolved").
+        #
+        # DEFECT-1 adversarial-review correction (2026-09-01) — THE FLAP HOLE:
+        # fingerprint:status alone still collides on a flap. fire(t0) ->
+        # resolve(t+10s) -> RE-FIRE(t+20s) yields keys fp:firing, fp:resolved,
+        # fp:firing — the re-fire's key equals the FIRST firing's key inside
+        # the ~2 min dedup window, so JetStream ACKs it duplicate=true and
+        # silently DISCARDS a genuinely new firing episode (persistence never
+        # re-arms -> missed page; parent_child suppression never re-registers;
+        # and under Phase B a "successful" deduped publish skips the legacy
+        # fan-out too — total loss). Appending `starts_at` closes it:
+        # Alertmanager keeps startsAt STABLE for the life of one firing episode
+        # (group_interval re-notifications carry the SAME startsAt) and mints a
+        # NEW startsAt for a re-fire after resolve — so redelivery/re-notification
+        # still dedupes while a new episode gets a new key. `starts_at` is the
+        # top-level ISO string built in tm/tasks.py create_notification_payloads()
+        # (tasks.py:724: `alert.starts_at.isoformat() if alert.starts_at else
+        # None`) on the same inner alert dict published at tasks.py:~530
+        # (`notification_payload["alerts"][0]`). When starts_at is None/missing
+        # (trap sources per D4 publish starts_at: None) the suffix is empty and
+        # the key degrades to exactly fingerprint:status — no worse than before.
         fingerprint = alert.get("fingerprint")
-        headers = {"Nats-Msg-Id": str(fingerprint)} if fingerprint else None
+        if fingerprint:
+            status = str(alert.get("status") or "unknown").strip().lower() or "unknown"
+            starts_at = alert.get("starts_at") or ""
+            headers = {"Nats-Msg-Id": f"{fingerprint}:{status}:{starts_at}"}
+        else:
+            headers = None
         # mypy: self._loop is set inside _ensure_started's lock above.
         loop = self._loop
         # 96.2 review IN-04: an explicit guard instead of a bare `assert` — the
```

### Why it is correct

* The alert dict published here is the one built in
  `oscar-alertmanager/src/tm/tasks.py:721` (`create_notification_payloads`), which
  sets a **top-level `status`** straight off `alert.status` — the Alertmanager
  lifecycle string (`firing` / `resolved`). Confirmed at the call site
  (`tasks.py:~530` publishes `notification_payload["alerts"][0]`), not guessed.
  The correlator reads the same field: `oscar-correlator/src/app/patterns/base.py:136`
  `str(alert.get("status", "")).strip().lower()`.
* A firing and its resolve share a fingerprint by design, so a fingerprint-only
  `Nats-Msg-Id` made JetStream classify the resolve as a duplicate publish and
  discard it inside the stream's dedup window (`OSCAR_ALERTS` is created with no
  explicit `duplicate_window`, so the JetStream default of 2 minutes applies).
  Adding the lifecycle state to the key makes firing and resolve **distinct
  messages** while keeping genuine redelivery idempotency **per lifecycle state**
  (a redelivered firing still dedups against the firing).
* **Post-review correction — `starts_at` suffix (2026-09-01):** status alone
  left THE FLAP HOLE — a re-fire after a resolve reuses `fp:firing` and is
  silently dropped inside the window (review Fix 1 / 1a, proven by the attack
  test). `starts_at` is the episode identity: same for group_interval
  re-notifications and celery/webhook redeliveries (same payload dict), NEW for
  a re-fire after resolve. So the corrected key delivers a new episode while
  still deduping redelivery and re-notification. Rejected alternatives per the
  review: an occurrence counter (no monotonic field exists in the published
  dict), `ends_at`/event timestamp (redelivery could carry a re-serialized
  differing value).
* Missing/empty/non-string `status` degrades to a stable `"unknown"` token —
  no crash, and still a deterministic dedup key. `starts_at: None`/missing
  (trap sources, D4 — `tasks.py:723` publishes `starts_at: None`) degrades the
  suffix to empty, i.e. exactly the `fingerprint:status` behaviour; closing
  that residue is D4's startsAt end-to-end work, not this fix.
* The pre-existing behaviour of **omitting the header entirely when there is no
  fingerprint** is preserved exactly.

### Blast radius

* Only the NATS publish path to `oscar.alerts.stored`, i.e. the correlator's copy
  of each alert. The HTTP notifier fan-out (AlertHistory / ESB ticketing / SMTP /
  LOGGER) is a separate branch and is untouched.
* JetStream will now accept up to 2 messages per fingerprint per dedup window
  instead of 1 (one per lifecycle state). Stream volume rises by at most the
  number of resolves — bounded, and it is the whole point.
* Any consumer relying on "one message per fingerprint per 2 min" would see both.
  The only consumer of this subject is the correlator (`correlator-main` durable),
  and it is designed to see resolves — `patterns/base.py:123 is_resolved()` and
  `patterns/persistence.py:253` exist precisely for that.

### To deploy

Rebuild + restart **oscar-alertmanager** (the FastAPI service AND the celery
worker `oscar-alertmanager-alertmanager_worker-1` — the worker is the process
that actually publishes). No correlator, eventmesh or NATS change; no stream
reconfiguration.

### How to verify live

1. Fire a trap/alert, then send its resolve **~8 seconds later** (same fingerprint).
2. `GET /api/v1/correlation_rules/audit?fingerprint=<16-hex am_fingerprint>` must
   now show the resolve reaching the correlator.
3. With a persistence rule (`unresolved_for_seconds=30`) armed on that alertname,
   the outcome must be `persistence_resolved_cancelled` and **no**
   `persistence_threshold_reached` / `parent_emitted` — this is exactly probe A
   from FINDINGS-DEFECTS.md, which previously produced a false synthetic parent.
4. The corrtest builtin `resolution-cancels-persistence` (persistence N01) should
   flip from FAIL to PASS.
5. **Flap re-fire (post-review):** fire, resolve ~10s later, then RE-FIRE the
   same alert ~10s after that (Alertmanager mints a new `startsAt`). The audit
   for that fingerprint must show the re-fire reaching the correlator (three
   deliveries, not two) — under the status-only key the re-fire was silently
   deduped inside the 2 min window.

---

## DEFECT 2 (MEDIUM) — `/validate` accepts what create rejects, and the 422 leaks a row

Both halves live in **one service**: `oscar-correlator`. The rejection
predicate now lives in **one place**: `src/app/core/rule_store.py`
(`emit_spec_alertname_error()`, module level, directly consumed by the loader
gate) — post-review correction, see below. Nothing in the middleware or
adminui needed to change — the middleware only proxies
`/api/v1/correlation_rules/*` to these routes, and the UI import dialog is
fixed server-side (its POST target now runs the gate).

**Files:line (post-review shape, 2026-09-01):**
* `oscar/oscar-correlator/src/app/core/rule_store.py` — NEW module-level
  `emit_spec_alertname_error(pattern, emit_spec)` (the single source of truth);
  the loader's inline gate refactored to call it.
* `oscar/oscar-correlator/src/app/routers/rules.py` — the local
  `_emit_spec_alertname_error()` MIRROR is **deleted**; `_validate_emit_spec()`
  calls `rule_store.emit_spec_alertname_error()`; called from `create_rule`,
  `batch_create_rules`, `update_rule`, folded into `validate_rule`, and
  (post-review) applied per-document in `_import_one_yaml_doc` (import path);
  compensating delete on `create_rule`'s post-load check unchanged.

### Diff

```diff
diff --git a/oscar-correlator/src/app/routers/rules.py b/oscar-correlator/src/app/routers/rules.py
index d603f1caf..56eefc255 100644
--- a/oscar-correlator/src/app/routers/rules.py
+++ b/oscar-correlator/src/app/routers/rules.py
@@ -131,6 +131,42 @@ def _validate_match_criteria(pattern: str, match_criteria: dict) -> None:
         )
 
 
+def _validate_emit_spec(pattern: str, emit_spec) -> None:
+    """Raise HTTPException(422) when the loader would reject this rule's
+    emit_spec — called BEFORE any insert/update so no row is ever persisted for
+    a rule the loader will not accept (DEFECT-2).
+
+    DEFECT-2 adversarial-review correction (2026-09-01): the predicate itself
+    now lives in ``rule_store.emit_spec_alertname_error`` — the SAME function
+    the loader gate calls — instead of a hand-copied mirror here, so the two
+    can never drift (drift would resurrect validate-says-valid / loader-rejects
+    for any future rejection reason)."""
+    reason = rule_store.emit_spec_alertname_error(pattern, emit_spec)
+    if reason is not None:
+        raise HTTPException(
+            status_code=422,
+            detail=[{"field": "emit_spec.alertname", "message": reason}],
+        )
+
+
 @router.get("/rules", response_model=RuleListResponse, summary="List Correlation Rules")
 async def list_rules(
     page: int = Query(1, ge=1),
@@ -193,6 +229,9 @@ async def create_rule(payload: RuleCreateRequest) -> RuleResponse:
         raise HTTPException(status_code=400, detail=exc.message)
 
     _validate_match_criteria(payload.pattern, payload.match_criteria)
+    # DEFECT-2: run the loader's emit_spec gate BEFORE the insert so a rule the
+    # loader would reject never reaches the table.
+    _validate_emit_spec(payload.pattern, payload.emit_spec)
 
     # Estimate fields are advisory/validation-only — strip before the ORM insert.
     values = _strip_advisory(payload.model_dump(exclude_unset=True))
@@ -206,9 +245,19 @@ async def create_rule(payload: RuleCreateRequest) -> RuleResponse:
     report = await rule_store.load()
     for entry in report.rules:
         if entry.get("id") == new_id and entry.get("status") == "rejected":
+            # DEFECT-2: a 422 must not leave a row behind. The pre-persist gates
+            # above should make this unreachable, but if the loader still rejects
+            # the new rule, compensate — delete it and reload the index — so the
+            # create is all-or-nothing from the caller's point of view.
+            async with smaker() as session:
+                await session.execute(
+                    sa_delete(CorrelationRules).where(CorrelationRules.id == new_id)
+                )
+                await session.commit()
+            await rule_store.load()
             raise HTTPException(
                 status_code=422,
-                detail=f"rule persisted but rejected by loader: {entry.get('reason')}",
+                detail=f"rule rejected by loader (not persisted): {entry.get('reason')}",
             )
 
     return await _get_rule_or_404(new_id)
@@ -281,6 +330,18 @@ async def validate_rule(payload: dict) -> ValidateResponse:
             ],
         )
 
+    # DEFECT-2: validate must be a FAITHFUL dry run of create — run the loader's
+    # emit_spec gate here too, otherwise validate returns valid:true for a rule
+    # create then rejects with 422.
+    emit_reason = _emit_spec_alertname_error(rule.pattern, rule.emit_spec)
+    if emit_reason is not None:
+        return ValidateResponse(
+            valid=False,
+            errors=[
+                ValidationErrorDetail(field="emit_spec.alertname", message=emit_reason)
+            ],
+        )
+
     return ValidateResponse(valid=True, errors=[])
 
 
@@ -311,6 +372,9 @@ async def batch_create_rules(payload: List[RuleCreateRequest]) -> List[RuleRespo
                 detail=f"Rule #{idx} '{item.name}': {exc.message}",
             )
         _validate_match_criteria(item.pattern, item.match_criteria)
+        # DEFECT-2: same pre-persist emit_spec gate as the single create, so the
+        # batch is rejected before any insert rather than leaking rows.
+        _validate_emit_spec(item.pattern, item.emit_spec)
 
     new_ids: List[int] = []
     smaker = get_async_session_maker()
@@ -403,6 +467,11 @@ async def _import_one_yaml_doc(fname: str, raw_entry: bytes, result: RuleImportR
             raise ValueError("YAML document is not a mapping")
         rule = RuleCreateRequest(**data)
         _validate_match_criteria(rule.pattern, rule.match_criteria)
+        # DEFECT-2 adversarial-review correction (2026-09-01): the emit_spec
+        # gate IS applied here now — the adminui CorrelationImportDialog posts
+        # to this endpoint, so leaving it ungated silently recreated D2 from
+        # the UI (HTTP success, row persisted, loader rejects it, rule listed
+        # but never evaluates). The gate honours import's existing PER-FILE
+        # partial-error contract: a document the loader would reject is
+        # recorded in result.errors[{filename, message}] with the loader's own
+        # reason string and skipped — it never blocks the other documents and
+        # import stays partial, not all-or-nothing.
+        emit_reason = rule_store.emit_spec_alertname_error(rule.pattern, rule.emit_spec)
+        if emit_reason is not None:
+            raise ValueError(f"emit_spec.alertname: {emit_reason}")
         # Phase 99 (GRD-01): a hand-authored import may carry the advisory
         # estimate fields (machine exports never do); strip them so the ORM
         # insert/update never sees a non-column.
@@ -543,6 +612,15 @@ async def update_rule(rule_id: int, payload: RuleUpdateRequest) -> RuleResponse:
             # match_criteria against the new pattern.
             _validate_match_criteria(values["pattern"], existing.match_criteria or {})
 
+        # DEFECT-2: an edit must not be able to turn a loadable rule into one the
+        # loader rejects. Gate the EFFECTIVE (merged) pattern + emit_spec before
+        # the UPDATE, so the row on disk always matches what the loader accepts.
+        if "emit_spec" in values or "pattern" in values:
+            _validate_emit_spec(
+                values.get("pattern", existing.pattern),
+                values["emit_spec"] if "emit_spec" in values else existing.emit_spec,
+            )
+
         # Estimate fields are advisory/validation-only — strip before the ORM
         # update. If the patch carried ONLY advisory fields there is nothing
         # persistable left.
```

### Why it is correct

* **(post-review)** the predicate is `rule_store.emit_spec_alertname_error()` —
  the LITERAL SAME FUNCTION the loader calls, hoisted next to the loader gate,
  including the `parent_child` carve-out (that pattern never emits a synthetic
  parent, so it legitimately has no `emit_spec`). Not a mirror any more: the
  review confirmed the old copy was faithful today but structurally drift-prone
  (a new loader rejection reason would resurrect D2 on validate, and leak
  through batch/update, which have no post-load backstop). Drift for this gate
  is now impossible; a drift-probe test patches the shared function and asserts
  the router's verdict follows it.
* **(a) validate is now a faithful dry run**: it returns
  `{valid:false, errors:[{field:"emit_spec.alertname", …}]}` for exactly the
  bodies create rejects. It is still 100% DB-free (the existing test asserting
  `get_async_session_maker` is never called during validate still passes).
* **(b) no row is left behind**: the gate runs *before* the INSERT, alongside the
  existing `_validate_match_criteria` gate, so the 422 now happens with no DB
  session opened at all. The post-load "belt-and-suspenders" branch is kept for
  any *other* loader rejection reason, but it now **DELETEs the row and reloads
  the index** before raising, so the create is all-or-nothing either way. Its
  message changed from the misleading `"rule persisted but rejected by loader"`
  to `"rule rejected by loader (not persisted)"`.
* `update_rule` is gated on the **effective** (existing row merged with the patch)
  pattern + emit_spec, so an edit cannot turn a loadable rule into an unloadable
  one — same merge strategy the existing guardrail check already uses.

### ~~Deliberately NOT changed~~ — REVERSED by the adversarial review (2026-09-01)

* **`POST /rules/import` is now GATED.** The original carve-out claimed import
  was unreachable-in-practice; the review proved
  `oscar-adminui/src/views/pages/rules/CorrelationImportDialog.js:75` posts to
  `/api/correlation-rules/import` → the ungated `POST /rules/import`, so a
  normal UI user importing a historical export with `emit_spec: null` on a
  flood rule got HTTP success + a persisted row the loader silently rejected —
  D2 recreated, minus even the 422. The fix gates import server-side with the
  SAME shared function, while HONOURING the existing partial-error contract: a
  rejected document lands in `RuleImportResult.errors[{filename, message}]`
  (loader's reason string) and the other documents still import — never
  all-or-nothing, no route/status-code change, no adminui change needed (the
  dialog already renders per-file errors from that field). Two stale import
  test fixtures (flood docs with no `emit_spec`) encoded the ungated behaviour
  and were updated to carry `emit_spec: {alertname: flood_parent}`.
* The loader gate's BEHAVIOUR in `rule_store.py` — unchanged, and it stays the
  authority. Its inline predicate was refactored (not changed) into the shared
  module-level `emit_spec_alertname_error()` that the router now also calls.
* batch/update post-load report inspection — still not added: the shared
  pre-persist gate runs on every persist path and covers every known rejection
  reason, and predicate drift is now structurally impossible. (Review listed
  this as "optionally".)

### Blast radius

* Any API client that today creates a non-`parent_child` rule with no
  `emit_spec.alertname` now gets a **422 instead of a 201-then-broken rule**.
  That rule never loaded anyway (the loader always rejected it), so nothing that
  used to work stops working — but a caller that ignored the 422 and assumed the
  rule existed will now correctly fail earlier. The adminui rule wizard already
  collects `emit_spec.alertname`.
* Six existing unit-test payloads were stale (flood rules with no `emit_spec`)
  and were updated to carry `emit_spec: {alertname: "flood_parent"}` — they had
  encoded the buggy behaviour.

### To deploy

Rebuild + restart **oscar-correlator** only.

### How to verify live

1. `POST /api/v1/correlation_rules/validate` with a flood rule and no `emit_spec`
   → must now return `valid:false` with field `emit_spec.alertname`
   (was `{"valid":true,"errors":[]}`).
2. `POST /api/v1/correlation_rules` with the identical body → 422 whose detail
   names `emit_spec.alertname`.
3. Immediately `GET /api/v1/correlation_rules?perPage=100` and confirm **no new
   row** and that `total` is unchanged (previously this leaked a row — e.g. the
   orphaned id 63 that had to be deleted by hand).
4. Add `"emit_spec": {"alertname": "x"}` → 201, and `/validate` on the same body
   → `valid:true`. validate and create now agree in both directions.
5. **(post-review) Import gate:** via the UI (Correlation Rules → Import) or
   `POST /api/v1/correlation_rules/import`, upload a zip containing one valid
   flood rule and one flood rule with `emit_spec: null`. Response must be 200
   with the valid rule in `created[]`/`updated[]` and the other in
   `errors[{filename, message}]` naming
   `emit_spec_alertname_required_for_pattern_flood`; the rule list must NOT
   contain the rejected rule. A `parent_child` doc with no emit_spec must still
   import cleanly (carve-out intact).

---

## DEFECT 3 (HIGH) — Correlation tab can never show correlation (wrong fingerprint domain)

> **Adversarial review verdict (2026-09-01): DEPLOY** — every attack failed,
> including live probes of the oldest history rows and a real audit row with
> `parent_fingerprint` set. **Deploy-time caveat:** zero synthetic parents have
> ever landed in alert history (Shadow has never emitted one), so the parent
> view is code-proven, not live-proven — the verify-live step 4 below (children
> grid on a synthetic parent) MUST be run at deploy time. Fix unchanged.

**File:line:**
* `oscar-adminui/src/views/pages/alerts/correlationTabLib.js` — new
  `AM_FINGERPRINT_LABEL` + `correlationFingerprintOf()` exports
* `oscar-adminui/src/views/pages/alerts/CorrelationTab.js:90` (now :97) — the
  lookup key; and the empty-state copy at :544

### Diff

```diff
diff --git a/src/views/pages/alerts/correlationTabLib.js b/src/views/pages/alerts/correlationTabLib.js
index cbada047..9158bbea 100644
--- a/src/views/pages/alerts/correlationTabLib.js
+++ b/src/views/pages/alerts/correlationTabLib.js
@@ -28,6 +28,24 @@ export const isSyntheticParent = alert =>
     label => label && label.Label === SYNTHETIC_SOURCE_LABEL && label.Value === SYNTHETIC_SOURCE_VALUE
   )
 
+// DEFECT-3 (2026-08-30): CorrelationAudit.alert_fingerprint is the 16-hex
+// AlertManager fingerprint carried on the alert as the `am_fingerprint` LABEL —
+// NOT the 12-hex AlertHistory `alert.fingerprint`. Querying the audit API with
+// `alert.fingerprint` returns 0 rows for EVERY alert in every pipeline mode, so
+// the Correlation tab could never show correlation. Resolve the label instead.
+export const AM_FINGERPRINT_LABEL = 'am_fingerprint'
+
+// Returns the correlation-audit key for this alert, or null when the alert
+// carries no `am_fingerprint` label. Deliberately does NOT fall back to
+// `alert.fingerprint`: that value is in the wrong domain and can only ever
+// produce an empty, misleading result.
+export const correlationFingerprintOf = alert => {
+  const labels = Array.isArray(alert?.labels) ? alert.labels : []
+  const hit = labels.find(l => l && l.Label === AM_FINGERPRINT_LABEL && l.Value)
+
+  return hit ? String(hit.Value) : null
+}
+
 // Three mutually-exclusive view modes (D-02): 'parent' (label wins) > 'child'
 // (audit rows exist) > 'empty' (neither).
 export const VIEW_MODE = { PARENT: 'parent', CHILD: 'child', EMPTY: 'empty' }
```

```diff
diff --git a/src/views/pages/alerts/CorrelationTab.js b/src/views/pages/alerts/CorrelationTab.js
index f5456ef3..beec099e 100644
--- a/src/views/pages/alerts/CorrelationTab.js
+++ b/src/views/pages/alerts/CorrelationTab.js
@@ -74,6 +74,7 @@ import {
   childFingerprintsOf,
   resolveWindowInfo,
   resolveRuleRef,
+  correlationFingerprintOf,
   RULE_MANAGER_HREF,
   distinctHostCount,
   resolveDisplayFromMap
@@ -87,7 +88,13 @@ const CorrelationTab = ({ alert, tabValue = AUDIT_TAB_VALUE, onOpenAlert }) => {
   const ability = useContext(AbilityContext)
   const [timezone] = useAtom(timezoneAtom)
 
-  const fingerprint = alert?.fingerprint
+  // DEFECT-3: the correlation audit is keyed on the 16-hex `am_fingerprint`
+  // LABEL, not the 12-hex AlertHistory `alert.fingerprint`. Using the latter
+  // returned 0 rows for EVERY alert, so the tab always claimed "not
+  // correlated". The label is already on the alert record this component
+  // receives, so resolve it client-side — no extra round-trip and no
+  // server-side 12->16 hex translation table to keep in sync.
+  const fingerprint = correlationFingerprintOf(alert)
   const canView = canViewCorrelationTab(ability)
   const fetchEnabled = isAuditFetchEnabled(tabValue) && Boolean(fingerprint) && canView
 
@@ -537,12 +544,17 @@ const CorrelationTab = ({ alert, tabValue = AUDIT_TAB_VALUE, onOpenAlert }) => {
       <Box sx={{ ...centeredBox, py: 8, textAlign: 'center' }}>
         <Icon icon='mdi:link-off' fontSize={48} style={{ opacity: 0.5 }} />
         <Typography variant='h6' sx={{ mt: 2 }}>
-          {t('This alert was not correlated')}
+          {fingerprint ? t('This alert was not correlated') : t('Correlation cannot be looked up for this alert')}
         </Typography>
+        {/* DEFECT-3: the old copy blamed Shadow mode ("dispatch is disabled
+            platform-wide"), which is wrong — correlation audit rows are written
+            UNCONDITIONALLY in Shadow, and only the synthetic-parent emit is
+            gated. That wording made a lookup bug look like expected behaviour
+            and stopped operators investigating. */}
         <Typography variant='body2' color='text.secondary' sx={{ mt: 1, maxWidth: 480 }}>
-          {t(
-            "No correlation rule evaluated or matched this alert. If correlation dispatch is disabled platform-wide, most alerts will show this state until it's enabled."
-          )}
+          {fingerprint
+            ? t('No correlation rule evaluated or matched this alert.')
+            : t('This alert carries no am_fingerprint label, which is the key the correlation audit is recorded under.')}
         </Typography>
       </Box>
     )
```

### Why it is correct, and why client-side

* The label **is already on the alert object the component receives**. The alert
  record carries `labels` as `Array<{Label, Value}>` (the shape every other
  consumer in this file and in `AlertDetailPanel.js` already uses), and
  FINDINGS-OSCAR-BACKEND.md §0 shows the same record carrying both
  `oscar_fingerprint 31038f7b3e62` (12-hex) and
  `am_fingerprint 0be9385a228040e7` (16-hex).
* So the **client-side resolution is chosen over a server-side translation in the
  Next.js API route**: there is no extra round-trip, no new endpoint, no 12→16
  hex mapping table to build or keep in sync (that mapping does not exist as a
  queryable relation — a server-side route would have to re-read AlertHistory to
  recover a label it already sent to the browser). It is also a 2-line change
  local to the component, versus new server code on a shared proxy route.
* `fingerprint` feeds **both** consumers — `/api/correlation-audit/<fp>` and
  `/api/correlation-audit/children?parent_fingerprint=…` — because both already
  read the same `fingerprint` const. Both are fixed by the one change.
* `correlationFingerprintOf` returns **null** rather than falling back to
  `alert.fingerprint`. A fallback would silently reissue the wrong-domain query
  that caused the defect. Null disables the fetch (the existing
  `Boolean(fingerprint)` gate) and produces an honest empty state.
* Empty-state copy: the old text blamed Shadow mode. That is factually wrong —
  audit rows are written **unconditionally** in Shadow
  (`oscar-correlator/src/app/core/consume.py:539`, buffer-first, before any gate);
  only the synthetic emit is gated. That wording is precisely what stopped this
  bug being found. It is now split: "This alert was not correlated / No
  correlation rule evaluated or matched this alert." when we have a key, and a
  distinct "Correlation cannot be looked up for this alert / this alert carries no
  am_fingerprint label" when we do not.

### Blast radius

* UI-only, and scoped to the Correlation tab of the alert detail panel. No API,
  no backend, no schema.
* Alerts whose history record has **no** `am_fingerprint` label will now show the
  new "cannot be looked up" state instead of a query guaranteed to return nothing.
  That is a truthful downgrade, not a regression — the old query returned zero
  rows for those alerts too.
* The synthetic-parent branch (`isSyntheticParent`, label-wins) is untouched, so
  parent/child/empty view-mode selection is unchanged.

### To deploy

Rebuild + restart **oscar-adminui** (Next.js build — this is client-side code, so
a rebuilt bundle is required; a container restart alone will not pick it up).

### How to verify live

1. Open any alert that has a correlation audit row — e.g. the
   `CORRTEST_COOCCURRENCE_P01_DISKFULL_11CY4VT9` case from FINDINGS-DEFECTS.md,
   whose 16-hex key is `106f21bede4aee8f`.
2. Its Correlation tab must now render the audit row(s) (`enriched`,
   `parent_emitted`) instead of "This alert was not correlated".
3. Cross-check in the browser devtools network tab that the request is
   `/api/correlation-audit/106f21bede4aee8f` (16 hex), **not**
   `/api/correlation-audit/0564b2d974c0` (12 hex).
4. On a synthetic parent, the children grid must populate — its
   `/api/correlation-audit/children?parent_fingerprint=…` now also carries the
   16-hex key.
5. This is independent of `CORRELATOR_DISPATCH_ENABLED`; it must work in Shadow.

---

## Tests

| Suite | Command | Result (post-review, 2026-09-01) |
|-------|---------|--------|
| alertmanager | `PYTHONPATH=src python3 -m pytest tests/test_nats_publisher.py -q` | **20 passed** (4 new + 3 post-review) |
| correlator | `python3 -m pytest tests/test_rules_router.py -q` | **25 passed** (4 new + 1 post-review drift probe) |
| correlator (import) | `python3 -m pytest tests/test_rules_router_import.py -q` | **12 passed** (2 post-review, 2 fixtures updated) |
| correlator (regression) | `... test_rules_router_txn/_export test_rule_store -q` | **12 passed** |
| adminui | `npx vitest run src/views/pages/alerts/CorrelationTab.test.js` | **62 passed** (3 new) |
| attack test | `PYTHONPATH=<am>/src pytest scratchpad/test_attack_flap_hole.py -q` | **2 passed** (updated: now asserts the hole is CLOSED) |

All four suites are mocked-unit lanes — no MySQL, Redis, NATS or HTTP to the live
stack. Each new test fails against the pre-fix code by construction:

* `test_dedup_key_distinguishes_firing_from_resolve` — asserts the firing and
  resolve headers differ (identical before the fix).
* `test_dedup_key_missing_status_does_not_crash`, `test_no_header_when_fingerprint_absent`
  — pin the defensive behaviour the fix must preserve.
* `test_validate_rejects_flood_without_emit_spec_alertname` — was `valid:true`.
* `test_create_without_emit_spec_alertname_422_and_never_inserts` — asserts the
  session maker is **never constructed**, i.e. no row can leak.
* `test_create_deletes_the_row_when_the_loader_still_rejects` — asserts the
  compensating DELETE + second reload on the belt-and-suspenders path.
* `test_validate_allows_parent_child_without_emit_spec` — pins the carve-out.
* `correlationFingerprintOf` suite — asserts the returned key is the 16-hex label
  and explicitly `not.toBe(alert.fingerprint)`.

Post-review tests (2026-09-01):

* `test_refire_after_resolve_gets_a_new_key` — a re-fire (new startsAt) gets a
  DIFFERENT key from the first firing (fails against the status-only key).
* `test_group_interval_renotification_keeps_same_key` — same startsAt → same key.
* `test_none_starts_at_degrades_to_status_only_key` — `starts_at: None` yields
  `fp:status:` and equals the missing-field key.
* `test_emit_spec_gate_is_the_shared_loader_function_not_a_mirror` — asserts the
  router mirror is gone and patching `rule_store.emit_spec_alertname_error`
  changes validate's verdict (drift probe).
* `test_import_flood_without_emit_spec_alertname_reports_per_file_error` — import
  reports the loader's reason per-file and still imports the good documents.
* `test_import_parent_child_without_emit_spec_is_still_accepted` — carve-out
  survives in the import gate.

## Anything else deliberately NOT changed

* **The `OSCAR_ALERTS` `duplicate_window`** (`oscar-eventmesh/.../streams_bootstrap.py`)
  was left alone. Shrinking it is the mitigation-only option (c) from
  FINDINGS-DEFECTS.md — it narrows the race without removing it, and it is a
  stream reconfiguration with cross-service blast radius. Option (a) is
  sufficient and correct.
* **The Phase-B `_build_alert_group` label-shape defect** (FINDINGS-OSCAR-BACKEND
  §5 — correlator POSTs `labels` as a dict, AlertHistoryHandler reads the v1 list
  form) was **not** touched. It is a real, still-unfixed blocker for Phase B, but
  it is not one of the three assigned defects and it is a ~10-line change in a
  path that would want its own live proof.
* **No env/flag was flipped** — `CORRELATOR_DISPATCH_ENABLED` stays `false`.

---

## D1-b (2026-09-01) — decision-dispatch dedup key eats a same-window resolve (correlator consume.py)

**Twin of DEFECT 1, one layer down.** Fix 1 made the TRANSPORT dedup
(`Nats-Msg-Id`) lifecycle-aware; the correlator's own decision-dispatch dedup in
`oscar/oscar-correlator/src/app/core/consume.py` had the identical hole:
`_dedup_key(fingerprint, rule_id, window_id)` carried NO lifecycle state, so a
resolve landing in the SAME window as its firing (proven live 2026-09-01: rule
362, fp `feed0000feed0002`, window `362:global:1788247542000`, resolve @+8s →
"dedup skip (redelivery no-op)") collided with the firing's key and its
persistence CANCEL decision (audit reason `persistence_resolved_cancelled`) was
silently skipped. Both decisions carry outcome `enriched`, so outcome cannot
discriminate; and reason strings embed evaluate-time values
(`persistence_first_seen` embeds `scheduled_at`, derived from `now_ms` when
startsAt is absent), so keying on the reason would break redelivery dedup.

### Fix

Mirror the transport fix: the dedup key gains a **lifecycle token** built only
from fields carried verbatim on the inbound alert dict —
`{status(normalized)}:{starts_at (fallback starts_at_ms), verbatim, unparsed}`.
The alert dict IS reachable at the dedup site: `_process_decision(alert,
decision, ...)` receives it from `evaluate_worker` (single caller), so both
`status` and `starts_at`/`starts_at_ms` are available — the status-only
degradation applies only when the message itself carries neither startsAt field
(trap sources publishing `starts_at: None` — D4 residual).

New key: `oscar:correlator:dedup:{fp}:{rule_id}:{window_id}:{status}:{starts}`.

### Diff

```diff
diff --git a/oscar-correlator/src/app/core/consume.py b/oscar-correlator/src/app/core/consume.py
index a43b478f1..077a40726 100644
--- a/oscar-correlator/src/app/core/consume.py
+++ b/oscar-correlator/src/app/core/consume.py
@@ -135,15 +135,66 @@ class _PoisonMessageError(Exception):
     """
 
 
-def _dedup_key(fingerprint: str, rule_id: Any, window_id: str) -> str:
-    """Redelivery-idempotency key (92-CR-03 / D-08).
+def _dedup_key(
+    fingerprint: str, rule_id: Any, window_id: str, lifecycle: str = ""
+) -> str:
+    """Redelivery-idempotency key (92-CR-03 / D-08 / DEFECT D1-b).
+
+    Keyed on the composite ``(fingerprint, rule_id, window_id, lifecycle)`` —
+    computed PER rule decision after the 93.1-08 multi-decision fan-out — so a
+    redelivered message whose key already exists skips dispatch+emit+audit for
+    that decision but still acks. TTL = window + cooldown (see ``evaluate``
+    dedup_ttl_seconds).
+
+    DEFECT D1-b (2026-09-01, proven live): without ``lifecycle`` the key was
+    only ``(fingerprint, rule_id, window_id)`` — so a RESOLVE landing in the
+    SAME window as its firing (same fp, same rule, same window_id) collided
+    with the firing's key and was skipped as a "redelivery no-op", silently
+    eating the persistence CANCEL decision (audit reason
+    persistence_resolved_cancelled never written; cancelled timer state
+    diverged from audit). Both decisions carry outcome "enriched", so outcome
+    could not discriminate; and the reasons embed evaluate-time values
+    (persistence_first_seen embeds scheduled_at derived from now_ms when
+    startsAt is absent), so keying on the reason string would break redelivery
+    idempotency. The discriminator is therefore the triggering ALERT's own
+    lifecycle identity — status + startsAt, verbatim from the inbound message
+    (see ``_lifecycle_token``) — mirroring the transport-layer Nats-Msg-Id fix
+    (``f"{{fp}}:{{status}}:{{starts_at}}"``): a fire→resolve→re-fire flap
+    inside one window yields three distinct keys, while a JetStream redelivery
+    of any of them reproduces its key exactly and still dedupes.
+    """
+    return (
+        f"oscar:correlator:dedup:{fingerprint}:{rule_id}:{window_id}:{lifecycle}"
+    )
+
 
-    Keyed on the composite ``(fingerprint, rule_id, window_id)`` — computed PER
-    rule decision after the 93.1-08 multi-decision fan-out — so a redelivered
-    message whose key already exists skips dispatch+emit+audit for that decision
-    but still acks. TTL = window + cooldown (see ``evaluate`` dedup_ttl_seconds).
+def _lifecycle_token(alert: Dict[str, Any]) -> str:
+    """Lifecycle discriminator for ``_dedup_key`` (DEFECT D1-b).
+
+    Built ONLY from fields carried verbatim on the inbound alert dict — never
+    from evaluate-time values (now_ms, scheduled_at) — so a NATS redelivery of
+    the same message deterministically reproduces the same token:
+
+    - ``status`` — normalized like ``BasePattern.is_resolved`` reads it
+      (str/strip/lower; missing → "unknown"). Distinguishes a firing from its
+      resolve in the same window (the D1-b collision).
+    - ``starts_at`` (fallback ``starts_at_ms``), verbatim as published, NOT
+      parsed — parsing falls back to wall-clock for a missing/garbled value,
+      which would break redelivery determinism. Distinguishes a re-fire (new
+      episode, new startsAt minted by alertmanager) from a redelivery or an AM
+      group_interval re-notification of the SAME episode (startsAt stable) —
+      the same flap reasoning as the transport-layer Nats-Msg-Id fix. When
+      both fields are absent (trap sources publishing starts_at: None — D4)
+      the token degrades to status-only: fire/resolve still discriminated, and
+      the residual re-fire-in-same-window collision is bounded by window
+      rotation and closed for real by the D4 startsAt end-to-end work.
     """
-    return f"oscar:correlator:dedup:{fingerprint}:{rule_id}:{window_id}"
+    status = str(alert.get("status", "") or "").strip().lower() or "unknown"
+    starts = alert.get("starts_at")
+    if starts is None or starts == "":
+        starts = alert.get("starts_at_ms")
+    starts_token = "" if starts is None else str(starts).strip()
+    return f"{status}:{starts_token}"
 
 
 # ---------------------------------------------------------------------------
@@ -497,10 +548,15 @@ async def _process_decision(
     """
     window_id = decision.get("window_id")
     if window_id:
+        # DEFECT D1-b: the key carries the triggering alert's lifecycle token
+        # (status + verbatim startsAt) so a resolve in the SAME window as its
+        # firing is NOT mistaken for a redelivery of that firing (the live
+        # 2026-09-01 defect: persistence_resolved_cancelled silently skipped).
         dedup_key = _dedup_key(
             decision.get("alert_fingerprint", ""),
             decision.get("rule_id"),
             window_id,
+            _lifecycle_token(alert),
         )
         ttl = int(decision.get("dedup_ttl_seconds") or _DEFAULT_DEDUP_TTL_SECONDS)
         # 96.2-01 (M-2 / CR-1): set_nx_ex now RAISES RedisUnavailableError on a
diff --git a/oscar-correlator/tests/test_consume.py b/oscar-correlator/tests/test_consume.py
index af63da012..d7f25bc9c 100644
--- a/oscar-correlator/tests/test_consume.py
+++ b/oscar-correlator/tests/test_consume.py
@@ -297,7 +297,10 @@ async def test_transient_error_releases_claimed_dedup_key(make_alert, monkeypatc
 
     msg.nak.assert_awaited()
     msg.ack.assert_not_awaited()
-    dedup_key = consume._dedup_key("fpX", 7, "7:grp:1000")
+    # D1-b: the claimed key includes the alert's lifecycle token.
+    dedup_key = consume._dedup_key(
+        "fpX", 7, "7:grp:1000", consume._lifecycle_token(alert_dict)
+    )
     redis_client.delete_key.assert_any_await(dedup_key)
 
 
@@ -349,7 +352,9 @@ async def test_forced_redis_error_midevaluate_naks_then_redelivery_readvances_wi
     try:
         await rc.delete_key(win_key)
         await rc.delete_key(f"oscar:correlator:fired:{rule.id}:{group_hash}")
-        await rc.delete_key(consume._dedup_key("fp_live_1", rule.id, ""))
+        await rc.delete_key(consume._dedup_key(
+            "fp_live_1", rule.id, "", consume._lifecycle_token(alert_dict)
+        ))
     except Exception:
         pass
 
```

Plus new test file `oscar-correlator/tests/test_consume_dedup_d1b.py`
(4 tests: firing+resolve same window both processed; redelivered resolve
deduped; flap trio → 3 distinct keys + deterministic reproduction; token
determinism / starts_at_ms fallback / status-only degradation).

### Self-attack table

| Attack | Outcome |
|---|---|
| Flap in one window: fire → resolve → re-fire | **3 distinct keys.** Status splits fire/resolve; the re-fire's NEW startsAt (new episode minted by AM) splits it from the original firing. Proven by `test_flap_trio_yields_three_distinct_keys`. |
| Redelivery of each of the three | **Deduped.** Token is built ONLY from verbatim message fields (status, starts_at/starts_at_ms as published, never parsed — parsing falls back to wall-clock and would break determinism). Identical inbound bytes → identical key. Proven by `test_redelivered_resolve_is_still_deduped` + determinism assertions. |
| AM group_interval re-notification of a continuing firing | **Still deduped** (same episode → startsAt stable → same token) — matches transport-fix attack 1d; flood/threshold counts not inflated by THIS gate. (Window counting itself is upstream of the dedup gate — pattern `evaluate()` runs before it — so counting semantics were never governed by this key anyway.) |
| Repeated flood occurrences double-audited? | **No regress.** Distinct flood occurrences are DIFFERENT alerts with different fingerprints → always had distinct keys. The only repeated-delivery cases sharing fp+status+startsAt are JetStream redeliveries and AM re-notifications of one episode — both still dedupe. |
| Resolve → resolve (duplicate clear) | Same token → deduped inside TTL, exactly like transport 1b. Correct: the cancel is idempotent. |
| Timer-fired decisions (persistence_check / absence on_timer) | **Unaffected — verified, not assumed.** `timer_subsystem.py` `_fire_one` buffers audit + emits directly (93.1-10 net-deletion path); it never calls `_process_decision` and holds no dedup gate (its trigger-once is the separate `persistence_fired:`/fired-flag keyspace). `_process_decision`'s only caller is `evaluate_worker`. |
| CR-1 claimed-key release on nak | Release deletes the exact key claimed (same computation, alert in scope) — existing `test_transient_error_releases_claimed_dedup_key` updated and green. |
| status missing/garbled | Normalized like `BasePattern.is_resolved` (str/strip/lower; missing → "unknown") — two events can only both map to "unknown" if neither carries a status, in which case `is_resolved` cannot discriminate them either (moot, per transport attack 1c). |
| starts_at AND starts_at_ms both absent (trap sources, D4) | Token degrades to status-only: fire/resolve in one window ARE still discriminated (the proven live defect is closed); the residual re-fire-in-same-window collision remains for status-only alerts, bounded by window rotation (a re-fire in a LATER window gets a new window_id) and closed for real by D4's startsAt end-to-end work. Documented in the `_lifecycle_token` docstring. |
| Rolling-deploy key-shape change | Old 3-part keys in Redis no longer match new 4-part keys → for messages already processed pre-deploy and redelivered post-deploy, dedup misses once → at worst ONE bounded duplicate audit row per in-flight message during the deploy window (same bounded-duplicate stance as CR-1/D-08). No loss direction. |

### What must be recompiled

- **correlator only**: `./oscar compile correlator` then restart. No schema, no
  env, no middleware/alertmanager change.

### Live verification (the flap probe)

1. With a persistence rule (e.g. rule 362) active, fire the probe alert
   (fp stable), send its **resolve @ +8s** (same window), **re-fire @ +16s**.
2. Expect `CorrelationAudit` rows in order:
   `persistence_first_seen` → **`persistence_resolved_cancelled`** (the row
   that was missing pre-fix) → `persistence_first_seen` (re-fire re-arms),
   then `parent_emitted` at ~+46s (unresolved_for elapses on the re-fire).
3. Correlator log must NOT show `dedup skip (redelivery no-op)` for the
   resolve; a genuine NATS redelivery of the resolve must still show it.

### Test counts

`PYTHONPATH=src python -m pytest tests/` in oscar-correlator: **472 passed,
1 skipped** (correlator_live) — includes the 4 new D1-b tests and the 2
updated key-derivation call sites in `test_consume.py`. Zero pre-existing
failures.

### Residual risk

- Status-only degradation for startsAt-less (trap-source) alerts: re-fire in
  the SAME window still collides (fire/resolve does not). Bounded by window
  rotation; owned by D4.
- One bounded duplicate audit row per in-flight message across the deploy
  boundary (key-shape change). Accepted per D-08 bounded-duplicate stance.
