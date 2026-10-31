# SNMP mixed smoke on oscar.home.arpa — 2026-10-31

**Result: mixed smoke failed; do not advance to higher rates yet.** All four isolated family preflights passed after installing missing MIBs. The mixed run received/forwarded all 34 packets, but five of fourteen expected original problem History records remained absent. Three other originals were correctly resolved through late-clear handling and are conservatively reported as mismatches by the current fast-clear-only observer.

## Scope and frozen site

Beast (192.168.0.152) sent SNMPv2c to **oscar.home.arpa (192.168.0.147), UDP 162** and hosted the append-only webhook sink. Beast was not the OSCAR target. CorrTest is visible at http://localhost:8787. Target History reads used `https://oscar.home.arpa/ext/mw`, an owner-only API credential file and the site's corrected KeyUsage CA. No credentials are included in this report or run exports.

The site retained the installed capacity Admin UI/Middleware, threshold correlator, guardproof Taskmanager and logging trapreceiver images. No concurrency, alert rules, mapping or admission settings were changed. Exact image digests, container IDs and configuration snapshots are in the evidence directory. Override SHA-256: `17f674ee687b2565a7f665489c399668ba059e00e10163cd47666d3747da87b3`. Before/after container IDs, images, restart counts and OOM state were unchanged.

The existing email-group notifier and two enabled RabbitMQ forwarders were temporarily disabled. LOGGER, CORRELATION_EVENTS and ALERTHISTORY stayed enabled; a temporary generic webhook captured test receipts. All original notifier statuses were restored and verified. The temporary notifier was deleted after all its deliveries were sent and audited. Existing correlation rules were inactive; these tests exercise SNMP problem/recovery matching, not the separate correlation-engine patterns or ServiceNow.

## Rounds

| Round | Profile | Result and evidence |
| --- | --- | --- |
| `crt_0YEQY1TT8TNKFRVZNP4Y7XAE36` | First isolated preflight | Missing Juniper MIBs produced empty named fields in the mapped alert key. Stopped before bulk traffic; sent the owned recovery. Two History events eventually resolved. Original INCONCLUSIVE / UNKNOWN result retained. |
| `crt_1141WKWG0D707KN13ZQERTTVBY` | Four family preflights, then 1 nominal local send/s for 20 seconds; minimum clear delay 5 seconds; drain observation 180 seconds | All four preflights passed. Bulk completed, but exact final History failed: 9 original problems + 14 recovery events instead of 14 + 14. COMPLETED / FAIL / UNKNOWN retained. |

Families: Juniper DHCPv6 interface client limit, DHCPv4 interface client limit, AAA authentication server and address-pool threshold. The second offer ran **08:22:54.131–08:23:13.179 UTC**. Its 34 packets comprised 12 preflight packets (four problem/repeat/clear triplets), 20 offer packets (ten problems, two repeats, eight clears), and two tail clears. No local send failures or unattempted planned packets; achieved local invocation rate 0.9976/s, maximum scheduling lag 28 ms. This is a packet rate, not a new-alert rate or individually acknowledged UDP delivery.

Receiver aggregate counters increased by **34 received and 34 forwarded**. All 14 recovery History events appeared. No packet loss was evident in these aggregate counters; they do not individually prove each repeated datagram's processing.

## Prerequisite and harness corrections

Five absent vendor MIB sources were installed additively from the local migration corpus: JUNIPER-SMI, JUNIPER-JDHCPV6-MIB, JUNIPER-JDHCP-MIB, JUNIPER-USER-AAA-MIB and JUNIPER-MIMSTP-MIB. The supported compile-only job loaded all five; exact OID-to-field checks passed. No mapping changes or YAML synchronization occurred. The overall job was partial (196/205 modules): nine other modules failed compilation. Their prior compilation state was not measured, so this does not establish a regression or prove those failures predated this work. The added sources remain a site prerequisite; they have not been baked into release images.

The first run also exposed a harness error: OSCAR intentionally writes a separate informational recovery History event. CorrTest now checks the original/recovery pair, distinct IDs/fingerprints, normalized group, exact key/agent/severity and matching fast-clear correlation key/value/count. It rejects duplicate or contradictory identities, requires stable resolved preflight reads, and retains DIRTY cleanup when a known problem is firing even if other evidence mismatches. Production OSCAR lifecycle/durability code was not changed.

Claude Opus and Codex Astra reviewed these observer changes. Duplicate-precedence, correlation-value, known-firing cleanup and repeated resolved-read findings were addressed. Red/green regression evidence, the full Go suite and final focused race tests are retained with the raw evidence. A general cross-key token scan was not added to CorrTest; this run instead used independent SQL retrieval across all labels and checked correlation-key uniqueness.

## Exact reconciliation

- Expected: 14 original problem identities, 14 separate recovery identities.
- Observed: **9 original problems, 14 recoveries; all 23 observed records resolved**. Final SQL read after cleanup still showed 23.
- Six originals resolved through `trap_correlation`; three through **`trap_correlation_late`**. Those three are valid late-clear outcomes, not demonstrated application failures. Their initial recovery labels retain zero-match, so the current strict fast-clear observer reports mismatch.
- Five missing originals: bulk tokens ending **4, 5, 6, 8, 9** (DHCPv6: 4/6/9; DHCPv4: 5/8), with prefix `ct-crt_1141WKWG0D707KN13ZQERTTVBY-`. Their recovery events exist. No correlation key was shared across different observed episode tokens.
- Controlled webhook: **20 owned receipts**, comprising six original firing notifications and fourteen informational recovery notifications; no duplicate fingerprint/status pair. Independently matched exact token, role, key, group, agent and correlation key to History, then joined Audit by History fingerprint. Native webhook and History fingerprints differ and must not be directly equated.
- **20 matching logical Audit successes, zero duplicate successes, and 20 owned durable delivery rows sent with audit completed.** No sink/Audit identity differences after the correct join. Three late-created resolved originals were not assumed to require firing notifications. The absent five originals prevent complete lifecycle qualification even though emitted notifications reconcile.
- Final Audit also contains suppression rows, including resolved transitions after administrative cleanup; these are not additional successful sends. The harness recorded one transient observation-read error.

The site was not overloaded in this bounded run. Over 20 samples spanning preflight through drain, host CPU averaged **13.73%**, with a **17.96%** peak sampling interval. Sampled waiting depth and oldest queue age stayed zero (15-second samples can miss brief queues). SQL connections peaked at 202, running threads at 3; global row-lock counters increased by 62 waits / 388 ms. These are whole-site measurements, not per-test attribution. No container restart/image/OOM change occurred. Two API read probes were 0.411 seconds for History and 0.047 seconds for Tasks; neither measures browser rendering or independent task execution.

## Cleanup and remaining hypothesis

After recording the verdict, scoped administrative cleanup explicitly ended **15 still-active Native Alertmanager recovery entries** (fourteen from round two, one from round one). Every entry was checked for the owned run token and recovery role. Both Native run filters then returned zero. This was administrative cleanup, **not natural drain or proof of complete alert persistence**. Historical records and original run verdicts remain intact. Observer and sink processes stopped after reconciliation/restoration.

The leading hypothesis is short-lived problems clearing before their first grouped Native Alertmanager notification creates History. Native routing uses group_wait 5 seconds and group_interval 30 seconds. The alert-ingestion path can end matching Native alerts when a recovery is recorded even if the original History row has not yet appeared (`oscar-alertmanager/src/app/routers/alerts.py`, `_end_active_alerts_in_am` call). Missing originals are later DHCP members of repeated alert-name groups. This is supporting correlation, not a proven causal trace. Existing late-clear behavior correctly handled three other cases and must be preserved.

Next: reproduce one DHCP family with unique run identities and compare short versus longer problem lifetimes against the same batching configuration. Capture each trap, Native state, forwarding/ingestion timestamps, History, Audit and durable delivery before changing any production logic. Separately extend the observer to accept independently proven late-clear outcomes; do not simply relax zero-match checks. Only after exact persistence/clearing passes should 5/s, 10/s and 25/s SNMP qualification resume.

## Evidence and limits

Local evidence: `~/.local/share/oscar-load-qualification/snmp-mixed-20261031-01/` contains both verified CorrTest exports, plans/reports, append-only sink, SQL History/Audit, durable reconciliation, sampled host/container/queue/SQL metrics, image/config snapshots, MIB hashes/jobs, review/test logs and cleanup records. Round one files are preserved under `round1/`. Final summary: `final-summary.json`; exact notification join: `notification-final-reconciliation.json`.

This run does **not** qualify sustained SNMP throughput, 25 alerts/s, full browser responsiveness, independent task execution, ServiceNow ticketing or all vendor trap mappings. The five missing original History records remain unresolved; do not describe this as a passing alert-flow or release qualification.
