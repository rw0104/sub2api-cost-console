-- HOST-P1-08: make route fallback an explicit, persisted binding policy.
-- Empty/legacy rows are deliberately normalized to fail_closed so enabling
-- this column cannot widen routing behavior after an upgrade.
ALTER TABLE sub2api_plugin_bindings
    ADD COLUMN IF NOT EXISTS fallback_policy VARCHAR(32) NOT NULL DEFAULT 'fail_closed';

UPDATE sub2api_plugin_bindings
SET fallback_policy = 'fail_closed'
WHERE fallback_policy IS NULL OR fallback_policy = '';

ALTER TABLE sub2api_plugin_bindings
    DROP CONSTRAINT IF EXISTS sub2api_plugin_bindings_fallback_policy_check;

ALTER TABLE sub2api_plugin_bindings
    ADD CONSTRAINT sub2api_plugin_bindings_fallback_policy_check
    CHECK (fallback_policy IN ('fail_closed', 'next_plugin', 'builtin'));
