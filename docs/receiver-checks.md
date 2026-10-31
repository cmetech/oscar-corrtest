# Receiver checks from the CorrTest UI

Open **Run test** to launch either receiver check. These are bounded functional tests; the ordinary 25 alerts/s capacity driver remains separate.

## SNMP trap receiver

The SNMP check sends the H3C Smart Link `hh3cSmlkInactiveLinkDown` problem trap (`.1.3.6.1.4.1.25506.2.147.2.0.3`) followed by its `hh3cSmlkInactiveLinkUp` clear (`.0.4`). Both carry the same generated numeric Smart Link group ID and port `ifIndex`, plus a CorrTest run token in the interface description. The generated ATT migration mapping concatenates the first two varbinds into `alert_key`, which CorrTest uses to find the resulting alert. The fixture comes from `../snmp-migration/output/oscar_rules/h3c-hh3c-smlk-mapping.json` (source SHA-256 `c189502a3a4ac3ed6ce26b24f4161b904bc82fa0c62a6c53c6684f2ae13856cc`). CorrTest does not install or change this production mapping; it must already be loaded and active on the target.

On the CorrTest host, install Net-SNMP's `snmptrap` executable and set `CORRTEST_SNMP_COMMUNITY` in the managed environment or process environment. The UI accepts the UDP port and derives the destination hostname from the chosen OSCAR target. Ensure the trap receiver listens on that address and port and accepts this sender's community/source IP. Select the target and start the check from **Run test**. SNMPv2c is the supported fixture; SMTP and SNMPv3 are not included. CorrTest writes the community to a temporary owner-only Net-SNMP config file for each send, then removes the file; it does not put the community in command arguments or run evidence.

A passing run requires the expected mapped alert name, group, agent, `major` firing severity, exact generated `alert_key`, and the matching History row to become resolved after the clear. The persisted run includes the mapping source hash, OIDs, generated varbind identity, History ID and fingerprint, and a timeline. It never records the community. The check does not read back the installed mapping revision; a PASS proves the observed mapping behavior, while the source hash identifies the fixture CorrTest intended to exercise.

SNMP traps are unacknowledged UDP. Local `snmptrap` success is only evidence that the sender attempted the packet. If History never shows the problem, the run is inconclusive with unknown cleanup; if the problem appears but the clear does not, it fails with dirty cleanup. Investigate those rows before repeating the check. The check does not prove outbound notification delivery or sustained throughput.

## SNMP mixed load

**Run test → Run an SNMP load test** adds a repeatable native SNMPv2c traffic lane. The existing H3C single-pair check remains available. For the homelab, select `https://oscar.home.arpa/ext/mw` as the target; Beast is the sender, not the OSCAR target. The UDP destination hostname comes from that selected target, independently of HTTP/Traefik.

| Family | Weight among new identities | Problem / clear specific traps | Migration mapping file |
| --- | ---: | --- | --- |
| Juniper DHCPv6 interface client limit | 40% | 1 / 2 | `juniper-jdhcpv6-mapping.json` |
| Juniper DHCPv4 interface client limit | 30% | 2 / 3 | `juniper-jdhcp-jdhcp-1-3-mapping.json` |
| Juniper AAA authentication server | 20% | 3 / 4 | `juniper-user-aaa-mapping.json` |
| Juniper address-pool threshold | 10% | 5 / 6 | `juniper-user-aaa-mapping.json` |

Select the mix or an individual family. The embedded fixtures retain the migration source SHA-256, numeric OIDs, MIB varbind types, expected mapping fields and full run token in the actual alert key. Small samples approximate the weighted mix. H3C's limited numeric identity is deliberately excluded from bulk traffic to avoid cross-run collisions.

Defaults are 5 local sends/s for a nominal 60 seconds, a 30-second minimum problem-to-clear interval, and up to 300 seconds for History drain. Bounds are 1–25 sends/s, 10–600 offer seconds, 1–120 seconds before clear, and 10–1800 drain seconds. Problems, repeats and clears all consume the send budget; **25 sends/s does not mean 25 new alerts/s**. Repeats preserve the original payload and leave at least one second before the scheduled clear. At a one-second clear delay there may be no eligible repeats.

The run first observes one firing, repeat-still-firing and resolved lifecycle per selected family. This checks the row shape after a repeat without claiming acknowledgement of that packet. A failed preflight prevents bulk traffic. These samples are separate from the measured load. The offer follows deterministic problem/repeat/clear slots, falling back to a new problem when no eligible repeat or clear exists. Remaining clears follow the offer at the same nominal send rate. The minimum clear delay applies to load packets; preflight clears follow firing observation, and cancellation cleanup may clear earlier.

Pacing limits local subprocess invocation starts, not exact UDP emission or arrival times. A delayed subprocess or network can shift packets. There is no catch-up burst. Scheduling lag above the larger of one second or 5% of scheduled elapsed offer time stops the load and produces an inconclusive result. For a 600-second offer, the lag tolerance reaches 30 seconds; measured rate and timestamps remain in the report. This is a bounded traffic generator, not a wire-rate guarantee.

The complete nonsecret plan is persisted before traffic. Each send has a durable `alert.prepared` event and `UNKNOWN` cleanup responsibility committed together before external I/O, followed by `trap.result`. The report separates preflight, offer, tail and cancellation cleanup; it includes attempts, local failures, unattempted plan packets, lag, achieved local-send rate and exact History identities. Report episode indexes address preflight episodes first, then the load plan episodes; packet-plan indexes address the load episodes alone. Local failures include all phases. Export the run normally to retain the plan, report and event ledger in the verified evidence bundle.

A PASS means the paced plan completed and each attempted owned problem has exactly one matching History identity with the expected original severity, agent/group and a resolved state. Identical pagination overlap is ignored; conflicting rows and distinct duplicate identities are not. The report explicitly marks individual UDP/repeat receipt, **causal attribution of resolution to a clear packet**, notification delivery and capacity qualification as unproven/not tested. Auto-resolution or another actor could resolve History: a resolved row alone does not establish which clear caused it. Qualification must independently collect receiver/OSCAR evidence, queues, notifications, CPU and UI/task probes.

Cancellation stops new problems and attempts paced clears for owned problems whose clear has not already been attempted. The subsequent canceled-run drain observation is capped at 30 seconds; sending the cleanup itself can take longer at low rates. Failed or ambiguous clears are never blindly retried. Missing/unresolved History remains UNKNOWN/DIRTY and blocks ordinary retention deletion. A ledger/storage failure stops external I/O, including cleanup, because further sends could not be recorded. After a crash or storage failure, the interrupted run retains cleanup responsibility; inspect its ledger and reconcile exact owned identities externally. Generic correlation cleanup is intentionally disabled for SNMP runs.

This mode requires `snmptrap` on the CorrTest service PATH, `CORRTEST_SNMP_COMMUNITY`, working target History credentials, installed active mappings, and receiver admission for the sender. It never installs mappings or changes notifiers/rules. Real target routing can send notifications and create tickets: use the intended test destination/notifier profile before starting. The community is held in a private 0700 directory with a 0600 `snmp.conf` for the run and removed on normal completion. A hard process kill can leave that private temporary directory; remove only the stopped run's residue after verifying no live sender owns it. Credentials are excluded from argv, subprocess output and evidence.

Suggested progression: a small 1/s mixed smoke, then 5/s normal traffic, then 10/s, followed by a separately instrumented 25/s burst. Do not call the SNMP lane qualified from a PASS here alone.

## Webhook receiver

The **webhook receiver scenario** button runs the existing `flood-ordinary-webhook` fixture embedded in the binary. It requires a target with the pinned `corrtest_ordinary_v1` receiver binding and the profile already installed. The scenario exercises receiver forwarding, Native Alertmanager, History and correlation assertions. See [ordinary webhook input](ordinary-webhook-input.md) for its exact profile hash, authentication and cleanup limits. Saved custom scenarios can also be launched from **Run test** after import through the Scenario workbench.

Neither receiver check starts automatically, changes OSCAR rate policy, installs mappings, or qualifies the full 25 alerts/s workload.
