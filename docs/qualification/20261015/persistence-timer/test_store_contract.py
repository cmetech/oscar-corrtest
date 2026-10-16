"""Opt-in real TimerStore contract; temporary SQLite only, no Redis or crash claim."""
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

import resume
from test_resume import checkpoint


@unittest.skipUnless(os.environ.get("CORRTEST_PERSISTENCE_SOURCE"), "explicit pinned backend source required")
class ActualTimerStoreTests(unittest.IsolatedAsyncioTestCase):
    async def test_reopened_store_projection_and_full_identity_are_distinct_contracts(self):
        source = Path(os.environ["CORRTEST_PERSISTENCE_SOURCE"])
        self.assertTrue(source.is_absolute())
        pin = json.loads(Path(__file__).with_name("source-pin.json").read_text())["sha256"]
        self.assertEqual(resume.fixture.source_digest(source), pin)
        sys.path.insert(0, str(source))
        from app.core.timer_store import TimerStore
        resume.fixture.check_imports(source.resolve())
        with tempfile.TemporaryDirectory() as directory:
            path = str(Path(directory) / "timers.sqlite")
            data = checkpoint()
            row = data["original_timer_row"]
            store = TimerStore(path)
            await store.connect()
            try:
                await store.upsert(*row[:6], json.loads(row[6]))
                # Bind to the exact original committed payload serialization.
                data["original_timer_row"] = list(await store.fetch(row[0]))
                await store.close()
                store = TimerStore(path)
                await store.connect()
                try:
                    await resume.validate_reopened_store(store, data)
                except ValueError as exc:
                    self.fail(f"unchanged committed row rejected after actual reopen: {exc}")
                # Same projected id/deadline cannot conceal a replaced anchor.
                await store.upsert(row[0], row[1], row[2], row[3], "replacement", row[5], json.loads(row[6]))
                with self.assertRaises(ValueError):
                    await resume.validate_reopened_store(store, data)
                await store.delete(row[0])
                with self.assertRaises(ValueError):
                    await resume.validate_reopened_store(store, data)
                await store.upsert(*row[:6], json.loads(row[6]))
                await store.upsert("foreign-timer", row[1], row[2], row[3], row[4], row[5], json.loads(row[6]))
                with self.assertRaises(ValueError):
                    await resume.validate_reopened_store(store, data)
            finally:
                await store.close()
        self.assertEqual(resume.fixture.source_digest(source), pin)


if __name__ == "__main__":
    unittest.main()
