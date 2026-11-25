-- The receivers lane (OSC-207) journals the webhook profile and trap mapping it
-- installs (rx_mapping, object namespace/name), removed by the lane or by cleanup.
-- SQLite cannot alter a CHECK constraint: rebuild the table and keep every row, as 0011 did.
CREATE TABLE campaign_mutations_0013 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('notifier_disable', 'notifier_enable', 'policy_create', 'policy_activate', 'sink_enable', 'incident_close', 'queue_override', 'dead_notifier_create', 'fingerprint_policy_enable', 'managed_provider_owner', 'native_owned_silence', 'native_owned_inhibitor', 'maint_pattern', 'maint_template', 'maint_window', 'rx_mapping')),
    object_id TEXT NOT NULL,
    prior_json TEXT CHECK (prior_json IS NULL OR json_valid(prior_json)),
    recorded_at TEXT NOT NULL,
    restored_at TEXT
) STRICT;

INSERT INTO campaign_mutations_0013(id, campaign_id, kind, object_id, prior_json, recorded_at, restored_at)
    SELECT id, campaign_id, kind, object_id, prior_json, recorded_at, restored_at FROM campaign_mutations;

DROP TABLE campaign_mutations;

ALTER TABLE campaign_mutations_0013 RENAME TO campaign_mutations;

CREATE INDEX campaign_mutations_open_idx ON campaign_mutations(campaign_id) WHERE restored_at IS NULL;
