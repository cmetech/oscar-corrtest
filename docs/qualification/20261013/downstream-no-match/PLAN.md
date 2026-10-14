# Downstream no-match control

- Opt-in Flood N01 `control: downstream-unmatched`, explicitly named single firing inputs and `downstream-no-match-count` assertions.
- Keep fixture rules unchanged; use a separate input alertname and prove exact predicate disjointness before mutation. No general matching evaluator.
- Require current named History identity plus fresh positive `released_no_trigger` / `no_matching_rules` audit with explicitly null rule identity. Empty audit, no index, missing labels, stale History and failures cannot pass.
- Retain downstream-only coverage: no selector revision/health, receiver admission, selected-table isolation, notification delivery or physical clear inference.
- Preserve defaults, preview/report/cleanup; add RED/GREEN, full-suite evidence and review before commit.
