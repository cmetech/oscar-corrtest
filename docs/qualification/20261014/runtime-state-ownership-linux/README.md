# Linux runtime ownership qualification — 14 October 2026

Base `37d3c2c7579105d564929009256d4c962d06bea7`, CorrTest reliability feature
worktree. This completes the Linux ARM64 execution portion left open by the
[original ownership qualification](../runtime-state-ownership/README.md).
Product code, test code, dependencies and prepared CLI binaries are unchanged.

## Result

The unchanged tests passed on Linux ARM64 (OrbStack Linux kernel
`7.2.9-orbstack-00391-g2b298d05fbd4`) with state on container-local tmpfs:

- Seven runtime test entries passed: live-owner refusal, release/isolation,
  failed initialization, nonregular lock refusal, diagnostic ownership,
  subprocess death/recovery and the subprocess helper.
- One integration test and its two subtests passed: `runs show` and `runs list`
  refuse while the original runtime retains UI access and unchanged run state.

There are **10 passing test/subtest entries**, no failures or skips, across two
executables. The helper returns without work during parent enumeration; the
process-death test executes its real helper subprocess. These are focused tests,
not another full-suite run or a Linux race-detector qualification.

The death test starts a separate process owning an OBSERVING run, proves a
competing process is refused, kills and joins the owner, then opens a replacement.
It observes INTERRUPTED/UNKNOWN responsibility, with no automatic input resume.
No live OSCAR endpoint, source injection, native clear or correlation dispatch is
used. The integration test uses an in-process HTTP recorder, not a live listener.

## Failures retained

The first Linux execution passed all tests. Its proof runner subsequently failed
an absence assertion because it expected uppercase `No such`, while Docker
returned lowercase `error: no such object`. The successful removal log and a
separate inspect confirmed that container was absent. This was a qualification
script defect, not a runtime ownership failure.

A second attempt failed before creating resources: the narrowed image-inspect
Go template accessed an absent `Volumes` map key. The runner now reads that
optional key with `index`. Both tooling failures and the initial passing test
output are archived; no product or test fix was needed. The final run exited zero
and confirmed complete cleanup. Earlier implementation RED evidence remains in
the original ownership archive; no artificial failing product test was added.

## Reproduction and resource boundary

Run from the CorrTest feature worktree:

```sh
python3 docs/qualification/20261014/runtime-state-ownership-linux/run-proof.py
```

The runner uses the already cached Go 1.27.0 compiler, with dependency network
access disabled, to cross-compile static Linux ARM64 runtime/integration test
executables. It uses existing image
`sha256:9ea93ed22de014e1d4c39321d95fa514b038323631bb91fc5dbce53eeff0291d`
solely as a Linux execution environment. It overrides its entrypoint; no OSCAR
application process starts. The container runs as UID/GID 65534 with network
`none`, a read-only root, dropped capabilities, no-new-privileges, resource limits,
read-only test binaries and private `/tmp` tmpfs. No image build/pull, named
network or volume is needed.

The final run verified its ownership label before removing its single container,
confirmed absence, and removed both temporary binaries. The failed first run's
container was also removed. The inspection-only failure created no container.
All 171 tracked Go source/test/module file hashes matched before and after each
attempt. Full hashes, compiler commands, binary hashes, image identity and final
cleanup are retained in `final-manifest.json.gz`.

Every archive uses gzip timestamp zero. `evidence.json` records source/archive
hashes; `SHA256SUMS` covers this directory except itself. Unfiltered initial image
and container inspection logs remain private because they contain irrelevant
image environment metadata. Their hashes and a minimal derived cleanup record
are retained; final inspection logs contain only required fields.

## Limits

This qualifies Linux ARM64 kernel file locks and subprocess lifecycle on this
Docker VM with tmpfs. Windows and Linux AMD64 execution, persistent-disk power
loss, network filesystems, filesystem replacement and interrupted source-cleanup
replay remain unqualified. Killing a process is not a power-loss test. Older
binaries that ignore the lock remain unsupported concurrent owners.

No scoped parent/History runtime is commissioned by this test. No sustained
capacity, provider delivery, production recovery or correlation E2E acceptance is
claimed. **0/39 UAT cases accepted.**
