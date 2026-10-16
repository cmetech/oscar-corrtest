"""Host checks for refusal/authority and split-observation evidence, not crash proof."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import resume

SOURCE = "a" * 64
REDIS = "b" * 40
RUNTIME = {"python": "3.13 fixture", "redis": "8.1.0", "aiosqlite": "0.22.1"}


def checkpoint():
    return {
        "schema": 1, "kind": "persistence-before-process-kill", "source_sha256": SOURCE,
        "redis_run_id": REDIS, "runtime": dict(RUNTIME), "pid": 1,
        "first_seen_ms": 1000, "deadline_ms": 31000, "duration_seconds": 30,
        "prepared_at_ms": 2100, "prepared_elapsed_seconds": 1.1,
        "sqlite_journal_mode": "wal", "cancelled_absent_before_replay": True,
        "decisions": [], "positive_group": "group1", "positive_fingerprint": "original",
        "owned_keys": ["key" + str(i) for i in range(6)],
        "owned_timer_ids": ["timer1", "timer2", "timer3"],
        "rule": {"id": 123, "pattern": "persistence", "correlation_policy": None,
                 "match_criteria": {"match": {"alertname": "test"}, "unresolved_for_seconds": 30}},
        "original_timer_row": ["timer1", 31000, 123, "group1", "original", "persistence_check",
                               '{"first_seen_ms":1000,"unresolved_for_seconds":30,"group_values":{"site":"x"}}'],
        "evaluations": [{"input": {"fingerprint": "original", "status": "firing", "labels": {"site": "x"}}}],
    }


class CheckpointTests(unittest.TestCase):
    def test_prepare_marker_is_immutable_and_refuses_already_decided_timer(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            data = checkpoint()
            resume.fixture.write_kill_checkpoint(path, data, 2100, 1.1, 1)
            marker = path / "checkpoint.json"
            self.assertTrue(marker.exists(), "complete marker missing")
            original = marker.read_bytes()
            resume.validate_checkpoint(json.loads(original), SOURCE, REDIS, RUNTIME)
            with self.assertRaises((ValueError, FileExistsError)):
                resume.fixture.write_kill_checkpoint(path, data, 2200, 1.2, 1)
            self.assertEqual(marker.read_bytes(), original)
        with tempfile.TemporaryDirectory() as directory:
            data = checkpoint()
            data["decisions"] = [{"triggered": True}]
            with self.assertRaises(ValueError):
                resume.fixture.write_kill_checkpoint(Path(directory), data, 2100, 1.1, 1)
            self.assertFalse((Path(directory) / "checkpoint.json").exists())

    def test_complete_checkpoint_allowed(self):
        resume.validate_checkpoint(checkpoint(), SOURCE, REDIS, RUNTIME)

    def test_missing_checkpoint_fields_refused(self):
        for key in checkpoint():
            with self.subTest(key=key):
                data = checkpoint()
                del data[key]
                with self.assertRaises(ValueError):
                    resume.validate_checkpoint(data, SOURCE, REDIS, RUNTIME)

    def test_changed_source_runtime_deadline_or_owner_refused(self):
        changes = {"source_sha256": "c" * 64, "redis_run_id": "d" * 40,
                   "runtime": {"python": "other"}, "deadline_ms": 32000,
                   "positive_fingerprint": "new", "cancelled_absent_before_replay": False,
                   "decisions": [{"triggered": True}], "prepared_at_ms": 30999,
                   "owned_timer_ids": ["timer1"] * 3, "schema": True,
                   "prepared_elapsed_seconds": float("nan")}
        for key, value in changes.items():
            with self.subTest(key=key):
                data = checkpoint()
                data[key] = value
                with self.assertRaises(ValueError):
                    resume.validate_checkpoint(data, SOURCE, REDIS, RUNTIME)

    def test_pinned_bytes_required_before_checkpoint_is_trusted(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "checkpoint.json"
            original = json.dumps(checkpoint()).encode()
            path.write_bytes(original)
            digest = hashlib.sha256(original).hexdigest()
            self.assertEqual(resume.read_checkpoint(path, digest), checkpoint())
            path.write_bytes(original + b" ")
            with self.assertRaises(ValueError):
                resume.read_checkpoint(path, digest)
            path.write_bytes(b"null")
            with self.assertRaises(ValueError):
                resume.read_checkpoint(path, hashlib.sha256(b"null").hexdigest())

    def test_existing_original_row_required_no_authority_to_seed_or_replace(self):
        original = checkpoint()["original_timer_row"]
        resume.validate_rows([original], checkpoint())
        for rows in ([], [original, original], [original[:1] + [32000] + original[2:]],
                     [["timer2"] + original[1:]]):
            with self.subTest(rows=rows):
                with self.assertRaises(ValueError):
                    resume.validate_rows(rows, checkpoint())


class SplitObservationTests(unittest.TestCase):
    def evidence(self):
        return {
            "observation_mode": "split_process", "resume_started_ms": 5000,
            "observation_end_ms": 36100, "resume_elapsed_seconds": 31.1,
            "observation_gap_ms": 2900, "cleanup": True, "source_parity": True,
            "replayed": 1, "replay_identity_preserved": True,
            "cancelled_absent_after_replay": True, "preflight_original_rows": True,
            "final_timer_rows": 0, "final_timer_members": 0,
            "decisions": [{"observed_ms": 31100, "resume_elapsed_seconds": 26.1,
                           "decision": {"triggered": True, "outcome": "parent_emitted",
                                        "group_hash": "group1", "alert_fingerprint": "original",
                                        "window_start_ms": 1000}}],
        }

    def test_split_observation_passes_without_claiming_continuous_35s(self):
        resume.validate_resume_evidence(self.evidence(), checkpoint())

    def test_missing_partial_or_renewed_observation_refused(self):
        changes = {"observation_mode": "continuous", "resume_started_ms": 30000,
                   "observation_end_ms": 35000, "resume_elapsed_seconds": 30,
                   "observation_gap_ms": 0, "decisions": [], "cleanup": False,
                   "source_parity": False, "preflight_original_rows": False, "replayed": 0,
                   "cancelled_absent_after_replay": False, "replay_identity_preserved": False,
                   "final_timer_rows": 1, "final_timer_members": 1}
        for key, value in changes.items():
            with self.subTest(key=key):
                data = self.evidence()
                data[key] = value
                with self.assertRaises(ValueError):
                    resume.validate_resume_evidence(data, checkpoint())

    def test_early_duplicate_or_replaced_anchor_decision_refused(self):
        for mutation in ("early", "early_monotonic", "duplicate", "new_anchor", "cancelled"):
            with self.subTest(mutation=mutation):
                data = self.evidence()
                record = data["decisions"][0]
                if mutation == "early":
                    record["observed_ms"] = 30999
                elif mutation == "early_monotonic":
                    record["resume_elapsed_seconds"] = 1
                elif mutation == "duplicate":
                    data["decisions"].append(copy.deepcopy(record))
                elif mutation == "new_anchor":
                    record["decision"]["alert_fingerprint"] = "new"
                else:
                    record["decision"]["group_hash"] = "cancelled"
                with self.assertRaises(ValueError):
                    resume.validate_resume_evidence(data, checkpoint())


if __name__ == "__main__":
    unittest.main()
