# oscar-corrtest — OSCAR correlation test harness (locally patched)

START HERE: HANDOFF-20260901-correlation-fixes.md — campaign result, deployed fixes,
open design work, evidence map, and how to re-verify.

- bin/oscar-corrtest is built from THIS repo's patched source (contract adapters for this
  OSCAR build); upstream v1.0.2 binary does NOT work here. Build:
  podman run --rm -v "$PWD":/src:z -w /src -e CGO_ENABLED=0 \
    docker.io/library/golang:1.27.3-bookworm go build -o /src/bin/oscar-corrtest ./cmd/oscar-corrtest
- Testing protocol + hard-won pitfalls: .claude/skills/oscar-correlation-test/SKILL.md
- Resumable campaign loop: .claude/skills/oscar-correlation-campaign/SKILL.md + CAMPAIGN-STATE.md
- NEVER flip CORRELATOR_DISPATCH_ENABLED (Phase B blocked — see handoff).
- Rules of engagement: sequential runs only; correlation_rules must be 0 after every test
  block; assert on CorrelationAudit via the am_fingerprint LABEL.
