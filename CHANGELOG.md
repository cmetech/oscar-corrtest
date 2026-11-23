# Changelog

## Unreleased

- CorrTest reconciles managed manual and bulk resends from the original
  callback child receipts. A confirmed sink notification and a definitively
  skipped ALERTHISTORY ticket can qualify together. Missing or ambiguous
  children remain owned during cleanup. A missing operator session leaves
  signed resend recovery pending while other original receipts can settle.
  Operators need MySQL 8+ for the bounded receipt projection. The existing
  read-only grants cover it. No migration or configuration change is required.

- CorrTest managed qualification now checks the original source variant,
  controlled receiver, pinned policy revision, dispatch interval and physical
  provider receipt for timed actions. The run-owned silence and inhibitor
  companions and post-acknowledgement repeat use the same commissioned path.
  The dedicated and mixed V3 smoke plans reserve 20 minutes of load. Legacy
  scenarios and both 150-minute qualification windows retain their durations.
- Managed evidence exports an allowlisted History and label projection.
  Cleanup retains unresolved original requests and provider targets across
  reruns. Operators need the additional read-only workflow, actor audit and
  policy revision grants, scoped native silence access for the full configured
  campaign, a commissioned inhibitor rule and a functioning test PDI. See
  [Managed source campaigns](docs/managed-native-campaign.md). No database
  migration is required. The new scenarios retain their execution guards.

- Native campaign plans now drive isolated TL1 device traffic through a real
  probe and named receiver. The managed smoke uses native source observations,
  signed operator actions and independent provider receipts. The mixed plan
  adds background device traffic and a repeat-only and escalating episode for
  every ten-minute segment while retaining the ordinary workload.
- Operators running managed campaigns need a commissioned test Connection,
  a protected test operator session and read-only MySQL receipt access.
  See [Managed source campaigns](docs/managed-native-campaign.md) for the
  config fields, exact permissions and source-owned cleanup. The new scenarios
  remain gated until the integrated scorecard is complete. Existing campaign
  selectors and database schemas require no migration.

- CorrTest registers frozen 150-minute ordinary control and configured-policy
  plans, a mixed smoke, and a dedicated managed TL1 smoke. The new plans
  await native ingress and evidence support. Existing scenario hashes stay
  frozen.
- Campaign comparison now requires explicit reference IDs, a comparison mode,
  and a common scenario hash. Full qualification runs record build and
  workload configuration provenance in campaign status and evidence bundles.
- Existing comparison scripts must supply `--reference-run`, `--mode` and
  `--scenario-hash`. Older runs without the required recorded evidence are
  ineligible. The change uses the existing campaign event store and requires
  no database migration.
