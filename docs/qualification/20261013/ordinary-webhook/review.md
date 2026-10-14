# Fresh whole-cut review

Reviewer: `ordinary_webhook_review`, fresh context (`fork_turns=none`), GPT-6 Astra/high. Read-only review of current ordinary-webhook product/tests/docs; no live activity or independent suite rerun.

1. P2, `internal/oscar/ordinary_webhook.go`, `ResolveHistoryRecorded`: a downstream class changed to synthetic bypassed retained source labels/start checks and sent direct cleanup. Source observations could remain in cleanup despite an inconclusive exact-episode assertion. Retained uncertainty kept DIRTY but did not prevent the wrong POST. Fix: lookup retained source before allowing synthetic fallback; changed source labels fail before any clear.
2. P2, `internal/runtime/runtime.go`, ordinary preflight: the request event was durable before POST, but plan persistence occurred later in Runner.Execute. An interrupted probe recovered UNKNOWN yet lacked route metadata and displayed legacy direct coverage. Fix: save the ordinary compiled preflight plan on the existing queued run before probing. This bounded metadata write initializes no case/attempt rows; the runner retains that responsibility. Existing direct callers are unchanged.

`review-red.log.gz` reproduces both: changed source class was resolved through the wrong API; probe handlers observed absent route metadata and reopen reported alert-engine instead of ordinary-webhook. `review-green.log.gz` passes the same checks and adjacent client/runtime/SQLite suites. Reopen retains ordinary route and UNKNOWN source responsibility even when the in-memory probe had forwarded its clear; durable request evidence alone does not recreate certainty.

No other actionable finding reported. Live native grouping, callback delivery and selected admission remain unverified. Final suite is retained separately; no second review cycle or expanded feature scope.
