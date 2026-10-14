# Staged co-occurrence fixture

This cut adds one reliability fixture and regression coverage using existing scenario, compiler, checkpoint, audit, named History and cleanup contracts. It changes no production Go behavior, API, dependency or configuration flag.

`scenarios/reliability/co-occurrence-lifecycle-downstream.yaml` distinguishes four observations: the first required role enriches without a trigger; the second required role produces one trigger audit; the disk source becomes resolved while CPU stays firing; both source episodes become resolved. Distinct exact MatchClause alertnames and `min_matches=2` are checked against the compiled fixture. N01 omits CPU but matches the disk clause, so it remains a missing-member negative, not an ordinary no-match control.

The backend co-occurrence schema and pattern were inspected read-only. `required_matches` uses exact alertnames; the hot pattern counts distinct alertnames, reports `enriched` while advancing and `parent_emitted` on trigger, and does not advance on resolved inputs. The existing independent test model reports generic `no_trigger`; this fixture's test adapter maps that outcome to the real co-occurrence `enriched` contract without consulting assertions or case codes. Current source History uses the same explicit test adapter as the prior named episode tests. These are harness contract checks, not a live Redis/OSCAR qualification.

## Validation

| Evidence | Result |
|---|---|
| `focused-initial.log.gz` | Fixture decode, compile and staged runner checks passed. |
| `focused-final.log.gz` | Added exact required-clause and distinct-role binding checks passed. |
| `suite-final.jsonl.gz` | **687 passing test/subtest events across 27 packages; no failures.** |

The tests intentionally simulate three runner outcomes: valid evidence PASS; a recovery that never reaches History FAIL; stale episode History INCONCLUSIVE. In either negative simulation the fourth stimulus is withheld and completed checkpoint evidence is retained. Early no-trigger and later trigger counts are inspected separately. Receiver admission remains NOT_TESTED and physical clear NOT_PROVEN in every report.

No failing test invocation occurred in this fixture-only cut; the simulated failure/inconclusive verdicts above are expected counterexamples, not live findings. Commands: `go test ./internal/runner -run TestStagedCoOccurrenceFixture -count=1`, then `go test ./... -count=1 -json`. `git diff --check` passed. Evidence uses deterministic gzip (mtime=0). No containers, deployments or application mutations occurred.

An audit-only `parent_emitted` decision is not evidence of an emitted/delivered synthetic parent. Partial or complete source History recovery is not parent recovery or physical-clear authority. No UAT cases or capacity targets are accepted. Live timing, actual receiver input, producer barriers, correlation episode application, delivery receipts and safe parent recovery remain outside this fixture. Tickets are excluded, live SNMP deferred, absence/collector-health unsupported, and enhanced admission/dispatch/suppression/clear stay disabled.

Parent whole-cut review checked the actual backend pattern and found no actionable issue. See [reliability testing](../../../reliability-e2e.md) for transport and evidence limits.
