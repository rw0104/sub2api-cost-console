-- HOST-P1-08: v1 plugins use the same host route table as v2.
-- The old partial unique index serialized one enabled v1 route globally;
-- scope/priority selection now happens in the host route table instead.
DROP INDEX IF EXISTS idx_sub2api_plugin_bindings_one_enabled_scope;

CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_bindings_enabled_scope_v2
    ON sub2api_plugin_bindings(capability, platform, account_type, priority DESC, id)
    WHERE enabled = TRUE;
