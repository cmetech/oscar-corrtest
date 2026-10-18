CREATE TABLE installed_pack_runs (
    run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE RESTRICT,
    qualification_token TEXT NOT NULL UNIQUE CHECK (length(qualification_token) = 32 AND qualification_token NOT GLOB '*[^0-9a-f]*'),
    manifest_sha256 TEXT NOT NULL CHECK (length(manifest_sha256) = 64),
    api_target_id TEXT NOT NULL,
    api_base_url TEXT NOT NULL,
    registration_json TEXT NOT NULL CHECK (length(CAST(registration_json AS BLOB)) BETWEEN 1 AND 131072),
    registration_sha256 TEXT NOT NULL CHECK (length(registration_sha256) = 64),
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE installed_pack_attempts (
    run_id TEXT NOT NULL REFERENCES installed_pack_runs(run_id) ON DELETE RESTRICT,
    input_index INTEGER NOT NULL CHECK (input_index BETWEEN 0 AND 14),
    evidence_json TEXT NOT NULL CHECK (length(CAST(evidence_json AS BLOB)) BETWEEN 1 AND 16384),
    result_json TEXT CHECK (length(CAST(result_json AS BLOB)) BETWEEN 1 AND 2097152),
    recorded_at TEXT NOT NULL,
    completed_at TEXT,
    PRIMARY KEY (run_id, input_index),
    CHECK ((result_json IS NULL) = (completed_at IS NULL))
) STRICT;
