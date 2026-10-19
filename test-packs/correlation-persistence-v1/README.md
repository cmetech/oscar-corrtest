# Persistence showcase schedule v1

This directory currently contains a fixed offline schedule contract. It is **not an installable pack, executable scenario, source capability, or UAT pass**. The existing `correlation-operational-v1` Flood kit remains unchanged and retains its original inventory and acceptance boundary.

`scenarios/persistence-showcase.json` is the canonical output of `testpack.PersistenceContractJSON()`. `ValidatePersistenceContract` requires exactly the qualified contract, rejecting changed timings/counts, added authority fields, duplicate JSON keys and unknown cases. `PersistenceCases()` returns detached values. A repository test binds the shipped JSON bytes to that implementation.

The fourteen cases are:

- N30: count reaches three before five minutes; count reaches three at seven minutes after the age gate; one-hour unresolved waits with one and two occurrences; verified recovery before the age gate; later observations carrying unchanged count one.
- N14/N18/N28: 300/600/60/120 seconds respectively, each with original severity 4→Major and 5→Critical, preserving the Minor source.

Positive cases require accepted parent History **and** membership evidence before the future runner can submit recovery. Recovery is deliberately not placed on an unconditional timestamp. Observation budgets are finite test deadlines, not a qualified production scheduler guarantee. Both monotonic and wall-clock elapsed time must be checked by the future executor. The unchanged-count case represents that one P05 path only; it does not simulate transport retry, native notification repeat and manual resend by relabeling one operation.

All runs need an isolated source-alarm identity and one explicitly selected policy. This contract does not choose cross-rule precedence, reproduce every Netcool eligibility predicate, replace triggers, or establish production occurrence/original-severity provenance. The full P01–P22 matrix remains in `docs/uat/persistence-first-production.md`.

## Required before execution

1. Protected producer enrollment and durable episode/start/order/count identity, scoped readiness pinned to the source, selector and rule revisions; ordinary no-match uses the ordinary route.
2. Separately validated disabled rule/classifier inventory with actual label names and source-alarm grouping; existing Flood inventory accounted for without weakening its loader.
3. Durable pre-send records and ambiguous-outcome handling using the existing CorrTest journal/artifact path.
4. Authenticated complete source/parent History and Active readbacks, retained episode/incident membership, rule revision, severity mapping, parent create/clear actions and acceptance receipts. An audit decision alone cannot satisfy the parent barrier.
5. A runner and export verifier that bind all original observations, preserve failures and reject incomplete evidence. No execution adapter is added in this slice.

No production policies, capability dictionaries, deployed pack, admission switches or live alerts are changed by this artifact.
