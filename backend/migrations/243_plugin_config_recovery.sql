-- Only explicit administrator recovery writes an encrypted backup.
-- These are not silently pruned: restoring an old key may make them readable.
CREATE TABLE IF NOT EXISTS sub2api_plugin_config_backups (
    id BIGSERIAL PRIMARY KEY,
    plugin_id BIGINT NOT NULL REFERENCES sub2api_plugin_installations(id) ON DELETE CASCADE,
    binary_sha256 VARCHAR(64) NOT NULL,
    config_encrypted TEXT NOT NULL,
    recovered_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_plugin_config_backups_plugin
    ON sub2api_plugin_config_backups(plugin_id, id DESC);
