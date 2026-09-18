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

func TestPluginRepositoryProtectionConfigCAS(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	key := "local.test.protection-cas-" + time.Now().Format("150405.000000000")
	p, err := repo.Install(ctx, &service.PluginInstallation{PluginKey: key, Name: "Protection CAS", Version: "1.0.0", Manifest: service.PluginManifest{ID: key, SchemaVersion: 2, Version: "1.0.0", Name: "Protection CAS"},
		ArtifactData: []byte("synthetic"), ArtifactPath: "/tmp/cas.s2plugin", InstallPath: "/tmp/cas", BinaryPath: "/tmp/cas/plugin", BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted, State: service.PluginStateDisabled}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE id=$1", p.ID)
	})
	require.NoError(t, repo.UpdateConfigCAS(ctx, p.ID, "cipher-initial", p.BinarySHA256, ""))
	gate := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, next := range []string{"cipher-a", "cipher-b"} {
		wg.Add(1)
		go func(next string) {
			defer wg.Done()
			<-gate
			results <- repo.UpdateConfigCAS(ctx, p.ID, next, p.BinarySHA256, "cipher-initial")
		}(next)
	}
	close(gate)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, service.ErrPluginStateChanged)
			conflicted++
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, conflicted)
	saved, err := repo.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.Contains(t, []string{"cipher-a", "cipher-b"}, saved.ConfigEncrypted)
	require.ErrorIs(t, repo.UpdateConfigCAS(ctx, p.ID, "stale", p.BinarySHA256, "cipher-initial"), service.ErrPluginStateChanged)
	require.ErrorIs(t, repo.UpdateConfigCAS(ctx, p.ID, "wrong-version", strings.Repeat("b", 64), saved.ConfigEncrypted), service.ErrPluginStateChanged)
}
