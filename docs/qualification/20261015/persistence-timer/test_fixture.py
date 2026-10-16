"""Host-only checks: reject false success; no Redis/backend timing exercised."""
import copy
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("persistence_fixture", Path(__file__).with_name("run.py"))
fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fixture)


class EvidenceTests(unittest.TestCase):
    def evidence(self):
        return {
            "deadline_ms": 31000, "observation_end_ms": 36000,
            "elapsed_seconds": 35, "duration_seconds": 30,
            "positive_fingerprint": "first", "positive_group": "positive",
            "decisions": [{"observed_ms": 31100, "elapsed_seconds": 30.1,
                           "decision": {"triggered": True, "outcome": "parent_emitted",
                                        "group_hash": "positive", "alert_fingerprint": "first",
                                        "window_start_ms": 1000}}],
            "first_seen_ms": 1000, "cleanup": True, "source_parity": True,
            "replayed": 1, "replay_identity_preserved": True,
            "cancelled_absent_before_replay": True, "cancelled_absent_after_replay": True,
            "final_timer_rows": 0, "final_timer_members": 0,
        }

    def test_complete_bounded_observation_passes(self):
        fixture.validate_evidence(self.evidence())

    def test_early_missing_duplicate_or_foreign_decision_cannot_pass(self):
        for mutation in ("early", "missing", "duplicate", "foreign", "wrong_anchor", "wrong_epoch"):
            with self.subTest(mutation=mutation):
                data = self.evidence()
                if mutation == "early":
                    data["decisions"][0]["observed_ms"] = 30999
                elif mutation == "missing":
                    data["decisions"] = []
                elif mutation == "duplicate":
                    data["decisions"] *= 2
                elif mutation == "foreign":
                    data["decisions"][0]["decision"]["group_hash"] = "cancelled"
                elif mutation == "wrong_anchor":
                    data["decisions"][0]["decision"]["alert_fingerprint"] = "repeat"
                else:
                    data["decisions"][0]["decision"]["window_start_ms"] = 2000
                with self.assertRaises(ValueError):
                    fixture.validate_evidence(data)

    def test_partial_observation_replay_or_cleanup_cannot_pass(self):
        changes = {"observation_end_ms": 35000, "elapsed_seconds": 34.9,
                   "duration_seconds": 1, "cleanup": False, "source_parity": False,
                   "replayed": 0, "replay_identity_preserved": False,
                   "cancelled_absent_before_replay": False, "cancelled_absent_after_replay": False,
                   "final_timer_rows": 1, "final_timer_members": 1}
        for key, value in changes.items():
            with self.subTest(key=key):
                data = self.evidence()
                data[key] = value
                with self.assertRaises(ValueError):
                    fixture.validate_evidence(data)

    def test_missing_or_wrong_type_evidence_cannot_pass(self):
        for key in self.evidence():
            for value in (None, "unavailable"):
                with self.subTest(key=key, value=value):
                    data = copy.deepcopy(self.evidence())
                    data[key] = value
                    with self.assertRaises(ValueError):
                        fixture.validate_evidence(data)

    def test_source_hash_changes_if_loaded_code_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory) / "app"
            app.mkdir()
            code = app / "timer.py"
            code.write_text("original")
            original = fixture.source_digest(Path(directory))
            code.write_text("changed")
            self.assertNotEqual(original, fixture.source_digest(Path(directory)))

    def test_empty_or_symlink_source_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with self.assertRaises(ValueError):
                fixture.source_digest(root)
            (root / "app").mkdir()
            (root / "elsewhere.py").write_text("outside")
            (root / "app" / "code.py").symlink_to(root / "elsewhere.py")
            with self.assertRaises(ValueError):
                fixture.source_digest(root)


class CleanupTests(unittest.IsolatedAsyncioTestCase):
    async def test_close_error_does_not_hide_prior_failure_or_source_parity(self):
        result = {"error": "original timing failure"}
        calls = []

        async def close_error():
            raise RuntimeError("connection close failed")

        async def later_cleanup():
            calls.append("completed")

        with self.assertRaisesRegex(ValueError, "original timing failure"):
            try:
                raise ValueError("original timing failure")
            finally:
                await fixture.finish_evidence(result, [close_error, later_cleanup], True, lambda: True)
        self.assertEqual(result["error"], "original timing failure")
        self.assertEqual(calls, ["completed"])
        self.assertFalse(result["cleanup"])
        self.assertTrue(result["source_parity"])
        self.assertIn("connection close failed", result["cleanup_errors"][0])

    async def test_source_check_failure_is_recorded_after_cleanup(self):
        result = {}

        def inaccessible_source():
            raise OSError("source unreadable")

        await fixture.finish_evidence(result, [], True, inaccessible_source)
        self.assertTrue(result.get("cleanup"))
        self.assertFalse(result["source_parity"])
        self.assertIn("source unreadable", result["source_parity_error"])


if __name__ == "__main__":
    unittest.main()
