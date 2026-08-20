---
sketch: 003
name: operations-surfaces
question: "How should API-key settings, service status, and live logs be presented?"
winner: "B"
tags: [settings, logs, operations, service]
---

# Sketch 003: Operations Surfaces

## Design Question

How should operators configure the global OSCAR API key, understand background-service state, and inspect live application logs without diluting the correlation-testing workflow?

## How to View

```bash
open .planning/sketches/003-operations-surfaces/index.html
```

## Variants

- **A: Dedicated pages** — Settings and Logs are separate primary navigation destinations.
- **B: Unified operations (selected)** — configuration, service state, and live logs share one dense workspace.
- **C: Bottom log console** — Settings remains primary while logs open as a persistent bottom console.

## What to Look For

- Whether key status and replacement behavior are explicit without displaying the secret.
- Whether service state and paths are easy to diagnose.
- Whether live logs are accessible without overwhelming normal pages.
- Whether level, source, text filtering, pause/resume, and download controls feel natural.
