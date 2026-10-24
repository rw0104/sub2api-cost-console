ALTER TABLE sub2api_plugin_bindings
    ADD COLUMN IF NOT EXISTS priority INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS account_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS user_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS group_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS max_concurrency INTEGER NOT NULL DEFAULT 32,
    ADD COLUMN IF NOT EXISTS timeout_ms INTEGER NOT NULL DEFAULT 0;

ALTER TABLE sub2api_plugin_bindings
    ADD CONSTRAINT sub2api_plugin_bindings_priority_check CHECK (priority BETWEEN -1000 AND 1000),
    ADD CONSTRAINT sub2api_plugin_bindings_concurrency_check CHECK (max_concurrency BETWEEN 1 AND 256),
    ADD CONSTRAINT sub2api_plugin_bindings_timeout_check CHECK (timeout_ms BETWEEN 0 AND 5000),
    ADD CONSTRAINT sub2api_plugin_bindings_ids_check CHECK
        (jsonb_typeof(account_ids) = 'array' AND jsonb_typeof(user_ids) = 'array' AND jsonb_typeof(group_ids) = 'array');

-- v1 still has a single transport for each platform/account type. v2 hooks
-- choose a deterministic priority winner after evaluating finer scopes.
DROP INDEX IF EXISTS idx_sub2api_plugin_bindings_one_enabled_scope;
CREATE UNIQUE INDEX idx_sub2api_plugin_bindings_one_enabled_scope
    ON sub2api_plugin_bindings(capability, platform, account_type)
    WHERE enabled = TRUE AND capability = 'openai.oauth.outbound_transport.v1';
