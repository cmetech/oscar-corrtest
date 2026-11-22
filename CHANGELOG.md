# Changelog

## Unreleased

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
