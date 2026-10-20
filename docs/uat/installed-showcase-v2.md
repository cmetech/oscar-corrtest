# Retained ordinary-pattern showcase v2

The v2 pack tools exercise the real webhook receiver → Native Alertmanager → OSCAR Alert Engine/History → correlator path. They require a synthetic parent in physical Alert History for every positive parent-producing case. An audit entry alone cannot pass.

These are additive Python tools beside CorrTest's pack builder/installer. They are **not** an extension of the existing Go `test-pack run` command. That command and its v1 Flood audit-only contract remain unchanged. The existing v1 pack and rule stay installed.

## Finite cases

Run each case separately with a fresh unpredictable run identity. Rules group on that identity and the case's site. Inputs are initial firing observations, paced three seconds apart; the tool then observes for at least 90 seconds on both wall and monotonic clocks. It sends no clears and performs no remote configuration changes.

| Pattern | Positive | Negative | Required positive evidence |
|---|---|---|---|
| Flood | Five different inputs | Four inputs | One Critical synthetic parent |
| Co-occurrence | A and B | A only | One Critical synthetic parent |
| Sequence | A then B, distinct source timestamps | B then A | One Critical synthetic parent |
| Threshold | Three distinct device labels | Three inputs sharing one device | One Critical synthetic parent |
| Cross-source | `snmp` and `api` source labels | Two `snmp` labels | One Critical synthetic parent |
| Persistence | One unresolved input for 30 seconds | Unmatched alert name | One Critical synthetic parent, with trigger and parent timestamps no earlier than the deadline |
| Parent–child | Real parent followed by child | Child without parent | Parent registration and one child link to that real parent; no synthetic parent |

Every case also requires exact Native identities, unchanged source severity and labels in History, the Native/OSCAR fingerprint relationship, complete bounded audit reads, and unchanged rule/runtime/profile readback. Parent assertions bind the rule, window, run, site, severity, canonical OSCAR fingerprint and audit fingerprint. The negative Persistence case is only a no-match control; it does not test clear-before-deadline behavior.

Cross-source inputs deliberately carry controlled source-category labels; they do not prove authenticated production adapter provenance. Persistence here is the existing time-only mode. The separate offline `correlation-persistence-v1` schedules describe production cases #30/#14/#18/#28, but this showcase does **not** qualify source-occurrence gates, mapped severity, protected selected-source commissioning, source recovery, outage recovery, external notification providers, or throughput/capacity. Collector-health/absence and ticket-provider cases are excluded.

## Build and install prerequisites

From the feature CorrTest checkout, use the existing project Python environment (with `httpx`, PyYAML and backend builder dependencies):

```sh
python test-packs/build_showcase.py --backend ../oscar --output /absolute/path/showcase-kit
```

The builder runs the actual backend rule validator, profile compiler and project-pack builder. The resulting pack includes the separate `corrtest_showcase_v2` profile; the v1 profile/hash is unchanged. `device` and `oscar_source` survive this v2 profile; unlisted control fields do not.

Installation is a separate operator step using OSCAR's existing pack and rule import tools. The rule archive contains seven **disabled** static rules. Before running, the isolated target must have:

- The installed `corrtest_showcase` version `0.2.0` mapping pack, activated and matching its packaged state.
- All seven exact showcase rules enabled, plus the unchanged retained `corrtest-operational-v1-flood` rule. The v1 rule may remain enabled or disabled; its authored behavior must match the original v1 contract.
- Coordinated ordinary delivery ownership enabled (`authoritative=true`, `dispatch_enabled=true`), a fresh healthy breaker, and all runtime dependencies ready. The runtime field `authoritative` is the legacy delivery owner switch, not enhanced source admission. Separately, `enhanced_correlation.production_enabled` must remain false and every installed rule must retain its ordinary policy. The harness changes neither delivery-owner flags nor source capabilities. The operator must coordinate the Alertmanager/correlator delivery cutover first to avoid duplicate notifications.
- History-only or otherwise controlled laboratory destinations, and exclusive target ownership through execution and evidence verification. This exclusion/destination condition is operator-declared; public readbacks cannot prove continuous exclusion or detect change/use/restore between reads.
- Verified HTTPS and explicit API/receiver credentials. Both endpoints must have the same origin. The API can use the `/ext/mw` prefix; the receiver base ends with `/webhookreceiver`.

Record the kit's manifest SHA-256 separately from the artifact. It is an input pin, not a self-asserted trust claim.

## Run and verify

Use a private ownership JSON file with this shape (replace the target and reference):

```json
{"format":"corrtest-owned-target-v1","target":"https://beast/ext/mw","owner_reference":"isolated-correlation-uat","exclusive_through_export":true,"stable_configuration_and_processes":true,"destinations_history_or_controlled_lab":true}
```

Provide credentials through environment variables, not command arguments. Then run one fixed case:

```sh
python test-packs/run_showcase.py run \
  --kit /absolute/path/showcase-kit \
  --manifest-sha256 MANIFEST_SHA256 \
  --api-base https://beast/ext/mw \
  --receiver-base https://beast/webhookreceiver \
  --ownership /absolute/path/ownership.json \
  --case flood-positive \
  --evidence /absolute/path/new-evidence-directory \
  --ca-file /absolute/path/lab-ca.pem
```

Default credential names are `CORRTEST_API_KEY` and `CORRTEST_RECEIVER_KEY`; flags can select different environment variable names. `--help` lists all 14 case identifiers. Reusing an evidence directory is rejected.

The evidence directory contains the original pinned kit, private fsync-before-send intents, exact request/response bytes, profile revision acknowledgments, wall/monotonic timestamps, pre/postflight readbacks, observations, report and a closed hash inventory. The verifier replays the original HTTP transcript and recomputes the evidence assertions; it does not trust the summarized observation/report alone.

```sh
python test-packs/run_showcase.py verify \
  --evidence /absolute/path/evidence-directory \
  --manifest-sha256 MANIFEST_SHA256
```

This verification makes no network calls. Preserve the entire directory when transferring or archiving evidence and run verification again after transfer. Hashes detect changed bytes and semantic replay detects inconsistent summaries; they are not a cryptographic attestation of an untrusted capture host or target.

A successful case reports `PASS / DIRTY` and exits **4**, because its alerts and rules remain for inspection. `DIRTY` means retained test inputs, not a failed assertion. An uncertain send reports `INCONCLUSIVE / UNKNOWN`, stops immediately, and never retries. HTML 502 responses, malformed acknowledgments, cancellation and clock regressions retain failure evidence. Preflight rejection sends nothing and reports `NOT_REQUIRED` cleanup. A hard process kill or storage failure can leave only a partial private journal; inspect it before any new action. The tool does not resume or automatically clean partial runs.

The finite harness reads a maximum of 100 rows per query and rejects incomplete/excess pages. Repeated retained runs can eventually exceed that bound; treat that as an explicit evidence limitation rather than dropping rows. This is a correctness showcase, not a load generator.
