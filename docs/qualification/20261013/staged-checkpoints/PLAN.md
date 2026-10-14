# Bounded staged checkpoints

- [x] Strict optional case checkpoints: unique name, increasing afterEvent ordinal, positive bounded window, existing assertions. Reserve the window before the next scheduled input.
- [x] Observe only the accepted input prefix, for the full checkpoint interval; require all checkpoint assertions to be supported and scored. Stop subsequent input on failure/incomplete evidence.
- [x] Retain checkpoint results and interval evidence through normal completion and later failures; reuse owned cleanup. Expose coverage in preview, canonical report and visible timeline.
- [x] Focused counterexamples plus full Go suite; retain RED/GREEN evidence and local feature commit.

No receiver transport, barrier API, physical-clear or ordinary no-match isolation claims. Existing scenarios without checkpoints retain behavior. Every mutation uses the absolute feature worktree.
