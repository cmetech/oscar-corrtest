# Staged sequence fixture and real-client qualification — 2026-10-13

CorrTest feature worktree only; no production runner/schema change, backend edit, new dependency, live request, deployment or container. All HTTP tests use auto-closed local httptest servers and temporary SQLite.

## Contract inspected

- OSCAR `oscar-correlator/src/app/patterns/sequence.py`: resolved inputs do not advance; group-local entries are sorted by event milliseconds with arrival order for ties; only the chronological tail is compared; ordinary decisions are enriched and the first trigger is parent_emitted.
- OSCAR `schemas/match_criteria/sequence.py`: ordered exact alertname clauses, minimum two.
- OSCAR `core/lua/add_sequence_and_check.lua`: replace duplicate fingerprint, bounded list, event timestamp lower bound. Future timestamp clamp is in `patterns/base.py`; tests keep processing time at or after each event rather than qualifying the clamp.
- Existing compiler and builtin use login_failure → privileged_command and reversed negative order.

## Fixture and tests

New `scenarios/reliability/sequence-lifecycle-downstream.yaml` sends two named roles three seconds apart, observes first-step/no-trigger and completed-tail/trigger checkpoints separately, then checks exact partial/all source History recovery. N01 reverses the two required roles and checks enriched/no-trigger for the full checkpoint. This is matched wrong order, not no-match.

The existing real-client wire responder now supports only Flood and sequence. It reuses the independent model, real rule/History/audit JSON and retained named episode cleanup. Sequence's generic model nontrigger is encoded as the actual enriched outcome. Fault modes force a reversed-tail false trigger, omit startsAt, or ignore recovery in History; the runner returns FAIL, INCONCLUSIVE and FAIL respectively and stops later stimuli when applicable. Valid sequence passes five checkpoints across six inputs. NOT_PROVEN/NOT_TESTED coverage remains asserted.

`red-model.log` records seven reproduced model mismatches: subsequence instead of exact tail, resolution advancing a sequence, both event/arrival ordering differences, expired first step, mixing groups and duplicate fingerprint advance. `green-model.log` passes ten focused model cases after the first-trigger model correction. `red-http.log` records the four wire cases failing before sequence responder support; `green-http.log` passes the four sequence cases together with all fourteen existing Flood wire cases.

`full-suite.jsonl` and `final-suite.jsonl` each record **746 tests/subtests across 27 packages passing**, zero failures (`go test ./... -count=1 -json`). The final run follows the processing-clock test refinement above. Failures and passes are retained as deterministic gzip artifacts.

## Limits and remaining work

This is direct OSCAR Alert Engine downstream injection and local model qualification. Parent emission audit is a decision, not parent creation/delivery proof. Source History recovery is not parent or physical clear. Redis timer expiry, cooldown rearming/refire, future skew clamp, multiworker races and broker delivery remain unqualified. No receiver admission, bundle revision, producer barrier, notification guarantee, capacity result or any of 39 UAT acceptances is claimed. Absence/collector-health remains unsupported; tickets and live SNMP are excluded.

Read-only review of the next restart-reconciliation boundary also found that persisted input + exact History cannot prove an original accepted request finished: delayed work may arrive after a cleanup request, and rule deletion does not prove correlation actions drained. Existing resolution facts lack episode/action identity. No automatic reconciliation or CLEAN-granting feature was added; a concrete completion/ordering contract remains necessary.

## Whole-cut review disposition

The reviewer found one P2 regression in the new test model: for a rule A → B, an unmatched same-rule-tag X was incorrectly inserted into the tail, so A,X,B failed where the real rule matcher filters X and the pattern sees A,B. The recommendation was to skip inputs that match none of the sequence clauses before appending them.

`red-review-unmatched.log` reproduced the finding. The model now checks `matchesAny` before inserting; `green-review-unmatched.log` passes the focused cases. `review-fixed-suite.jsonl` records the final **747 tests/subtests passing across 27 packages**, zero failures. The reviewer reported no additional finding; one fix pass was applied. The staged scenario itself was unaffected. All earlier runs and the original finding are retained.

