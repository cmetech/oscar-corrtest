# Independent showcase v2 review

Reviewer: `persistence_severity`, independent of implementation. Read-only product review; no live requests, deployment, pack changes or test resource creation by the reviewer.

## Scope

Feature CorrTest files: `test-packs/build_showcase.py`, `test_showcase_pack.py`, `run_showcase.py`, `test_run_showcase.py`, and `internal/oscar/showcase_parent.go` / `showcase_parent_test.go`. Supporting backend contracts checked include synthetic parent construction, Alertmanager direct-ingress fingerprint handling, parent-child audit decisions, Persistence timer audit timestamps, and existing History wire parsing.

The additive harness uses the real `/webhookreceiver/webhook/corrtest_showcase_v2` route. It does not invoke a phase-B/direct OSCAR Alertmanager shortcut for source inputs. The Go parent oracle is a component helper, not a deployed run or a source-authority implementation.

Final scoped disposition: **no unresolved actionable findings in the reviewed tooling**. Live qualification remains gated by the coordinated delivery-owner setup and the explicit limits below.

## Review findings and disposition

| Severity | Finding | Required correction and current disposition |
| --- | --- | --- |
| High | Root review found `dispatch_enabled=True` plus `authoritative=False` is impossible: `FailOpenState.correlator_dispatch_enabled` is `authoritative and not tripped`. The mocks invented a runtime state. | Fixed: require the actual ordinary dispatch contract and fresh healthy breaker/operational readback. Tests now instantiate the real backend `FailOpenState`. Enhanced-source disable is a separate `enhanced_correlation.production_enabled=False` check. Builder requirements and operator documentation now distinguish these gates. Root owns coordinated Alertmanager/correlator cutover review; this harness makes no live flag change. |
| High | Parent History could have canonical OSCAR fingerprint B while audit expected A, because only its `am_fingerprint` label was tied to A. | Fixed in Python and Go: bind actual History fingerprint and `oscar_fingerprint` to original audit parent identity. Synthetic direct ingress may legitimately set `am_fingerprint` to that same value; do not invent a distinct Native fingerprint for it. |
| High | Time-only Persistence could PASS with an immediately emitted parent, followed by a ninety-second wait. | Fixed: original trigger timestamp and physical parent timestamps must be at or after the thirty-second deadline. Early-parent mutation is rejected. This remains ordinary time-only Persistence. |
| Medium | Pattern-only rule selection chose retained v1 Flood when the real ID-ascending inventory put it before v2. The original fixture put it last. | Fixed: exact v2 rule-name lookup; fixture now places v1 first. Retained v1 authored fields are also pinned before any send. |
| Medium | Parent-child oracle counted both the parent's registration and the child's link as child links. | Fixed: require the actual parent registration decision separately, count and attribute only child linkage. |
| Medium | KeyboardInterrupt after a send attempt skipped report/evidence finalization. | Fixed: interrupted send produces INCONCLUSIVE/UNKNOWN with one attempt, no retry and no next input. |
| Medium | A raw HTML502 or malformed200 response made the final verifier throw even when the run correctly retained uncertain acceptance. | Fixed: retain and hash raw failed-response bytes; parsing/schema success remains required for acknowledgment/PASS. Both cases remain verifiable INCONCLUSIVE/UNKNOWN. |
| Medium | Export trusted ownership and duration report fields without rechecking their independent prerequisites/original timing. | Fixed: validate ownership/origins during export, retain wall/monotonic HTTP timestamps and recompute interval from the original last acknowledgment and first observation request. A real advancing clock test closes the initial exact-timestamp equality mistake. |
| Medium | Chronology checks also rejected a genuinely failed clock-regression attempt instead of allowing verifiable INCONCLUSIVE evidence. | Fixed: runtime stops on observed clock regression, retaining the attempted send as UNKNOWN; the verifier preserves valid INCONCLUSIVE evidence while rejecting regressed chronology for PASS. Regression test confirms one send and no continuation. |

## Verified boundaries

- Pack builder invokes real backend rule/profile validation; rules are authored disabled, with explicit installation/runtime preconditions.
- Preflight requires the exact seven v2 rules plus the known retained v1 rule, controlled profile revision and pack content, complete loaded inventory, healthy dependencies, fresh operational ordinary dispatch and separately disabled enhanced production admission. Postflight rejects observed changes.
- The finite source schedule uses a durable journal before each send, three-second pacing, bounded input/observation duration and zero automatic retries. The harness performs no rule mutation, pack removal or speculative source clear.
- Source Native records and physical History records are joined through their recorded fingerprints, run/source identity and original labels. Parent evidence must include actual History, original audit attribution and fixed rule/window/run identity; audit alone cannot PASS.
- Complete page counts are required. Truncated History/audit mutations fail. The bounded scenario refuses oversized results rather than silently treating an incomplete first page as complete.
- Offline verification replays the original HTTP requests/responses, rebuilds inventory and observation structures and reruns semantic checks. Rebound summary substitutions cannot replace original responses.

## Evidence and limits

Read final focused `showcase-real-dispatch-green.log`: **43 passed** (nine pack plus thirty-four harness tests), including the runtime-gate and clock refinements. `showcase-go-regression.log` ends with successful package results. Earlier RED and intermediate GREEN logs are retained in this directory. The broader Python rerun in `showcase-python-final.log` reports **165 passed** across the v1 builder/installer and v2 pack/harness. The reviewer read these records and did not rerun the author-owned suites.

The corrected candidate in `kit-v2-ready` has independently checked manifest SHA256 `bcd2c03a97507e8c896797c8acc5675f6c2211f79ba57a0ea871924467c03cef`. Earlier kit output remains superseded evidence and is not the deployment candidate.

Mock HTTP and simulated-clock runs validate the harness/oracle, not deployed UAT. No live case was executed in this review. A source-independent ownership declaration cannot detect an unobserved change/use/restore between reads; exclusive isolated ownership remains a prerequisite. Physical History is not proof of delivery to an external notification provider. Abrupt SIGKILL/power loss may leave a partial durable journal requiring inspection; it cannot become PASS.

The fourteen cases cover ordinary pattern positives/negatives. Persistence is thirty-second **legacy time-only** behavior, with an unmatched-input negative. This does not qualify the production occurrence gate, mapped severity, source recovery, crash continuation, production durations, moderate90/10 load, throughput, or the full39-case matrix. Preserve the pack and retained source/parent responsibility for subsequent inspection.
