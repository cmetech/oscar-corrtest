# Receiver checks from the CorrTest UI

Open **Run test** to launch either receiver check. These are bounded functional tests; the ordinary 25 alerts/s capacity driver remains separate.

## SNMP trap receiver

The SNMP check sends the H3C Smart Link `hh3cSmlkInactiveLinkDown` problem trap (`.1.3.6.1.4.1.25506.2.147.2.0.3`) followed by its `hh3cSmlkInactiveLinkUp` clear (`.0.4`). Both carry the same generated numeric Smart Link group ID and port `ifIndex`, plus a CorrTest run token in the interface description. The generated ATT migration mapping concatenates the first two varbinds into `alert_key`, which CorrTest uses to find the resulting alert. The fixture comes from `../snmp-migration/output/oscar_rules/h3c-hh3c-smlk-mapping.json` (source SHA-256 `c189502a3a4ac3ed6ce26b24f4161b904bc82fa0c62a6c53c6684f2ae13856cc`). CorrTest does not install or change this production mapping; it must already be loaded and active on the target.

On the CorrTest host, install Net-SNMP's `snmptrap` executable and set `CORRTEST_SNMP_COMMUNITY` in the managed environment or process environment. The UI accepts the UDP port and derives the destination hostname from the chosen OSCAR target. Ensure the trap receiver listens on that address and port and accepts this sender's community/source IP. Select the target and start the check from **Run test**. SNMPv2c is the supported fixture; SMTP and SNMPv3 are not included. CorrTest writes the community to a temporary owner-only Net-SNMP config file for each send, then removes the file; it does not put the community in command arguments or run evidence.

A passing run requires the expected mapped alert name, group, agent, `major` firing severity, exact generated `alert_key`, and the matching History row to become resolved after the clear. The persisted run includes the mapping source hash, OIDs, generated varbind identity, History ID and fingerprint, and a timeline. It never records the community. The check does not read back the installed mapping revision; a PASS proves the observed mapping behavior, while the source hash identifies the fixture CorrTest intended to exercise.

SNMP traps are unacknowledged UDP. Local `snmptrap` success is only evidence that the sender attempted the packet. If History never shows the problem, the run is inconclusive with unknown cleanup; if the problem appears but the clear does not, it fails with dirty cleanup. Investigate those rows before repeating the check. The check does not prove outbound notification delivery or sustained throughput.

## Webhook receiver

The **webhook receiver scenario** button runs the existing `flood-ordinary-webhook` fixture embedded in the binary. It requires a target with the pinned `corrtest_ordinary_v1` receiver binding and the profile already installed. The scenario exercises receiver forwarding, Native Alertmanager, History and correlation assertions. See [ordinary webhook input](ordinary-webhook-input.md) for its exact profile hash, authentication and cleanup limits. Saved custom scenarios can also be launched from **Run test** after import through the Scenario workbench.

Neither receiver check starts automatically, changes OSCAR rate policy, installs mappings, or qualifies the full 25 alerts/s workload.
