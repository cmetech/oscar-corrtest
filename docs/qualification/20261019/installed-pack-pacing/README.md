# Fixed installed-pack input pacing — 2026-10-19

The installed fifteen-input Flood correctness runner previously sent its complete input sequence as fast as HTTP acknowledgments arrived. A live UAT attempt encountered the unchanged ingress burst limit after ten inputs. That original failed run remains immutable and is not retried by this change.

The smallest runtime change adds a cancellation-aware one-second wait before inputs 1–14, after the previous exact-revision acknowledgment has been durably recorded. Input 0 is immediate. The existing sender still checks its original sixty-second injection deadline and cancellation before recording or sending another attempt. Uncertain responses still stop immediately with no retry, and forwarding evidence retains its existing UNKNOWN/DIRTY semantics. No rate-limit settings, target APIs, configuration writes, pack rules, pack hashes, or retry policies changed. This is correctness pacing, not a load test or throughput claim.

Focused tests exercise the real installed runner with its HTTP peer, SQLite/artifact stores, and controlled clock/sleep seam:

- fourteen waits, first input immediate, at least one second between sends;
- cancellation during pacing records/sends no next input;
- sixty-second original deadline remains authoritative after a long wait;
- a 502 on the second paced send stops without retry and retains UNKNOWN responsibility.

The existing delayed final-ack fixture now delays 41 seconds after fourteen paced seconds, retaining the original 55-second final acknowledgment and full post-ack observation assertion. The wall-clock jump remains in the first observation sleep, where its original monotonic-window assertion belongs.

## Evidence

- `red.log`: all four new focused tests failed before the implementation.
- `green.log`: all four focused tests passed after the six-line runner change (6.523s).
- `regression.log`: installed-mode tests passed across six affected packages; runner 75.755s, runtime 1.835s, command 2.206s, OSCAR adapters 2.496s, evidence 2.402s, SQLite persistence 5.342s.
- `race.log`: all four focused tests passed under Go's race detector (35.595s).
- Independent read-only review by the source-contract agent found no actionable issue. It checked cancellation, original injection deadline, no-retry behavior, final-ack monotonic/durable timing, and the updated fault fixtures.

Commands, run from the feature CorrTest checkout with Go 1.27.0 darwin/arm64:

```sh
go test ./internal/runner -run 'TestInstalledRunner(PacesFixed|CancellationDuringPacing|PacingKeeps|PacedAmbiguous)' -count=1
go test ./internal/runner ./internal/runtime ./internal/command ./internal/oscar ./internal/evidence ./internal/persistence/sqlite -run Installed -count=1
go test -race ./internal/runner -run 'TestInstalledRunner(PacesFixed|CancellationDuringPacing|PacingKeeps|PacedAmbiguous)' -count=1
```

No live sends or containers were used in this implementation lane. A fresh separately authorized UAT run is needed to verify pacing through the deployed proxy; the previous failed run cannot be converted into a pass. The installed pack remains installed.
