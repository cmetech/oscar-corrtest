# Changelog

## Unreleased

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
