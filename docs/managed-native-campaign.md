# Managed source campaigns

The managed lane listens as a single test network element for an independently
commissioned OSCAR TL1 probe. It speaks the generic-tl1-example@1 plain TCP
login, heartbeat, logout and autonomous OC3 alarm grammar. A live probe owns
the event UUIDs, episode starts, durable FIFO and quiet refreshes. The named
receiver, native Alertmanager, signed callback, History and normal target
handlers process those events. A locally completed socket write identifies an
offered event. Qualification also requires downstream source and provider proof.

## Operator preparation

Use a disposable, isolated target and ticket provider. Another session merges
the coordinated changes, patches the combined build onto oscar.home.arpa,
and records deployed versions before running these commands. Keep the
correlator dispatch flag at its existing setting. A missing eligible reference
leaves the before/after baseline missing.

1. Commission the stock TL1 probe pack, protected Connection reader and separate
   scoped receiver writer in accordance with OSCAR's TL1 administration guide.
   Configure a dedicated source, TID, test credentials, Connection locator and
   receiver mapping revision with verified source binding. Retain the original
   probe journal. Point the probe at the CorrTest controller's designated TCP
   listener. Select plain TCP and generic-tl1-example@1, direct login or
   header wakeup, and configure keepalive and quiet refresh. Generic reports
   are synthetic device grammar on a real collector path. Provision a scope
   with clear authority for fresh, uninterrupted episodes. Preserve historical
   coverage gaps when later reconnecting for a new campaign.
2. Enable the controlled CAMPAIGN_SINK capture and the normal ALERTHISTORY path.
   Route test alerts only to the isolated sink and test PDI, including Hooks.
   Control all other potential downstream destinations. For the dedicated
   smoke, already disable every other external notifier. The smoke checks
   these states and leaves site-owned notifiers alone. The existing mixed
   campaign uses its normal mutation ledger for notifier isolation. Permit
   short initial group waits and group changes. Use a native ordinary
   repeat interval longer than the managed smoke's quiet timer window.
   Commission the tested policy feature on the controlled recipient. First
   activation can permanently enroll the recipient. Do that only in a
   disposable recipient scope approved for qualification.
3. Keep the TL1 mapping's stock source identity, alarm key and annotations.
   For this test source and TID, route `aid` matching
   `^CT-[0-9a-f]{24}-AUTO-000000$` to label-driven `create_ticket=snow`.
   Set `create_ticket=false` for the other fixture AIDs. Preserve the AID in
   each ticket summary and a unique test run token in synthetic parent ticket
   summaries. Configure the ticket handler's expected Major priority using the
   actual PDI mapping. Confirm all effects target that PDI. The harness reads
   the associated incident number, sys_id and stored priority independently.
   Major and Critical may produce the same provider priority.
4. The full mixed campaign commissions the suppression companions after the
   verified Minor→Major transition. The isolated native ingress must allow
   silence create/list/get/expire and alert POST/GET using CorrTest's configured
   native v2 transport. The harness creates an eight-minute, run-owned silence
   with four exact equalities (AID, source, TID and Major), retains the returned
   ID and checks creator, unique comment and all matchers before expiry.
   On an uncertain POST it looks up that subject and unique comment and never
   reissues the create. Route the control alert `CorrTestNativeInhibitor` to a
   test null receiver. Precommission an inhibition rule for the dedicated
   source/TID with that exact source alertname, target `integration=tl1_probe`,
   target `severity=major` and target
   `aid=~"CT-[0-9a-f]{24}-inhibit-000000"`. Set the inhibitor equality fields
   to `aid`, `source_id` and `node`. The harness raises and clears only its
   uniquely labelled v2 control alert; both subjects must start unsuppressed.
   Route each managed subject to exactly one commissioned canonical receiver,
   preserve individual AID/startsAt/correlation identity in sink payloads and
   retain native group/repeat intervals in the configuration digest.
5. Route independent ordinary native v2 `campaign_lane=native-direct` offers
   to a dedicated directly captured receiver, and `native-canonical` to the
   canonical OSCAR receiver with CAMPAIGN_SINK delivery. Keep direct and
   canonical receiver names distinct and retain physical capture on both.
   Use an actual native repeat interval below five minutes on this ordinary
   route. The unchanged canonical v2 update retains the original start and
   labels. The receiver's own schedule determines actual recurring callbacks.
   Save the same routes on both builds for the ordinary control.
6. Supply a dedicated operator's current NextAuth session as a private file
   containing the request Cookie header value alone. The session must remain
   valid through the campaign and have alert read, resend, acknowledgement,
   ticket creation and notification-audit read permissions. CorrTest calls
   the real HTTPS AdminUI API routes on the target origin. Next signs the
   server-to-server request. No actor signing key is provisioned to CorrTest.
   Supply the normal campaign-target key with user-scoped notifier policy
   CRUD, read and activation permissions, plus the existing worker, metrics,
   sink, SSH, PDI and front-office configuration as applicable.
7. Install Oracle MySQL CLI `mysql` on the controller PATH. Provide a private
   defaults file selecting the correct database, TLS CA, host and a dedicated
   read-only account. Client options force `--ssl-mode=VERIFY_IDENTITY`.
   Grant SELECT on AM_AlertHistoryWriteReceipt, AM_NativeActionEpisode,
   AM_NativeActionVariant, AM_NativeActionOperation, AM_NativeActionTarget,
   AM_NativeCallbackReceipt, TM_NotificationDeliveries, TM_ResendEpisodeState,
   TM_ResendScheduleDispatch, TM_ResendPolicyRevision, AM_AlertTicketWorkflow,
   TKT_Ticketing_Audit, AM_AlertResend and AM_AlertAudit. Enable the normal
   automatic ticket workflow visibility path on the isolated test provider
   so its original flow, associated History episode, operation and success
   audit can be joined. The adapter reads five fixed SELECT projections per
   full snapshot. Recovery and the manual ticket check use separate fixed
   SELECT projections of the issued request and controlled History. The
   all-zero UUID precheck verifies access without selecting a live test alert.
   Receipt reads are controller measurement traffic. Record their elapsed
   durations and include the measurement load in the configuration manifest.

Add the optional `ManagedTL1` object to the campaign server's JSON config.
Choose explicit private/loopback addresses, test identifiers and actual paths.
Use mode 0600 on all three credential files. Set the deployed effective mapping
hash, read back after enabling/customizing the selected profile.

```json
{
  "ManagedTL1": {
    "Listen": "127.0.0.1:19001",
    "ProbeIP": "127.0.0.1",
    "TID": "CTNE01",
    "SourceID": "corrtest-lab",
    "Locator": "dedicated-connection-id",
    "MappingRevision": "<64 lowercase hex characters from effective mapping>",
    "CredentialFile": "/run/secrets/corrtest-device-login.json",
    "SessionCookieFile": "/run/secrets/corrtest-operator-cookie",
    "MySQLDefaultsFile": "/run/secrets/corrtest-reader.cnf",
    "TicketPriority": "1"
  }
}
```

The device credential file contains a JSON object with username and password.
The probe peer IP must match exactly. The listener accepts one authenticated
session per run. It stops on session loss with the original journal and source
ownership intact. No replay or automatic device reprovisioning occurs.

## Workload and evidence

The managed-only functional smoke allows 20 minutes of source lifecycle plus five minutes
of planned drain. Preparation (including first policy activation) precedes T0.
It exercises Minor to Major continuity, manual resend, independent one-minute
ordinary resends, three-minute Critical escalation, later Critical resend,
acknowledgement and its subsequent Critical repeat, native source clear, resolved eligibility, fresh refire,
mixed bulk, an unrelated sibling, automatic and manual ticketing. The API
exercise does not inspect browser button rendering. The mixed V3 smoke also
reserves 20 minutes of load, with its ordinary standing pool through T+20 and
a two-minute drain. The full 150-minute run retains its 30-minute drain.
The ticket observation budget is 150 seconds per provider case. The automatic
History lookup also permits the existing anti-flap hold. Commission an
environment whose actual hold plus write/provider latency fits the relevant
window and whose total lifecycle fits the smoke budget. A longer deployment
hold is an explicit missing result. Record the actual hold in the manifest.

Ordinary policy priorities are their established positions (R1–R4, 1–4).
The immutable managed workload configuration assigns TL1=5, REPEAT=6 and
EDIT=7. Existing site conflicts remain catalog errors. The test-only rules
match exact source, TID and run-owned condition with a missing-safe equality
chain. Missing TL1 properties are ordinary misses. The POL condition is shared
by the functional, assigned mixed, escalating standing and suppression AIDs.
Only the owned full-campaign companions use silence CRUD or the inhibitor.
Each sustained subject uses a root-only DB sample about every two seconds
plus a full late-segment snapshot. With two subjects for 150 minutes the
sampling ceiling is roughly 9,000 root-only reads. Persist measured timings
and query count per segment. An uncertain dispatch bracket is inconclusive.
The original source closure marker and provider-owner mutation ledger have
separate meanings. Cleanup re-reads original requests or exact targets and
leaves unresolved ownership DIRTY across operator reruns.

The 150-minute mixed workload retains the existing baseline, ticket window,
correlation sets, ordinary redelivery policies and standing pool. It adds the
same functional lane, further suppression and policy lifecycle checks, and
a serial correlation companion with one genuine auto-ticketed synthetic parent.
All source and target investigation is bounded to that campaign. The ordinary
control uses only public native v2 traffic, source readback, baseline History
and capture on the two named native receiver routes. Record the pre-build
control with the common harness and deployment-specific routing manifest.

The initial mixed rate is 0.2 additional TL1 PDUs/second, including clears.
Mixed-rate slots emit Minor, Major, clear in interleaved groups of up to 64,
with complete lifecycles only. Alternating slots use no policy or the escalation
policy. Additionally, a repeat-only and escalating subject run throughout
each ten-minute segment. The full campaign expects 15 segments per policy,
including the last segment. Their source raises/clears and all functional
inputs are additional traffic. D3 calculates the selected share and stores
the effective scenario hash, requested and actual delivery rates. Record
all offered PDUs, written, definitely unentered, unknown, downstream
observed and provider outcome counts separately.

Campaign events include the original wire and action intent, source scope,
native authority/episode/generation, exact variant start and revision,
live validity readback, pinned policy binding, source/effective severity,
target IDs and outcomes, measured dispatch observation brackets, independent
sink times/IDs, original PDI IDs and priorities, and per-segment reason.
Within one functional episode there are bounded SELECT sentinels for 256
source receipts/bindings/dispatch roots and 512 physical rows. A snapshot
that exceeds a sentinel is incomplete. Ten-minute subjects rotate and the
mixed-rate source clearance uses paginated History by run-specific condition.
The final segment is sampled eight seconds before its boundary, and physical
source clearance proceeds during drain. Retain final source and receipt
tails when scoring the whole window.

The smoke rejects a timing sample whose observation window overlaps the
earliest eligible instant. It never applies the legacy symmetric repeat
tolerance to an escalation. A timed observation with an incomplete bracket
requires stronger operator evidence. The sustained segment also checks
provider callback arrival versus independent deadlines. Capture dispatch
timestamps and callback/child/provider latency independently for D3 release
scoring. The SQL adapter exports no service assertions, private command
bodies, DB credentials, target URLs or entry tokens.

Record mutation intent before policy and ticket changes. Owned wire clears
are verified against History under the original source/TID/AID. An
interruption, ambiguous ticket or failed source readback can leave DIRTY
cleanup. Reconcile the existing collector journal, original History and
operations and the controlled PDI, retaining IDs and evidence. Never create
replacement tickets or restart the device replay to manufacture proof.

## Operator commands

Run after all reviewed sources have merged and the target is patched.
These commands and fixtures are UNRUN. D1's selector guard remains until
D3 wires aggregate evidence and enables each selector. Source review alone
does not qualify any combination.

```sh
go test ./internal/campaign/managed ./internal/campaign/targetops ./internal/campaign/lane ./internal/campaign/engine ./internal/campaign/resend ./internal/campaign/score -count=1
bin/oscar-corrtest campaign precheck --server "$CORRTEST_SERVER" --token-file "$CORRTEST_TOKEN_FILE" --scenario managed-alerts-smoke@v1 --run-by human --output json
bin/oscar-corrtest campaign run --server "$CORRTEST_SERVER" --token-file "$CORRTEST_TOKEN_FILE" --scenario managed-alerts-smoke@v1 --run-by human --wait --output json
bin/oscar-corrtest campaign export "$RUN_ID" --server "$CORRTEST_SERVER" --token-file "$CORRTEST_TOKEN_FILE" --output-file "$RUN_ID-evidence.zip"
```

Run the full alerting-day@v3 after the managed smoke, using the final D3/D4
share, provenance and comparison instructions. Capture actual deployment,
clock and workload digests, packet/sink receipts, cleanup state and case
failures. Use a separate disposable fault lane for direct-entry process loss,
unknown provider ownership, >4096 tracked sources and other transport/recovery
companions. Keep fault traffic outside a comparable load window.
