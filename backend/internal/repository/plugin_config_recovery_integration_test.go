//go:build integration

package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginConfigRecoveryBacksUpAtomicallyAndRejectsConcurrentOverwrite(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	p, err := repo.Install(ctx, publisherInstallation("local.recovery-"+time.Now().Format("150405.000000000")), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM sub2api_plugin_installations WHERE id=$1", p.ID) })
	require.NoError(t, repo.UpdateConfig(ctx, p.ID, "original-unreadable-cipher", p.BinarySHA256))
	current, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	// Force the UPDATE to fail after the backup INSERT. Both must roll back.
	_, err = integrationDB.Exec("ALTER TABLE sub2api_plugin_installations ADD CONSTRAINT recovery_test_reject CHECK(config_encrypted <> 'reject-new')")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("ALTER TABLE sub2api_plugin_installations DROP CONSTRAINT IF EXISTS recovery_test_reject")
	})
	require.Error(t, repo.RecoverConfig(ctx, current, "reject-new", nil))
	var count int
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM sub2api_plugin_config_backups WHERE plugin_id=$1", p.ID).Scan(&count))
	require.Zero(t, count)
	afterFailure, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, current.ConfigEncrypted, afterFailure.ConfigEncrypted)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, encrypted := range []string{"next-a", "next-b"} {
		wg.Add(1)
		go func(i int, encrypted string) {
			defer wg.Done()
			errs[i] = repo.RecoverConfig(ctx, current, encrypted, nil)
		}(i, encrypted)
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrPluginStateChanged)
		}
	}
	require.Equal(t, 1, success)
	var backup string
	require.NoError(t, integrationDB.QueryRow("SELECT config_encrypted FROM sub2api_plugin_config_backups WHERE plugin_id=$1", p.ID).Scan(&backup))
	require.Equal(t, "original-unreadable-cipher", backup)
	saved, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Contains(t, []string{"next-a", "next-b"}, saved.ConfigEncrypted)
	require.Equal(t, service.PluginStateDisabled, saved.State)
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM sub2api_plugin_config_backups WHERE plugin_id=$1", p.ID).Scan(&count))
	require.Equal(t, 1, count)
	stale := *saved
	stale.BinarySHA256 = strings.Repeat("b", 64)
	require.ErrorIs(t, repo.RecoverConfig(ctx, &stale, "stale", nil), service.ErrPluginStateChanged)
}

func TestPluginConfigRecoveryRequiresDisabledBindings(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	p, err := repo.Install(ctx, publisherInstallation("local.recovery-bindings-"+time.Now().Format("150405.000000000")),
		[]service.PluginBinding{{Capability: service.PluginCapabilityOpenAIOAuthOutbound, Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 100}})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM sub2api_plugin_installations WHERE id=$1", p.ID) })
	require.NoError(t, repo.UpdateConfig(ctx, p.ID, "old", p.BinarySHA256))
	current, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.RecoverConfig(ctx, current, "replacement", nil), service.ErrPluginStateChanged)
}
