CREATE TABLE existing_feeds (
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    profile TEXT NOT NULL CHECK (profile IN ('splunk','middleware_custom_alert')),
    cohort_id TEXT NOT NULL,
    receiver_origin TEXT NOT NULL,
    manifest_json TEXT NOT NULL CHECK (length(manifest_json) BETWEEN 1 AND 2097152),
    created_at TEXT NOT NULL,
    PRIMARY KEY (run_id, profile),
    UNIQUE (receiver_origin, cohort_id, profile)
) STRICT;

CREATE TABLE existing_feed_attempts (
    run_id TEXT NOT NULL,
    profile TEXT NOT NULL,
    fixture_index INTEGER NOT NULL CHECK (fixture_index BETWEEN 0 AND 5),
    evidence_json TEXT NOT NULL CHECK (length(evidence_json) BETWEEN 1 AND 2097152),
    result_json TEXT CHECK (length(result_json) BETWEEN 1 AND 2097152),
    recorded_at TEXT NOT NULL,
    completed_at TEXT,
    PRIMARY KEY (run_id, profile, fixture_index),
    FOREIGN KEY (run_id, profile) REFERENCES existing_feeds(run_id, profile) ON DELETE RESTRICT,
    CHECK ((result_json IS NULL) = (completed_at IS NULL))
) STRICT;
