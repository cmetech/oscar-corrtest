"""Finite actual-component test. No receiver, broker, MySQL or dispatch proof."""
import argparse
import asyncio
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import re
import secrets
import sys
import time
from types import SimpleNamespace


def require(condition, message):
    if not condition:
        raise ValueError(message)


def source_digest(source):
    """Pin all Python/Lua source under app, including transitive imports."""
    files = sorted(p for p in (source / "app").rglob("*") if p.suffix in (".py", ".lua"))
    require(bool(files), "empty backend source")
    digest = hashlib.sha256()
    for path in files:
        require(not path.is_symlink() and path.resolve().is_relative_to(source.resolve()),
                "symlink or escaped backend source")
        digest.update(path.relative_to(source).as_posix().encode() + b"\0")
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def validate_evidence(data):
    """No success from partial observation or inferred/missing timer evidence."""
    try:
        numeric = ("deadline_ms", "observation_end_ms", "elapsed_seconds", "duration_seconds", "first_seen_ms")
        for key in numeric:
            require(type(data[key]) in (int, float) and math.isfinite(data[key]), f"invalid {key}")
        require(data["duration_seconds"] == 30, "production duration minimum required")
        require(data["deadline_ms"] == data["first_seen_ms"] + 30000, "wrong original deadline")
        require(data["observation_end_ms"] >= data["deadline_ms"] + 5000, "incomplete wall observation")
        require(data["elapsed_seconds"] >= 35, "incomplete monotonic observation")
        for key in ("cleanup", "source_parity", "replay_identity_preserved",
                    "cancelled_absent_before_replay", "cancelled_absent_after_replay"):
            require(data[key] is True, f"unproved {key}")
        for key, expected in (("replayed", 1), ("final_timer_rows", 0), ("final_timer_members", 0)):
            require(type(data[key]) is int and data[key] == expected, f"invalid {key}")
        for key in ("positive_fingerprint", "positive_group"):
            require(type(data[key]) is str and bool(data[key]), f"invalid {key}")
        require(type(data["decisions"]) is list and len(data["decisions"]) == 1,
                "expected exactly one timer decision and no cancelled-group decision")
        observed = data["decisions"][0]
        for key in ("observed_ms", "elapsed_seconds"):
            require(type(observed[key]) in (int, float) and math.isfinite(observed[key]), "invalid fire time")
        require(data["deadline_ms"] <= observed["observed_ms"] <= data["observation_end_ms"], "early/late fire")
        # Integer milliseconds can truncate the starting timestamp by <1ms.
        require(29.999 <= observed["elapsed_seconds"] <= data["elapsed_seconds"], "early monotonic fire")
        decision = observed["decision"]
        require(decision["triggered"] is True and decision["outcome"] == "parent_emitted", "missing trigger")
        require(decision["group_hash"] == data["positive_group"], "wrong group fired")
        require(decision["alert_fingerprint"] == data["positive_fingerprint"], "wrong anchor fired")
        require(decision["window_start_ms"] == data["first_seen_ms"], "wrong source epoch")
    except (KeyError, TypeError, IndexError) as exc:
        raise ValueError("missing or malformed timer evidence") from exc


def check_imports(source):
    for name, module in tuple(sys.modules.items()):
        path = getattr(module, "__file__", None)
        if (name == "app" or name.startswith("app.")) and path:
            require(Path(path).resolve().is_relative_to(source), "backend imported outside pinned source")


async def finish_evidence(result, operations, admitted, source_check):
    """Finish every cleanup step and preserve an already-active failure."""
    errors = []
    for operation in operations:
        try:
            await operation()
        except Exception as exc:
            errors.append(type(exc).__name__ + ": " + str(exc))
    result["cleanup_errors"] = errors
    result["cleanup"] = admitted and not errors
    try:
        result["source_parity"] = source_check() is True
    except Exception as exc:
        result["source_parity"] = False
        result["source_parity_error"] = type(exc).__name__ + ": " + str(exc)


async def qualify(args, result):
    require(args.source.is_absolute(), "absolute backend src path required")
    source = args.source.resolve()
    require(re.fullmatch(r"[0-9a-f]{64}", args.source_sha256) is not None, "explicit source hash required")
    require(source_digest(source) == args.source_sha256, "backend source hash mismatch")
    require(re.fullmatch(r"[0-9a-f]{40}", args.isolated_redis_run_id) is not None,
            "root-owned isolated Redis run_id required")
    url = os.environ.get("CORRTEST_PERSISTENCE_REDIS_URL", "")
    require(bool(url), "CORRTEST_PERSISTENCE_REDIS_URL required; no default target")
    require(not any(name == "app" or name.startswith("app.") for name in sys.modules),
            "backend must not be preloaded")
    sys.path.insert(0, str(source))
    import redis.asyncio as redis
    from app.core.redis_client import RedisClient
    from app.core.rule_index import RuleIndex
    from app.core.timer_store import TimerStore
    from app.core.timer_subsystem import TimerSubsystem, TIMER_ZSET
    from app.patterns.persistence import PersistencePattern
    from app.schemas.rule import Rule
    from app.schemas.match_criteria.persistence import PersistenceMatchCriteria
    check_imports(source)
    result["runtime"] = {"python": sys.version, "redis": importlib.metadata.version("redis"),
                         "aiosqlite": importlib.metadata.version("aiosqlite")}

    client = RedisClient()
    client._r = redis.from_url(url, socket_connect_timeout=2, socket_timeout=5, decode_responses=True)
    store = None
    timers = None
    admitted = False
    keys, timer_ids = [], []
    try:
        info = await client._r.info("server")
        require(info["run_id"] == args.isolated_redis_run_id, "Redis process identity mismatch")
        require(await client._r.dbsize() == 0, "isolated Redis database must be empty")
        admitted = True
        result["redis_run_id"] = info["run_id"]
        result["redis_version"] = info["redis_version"]
        nonce = secrets.token_hex(12)
        rule = Rule(id=int(nonce[:12], 16), name="corrtest_persistence_" + nonce,
                    pattern="persistence", window_seconds=60, group_by_labels=["site"],
                    match_criteria={"match": {"alertname": "corrtest_" + nonce}, "unresolved_for_seconds": 30},
                    emit_spec={"alertname": "unused_dispatch_disabled"})
        PersistenceMatchCriteria.model_validate(rule.match_criteria)
        index = RuleIndex()
        index.replace([rule])
        pattern = PersistencePattern()
        started = time.monotonic()
        first_seen = int(time.time() * 1000)
        result.update(first_seen_ms=first_seen, deadline_ms=first_seen + 30000,
                      duration_seconds=30, decisions=[], evaluations=[])

        class AuditCapture:
            async def buffer(self, decision):
                result["decisions"].append({"observed_ms": int(time.time() * 1000),
                                            "elapsed_seconds": time.monotonic() - started,
                                            "decision": dict(decision)})

        store = TimerStore(str(args.output / "timers.sqlite"))
        await store.connect()
        async with store._conn.execute("PRAGMA journal_mode") as cursor:
            result["sqlite_journal_mode"] = (await cursor.fetchone())[0]
        require(result["sqlite_journal_mode"] == "wal", "actual WAL mode required")

        def subsystem():
            ts = TimerSubsystem()
            ts.configure(client, store, index, AuditCapture(), None, None,
                         SimpleNamespace(correlator_dispatch_enabled=False),
                         SimpleNamespace(CORRELATOR_TIMER_POLL_INTERVAL_MS=100,
                                         CORRELATOR_TIMER_BATCH_SIZE=100,
                                         CORRELATOR_TIMER_FIRE_CONCURRENCY=10))
            client.timer_backend = ts
            return ts

        timers = subsystem()
        inputs = {}
        for group in ("positive", "cancelled", "other"):
            alert = {"fingerprint": nonce + "_" + group, "status": "firing", "starts_at_ms": first_seen,
                     "labels": {"alertname": "corrtest_" + nonce, "site": nonce + "_" + group}}
            group_hash = pattern._compute_group_hash(alert, rule.group_by_labels)
            inputs[group] = alert
            timer_ids.append(pattern._timer_id(rule.id, group_hash))
            keys.extend([f"oscar:correlator:persistence:{rule.id}:{group_hash}",
                         f"oscar:correlator:persistence_fired:{rule.id}:{group_hash}"])
        result.update(owned_keys=keys, owned_timer_ids=timer_ids, rule=rule.model_dump(),
                      positive_group=pattern._compute_group_hash(inputs["positive"], rule.group_by_labels),
                      positive_fingerprint=inputs["positive"]["fingerprint"])
        # Persist ownership before any pattern mutation, for interrupted cleanup.
        (args.output / "ownership.json").write_text(json.dumps({"keys": keys, "timer_ids": timer_ids,
                                                               "redis_run_id": info["run_id"]}, indent=2))

        async def evaluate(alert):
            decision = await pattern.evaluate(alert, rule, client)
            require(decision is not None, "fixture input failed actual MatchClause")
            result["evaluations"].append({"input": alert, "decision": decision})
            return decision

        await evaluate(inputs["positive"])
        await evaluate(inputs["cancelled"])
        original = await store.fetch(timer_ids[0])
        require(original is not None and original[1] == result["deadline_ms"], "original deadline not stored")
        require(original[4] == result["positive_fingerprint"], "original anchor not stored")
        result["original_timer_row"] = original
        await timers.start()
        await asyncio.sleep(1)
        repeated = dict(inputs["positive"], fingerprint=nonce + "_repeat", starts_at_ms=int(time.time() * 1000))
        decision = await evaluate(repeated)
        require(decision["reason"] == "persistence_already_tracking", "repeat replaced anchor")
        require(await store.fetch(timer_ids[0]) == original, "repeat moved original deadline/identity")
        await evaluate(dict(inputs["cancelled"], status="resolved", fingerprint=nonce + "_different"))
        await evaluate(dict(inputs["other"], status="resolved", fingerprint=inputs["positive"]["fingerprint"]))
        require(await store.fetch(timer_ids[0]) == original, "other-group clear cancelled positive timer")
        result["cancelled_absent_before_replay"] = (
            await store.fetch(timer_ids[1]) is None and await client._r.zscore(TIMER_ZSET, timer_ids[1]) is None
            and await client.get_str(keys[2]) is None)
        require(result["cancelled_absent_before_replay"], "clear did not cancel both stores/anchor")
        await timers.stop()
        await store.close()
        # Orderly reopen, not process-kill or power-loss recovery.
        store = TimerStore(str(args.output / "timers.sqlite"))
        await store.connect()
        timers = subsystem()
        require(await store.fetch(timer_ids[0]) == original, "SQLite reopen lost original timer")
        require(await client._r.zrem(TIMER_ZSET, timer_ids[0]) == 1, "owned hot timer was missing before removal")
        result["replayed"] = await timers.replay_from_store()
        result["replay_identity_preserved"] = (await store.fetch(timer_ids[0]) == original
            and await client._r.zscore(TIMER_ZSET, timer_ids[0]) == original[1])
        result["cancelled_absent_after_replay"] = (
            await store.fetch(timer_ids[1]) is None and await client._r.zscore(TIMER_ZSET, timer_ids[1]) is None)
        require(time.monotonic() - started < 10, "setup/reopen consumed cancellation timing margin")
        require(not result["decisions"], "timer fired during setup before deadline")
        await timers.start()
        # Observe the entire negative window; don't exit early on positive evidence.
        while time.monotonic() - started < 35.1:
            require(timers._task is not None and not timers._task.done(), "actual poll task stopped unexpectedly")
            await asyncio.sleep(0.1)
        await timers.stop()
        result.update(observation_end_ms=int(time.time() * 1000), elapsed_seconds=time.monotonic() - started,
                      final_timer_rows=len(await store.select_for_replay()),
                      final_timer_members=await client._r.zcard(TIMER_ZSET))
        require(abs(result["observation_end_ms"] - first_seen - result["elapsed_seconds"] * 1000) < 1000,
                "wall-clock jump invalidated timing observation")
    finally:
        async def remove_owned():
            if admitted:
                if timer_ids:
                    await client._r.zrem(TIMER_ZSET, *timer_ids)
                if keys:
                    await client._r.delete(*keys)
                require(await client._r.dbsize() == 0, "Redis has residual keys; no broad cleanup permitted")

        async def close_redis():
            # Installed host redis-py 4.6 has async close(), not aclose().
            # This fixture runtime is recorded, not packaged-release qualification.
            await client._r.close(close_connection_pool=True)

        def source_check():
            check_imports(source)
            return source_digest(source) == args.source_sha256

        operations = ([timers.stop] if timers else []) + ([store.close] if store else [])
        await finish_evidence(result, operations + [remove_owned, close_redis], admitted, source_check)
    validate_evidence(result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="absolute oscar-correlator/src")
    parser.add_argument("--source-sha256", required=True)
    parser.add_argument("--isolated-redis-run-id", required=True, help="root-owned dedicated Redis INFO server run_id")
    parser.add_argument("--output", type=Path, required=True, help="new evidence directory; never overwritten")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    result = {"status": "FAIL", "source_sha256": args.source_sha256, "dispatch_enabled": False,
              "scope": "actual Persistence/TimerSubsystem/Redis/SQLite with fixture audit capture",
              "receiver": "NOT_TESTED", "physical_clear": "NOT_PROVEN", "uat": "NOT_ACCEPTED"}
    try:
        asyncio.run(asyncio.wait_for(qualify(args, result), timeout=60))
        result["status"] = "PASS"
    except Exception as exc:
        result["error"] = type(exc).__name__ + ": " + str(exc)
    (args.output / "result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print(json.dumps({"status": result["status"], "output": str(args.output), "error": result.get("error")}))
    return 0 if result["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
