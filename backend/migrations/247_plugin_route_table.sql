-- HOST-P1-08: persist one route representation for v1 and v2 bindings.
-- The route ID deliberately reuses the binding ID during the migration window
-- so diagnostics and existing UI binding references remain stable.
CREATE TABLE IF NOT EXISTS sub2api_plugin_routes (
    id BIGINT PRIMARY KEY REFERENCES sub2api_plugin_bindings(id) ON DELETE CASCADE,
    plugin_id BIGINT NOT NULL REFERENCES sub2api_plugin_installations(id) ON DELETE CASCADE,
    capability VARCHAR(160) NOT NULL,
    platform VARCHAR(32) NOT NULL,
    account_type VARCHAR(32) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    rollout_percent INTEGER NOT NULL DEFAULT 100,
    priority INTEGER NOT NULL DEFAULT 0,
    account_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    user_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    group_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    max_concurrency INTEGER NOT NULL DEFAULT 0,
    timeout_ms BIGINT NOT NULL DEFAULT 0,
    fallback_policy VARCHAR(32) NOT NULL DEFAULT 'fail_closed',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT sub2api_plugin_routes_rollout_check CHECK (rollout_percent BETWEEN 0 AND 100),
    CONSTRAINT sub2api_plugin_routes_fallback_policy_check
        CHECK (fallback_policy IN ('fail_closed', 'next_plugin', 'builtin')),
    CONSTRAINT sub2api_plugin_routes_scope_unique
        UNIQUE (plugin_id, capability, platform, account_type)
);

CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_routes_enabled_scope
    ON sub2api_plugin_routes(capability, platform, account_type, priority DESC, id)
    WHERE enabled = TRUE;

INSERT INTO sub2api_plugin_routes (
    id, plugin_id, capability, platform, account_type, enabled, rollout_percent, priority,
    account_ids, user_ids, group_ids, max_concurrency, timeout_ms, fallback_policy,
    created_at, updated_at
)
SELECT id, plugin_id, capability, platform, account_type, enabled, rollout_percent, priority,
       account_ids, user_ids, group_ids, max_concurrency, timeout_ms,
       COALESCE(NULLIF(fallback_policy, ''), 'fail_closed'), created_at, updated_at
FROM sub2api_plugin_bindings
ON CONFLICT (id) DO NOTHING;
