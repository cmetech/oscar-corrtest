# Ordinary showcase v2 qualification evidence

Implementation branch: `feat/correlation-reliability-20260919`, CorrTest only. No live deployment, source traffic, container creation, main-branch changes or capability bypass occurred in this workstream.

Logs are stored as lossless `.log.gz` copies to preserve raw output, including pytest whitespace. `log-sha256.json` records both raw and compressed hashes; `gzip -dc <file>.log.gz` reads a log.

Final checks:

- **165 Python tests passed** across the existing v1 pack builder/installer and additive v2 pack/harness. Command: `PYTHONDONTWRITEBYTECODE=1 ../main-merge-20261017/host-venv/bin/python -m pytest -q -p no:cacheprovider test-packs/test_operational_pack.py test-packs/test_install_operational.py test-packs/test_showcase_pack.py test-packs/test_run_showcase.py`. See `showcase-python-final.log`.
- **Full Go suite passed**: `GOPROXY=off GOTOOLCHAIN=go1.27.0 go test -p 4 ./... -count=1`. See `showcase-go-regression.log`. Existing v1 Go contracts were preserved.
- Focused final Python subset: **43 passed** (9 pack cases and 34 harness cases), including actual backend FailOpenState construction, failed receiver responses, cancellation, source/parent attribution, elapsed-time checks and independent original-transcript replay. See `showcase-real-dispatch-green.log`.
- The pack builder used actual backend rule validation, receiver profile compilation and project-pack archive validation. HTTP scenario boundaries and clocks were simulated; these results are not deployed UAT or production capacity evidence.

The tests first exposed missing implementation. Subsequent adversarial review reproduced substantive defects: summary-only evidence substitution, canonical parent fingerprint mismatch, early Persistence emission, wrong retained Flood rule selection, parent-registration double counting, failure-response export rejection and an impossible ordinary dispatch gate. Their RED logs and subsequent GREEN logs are retained here. Initial intermediate failures also include test-fixture corrections (the peer referenced the fixture function instead of its path and reused the first source timestamp), and an indentation error while replacing the gate fixture. These are recorded as failures, not passed behavior or hidden test runs.

The final independent reviewer checked the actual backend synthetic-parent, source History, parent-child, Persistence timer and runtime gate contracts. The gate correction is particularly important: `authoritative=true` is ordinary correlator delivery ownership; enhanced source admission is a separate disabled condition. This workstream did not change live flags. A coordinated Alertmanager/correlator delivery cutover must be reviewed before running this dispatching showcase.

Read [the invocation and scope guide](../../../uat/installed-showcase-v2.md). Fourteen finite ordinary cases cover six synthetic-parent patterns plus real parent–child linkage, each with a positive and negative. Positive time-only Persistence is thirty seconds; its negative is unmatched input. Production occurrence-gated and mapped-severity Persistence (#30/#14/#18/#28), exact recovery, abrupt-outage continuation, moderate 90/10 load and external provider delivery remain outside this showcase's acceptance scope. None of the 39 qualification cases is fully accepted from these local tests.

Portable evidence is the whole private run directory. Its verifier checks original raw HTTP bytes, exact fixed request sequence, immutable source input bytes, runtime/profile/rule consistency, elapsed observations and semantic results. It cannot attest an untrusted capture host or detect changes between requests. A hard kill/storage failure can leave only the partial fsynced journal; no automatic retry/resume/cleanup occurs. The tool preserves successful `PASS / DIRTY` and uncertain `INCONCLUSIVE / UNKNOWN` distinctions.

Candidate artifact (outside this repository): `../e2e-readiness-20261020/corrtest/kit-v2-ready`, manifest SHA-256 `bcd2c03a97507e8c896797c8acc5675f6c2211f79ba57a0ea871924467c03cef`. The earlier `kit-v2` is explicitly superseded intermediate evidence and must not be deployed. Source hashes are recorded in `source-sha256.json`.
