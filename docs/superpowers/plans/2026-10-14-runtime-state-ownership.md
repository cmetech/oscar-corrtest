# Runtime state ownership implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent a second CorrTest process from marking a live run interrupted or mutating its state through startup recovery.

**Architecture:** One runtime holds a nonblocking OS lock on a persistent regular file in its local state directory before opening SQLite. The same runtime serves its embedded UI; a second CLI/server refuses explicitly. Runtime close releases only after active cleanup/database close; process death releases the kernel lock without PID guessing or stale-file deletion.

**Tech Stack:** Existing Go, SQLite and already present `golang.org/x/sys` (promoted to direct dependency, unchanged version); Unix flock and Windows LockFileEx.

**Spec:** The original single-process design in `docs/superpowers/specs/2026-08-19-oscar-correlation-test-harness-design.md`, plus the concrete RED in this cut: opening a second runtime changes the first runtime's OBSERVING run to INTERRUPTED.

## Global constraints

Feature CorrTest worktree only. No OSCAR calls, containers, deployment, source resume or shared-store multi-process design. Retain prepared c2dfd87 Linux binary unchanged. Local filesystems only; private state directory must not be replaced/unlinked while owned. No lock-file deletion or PID-based forced takeover. Existing older binaries do not honor the new lock and must be stopped before upgrade.

## Review focus

- Startup failures or diagnostic mode release ownership at the wrong time.
- Second open, including another process, recovers live work before refusing.
- Abrupt death leaves a file that prevents legitimate interrupted-run recovery.
- Repeated Close releases another runtime's newly acquired ownership.
- Platform-specific locking or shutdown changes weaken existing cleanup/UI behavior.

### Task 1: Enforce runtime lifetime ownership

**Files:** add `internal/runtime/state_lock.go`, `state_lock_unix.go`, `state_lock_windows.go`, `state_lock_test.go`; update `internal/runtime/runtime.go`, `go.mod` and README. Keep lower-level SQLite repository usable by its existing tests/backup; the application Runtime owns startup recovery.

- [x] Reproduce second-open mutation of live OBSERVING state before product edits. Expected failing test: `TestSecondRuntimeCannotInterruptActiveRun`.
- [x] Add local subprocess ownership/crash-recovery, independent-directory, failed-open, diagnostic and repeated-close cases. No real OSCAR services.
- [x] Acquire persistent `.corrtest.lock` before `storage.Open`; reject nonregular/symlink lock files, perform nonblocking kernel lock, keep descriptor. Release on every failed initialization; retain it for diagnostic runtimes too.
- [x] Release exactly once on runtime close, after active cleanup and database close. Preserve existing single-process UI and no automatic retry behavior.
- [x] Run focused tests, full Go suite, race checks for ownership/runtime, and release-platform compile checks. macOS execution cannot qualify Windows/Linux kernel behavior; record that limit rather than claiming it.
- [x] Fresh whole-cut review; one minimal fix pass if necessary, archive failures/passes with deterministic gzip, document operator behavior and feature commit.

Qualification: [RED/GREEN, suite, race, platform compilation and fresh review](../../qualification/20261014/runtime-state-ownership/README.md). Windows/Linux execution remains explicitly unqualified.
