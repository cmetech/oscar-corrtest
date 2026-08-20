# Custom Scenarios and Operational Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete version 1 with strict YAML/JSON custom scenarios, retention and export operations, authenticated remote serving, hardened packaging, browser/API qualification, and all-pattern release evidence.

**Architecture:** Custom documents decode into the same closed scenario model and compiler used by built-ins. Remote serving is an explicit mode requiring either built-in bearer/session authentication or a declared authenticated reverse proxy; the loopback default remains unchanged. Retention and exports operate through shared services with exact identifiers and confirmation.

**Tech Stack:** Existing Go stack plus pinned `gopkg.in/yaml.v3`; standard-library ZIP, HTTP auth, templates, and OCI packaging files.

**Spec:** `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`

## Global Constraints

- Strict unknown-field rejection, bounded files/documents/repeats/durations, no expressions, shell interpolation, HTML, JavaScript, or inline credentials.
- Remote bind is impossible without `--remote-mode bearer|trusted-proxy`; bearer secrets stay reference-only; trusted proxy requires explicit header/name configuration and direct-request rejection.
- Delete/retention never removes cleanup-dirty runs or unverified artifacts without exact ID plus `--yes`.
- All eight patterns retain positive and negative built-ins and can execute twice without interference.

---

### Task 1: Strict custom scenario input

- [ ] Write failing decoder/compiler tests for valid YAML/JSON, duplicate keys, aliases, multiple docs, unknown fields, oversized input, reserved labels, unsafe values, budgets, canonical normalization, and equivalent YAML/JSON digests.
- [ ] Add pinned YAML dependency and implement strict decode into the closed model; persist original source and digest.
- [ ] Add `scenario validate`, `scenario import`, `plan`, and Scenarios UI preview using the shared compiler.
- [ ] Run tests and commit `feat: add strict custom scenarios`.

### Task 2: Retention, deletion, and complete evidence operations

- [ ] Write failing service/CLI/HTTP tests for exact-ID deletion, confirmation, cleanup-dirty refusal, artifact hash validation, atomic bundle creation, and database-plus-evidence backup documentation.
- [ ] Implement retention preview/apply, manual deletion, bundle verification command, and UI actions with CSRF/same-origin protection.
- [ ] Run tests and commit `feat: add safe retention and evidence operations`.

### Task 3: Authenticated remote serving

- [ ] Write failing table tests proving every wildcard/non-loopback bind is rejected without remote mode, bearer/session authentication protects all HTML/API/SSE/download routes, proxy mode rejects missing/untrusted identity headers, cookies are Secure on TLS, and secrets never enter logs/reports.
- [ ] Implement explicit remote-mode configuration, credential-reference resolution, constant-time bearer/session validation, login/logout where applicable, rate limits, secure cookies, and proxy trust policy; preserve loopback no-auth behavior.
- [ ] Run web race/security tests and commit `feat: harden authenticated remote serving`.

### Task 4: Packaging, browser/API, and release qualification

- [ ] Add OCI scratch/distroless packaging with CA certificates, systemd credential examples, JSON Schema and built-in scenario docs to release archives.
- [ ] Add browser smoke script using only a downloaded pinned browser runner in CI or Go HTTP/browser contract tests when unavailable; cover target creation, doctor, fake run, SSE, history, export, theme, keyboard/focus, and auth.
- [ ] Add `plan7-gate` and `release-gate` covering all 16 minimum cases, repeated-suite isolation, cancellation/restart cleanup, security scanners, race, standalone archive, reproducible packages, amd64/arm64 static binaries, manifest/checksum verification, and optional live OSCAR qualification.
- [ ] Run `make clean && make ci release-gate`; update README/development/operator docs; commit `test: qualify oscar corrtest version one`.

