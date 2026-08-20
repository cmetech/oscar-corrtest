---
sketch: 002
name: scenario-workbench
question: "How should examples, source authoring, validation, and compiled preview coexist?"
winner: "A"
tags: [scenarios, editor, workflow, documentation]
---

# Sketch 002: Scenario Workbench

## Design Question

How should technical OSCAR users browse built-in examples, inspect the exact scenario source, derive a custom scenario, validate it, and understand the compiled P01/N01 contract?

## How to View

```bash
open .planning/sketches/002-scenario-workbench/index.html
```

## Variants

- **A: Three-pane workbench (selected)** — catalog, source editor, and compiled contract remain visible together.
- **B: Source-first split** — horizontal example selector above a larger editor and structured preview.
- **C: Catalog and inspection drawer** — dense catalog first; source and contract open in a full-height drawer.

## What to Look For

- How quickly an operator can inspect an existing built-in example.
- Whether creating a custom scenario from an example is obvious.
- Whether schema guidance and errors are available without crowding the source.
- Whether P01/N01 behavior, generated alert names, and evidence assertions are understandable before a run.
