# Actual producer journal, controlled boundary fixture

`records/` was emitted by the committed `test-packs/install_operational.py` at CorrTest `644984d1742ebf6f4e0d1682c9f866961088e924`, using the genuine existing `../kit/` distribution and the existing installer test `Peer` / project-command control. No target or command was invoked. All 42 original JSON files are unmodified producer bytes: attempt, completion and 20 before/after pairs. The production installer also copied all ten genuine kit files into its private `kit/`; Go tests reconstruct that exact subtree from `../kit/` without duplicating it here.

`provenance.json` records the exact three Python producer/test source hashes, manifest hash, each retained file size/hash, and the successful result of the real Python `verify_install` over this capture. That verification used the same controlled HTTP boundary and only GETs. This is cross-language host contract evidence, not installed or deployed proof. UUID4 identifiers are actual generated values, so regeneration creates new capture hashes rather than reproducible IDs.

`generate.py` runs the actual installer and verification. Its only boundary substitutions are the existing controlled project installer and HTTP peer, plus no-op sleep between the two bounded queued/running polls. The peer deliberately emits indented wire JSON: recorded `body_sha256` describes those unretained bytes, while the journal retains only the parsed body. Go must not mistake canonical parsed JSON for the original raw response. The credential is test-only and checked absent from every retained file. Regenerating requires the Python installer's existing host dependencies; ordinary Go tests do not execute Python.

Regeneration from the CorrTest root:

```
python3.13 internal/testpack/testdata/installation/generate.py
```

The baseline capture uses the original root API base. Prefix support tests separately bind controlled copies to exact `/ext/mw`; they do not relabel this capture as public-proxy UAT. The read-only loader requires private 0700 directories and 0600 files; Go tests copy fixtures to those modes because Git does not retain private permission bits.
