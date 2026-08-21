# Built-in correlation scenarios

Every built-in creates two temporary, run-owned correlation rules: `P01` proves
the expected behavior and `N01` proves a nearby non-triggering case. It does
not create ordinary OSCAR alert rules. Alert names follow
`CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>` and every alert carries
the full `oscar_test_*` identity contract. Open the linked tutorial to compare
the basic and advanced YAML, compiled contract, API JSON, and lifecycle before
opening an unsaved draft in Scenarios.

| Pattern | Positive proof | Negative proof | Tutorial |
|---|---|---|---|
| `flood` | Five stable occurrences emit exactly one parent | Four occurrences remain below threshold | [Flood](/authoring?section=patterns&pattern=flood) |
| `co_occurrence` | All required alert names occur in the window | Required member is absent | [Co-occurrence](/authoring?section=patterns&pattern=co_occurrence) |
| `sequence` | Ordered login-failure then privileged-command events emit | Reversed order does not emit | [Sequence](/authoring?section=patterns&pattern=sequence) |
| `persistence` | Firing alert remains unresolved for the timer threshold | Resolution cancels the timer | [Persistence](/authoring?section=patterns&pattern=persistence) |
| `absence` | Expected heartbeat becomes absent for the threshold | Heartbeat remains eligible/non-triggering | [Absence](/authoring?section=patterns&pattern=absence) |
| `parent_child` | Active parent links and suppresses its child per notifier | Unmatched child is released with audit evidence | [Parent-child](/authoring?section=patterns&pattern=parent_child) |
| `cross_source` | Same semantic alert arrives from required distinct sources | Repeated single source does not emit | [Cross-source](/authoring?section=patterns&pattern=cross_source) |
| `threshold` | Three distinct `device` values emit | Repeated value stays below cardinality | [Threshold](/authoring?section=patterns&pattern=threshold) |

The compiler rewrites all logical roles into run-unique alert names and current OSCAR public-v1 match-criteria keys. Run `oscar-corrtest plan builtin:<pattern> --target <id> --pipeline-mode phase_b_dispatch --output json` to inspect the exact mutation budget and OSCAR filters before execution.

The built-in parent-child case explicitly uses notifier name `email`. If a target uses different notifier names, import a custom parent-child scenario and set `suppressForNotifiers` and/or `tagForNotifiers` to the exact configured OSCAR names; notifier names are never inferred from UI text.
