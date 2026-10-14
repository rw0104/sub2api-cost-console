//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// upstreamV0216Migrations are the files upstream Sub2API v0.2.16 added. They
// reuse numbers 242-245, which this fork already published for its plugin
// tables, so the runner has to apply them by filename rather than by any
// notion of a schema version watermark.
var upstreamV0216Migrations = []string{
	"242_group_i18n.sql",
	"243_gemini_cached_contents.sql",
	"244_gemini_cached_contents_channel_usage.sql",
	"245_gemini_cached_contents_mutation_lease.sql",
}

// forkPluginMigrations share numbers with the upstream files above and are
// already applied on every installed v0.3.24 database.
var forkPluginMigrations = []string{
	"242_plugin_publishers.sql",
	"243_plugin_config_recovery.sql",
	"244_plugin_revision_operations.sql",
	"245_plugin_route_fallback_policy.sql",
	"246_plugin_v1_route_table.sql",
	"247_plugin_route_table.sql",
}

// TestMigrationsRunner_UpgradesFromDesktopV0324WithoutRenumbering proves the
// decision to keep both the upstream and the fork filenames unchanged: a
// database that already ran the v0.3.24 set picks up exactly the four upstream
// files, the fork's same-numbered plugin migrations stay untouched, and a
// second start is a no-op.
func TestMigrationsRunner_UpgradesFromDesktopV0324WithoutRenumbering(t *testing.T) {
	ctx := context.Background()
	db := newScratchMigrationDatabase(t, "upgrade_v0324")

	// 1) Reproduce a shipped v0.3.24 database: the full set minus the files
	// upstream added in v0.2.16.
	require.NoError(t, applyMigrationsFS(ctx, db, migrationsFSWithout(t, upstreamV0216Migrations...)))

	before := appliedMigrationChecksums(t, db)
	for _, name := range upstreamV0216Migrations {
		require.NotContainsf(t, before, name, "%s must not be applied before the upgrade", name)
	}
	for _, name := range forkPluginMigrations {
		require.Containsf(t, before, name, "the v0.3.24 baseline must already contain %s", name)
	}

	// 2) Start the v0.2.16 core: only the four upstream files are pending.
	require.NoError(t, ApplyMigrations(ctx, db))

	after := appliedMigrationChecksums(t, db)
	require.ElementsMatch(t, upstreamV0216Migrations, addedMigrations(before, after),
		"the upgrade must apply exactly the upstream v0.2.16 migrations")
	for _, name := range forkPluginMigrations {
		require.Equalf(t, before[name], after[name], "%s must not be re-applied or rewritten", name)
	}

	// The upstream schema is actually present, not just recorded.
	requireColumn(t, scratchTx(t, db), "groups", "i18n", "jsonb", 0, false)
	requireColumnDefaultContains(t, scratchTx(t, db), "groups", "i18n", "{}")
	for _, column := range []string{"channel_usage", "mutation_token", "mutation_lease_until"} {
		requireTableColumnExists(t, db, "gemini_cached_contents", column)
	}
	// The fork tables that share numbers 242-247 with upstream are intact.
	requireTableColumnExists(t, db, "sub2api_plugin_publishers", "key_id")
	requireTableColumnExists(t, db, "sub2api_plugin_bindings", "fallback_policy")
	requireTableColumnExists(t, db, "sub2api_plugin_routes", "plugin_id")

	// 3) A restart applies nothing and keeps every checksum stable.
	require.NoError(t, ApplyMigrations(ctx, db))
	require.Equal(t, after, appliedMigrationChecksums(t, db), "a restart must be a no-op")
}

// TestMigrationsRunner_AppliesTheMergedSetToAnEmptyDatabase covers a fresh
// install: the upstream and fork files interleave in filename order, so the
// same-numbered files must not depend on each other.
func TestMigrationsRunner_AppliesTheMergedSetToAnEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	db := newScratchMigrationDatabase(t, "fresh_v0216")

	require.NoError(t, ApplyMigrations(ctx, db))

	applied := appliedMigrationChecksums(t, db)
	for _, name := range append(append([]string{}, upstreamV0216Migrations...), forkPluginMigrations...) {
		require.Containsf(t, applied, name, "a fresh install must apply %s", name)
	}
	requireColumn(t, scratchTx(t, db), "groups", "i18n", "jsonb", 0, false)
	requireTableColumnExists(t, db, "gemini_cached_contents", "mutation_lease_until")
	requireTableColumnExists(t, db, "sub2api_plugin_routes", "plugin_id")
}

// migrationsFSWithout returns the embedded migration set minus the named files.
func migrationsFSWithout(t *testing.T, exclude ...string) fs.FS {
	t.Helper()

	excluded := make(map[string]struct{}, len(exclude))
	for _, name := range exclude {
		excluded[name] = struct{}{}
	}

	names, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err, "list embedded migrations")
	require.NotEmpty(t, names, "the embedded migration set must not be empty")

	filtered := make(fstest.MapFS, len(names))
	for _, name := range names {
		if _, skip := excluded[path.Base(name)]; skip {
			delete(excluded, path.Base(name))
			continue
		}
		content, err := fs.ReadFile(migrations.FS, name)
		require.NoErrorf(t, err, "read embedded migration %s", name)
		filtered[name] = &fstest.MapFile{Data: content}
	}
	require.Emptyf(t, excluded, "migrations to exclude are missing from the embedded set: %v", excluded)
	return filtered
}

// newScratchMigrationDatabase creates an empty database on the harness server
// so a migration history can be rebuilt without touching the shared one.
func newScratchMigrationDatabase(t *testing.T, purpose string) *sql.DB {
	t.Helper()
	require.NotEmpty(t, integrationDSN, "the integration harness must publish its DSN")

	name := fmt.Sprintf("sub2api_mig_%s_%d", purpose, time.Now().UnixNano())
	require.Regexp(t, `^[a-z0-9_]+$`, name)

	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name))
	require.NoErrorf(t, err, "create scratch database %s", name)

	parsed, err := url.Parse(integrationDSN)
	require.NoError(t, err, "parse harness DSN")
	parsed.Path = "/" + name

	db, err := openSQLWithRetry(ctx, parsed.String(), 30*time.Second)
	require.NoErrorf(t, err, "open scratch database %s", name)

	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := integrationDB.ExecContext(dropCtx,
			"DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(name)+" WITH (FORCE)"); err != nil {
			t.Logf("failed to drop scratch database %s: %v", name, err)
		}
	})
	return db
}

func scratchTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err, "begin scratch tx")
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// appliedMigrationChecksums maps every recorded migration to its checksum so a
// re-applied or rewritten file is visible, not just a missing one.
func appliedMigrationChecksums(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), "SELECT filename, checksum FROM schema_migrations")
	require.NoError(t, err, "read schema_migrations")
	defer func() { _ = rows.Close() }()

	applied := make(map[string]string)
	for rows.Next() {
		var filename, checksum string
		require.NoError(t, rows.Scan(&filename, &checksum), "scan schema_migrations row")
		applied[filename] = checksum
	}
	require.NoError(t, rows.Err(), "iterate schema_migrations")
	return applied
}

func addedMigrations(before, after map[string]string) []string {
	added := make([]string, 0, len(after)-len(before))
	for name := range after {
		if _, existed := before[name]; !existed {
			added = append(added, name)
		}
	}
	return added
}

func requireTableColumnExists(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()

	var exists bool
	err := db.QueryRowContext(context.Background(), `
SELECT EXISTS (
	SELECT 1
	FROM information_schema.columns
	WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
)
`, strings.ToLower(table), strings.ToLower(column)).Scan(&exists)
	require.NoErrorf(t, err, "query %s.%s", table, column)
	require.Truef(t, exists, "expected column %s.%s to exist", table, column)
}
