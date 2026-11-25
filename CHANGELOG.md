# Changelog

## Unreleased

- New `alerting-day-3ps@v1` (comparable) and `alerting-day-3ps-smoke@v1` (never comparable), OSC-184:
  `alerting-day@v4` and its smoke at a total offered firing rate of 3/s, and 6/s in 1-minute spikes
  (main: T+45 and T+105; smoke: T+4 and T+7). The main run keeps v4's shares: correlation 0.6/s
  (1.2/s in a spike), R1-R5 cohorts of 8 (0.31/s), fingerprint rounds of 9 a minute (0.15/s), every
  12th worker alert ticketed; the worker lane is 1.94/s (4.34/s in a spike). The smoke keeps smoke
  v4's controller lanes; its worker lane is 2.66/s (5.46/s). Phases, task lane, reports, homelab gate,
  pre-checks, limits and seed are v4's. Scenarios gain `offeredRate`/`offeredSpikeRate` (zero means
  v4's 5/s and 10/s), used to size the worker lane and by the per-minute offered-rate check; v4 and
  every older hash are unchanged. `campaign compare` refuses a 3ps run against v4 runs. Correlation
  offers vary by about one document per minute (36 -8/+7 at 0.6/s), so a 3ps minute can fall just
  outside the 5% offered-rate tolerance (INCONCLUSIVE, never FAIL). Live runs UNRUN.

- `alerting-day-smoke@v4` allows 8 correlation documents in flight (was 2), so its correlation share
  (0.2/s, 0.4/s in a spike) holds when OSCAR is slow and runs take up to 90 s, or 240 s for a
  ticketed run. Its second 1-minute spike moves from T+8 to T+7: cycling documents stop 4 minutes
  before the drain (T+8), so a T+8 spike never received its correlation share. Totals stay 5/s and
  10/s; the worker lane is unchanged (4.66/s, 9.46/s in a spike). The smoke hash changes (never
  comparable); `alerting-day@v4` is unchanged. Read the smoke correlation rate over T+1 to T+8.

- `campaign status` without an ID works however many campaigns exist: `GET /api/v1/campaign/runs`
  returns summaries (every campaign field except `Scorecard` and `Report`) 20 at a time, newest first,
  paged with `limit` (1-100) and `offset`; the CLI gains `--limit` and `--offset` and prints ID,
  scenario, status, verdict and start. Callers that read scorecards from the list must fetch
  `GET /api/v1/campaign/runs/{id}`; unparameterised list callers now get 20 runs, not 100. A
  campaign response over 16 MiB now fails with "response exceeds 16 MiB" instead of a decode error.
  Upgrade the server before relying on the new CLI.

- New `alerting-day@v4` (comparable) and `alerting-day-smoke@v4` (about 15 minutes, never
  comparable): the single-node mixed-load gate. Every lane runs at once for 150 minutes at a
  total offered firing rate of 5/s over all lanes (10/s in 5-minute spikes at T+45 and T+105),
  then a 30-minute drain. Correlation cycles set A and set B, up to 64 documents in flight paced to 1 alert/s (2/s in
  a spike, 20%), and tickets the synthetic parents of flood and threshold (campaign
  correlation runs no longer hold the runtime's run lock; each run's names carry its token); the fingerprint lane runs a verified round every minute under one
  scoped policy (15 posts a minute, paced); R1-R4 plus an R5 escalation cohort (critical after 4 minutes, a new episode
  every 15 minutes, posted through native Alertmanager because escalation acts on native
  episodes) are scored, the latter per episode from sink receipts; a homelab task runs
  through the task API every minute (v4 T+1 to T+145, smoke T+1 to T+7; every 5th request 3 runs at once, each with its own `Idempotency-Key`; gated on success and p95) and the homelab pack's scheduled jobs FAIL the run if their failure rate during load rises more than 5 points over the 30 minutes before T0 or a job stops (OSC-156); alert reports at T+60 and
  T+120 are read back from object storage and gated on History p95 while they run. v4
  paces cohort and fingerprint posts so no minute exceeds the budget, reports offered posts per
  minute by lane and is INCONCLUSIVE when a minute exceeds it by more than 5%. New blocking
  v4 pre-checks: B12 (RESEND_SCHEDULE_ENABLED), B13 (alert report approved), B14 (fingerprint
  policies commissioned) and B15 (one run of the homelab task succeeds as fo-01). `--skip-lane report` switches the report
  lane off for one run: B13 reads skipped, the scorecard marks the lane "skipped (by option)" and
  the run is not comparable. The worker must run the same
  build (BaselineSpec gained `bursts`). Older scenario hashes are unchanged. Live runs UNRUN.

- Source `39fae89` removes a duplicate standard-library import from the managed escalation smoke. Build and behavioral qualification remain UNRUN.

- Source `21796ca` checks normal routing scope for escalations and supports a commissioned
  Major-A/Critical-B case inside `managed-alerts-smoke@v1`. Set `ManagedTL1.CrossRouteReceiver`
  after installing both controlled routes. Standard smoke records the case as uncommissioned.
  Rebuild CorrTest and retain the distinct case evidence after the combined platform patch.
  Functional and load qualification are UNRUN.

- CorrTest reads the original receiver ID from the signed History write
  binding when qualifying managed rate and sustained deliveries. This lets
  a coalesced notification match its original source variant and receiver.
  Source `c4c82f0` requires no schema migration. Rebuild CorrTest before
  running the managed mixed qualification scenarios.

- CorrTest reconciles ordinary native delivery to per-route decisions, actual
  outbound attempts and each source episode, including attempts that produced
  no sink receipt. Mixed smoke needs the ordinary route/attempt projection.
  Full control and mixed qualification additionally need per-attempt SQL/HTTP
  spans. Managed-only smoke remains usable independently. Regenerate older
  companion exports to the contract in the OSCAR qualification guide before
  comparing new runs. Source \`5af1a89\` adds per-offer load coverage, original
  managed child and final-drain checks, oracle-based timer percentiles and
  separate source, physical-request and member counts. No database migration.

- CorrTest enables the dedicated TL1 managed smoke, mixed V3 smoke, same-work
  ordinary control and 150-minute mixed V3 qualification selectors. Mixed runs
  accept `--managed-share` as a fraction and compare the persisted effective
  scenario. The score records separate source, episode, physical receipt and
  provider evidence, per-segment closure, queue drain and cost observations.
  Full comparable runs need a commissioned independent per-run trace in the
  optional `NativeObserverDir`. Missing or sampled evidence prevents full
  qualification. Comparison reports `Items[].RunID` and exits 4 on a measured
  latency regression or failing candidate. No database migration is required.

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
