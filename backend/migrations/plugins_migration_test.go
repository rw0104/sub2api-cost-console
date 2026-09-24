package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPluginsMigrationKeepsAccountSchemaUnchanged(t *testing.T) {
	content, err := FS.ReadFile("229_plugins.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS sub2api_plugin_installations")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS sub2api_plugin_bindings")
	require.Contains(t, sql, "config_encrypted TEXT NOT NULL DEFAULT ''")
	require.Contains(t, sql, "REFERENCES sub2api_plugin_installations(id)")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_sub2api_plugin_bindings_one_enabled_scope")
	require.Contains(t, sql, "WHERE enabled = TRUE")
	require.NotContains(t, sql, "CREATE TABLE IF NOT EXISTS plugin_installations")
	require.NotContains(t, sql, "CREATE TABLE IF NOT EXISTS plugin_bindings")
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE ACCOUNTS")
	require.NotContains(t, sql, "account_id")
}

func TestPluginArtifactMigrationSupportsExistingInstallations(t *testing.T) {
	content, err := FS.ReadFile("230_plugin_artifacts.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE sub2api_plugin_installations")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS artifact_data BYTEA")
	require.NotContains(t, strings.ToUpper(sql), "ALTER TABLE ACCOUNTS")
}

func TestPluginRevisionOperationMigrationIsIdempotentAndScoped(t *testing.T) {
	content, err := FS.ReadFile("244_plugin_revision_operations.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS sub2api_plugin_operations")
	require.Contains(t, sql, "operation_id VARCHAR(96) PRIMARY KEY")
	require.Contains(t, sql, "REFERENCES sub2api_plugin_installations(id) ON DELETE CASCADE")
	require.Contains(t, sql, "expected_revision BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_operations_active")
}

func TestPluginRouteFallbackPolicyMigrationDefaultsClosed(t *testing.T) {
	content, err := FS.ReadFile("245_plugin_route_fallback_policy.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS fallback_policy VARCHAR(32) NOT NULL DEFAULT 'fail_closed'")
	require.Contains(t, sql, "SET fallback_policy = 'fail_closed'")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS sub2api_plugin_bindings_fallback_policy_check")
	require.Contains(t, sql, "CHECK (fallback_policy IN ('fail_closed', 'next_plugin', 'builtin'))")
}

func TestPluginV1RouteTableMigrationRemovesGlobalEnabledUniqueness(t *testing.T) {
	content, err := FS.ReadFile("246_plugin_v1_route_table.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP INDEX IF EXISTS idx_sub2api_plugin_bindings_one_enabled_scope")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_bindings_enabled_scope_v2")
}
