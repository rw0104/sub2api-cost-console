//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginRepositoryVersionSwapSnapshotsAndRejectsStaleState(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	key := "local.test.versions-" + time.Now().Format("150405.000000000")
	p := &service.PluginInstallation{PluginKey: key, Name: "Version test", Version: "0.1.0", State: service.PluginStateDisabled,
		Manifest:     service.PluginManifest{SchemaVersion: 2, ID: key, Version: "0.1.0"},
		ArtifactData: []byte("first-signed-package"), ArtifactPath: "/tmp/a.s2plugin", InstallPath: "/tmp/a", BinaryPath: "/tmp/a/plugin",
		BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted}
	current, err := repo.Install(ctx, p, []service.PluginBinding{{Capability: "request.preprocess.v1", Platform: "openai", AccountType: "oauth", RolloutPercent: 35}})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE id=$1", current.ID)
	})
	require.NoError(t, repo.BeginEnable(ctx, current.ID, current.BinarySHA256, service.PluginStateDisabled))
	bindings := append([]service.PluginBinding(nil), current.Bindings...)
	bindings[0].Enabled = true
	now := time.Now()
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, bindings, service.PluginStateEnabled, "", &now, service.PluginStateStarting, current.BinarySHA256))
	current, err = repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	stale := *current
	replacement := *current
	replacement.Version = "0.1.1"
	replacement.Manifest.Version = "0.1.1"
	replacement.BinarySHA256 = strings.Repeat("b", 64)
	replacement.ArtifactData = []byte("second-signed-package")
	replacement.ConfigEncrypted = "ENC:new-version-config"
	require.NoError(t, repo.UpdateConfig(ctx, current.ID, "ENC:old-version-config", current.BinarySHA256))
	_, err = repo.SwapVersion(ctx, &stale, &replacement)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	versions, err := repo.ListVersions(ctx, current.ID)
	require.NoError(t, err)
	require.Empty(t, versions, "failed CAS cannot create a history record")
	current, err = repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	swapped, err := repo.SwapVersion(ctx, current, &replacement)
	require.NoError(t, err)
	require.Equal(t, "0.1.1", swapped.Version)
	require.Equal(t, service.PluginStateEnabled, swapped.State)
	require.True(t, swapped.Bindings[0].Enabled)
	require.Equal(t, "ENC:new-version-config", swapped.ConfigEncrypted)
	require.Equal(t, 35, swapped.Bindings[0].RolloutPercent)
	versions, err = repo.ListVersions(ctx, current.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, "0.1.0", versions[0].Version)
	versionID := versions[0].ID
	require.WithinDuration(t, time.Now().Add(24*time.Hour), versions[0].ExpiresAt, 5*time.Second)
	snapshot, err := repo.GetVersion(ctx, current.ID, versions[0].ID)
	require.NoError(t, err)
	require.Equal(t, []byte("first-signed-package"), snapshot.ArtifactData)
	require.Equal(t, "ENC:old-version-config", snapshot.ConfigEncrypted)
	_, err = repo.GetVersion(ctx, current.ID+1, versions[0].ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "history belongs to one installation")
	_, err = integrationDB.ExecContext(ctx, "UPDATE sub2api_plugin_versions SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1", versions[0].ID)
	require.NoError(t, err)
	versions, err = repo.ListVersions(ctx, current.ID)
	require.NoError(t, err)
	require.Empty(t, versions)
	_, err = repo.GetVersion(ctx, current.ID, versionID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestPluginRepositoryRoutingPolicyRoundTripAndCAS(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	key := "local.test.routing-" + time.Now().Format("150405.000000000")
	p := &service.PluginInstallation{PluginKey: key, Name: "Routing test", Version: "0.1.0", State: service.PluginStateDisabled,
		Manifest: service.PluginManifest{SchemaVersion: 2, ID: key, Version: "0.1.0"}, ArtifactData: []byte("package"),
		ArtifactPath: "/tmp/routing.s2plugin", InstallPath: "/tmp/routing", BinaryPath: "/tmp/routing/plugin",
		BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted}
	current, err := repo.Install(ctx, p, []service.PluginBinding{{Capability: "request.preprocess.v1", Platform: "openai", AccountType: "oauth", RolloutPercent: 100}})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE id=$1", current.ID)
	})
	bindings := append([]service.PluginBinding(nil), current.Bindings...)
	bindings[0].Priority = 75
	bindings[0].RolloutPercent = 25
	bindings[0].AccountIDs = []int64{10, 20}
	bindings[0].UserIDs = []int64{7}
	bindings[0].GroupIDs = []int64{9}
	bindings[0].MaxConcurrency = 3
	bindings[0].TimeoutMS = 50
	// Force a wall-clock discontinuity; the policy revision must still advance.
	_, err = integrationDB.ExecContext(ctx, "UPDATE sub2api_plugin_installations SET updated_at=NOW()+INTERVAL '1 second' WHERE id=$1", current.ID)
	require.NoError(t, err)
	current, err = repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	saved, err := repo.UpdateRouting(ctx, current, bindings)
	require.NoError(t, err)
	require.True(t, saved.UpdatedAt.After(current.UpdatedAt))
	stored, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, saved.Bindings, stored.Bindings)
	require.Equal(t, 75, stored.Bindings[0].Priority)
	require.Equal(t, []int64{10, 20}, stored.Bindings[0].AccountIDs)
	require.Equal(t, []int64{7}, stored.Bindings[0].UserIDs)
	require.Equal(t, []int64{9}, stored.Bindings[0].GroupIDs)
	require.Equal(t, 3, stored.Bindings[0].MaxConcurrency)
	require.EqualValues(t, 50, stored.Bindings[0].TimeoutMS)
	_, err = repo.UpdateRouting(ctx, current, current.Bindings)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	unchanged, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, stored.Bindings, unchanged.Bindings)
}
