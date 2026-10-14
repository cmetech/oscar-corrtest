# Ordinary webhook input

The [basic Flood fixture](../scenarios/reliability/flood-ordinary-webhook.yaml) sends ordinary alerts through a webhook receiver profile, Native Alertmanager and the normal callback path. Public rule, History and audit requests still use the separate OSCAR API target. This is an additional input route, not selected source admission: `receiver-admission` remains unsupported and is refused. Existing scenarios and standalone `doctor` retain direct Alert Engine injection.

## Configure a controlled lab

Provision the [fixed Mapping artifact](fixtures/corrtest_ordinary_v1.json) under `webhook_profiles/corrtest_ordinary_v1` using the existing OSCAR Mapping tooling. CorrTest does not install it. Preserve its exact description and elements; the compiled effective hash is `a823e17c4f3e3d33ffdebae28165b5835ab3255f37be8f0c6c745d8dabc92284`. There are no nested Mapping dependencies. The profile copies labels and annotations and requires explicit RFC3339 `startsAt` and `endsAt`; it does not derive clear from `status`.

The receiver must include OSCAR backend commit `2b1ee32bd` (packaged W16 qualification checkpoint `670fc8f0c`) or the equivalent strict profile precondition and acknowledgment contract. CorrTest sends the fixed quoted hash in `If-Match`. Only HTTP 200 `{"status":"ok"}` with exactly matching `X-Oscar-Profile-Revision` is recorded as forwarded. A profile edit returns 412 before native publication. `acl_denied`, authentication failure, disabled/unavailable profile, validation failure and downstream errors do not count as forwarded. An old receiver may ignore the precondition and forward anyway: 200 without the matching acknowledgment is UNKNOWN, with no automatic retry or clear.

Configure the receiver's native destination to its approved Native Alertmanager endpoint, such as `/api/v2/alerts` or `/ext/alertmanager/api/v2/alerts`, with the existing receiver transport gate. Do **not** point it at OSCAR's ordinary-array front ingress `/api/v1/webhookreceiver/native/api/v2/alerts`: that route intentionally rejects `oscar_*` labels, including CorrTest identity. Do not remove the identity labels or relax that guard. The external receiver address/port is an explicit operator binding, not inferred from the public API target.

```sh
oscar-corrtest target add --name ordinary-lab \
  --url https://oscar.example --credential-env OSCAR_API_KEY \
  --receiver-url https://receiver.example/api/v1/webhookreceiver/webhook/corrtest_ordinary_v1 \
  --receiver-credential-env CORRTEST_RECEIVER_KEY \
  --receiver-validity-seconds 600

oscar-corrtest plan scenarios/reliability/flood-ordinary-webhook.yaml \
  --target <target-id> --pipeline-mode phase_b_dispatch
oscar-corrtest run scenarios/reliability/flood-ordinary-webhook.yaml \
  --target <target-id> --pipeline-mode phase_b_dispatch
```

Choose the pipeline mode that actually matches the lab; these commands do not activate enhanced dispatch or change native routing. The Targets UI offers the same optional receiver binding. Credential values are never saved in target metadata or request evidence; environment, mounted-file and systemd references are supported independently for each endpoint, with separate TLS/CA settings. URLs reject embedded userinfo, query credentials and fragments. The receiver profile name/hash cannot be overridden.

Firing validity is explicitly 60–3600 seconds, with no default. It must cover the entire scenario `maxDuration` plus 60 seconds for the ordinary probe and 30 seconds for cleanup; the example's 600 seconds covers the 4-minute fixture. Native grouping, routing, inhibition and callback delay must permit each case's evidence within its 60-second observation. CorrTest does not edit those controls. No automatic validity renewal occurs. Automatic expiry is an ordinary transport lifetime, not proof of physical recovery.

## What happens during a run

1. Validate the route, budget and existing public rule contract before sending input. The ordinary compiled plan is saved before probing so interrupted runs retain route identity. The durable ordinary label probe records its exact request bytes/hash, endpoint and profile precondition in SQLite before sending. Standalone `doctor` remains a direct API check and cannot establish ordinary receiver readiness.
2. Observe the probe's exact labels, fingerprint and millisecond episode start in History. History promotes severity to its top-level field; CorrTest retains that value separately and requires it to agree with the original severity (and with a severity label if both are present). Missing or changed severity cannot authorize a clear. If forwarding or read-back is uncertain, stop and retain DIRTY cleanup responsibility. If certain, record and send an explicit probe clear through the same pinned receiver profile. This confirms clear forwarding only; there is no separate wait for a resolved callback in this slice.
3. Install the normal temporary Flood rules and send five named firing inputs in P01, then four matching inputs in N01. Each POST is recorded before send. The positive case requires a `parent_emitted` audit and five exact firing History episodes; the negative case requires no trigger over its full observation interval and four exact firing episodes. A below-threshold negative is not no-match evidence.
4. Delete owned rules and clean up sources. Native source cleanup uses the frozen original labels and start, plus explicit current `endsAt`, through the same receiver. It requires known forwarding and exact current History identity. It never adds downstream fingerprint labels to native identity. Synthetic cleanup continues through the public Alert Engine path. Refire/repeated named inputs and explicit recovery stimuli are refused for this initial ordinary fixture.

All ordinary probe, source and clear requests refuse redirects and have no automatic POST retry. Missing or wrong acknowledgment, lost response, absent/ambiguous History or failed clear remains DIRTY. Restart cannot reconstruct the in-memory forwarding certainty from request evidence: interrupted possible source work remains UNKNOWN and rule-only cleanup cannot discharge it. `CLEAN` means known-forwarded source clear requests plus the existing owned cleanup result, **not** resolved callbacks, parent recovery, notification delivery or safe physical clear.

## Qualification boundary

Local tests use the actual HTTP client, compiler, runner, SQLite recording and separate receiver/public test servers. The test server translates the plain profile body into an independent callback/correlation model. It does not implement or qualify real Native Alertmanager grouping, coalesced callbacks, broker delivery, Redis/Lua or MySQL. Five unique native label identities must still emerge as five downstream evaluated alerts; live grouping and whole-callback continuation require backend/lab evidence independently.

Canonical reports, CLI summaries, UI and API previews identify the ordinary route. `ordinaryForwarding=PINNED_PROFILE_HTTP_ONLY` describes the checked transport contract, not proof that every attempted POST succeeded; individual attempt results and cleanup state remain authoritative. Native grouping/callback coverage remains `ORDINARY_ROUTE_NOT_INDEPENDENTLY_VERIFIED`, receiver selected admission `NOT_TESTED`, physical clear/lifecycle `NOT_PROVEN`. No source epoch/order, acceptance receipt, producer completeness, selected row isolation, notification delivery or UAT acceptance is added. Absence/collector-health remains unsupported and ticket-provider tests remain excluded.

Request evidence persists in existing SQLite RunEvents and exports with the timeline; existing bundle-size limits still apply. On export-limit failure the original evidence remains in SQLite. See the [qualification record](qualification/20261013/ordinary-webhook/README.md) for failures, passes and review limits.

The fixed profile is a repeatable lab stimulus contract, not a prerequisite for useful production ordinary correlation. Existing Splunk or migrated middleware senders need not change payloads to use ordinary window correlation. Their capabilities must be assessed independently; this lab profile grants them no exact recovery authority.
