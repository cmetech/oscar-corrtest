# Finite Threshold Redis oracle

Approved scope: test-only CorrTest HTTP runner qualification using the actual current backend Lua script and a root-owned Redis/redis-cli lane. No backend edits, dependency, provisioning, production activation or new public scenario contract.

- [x] Reproduce missing oracle use with host contract tests; default multi-value refusal remains.
- [x] Add an optional test-only Threshold oracle and scoped Redis CLI adapter. Explicit script path/hash and JSON command argv are mandatory; no shell, fallback, provisioning or global deletion.
- [x] Prepare first-trigger, duplicate value, separate group and fixed-bucket boundary; malformed/unavailable evidence stops the runner. Local History/recovery remains modeled.
- [x] Run focused/full local checks, then root-owned actual Redis qualification separately. Host skip is not qualification.

Root owns resources, final review and commits. Worker owns only CorrTest test fixtures and this cut's documentation. No UAT, deployed E2E, capacity, cooldown/rearm or recovery-authority acceptance is implied.

Host qualification: 35 focused tests/subtests and 927 full-suite tests/subtests passed, with one explicit actual-Redis skip. Root review and actual isolated Redis qualification passed; source parity and exact key/container cleanup verified. Evidence: [record](../../qualification/20261015/threshold-redis/README.md).
