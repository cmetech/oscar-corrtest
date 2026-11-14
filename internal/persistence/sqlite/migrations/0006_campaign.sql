CREATE TABLE campaigns (
    id TEXT PRIMARY KEY CHECK (length(id) > 0),
    scenario_id TEXT NOT NULL,
    scenario_hash TEXT NOT NULL,
    custom INTEGER NOT NULL DEFAULT 0 CHECK (custom IN (0, 1)),
    trigger TEXT NOT NULL CHECK (trigger IN ('cli', 'ui', 'recovery')),
    run_by TEXT NOT NULL CHECK (run_by IN ('agent', 'human')),
    trigger_identity TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PRECHECK', 'RUNNING', 'STOPPING', 'DRAINING', 'CLEANING_UP', 'COMPLETED', 'INTERRUPTED', 'RECOVERING')),
    verdict TEXT CHECK (verdict IN ('PASS', 'FAIL', 'INCONCLUSIVE', 'ERROR', 'SKIPPED')),
    cleanup_status TEXT CHECK (cleanup_status IN ('CLEAN', 'DIRTY', 'NOT_REQUIRED', 'UNKNOWN')),
    comparable INTEGER NOT NULL DEFAULT 0 CHECK (comparable IN (0, 1)),
    started_at TEXT NOT NULL,
    ended_at TEXT,
    scorecard_json TEXT CHECK (scorecard_json IS NULL OR json_valid(scorecard_json)),
    report_json TEXT CHECK (report_json IS NULL OR json_valid(report_json))
) STRICT;

CREATE INDEX campaigns_status_idx ON campaigns(status);

CREATE TABLE campaign_events (
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    type TEXT NOT NULL,
    level TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    summary TEXT NOT NULL,
    detail_json TEXT CHECK (detail_json IS NULL OR json_valid(detail_json)),
    PRIMARY KEY (campaign_id, sequence)
) STRICT;

CREATE TABLE campaign_prechecks (
    campaign_id TEXT PRIMARY KEY REFERENCES campaigns(id) ON DELETE RESTRICT,
    passed INTEGER NOT NULL CHECK (passed IN (0, 1)),
    detail_json TEXT CHECK (detail_json IS NULL OR json_valid(detail_json)),
    recorded_at TEXT NOT NULL
) STRICT;

CREATE TABLE campaign_runs (
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    PRIMARY KEY (campaign_id, run_id)
) STRICT;

CREATE INDEX campaign_runs_run_idx ON campaign_runs(run_id);

CREATE TABLE campaign_mutations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('notifier_disable', 'policy_create', 'policy_activate', 'sink_enable', 'incident_close')),
    object_id TEXT NOT NULL,
    prior_json TEXT CHECK (prior_json IS NULL OR json_valid(prior_json)),
    recorded_at TEXT NOT NULL,
    restored_at TEXT
) STRICT;

CREATE INDEX campaign_mutations_open_idx ON campaign_mutations(campaign_id) WHERE restored_at IS NULL;

CREATE TABLE campaign_lane_ledger (
    lane_id TEXT NOT NULL,
    seq INTEGER NOT NULL CHECK (seq >= 0),
    kind TEXT NOT NULL CHECK (kind IN ('firing', 'resolve')),
    identity TEXT NOT NULL,
    severity TEXT NOT NULL,
    instance TEXT NOT NULL,
    ticket INTEGER NOT NULL DEFAULT 0 CHECK (ticket IN (0,1)),
    scheduled_at TEXT NOT NULL,
    sent_at TEXT,
    status INTEGER,
    error_class TEXT CHECK (error_class IS NULL OR error_class IN ('', 'http', 'timeout', 'network', 'missed', 'cancelled')),
    latency_ms INTEGER,
    missed INTEGER CHECK (missed IS NULL OR missed IN (0, 1)),
    PRIMARY KEY (lane_id, seq),
    CHECK ((sent_at IS NULL) = (missed IS NULL))
) STRICT;
