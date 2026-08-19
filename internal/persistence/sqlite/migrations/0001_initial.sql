CREATE TABLE targets (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    base_url TEXT NOT NULL,
    api_profile TEXT NOT NULL DEFAULT 'public-v1',
    tls_insecure INTEGER NOT NULL DEFAULT 0 CHECK (tls_insecure IN (0, 1)),
    ca_path TEXT,
    credential_kind TEXT CHECK (credential_kind IS NULL OR credential_kind IN ('env', 'file', 'systemd')),
    credential_ref TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((credential_kind IS NULL) = (credential_ref IS NULL)),
    CHECK (NOT (tls_insecure = 1 AND ca_path IS NOT NULL))
) STRICT;

CREATE TABLE scenarios (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    api_version TEXT NOT NULL,
    source_document TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    built_in INTEGER NOT NULL DEFAULT 0 CHECK (built_in IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    short_token TEXT NOT NULL UNIQUE,
    target_id TEXT REFERENCES targets(id) ON DELETE RESTRICT,
    scenario_id TEXT REFERENCES scenarios(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('QUEUED','PREFLIGHT','SETTING_UP','INJECTING','OBSERVING','ASSERTING','CANCELLING','CLEANING_UP','INTERRUPTED','RECOVERING','COMPLETED')),
    verdict TEXT CHECK (verdict IS NULL OR verdict IN ('PASS','FAIL','INCONCLUSIVE','ERROR','SKIPPED')),
    cleanup_status TEXT NOT NULL CHECK (cleanup_status IN ('CLEAN','DIRTY','NOT_REQUIRED','UNKNOWN')),
    harness_version TEXT NOT NULL,
    capability_snapshot_json TEXT,
    compiled_plan_json TEXT,
    canonical_report_json TEXT,
    started_at TEXT,
    ended_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    terminal_error TEXT
) STRICT;

CREATE TABLE run_cases (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    stable_key TEXT NOT NULL,
    pattern TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    verdict TEXT CHECK (verdict IS NULL OR verdict IN ('PASS','FAIL','INCONCLUSIVE','ERROR','SKIPPED')),
    started_at TEXT,
    ended_at TEXT,
    UNIQUE (run_id, stable_key)
) STRICT;

CREATE TABLE run_events (
    id INTEGER PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    case_id TEXT REFERENCES run_cases(id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    type TEXT NOT NULL,
    level TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    summary TEXT NOT NULL,
    detail_json TEXT,
    UNIQUE (run_id, sequence)
) STRICT;

CREATE TABLE alert_attempts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    case_id TEXT REFERENCES run_cases(id) ON DELETE RESTRICT,
    event_id TEXT NOT NULL,
    event_index INTEGER NOT NULL,
    physical_alert_name TEXT NOT NULL,
    semantic_role TEXT NOT NULL,
    identity_labels_json TEXT NOT NULL,
    fingerprint TEXT,
    send_state TEXT NOT NULL,
    request_artifact_id TEXT,
    response_artifact_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE assertions (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    case_id TEXT NOT NULL REFERENCES run_cases(id) ON DELETE RESTRICT,
    stable_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    expected_json TEXT NOT NULL,
    observed_json TEXT,
    verdict TEXT CHECK (verdict IS NULL OR verdict IN ('PASS','FAIL','INCONCLUSIVE','ERROR','SKIPPED')),
    explanation TEXT,
    observation_start TEXT,
    observation_end TEXT,
    UNIQUE (case_id, stable_key)
) STRICT;

CREATE TABLE resources (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    external_id TEXT,
    external_name TEXT,
    ownership_token TEXT NOT NULL,
    lifecycle_state TEXT NOT NULL,
    created_at TEXT,
    deleted_at TEXT,
    cleanup_error TEXT
) STRICT;

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    case_id TEXT REFERENCES run_cases(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    relative_path TEXT NOT NULL UNIQUE,
    mime_type TEXT NOT NULL,
    sha256 TEXT,
    byte_size INTEGER CHECK (byte_size IS NULL OR byte_size >= 0),
    redaction_state TEXT NOT NULL,
    availability_state TEXT NOT NULL CHECK (availability_state IN ('PENDING','AVAILABLE')),
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX idx_targets_display_name ON targets(display_name);
CREATE INDEX idx_runs_created_at ON runs(created_at DESC);
CREATE INDEX idx_runs_target_status ON runs(target_id, status);
CREATE INDEX idx_runs_verdict_cleanup ON runs(verdict, cleanup_status);
CREATE INDEX idx_run_cases_pattern ON run_cases(run_id, pattern);
CREATE INDEX idx_run_events_sequence ON run_events(run_id, sequence);
CREATE INDEX idx_artifacts_run_kind ON artifacts(run_id, kind);
