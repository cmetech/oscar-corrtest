# Installed correlation test pack

Use this mode when the OSCAR test server owns an installed test pack and CorrTest runs on another machine. CorrTest verifies the original installation and exercises its installed rule. It does not create a substitute rule or remove the installed configuration afterward.

The first pack covers initial Flood matching: five inputs that should reach the threshold, four that should remain below it, and two independent groups of three that must not combine. It retains source identity, History and correlation audit evidence. An audit decision is not proof that a parent notification reached a destination. Other patterns, clear/refire, outage recovery and production capacity require their own qualification.

## Prepare the OSCAR test server

Use a dedicated, independently owned test estate. Follow the [pack build and installer instructions](../test-packs/correlation-operational-v1/README.md). The release contains the normal project pack, receiver Mapping, correlation-rule import, fixed scenario and installer. Install it through the normal OSCAR CLI and authenticated APIs, preserving the original private installation directory and ownership declaration.

The estate also needs its normal application services, supported schema, verified TLS, API credentials/permissions and controlled notification destinations. These are deployment prerequisites; the pack cannot create evidence that they exist. Keep enhanced admission and correlation dispatch disabled for this initial audit scope. Ordinary source alerts may still notify, so require an enabled History destination and keep every other destination confined to the lab.

Verify that `ALERTHISTORY` (URL `alerthistory://`) is enabled and its Taskmanager task and worker are ready. Middleware normally creates it during background startup, but a running service or an empty History query does not prove that setup succeeded. With no enabled History destination, ordinary inputs can be accepted without producing the History rows this scenario requires. Keep `ALERTHISTORY_TICKETING_ENABLED=false` and ticketing absent from the destination URL. Retain the destination/task readbacks before the test; a controlled email or webhook sink cannot replace History evidence.

Copy the release directory, completed original installation directory (including its copied `kit` and full journal), and original ownership declaration to the CorrTest machine. Obtain the manifest SHA256 through the trusted release channel. Do not replace missing receipts or compute a new trusted pin from an unknown artifact. Keep configuration, rules and processes under exclusive test ownership through observation and export.

## Configure CorrTest

Create two stored targets using credential references and the trusted CA. Replace the paths and hostname below. The API key for the receiver must have its actual webhook permission.

```sh
oscar-corrtest target add --name qualification-api \
  --url https://oscar-test.example/ext/mw \
  --credential-file /secure/oscar-api-key --ca-file /secure/lab-ca.pem

oscar-corrtest target add --name qualification-receiver \
  --url https://oscar-test.example/webhookreceiver \
  --credential-file /secure/oscar-receiver-key --ca-file /secure/lab-ca.pem
```

Retain the returned target IDs. This mode uses the receiver target's base URL; do not use the older `--receiver-url` ordinary-profile option. The API base must exactly match the original installation, including `/ext/mw` when present. No insecure TLS, internal-header override or fallback URL is supported.

Validate the copied installation offline:

```sh
oscar-corrtest test-pack plan \
  --manifest /qualification/kit/manifest.json \
  --manifest-sha256 TRUSTED_MANIFEST_SHA256 \
  --installation /qualification/original-installation \
  --ownership /qualification/ownership.json \
  --api-base https://oscar-test.example/ext/mw \
  --target API_TARGET_ID --receiver-target RECEIVER_TARGET_ID
```

`plan` reads the explicitly supplied artifacts. It does not open configuration, credentials or Runtime, contact OSCAR, or establish current server readiness. To execute, use the same arguments with `test-pack run`. The runner performs live checks before sending its fifteen fixed inputs. The first input is immediate; each later input waits at least one second after the previous confirmed acknowledgment, keeping this correctness check below ordinary ingress burst limits. All inputs still share the original 60-second injection deadline. Cancellation while waiting stops before another attempt is recorded. It records each attempt before transmission and stops on an uncertain outcome without resending.

## Read the result and preserve the evidence

The audit verdict and remaining source responsibility are separate. A clean audit PASS can still have `DIRTY` responsibility because the test sent firing alerts and does not issue physical clears. `UNKNOWN` means a send or result could not be conclusively accounted for. Both produce CLI exit code4. Do not turn that exit code into a retry loop.

Keep the returned run ID and inspect it with `oscar-corrtest runs show RUN_ID`. The installed export integration preserves original artifacts, durable attempts and outcomes, observed identities/audits, timing and configuration readbacks. Interrupted runs retain their actual missing report or pending evidence; verification does not invent a completed test.

```sh
oscar-corrtest export RUN_ID --output /qualification/evidence/RUN_ID.zip
oscar-corrtest verify-bundle /qualification/evidence/RUN_ID.zip
```

Successful bundle verification means the retained evidence is internally consistent. Read the recorded verdict, coverage and cleanup status; it is not proof of target ownership, delivery, recovery or production capacity. Original Active/audit rows are retained, but their full HTTP pagination envelopes are not. History retains complete response pages.

Generic cleanup refuses this mode. The independent test-estate owner must reconcile or tear down the exact owned target and retain that outcome. Do not delete installed rules or send speculative clears to make a test appear clean.

Local component tests and isolated Linux execution are available. Genuine authenticated installation, separate-host execution, repeated-run behavior and full UAT remain qualification gates. Residual same-alertname History inside the queried interval can prevent the initial clean-case result. The current pack is not an acceptance result for all correlation patterns.
