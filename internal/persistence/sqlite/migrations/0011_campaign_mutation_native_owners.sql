-- Managed native lanes journal a managed_provider_owner obligation (an
-- original provider outcome still owed) and the native_owned_silence /
-- native_owned_inhibitor suppressions they create, restored by cleanup.
-- SQLite cannot alter a CHECK constraint: rebuild the table and keep every row, as 0010 did.
CREATE TABLE campaign_mutations_0011 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('notifier_disable', 'notifier_enable', 'policy_create', 'policy_activate', 'sink_enable', 'incident_close', 'queue_override', 'dead_notifier_create', 'fingerprint_policy_enable', 'managed_provider_owner', 'native_owned_silence', 'native_owned_inhibitor')),
    object_id TEXT NOT NULL,
    prior_json TEXT CHECK (prior_json IS NULL OR json_valid(prior_json)),
    recorded_at TEXT NOT NULL,
    restored_at TEXT
) STRICT;

INSERT INTO campaign_mutations_0011(id, campaign_id, kind, object_id, prior_json, recorded_at, restored_at)
    SELECT id, campaign_id, kind, object_id, prior_json, recorded_at, restored_at FROM campaign_mutations;

DROP TABLE campaign_mutations;

ALTER TABLE campaign_mutations_0011 RENAME TO campaign_mutations;

CREATE INDEX campaign_mutations_open_idx ON campaign_mutations(campaign_id) WHERE restored_at IS NULL;
