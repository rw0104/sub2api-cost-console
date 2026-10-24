CREATE TABLE IF NOT EXISTS sub2api_plugin_secret_grants (
    plugin_id BIGINT NOT NULL REFERENCES sub2api_plugin_installations(id) ON DELETE CASCADE,
    capability VARCHAR(160) NOT NULL,
    alias VARCHAR(64) NOT NULL,
    value_encrypted TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id,capability,alias)
);
CREATE INDEX idx_sub2api_plugin_secret_grants_expiry ON sub2api_plugin_secret_grants(expires_at);
