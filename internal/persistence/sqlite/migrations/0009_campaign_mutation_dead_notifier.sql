-- Scenario delivery-retry@v1 records a dead_notifier_create mutation (the dead
-- webhook target it creates, keyed by name), restored by deleting it. SQLite
-- cannot alter a CHECK constraint: rebuild the table and keep every row, as 0008 did.
CREATE TABLE campaign_mutations_0009 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('notifier_disable', 'notifier_enable', 'policy_create', 'policy_activate', 'sink_enable', 'incident_close', 'queue_override', 'dead_notifier_create')),
    object_id TEXT NOT NULL,
    prior_json TEXT CHECK (prior_json IS NULL OR json_valid(prior_json)),
    recorded_at TEXT NOT NULL,
    restored_at TEXT
) STRICT;

INSERT INTO campaign_mutations_0009(id, campaign_id, kind, object_id, prior_json, recorded_at, restored_at)
    SELECT id, campaign_id, kind, object_id, prior_json, recorded_at, restored_at FROM campaign_mutations;

DROP TABLE campaign_mutations;

ALTER TABLE campaign_mutations_0009 RENAME TO campaign_mutations;

CREATE INDEX campaign_mutations_open_idx ON campaign_mutations(campaign_id) WHERE restored_at IS NULL;
