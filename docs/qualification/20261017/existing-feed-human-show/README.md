# Human runs-show coverage correction

`runs show` previously rendered the legacy Alert Engine coverage text for owned existing-feed runs. It now reports original Native/public History identity-only coverage and keeps correlation/window, delivery receipt and semantic task proof explicitly unproved. The existing cleanup field continues to display DIRTY or UNKNOWN. No request, retry, cleanup behavior, persistence, JSON or export changes are included.

Focused tests cover completed PASS/DIRTY using report routing and ERROR/UNKNOWN using retained-plan routing. Both reproduced the misleading legacy coverage before the change. The full command package then passed, and the full Go suite passed with cached dependencies (the existing guarded integration skips remain). Logs and two source hashes are adjacent. No binary or image was built.

This source-only wording correction is later than the immutable c2a6bb6 Linux CLI used for root's actual MySQL cohort qualification. That existing binary qualifies the actual workflow; these local tests qualify the new human wording. Do not attribute the wording to that earlier binary.

Root independently reviewed the exact two-file source slice with no blocker and reran the focused real command test successfully (0.356s). The full retained Go log has 27 passing packages and two no-test packages; that non-verbose log does not provide individual test/subtest counts.
