"""Resume only original committed timer responsibility after an externally proved kill."""
import argparse
import asyncio
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import re
import sqlite3
import sys
import time
from types import SimpleNamespace

import run as fixture

require = fixture.require


def validate_checkpoint(data, source, redis, runtime):
    try:
        require(type(data) is dict and type(data["schema"]) is int and data["schema"] == 1,
                "unknown checkpoint schema")
        require(data["kind"] == "persistence-before-process-kill", "wrong checkpoint kind")
        require(re.fullmatch(r"[0-9a-f]{64}", source) is not None and data["source_sha256"] == source,
                "checkpoint source mismatch")
        require(re.fullmatch(r"[0-9a-f]{40}", redis) is not None and data["redis_run_id"] == redis,
                "checkpoint Redis identity mismatch")
        require(type(data["runtime"]) is dict and data["runtime"] == runtime, "checkpoint runtime mismatch")
        for name in ("python", "redis", "aiosqlite"):
            require(type(runtime[name]) is str and bool(runtime[name]), "missing runtime version")
        for name in ("first_seen_ms", "deadline_ms", "prepared_at_ms", "duration_seconds", "pid"):
            require(type(data[name]) is int and data[name] > 0, f"invalid checkpoint {name}")
        require(type(data["prepared_elapsed_seconds"]) in (int, float)
                and math.isfinite(data["prepared_elapsed_seconds"])
                and 0 < data["prepared_elapsed_seconds"] < 10, "invalid preparation duration")
        require(data["duration_seconds"] == 30 and data["deadline_ms"] == data["first_seen_ms"] + 30000,
                "changed original deadline")
        require(data["first_seen_ms"] < data["prepared_at_ms"] < data["deadline_ms"] - 20000,
                "insufficient pre-kill margin")
        require(abs(data["prepared_at_ms"] - data["first_seen_ms"]
                    - data["prepared_elapsed_seconds"] * 1000) < 1000, "preparation clock jump")
        require(data["sqlite_journal_mode"] == "wal" and data["cancelled_absent_before_replay"] is True,
                "committed cancellation/WAL unproved")
        require(type(data["decisions"]) is list and data["decisions"] == [], "pre-kill timer already decided")
        for name, count in (("owned_keys", 6), ("owned_timer_ids", 3)):
            values = data[name]
            require(type(values) is list and len(values) == count
                    and all(type(v) is str and v for v in values) and len(set(values)) == count,
                    "invalid owned identities")
        for name in ("positive_group", "positive_fingerprint"):
            require(type(data[name]) is str and bool(data[name]), "missing positive identity")
        rule = data["rule"]
        require(type(rule["id"]) is int and rule["id"] > 0 and rule["pattern"] == "persistence"
                and rule["correlation_policy"] is None
                and rule["match_criteria"]["unresolved_for_seconds"] == 30, "invalid original rule")
        row = data["original_timer_row"]
        require(type(row) is list and len(row) == 7, "missing original timer row")
        require(row[:6] == [data["owned_timer_ids"][0], data["deadline_ms"], rule["id"],
                            data["positive_group"], data["positive_fingerprint"], "persistence_check"],
                "original row identity mismatch")
        payload = json.loads(row[6])
        require(payload["first_seen_ms"] == data["first_seen_ms"]
                and payload["unresolved_for_seconds"] == 30 and type(payload["group_values"]) is dict,
                "original payload mismatch")
        require(type(data["evaluations"]) is list and bool(data["evaluations"]), "missing original inputs")
    except (KeyError, TypeError, IndexError, json.JSONDecodeError) as exc:
        raise ValueError("missing or malformed checkpoint") from exc


def read_checkpoint(path, expected):
    require(re.fullmatch(r"[0-9a-f]{64}", expected) is not None, "externally pinned checkpoint hash required")
    require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 1024 * 1024,
            "missing/oversize/symlink checkpoint")
    raw = path.read_bytes()
    require(hashlib.sha256(raw).hexdigest() == expected, "checkpoint bytes changed")
    data = json.loads(raw)
    require(type(data) is dict, "checkpoint object required")
    return data


def validate_rows(rows, checkpoint):
    require([list(row) for row in rows] == [checkpoint["original_timer_row"]],
            "existing SQLite must contain only the original committed timer; no authority to seed/replace")


async def validate_reopened_store(store, checkpoint):
    # Actual replay API returns only (timer_id, fire_at_ms); fetch owns full rows.
    projected = await store.select_for_replay()
    original = checkpoint["original_timer_row"]
    require([list(row) for row in projected] == [original[:2]], "missing/foreign replay projection")
    full = await store.fetch(original[0])
    require(full is not None, "original timer disappeared before replay")
    validate_rows([full], checkpoint)


def validate_resume_evidence(data, checkpoint):
    try:
        require(data["observation_mode"] == "split_process", "split observation must remain explicit")
        for name in ("resume_started_ms", "observation_end_ms", "resume_elapsed_seconds", "observation_gap_ms"):
            require(type(data[name]) in (int, float) and math.isfinite(data[name]), "invalid split timing evidence")
        deadline = checkpoint["deadline_ms"]
        require(checkpoint["prepared_at_ms"] <= data["resume_started_ms"] <= deadline - 5000,
                "restart missed bounded pre-deadline margin")
        require(data["observation_gap_ms"] == data["resume_started_ms"] - checkpoint["prepared_at_ms"],
                "incorrect observation gap")
        require(data["observation_end_ms"] >= deadline + 5000, "incomplete post-restart observation")
        require(data["resume_elapsed_seconds"] >= (deadline + 5000 - data["resume_started_ms"]) / 1000,
                "incomplete monotonic post-restart observation")
        require(abs(data["observation_end_ms"] - data["resume_started_ms"]
                    - data["resume_elapsed_seconds"] * 1000) < 1000, "restart clock jump")
        for name in ("cleanup", "source_parity", "preflight_original_rows", "replay_identity_preserved",
                     "cancelled_absent_after_replay"):
            require(data[name] is True, f"unproved {name}")
        for name, expected in (("replayed", 1), ("final_timer_rows", 0), ("final_timer_members", 0)):
            require(type(data[name]) is int and data[name] == expected, f"invalid {name}")
        require(type(data["decisions"]) is list and len(data["decisions"]) == 1,
                "expected one original positive decision and no canceled decision")
        observed = data["decisions"][0]
        for name in ("observed_ms", "resume_elapsed_seconds"):
            require(type(observed[name]) in (int, float) and math.isfinite(observed[name]), "invalid fire time")
        require(deadline <= observed["observed_ms"] <= data["observation_end_ms"], "early/late fire")
        require((deadline - data["resume_started_ms"]) / 1000 - 0.001
                <= observed["resume_elapsed_seconds"] <= data["resume_elapsed_seconds"], "early monotonic fire")
        decision = observed["decision"]
        require(decision["triggered"] is True and decision["outcome"] == "parent_emitted", "missing trigger")
        require(decision["group_hash"] == checkpoint["positive_group"]
                and decision["alert_fingerprint"] == checkpoint["positive_fingerprint"]
                and decision["window_start_ms"] == checkpoint["first_seen_ms"], "replaced timer identity")
    except (KeyError, TypeError, IndexError) as exc:
        raise ValueError("missing or malformed split observation evidence") from exc


async def qualify_resume(args, result):
    require(args.source.is_absolute() and args.prepare.is_absolute(), "absolute source/prepare paths required")
    source, prepare = args.source.resolve(), args.prepare.resolve()
    data = read_checkpoint(prepare / "checkpoint.json", args.checkpoint_sha256)
    require(fixture.source_digest(source) == args.source_sha256, "backend source hash mismatch")
    runtime = {"python": sys.version, "redis": importlib.metadata.version("redis"),
               "aiosqlite": importlib.metadata.version("aiosqlite")}
    validate_checkpoint(data, args.source_sha256, args.isolated_redis_run_id, runtime)
    require(not (prepare / "result.json").exists(), "preparation exited normally; root SIGKILL proof required")
    require(int(time.time() * 1000) <= data["deadline_ms"] - 5000, "original timer too close/late for this cut")
    database = prepare / "timers.sqlite"
    require(database.is_file() and not database.is_symlink(), "original SQLite database required")
    wal = prepare / "timers.sqlite-wal"
    require(wal.is_file() and not wal.is_symlink() and wal.stat().st_size > 32, "committed WAL artifact required")
    result["preopen_sqlite_sha256"] = {
        name: hashlib.sha256((prepare / name).read_bytes()).hexdigest()
        for name in ("timers.sqlite", "timers.sqlite-wal", "timers.sqlite-shm") if (prepare / name).is_file()}
    # Read-only SQL rejects missing/replaced responsibility before bootstrap/replay.
    connection = sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)
    try:
        require(connection.execute("PRAGMA journal_mode").fetchone()[0] == "wal", "original WAL mode required")
        rows = connection.execute("SELECT timer_id, fire_at_ms, rule_id, group_hash, alert_fp, kind, payload_json FROM timers").fetchall()
        validate_rows(rows, data)
    finally:
        connection.close()
    result.update(runtime=runtime, redis_run_id=data["redis_run_id"], original_timer_row=data["original_timer_row"],
                  checkpoint_sha256=args.checkpoint_sha256, prepared_at_ms=data["prepared_at_ms"],
                  first_seen_ms=data["first_seen_ms"], deadline_ms=data["deadline_ms"])
    require(not any(name == "app" or name.startswith("app.") for name in sys.modules), "backend already imported")
    sys.path.insert(0, str(source))
    import redis.asyncio as redis
    from app.core.redis_client import RedisClient
    from app.core.rule_index import RuleIndex
    from app.core.timer_store import TimerStore
    from app.core.timer_subsystem import TimerSubsystem, TIMER_ZSET
    from app.patterns.persistence import PersistencePattern
    from app.schemas.rule import Rule
    from app.schemas.match_criteria.persistence import PersistenceMatchCriteria
    fixture.check_imports(source)
    rule = Rule.model_validate(data["rule"])
    PersistenceMatchCriteria.model_validate(rule.match_criteria)
    evaluations = data["evaluations"]
    require(len(evaluations) == 5, "original five input evaluations required")
    pattern = PersistencePattern()
    keys, timer_ids = [], []
    for evaluation in (evaluations[0], evaluations[1], evaluations[4]):
        group_hash = pattern._compute_group_hash(evaluation["input"], rule.group_by_labels)
        timer_ids.append(pattern._timer_id(rule.id, group_hash))
        keys.extend([f"oscar:correlator:persistence:{rule.id}:{group_hash}",
                     f"oscar:correlator:persistence_fired:{rule.id}:{group_hash}"])
    require(keys == data["owned_keys"] and timer_ids == data["owned_timer_ids"], "checkpoint ownership binding mismatch")
    url = os.environ.get("CORRTEST_PERSISTENCE_REDIS_URL", "")
    require(bool(url), "explicit isolated Redis URL required")
    client = RedisClient()
    client._r = redis.from_url(url, socket_connect_timeout=2, socket_timeout=5, decode_responses=True)
    admitted, store, timers = False, None, None
    try:
        info = await client._r.info("server")
        require(info["run_id"] == args.isolated_redis_run_id, "Redis process changed")
        require(await client.get_str(keys[0]) == data["positive_fingerprint"], "original positive anchor missing/replaced")
        for key in keys[1:]:
            require(await client.get_str(key) is None, "unexpected canceled/other/fired state before replay")
        require(await client._r.zrange(TIMER_ZSET, 0, -1, withscores=True)
                == [(timer_ids[0], float(data["deadline_ms"]))], "unexpected hot timer state before replay")
        require(await client._r.dbsize() == 2, "unexpected Redis keys; no replay authority")
        result["preflight_original_rows"] = True
        admitted = True
        store = TimerStore(str(database))
        await store.connect()
        await validate_reopened_store(store, data)
        index = RuleIndex()
        index.replace([rule])
        started = time.monotonic()
        result.update(resume_started_ms=int(time.time() * 1000), decisions=[])
        require(result["resume_started_ms"] <= data["deadline_ms"] - 5000, "restart consumed original deadline margin")
        result["observation_gap_ms"] = result["resume_started_ms"] - data["prepared_at_ms"]

        class AuditCapture:
            async def buffer(self, decision):
                result["decisions"].append({"observed_ms": int(time.time() * 1000),
                                            "resume_elapsed_seconds": time.monotonic() - started,
                                            "decision": dict(decision)})

        timers = TimerSubsystem()
        timers.configure(client, store, index, AuditCapture(), None, None,
                         SimpleNamespace(correlator_dispatch_enabled=False),
                         SimpleNamespace(CORRELATOR_TIMER_POLL_INTERVAL_MS=100,
                                         CORRELATOR_TIMER_BATCH_SIZE=100, CORRELATOR_TIMER_FIRE_CONCURRENCY=10))
        client.timer_backend = timers
        require(await client._r.zrem(TIMER_ZSET, timer_ids[0]) == 1, "original hot timer disappeared")
        result["replayed"] = await timers.replay_from_store()
        result["replay_identity_preserved"] = (
            list(await store.fetch(timer_ids[0]) or []) == data["original_timer_row"]
            and await client._r.zscore(TIMER_ZSET, timer_ids[0]) == data["deadline_ms"])
        result["cancelled_absent_after_replay"] = (
            await store.fetch(timer_ids[1]) is None and await client._r.zscore(TIMER_ZSET, timer_ids[1]) is None)
        await timers.start()
        duration = (data["deadline_ms"] + 5100 - result["resume_started_ms"]) / 1000
        while time.monotonic() - started < duration:
            require(timers._task is not None and not timers._task.done(), "actual poller stopped unexpectedly")
            await asyncio.sleep(0.1)
        await timers.stop()
        result.update(observation_end_ms=int(time.time() * 1000), resume_elapsed_seconds=time.monotonic() - started,
                      final_timer_rows=len(await store.select_for_replay()),
                      final_timer_members=await client._r.zcard(TIMER_ZSET))
    finally:
        async def remove_owned():
            if admitted:
                await client._r.zrem(TIMER_ZSET, *timer_ids)
                await client._r.delete(*keys)
                require(await client._r.dbsize() == 0, "residual Redis keys; broad cleanup forbidden")

        async def close_redis():
            await client._r.close(close_connection_pool=True)

        def source_check():
            fixture.check_imports(source)
            return fixture.source_digest(source) == args.source_sha256

        operations = ([timers.stop] if timers else []) + ([store.close] if store else [])
        await fixture.finish_evidence(result, operations + [remove_owned, close_redis], admitted, source_check)
    validate_resume_evidence(result, data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--source-sha256", required=True)
    parser.add_argument("--prepare", type=Path, required=True, help="original killed process evidence directory")
    parser.add_argument("--checkpoint-sha256", required=True, help="checkpoint hash captured externally by root")
    parser.add_argument("--isolated-redis-run-id", required=True)
    parser.add_argument("--output", type=Path, required=True, help="new resume evidence directory")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    result = {"status": "FAIL", "observation_mode": "split_process", "source_sha256": args.source_sha256,
              "scope": "existing committed Persistence timer reopen; external root manifest must prove SIGKILL",
              "dispatch_enabled": False, "receiver": "NOT_TESTED", "physical_clear": "NOT_PROVEN", "uat": "NOT_ACCEPTED"}
    try:
        asyncio.run(asyncio.wait_for(qualify_resume(args, result), timeout=60))
        result["status"] = "PASS"
    except Exception as exc:
        result["error"] = type(exc).__name__ + ": " + str(exc)
    (args.output / "result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps({"status": result["status"], "output": str(args.output), "error": result.get("error")}))
    return 0 if result["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
