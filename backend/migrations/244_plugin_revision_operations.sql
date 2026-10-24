-- Unified optimistic revision and durable operation journal for plugin writes.
-- Revision starts at one so an absent/legacy value cannot be mistaken for a
-- valid ETag.  Every control-plane mutation increments it atomically.
ALTER TABLE sub2api_plugin_installations
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;

UPDATE sub2api_plugin_installations
SET revision = 1
WHERE revision IS NULL OR revision < 1;

ALTER TABLE sub2api_plugin_installations
    ALTER COLUMN revision SET DEFAULT 1,
    ALTER COLUMN revision SET NOT NULL;

CREATE TABLE IF NOT EXISTS sub2api_plugin_operations (
    operation_id VARCHAR(96) PRIMARY KEY,
    plugin_id BIGINT NOT NULL REFERENCES sub2api_plugin_installations(id) ON DELETE CASCADE,
    operation_kind VARCHAR(48) NOT NULL,
    expected_revision BIGINT NOT NULL DEFAULT 0,
    target_revision BIGINT NOT NULL DEFAULT 0,
    stage VARCHAR(32) NOT NULL,
    result VARCHAR(32) NOT NULL DEFAULT '',
    error_code VARCHAR(96) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_operations_plugin
    ON sub2api_plugin_operations(plugin_id, created_at DESC, operation_id DESC);

CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_operations_active
    ON sub2api_plugin_operations(plugin_id, updated_at DESC)
    WHERE completed_at IS NULL;
