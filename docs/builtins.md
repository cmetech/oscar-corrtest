# Built-in correlation scenarios

Every built-in creates two temporary, run-owned rules: `P01` proves the expected behavior and `N01` proves a nearby non-triggering case. Alert names follow `CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>` and every alert carries the full `oscar_test_*` identity contract.

| Pattern | Positive proof | Negative proof |
|---|---|---|
| `flood` | Five stable occurrences emit exactly one parent | Four occurrences remain below threshold |
| `co_occurrence` | All required alert names occur in the window | Required member is absent |
| `sequence` | Ordered login-failure then privileged-command events emit | Reversed order does not emit |
| `persistence` | Firing alert remains unresolved for the timer threshold | Resolution cancels the timer |
| `absence` | Expected heartbeat becomes absent for the threshold | Heartbeat remains eligible/non-triggering |
| `parent_child` | Active parent links and suppresses its child per notifier | Unmatched child is released with audit evidence |
| `cross_source` | Same semantic alert arrives from required distinct sources | Repeated single source does not emit |
| `threshold` | Three distinct `device` values emit | Repeated value stays below cardinality |

The compiler rewrites all logical roles into run-unique alert names and current OSCAR public-v1 match-criteria keys. Run `oscar-corrtest plan builtin:<pattern> --target <id> --pipeline-mode phase_b_dispatch --output json` to inspect the exact mutation budget and OSCAR filters before execution.
