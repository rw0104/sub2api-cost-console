//go:build integration

package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestPluginRepositoryLifecycleIsAtomicAndOptimistic(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	pluginKey := "local.test.repository-" + strings.ToLower(time.Now().Format("150405.000000000"))
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM sub2api_plugin_installations WHERE plugin_key = $1`, pluginKey)
	}()

	manifest := service.PluginManifest{SchemaVersion: 1, ID: pluginKey, Name: "测试插件", Version: "1.0.0"}
	first := &service.PluginInstallation{
		PluginKey: pluginKey, Name: "测试插件", Version: "1.0.0", Manifest: manifest,
		ArtifactData: []byte("first-package"), ArtifactPath: "/tmp/first.s2plugin",
		InstallPath: "/tmp/first", BinaryPath: "/tmp/first/plugin",
		BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted,
		State: service.PluginStateDisabled,
	}
	bindings := []service.PluginBinding{{
		Capability: service.PluginCapabilityOpenAIOAuthOutbound,
		Platform:   service.PlatformOpenAI, AccountType: service.AccountTypeOAuth,
		RolloutPercent: 100,
	}}
	installed, err := repo.Install(ctx, first, bindings)
	require.NoError(t, err)
	artifact, err := repo.GetArtifact(ctx, installed.ID)
	require.NoError(t, err)
	require.Equal(t, first.ArtifactData, artifact)

	require.NoError(t, repo.BeginEnable(ctx, installed.ID, first.BinarySHA256, service.PluginStateDisabled))
	bindings[0].Enabled = true
	now := time.Now()
	require.NoError(t, repo.UpdateBindingsAndState(
		ctx, installed.ID, bindings, service.PluginStateEnabled, "", &now,
		service.PluginStateStarting, first.BinarySHA256,
	))

	second := *first
	second.Version = "1.1.0"
	second.Manifest.Version = second.Version
	second.BinarySHA256 = strings.Repeat("b", 64)
	second.ArtifactData = []byte("second-package")
	_, err = repo.Install(ctx, &second, bindings)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)

	bindings[0].Enabled = false
	require.NoError(t, repo.UpdateBindingsAndState(
		ctx, installed.ID, bindings, service.PluginStateDisabled, "", nil, "", first.BinarySHA256,
	))
	replaced, err := repo.Install(ctx, &second, bindings)
	require.NoError(t, err)
	require.Equal(t, installed.ID, replaced.ID)

	err = repo.Delete(ctx, replaced.ID, first.BinarySHA256)
	require.True(t, errors.Is(err, service.ErrPluginStateChanged))
	require.NoError(t, repo.Delete(ctx, replaced.ID, second.BinarySHA256))
}

func TestPluginRepositoryV2MetadataAndScopeIsolation(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	prefix := "local.test.extension-" + strings.ToLower(time.Now().Format("150405.000000000"))
	install := func(suffix, capability string) *service.PluginInstallation {
		t.Helper()
		manifest := service.PluginManifest{SchemaVersion: 2, ID: prefix + "." + suffix, Name: "Extension", Version: "0.1.0",
			Requires: service.PluginRequirements{PluginProtocol: 2, ExtensionAPI: 1, UIBridge: 1, Sub2API: ">=0.1.179"},
			Capabilities: []service.PluginCapability{{ID: capability, Platform: "openai", AccountType: "oauth", Kind: pluginv2.CapabilityKindHook,
				TimeoutMS: 200, FailureMode: pluginv2.FailureModeClosed, Synchronous: true,
				Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionRequestBody}}}}
		p, err := repo.Install(ctx, &service.PluginInstallation{PluginKey: manifest.ID, Name: manifest.Name, Version: manifest.Version, Manifest: manifest,
			ArtifactData: []byte("signed-package"), ArtifactPath: "/tmp/test.s2plugin", InstallPath: "/tmp/test", BinaryPath: "/tmp/test/plugin",
			BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted, State: service.PluginStateDisabled},
			[]service.PluginBinding{{Capability: capability, Platform: "openai", AccountType: "oauth", RolloutPercent: 25}})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE id = $1", p.ID)
		})
		require.Equal(t, manifest.Capabilities, p.Manifest.Capabilities)
		require.Equal(t, 1, p.Manifest.Requires.ExtensionAPI)
		return p
	}
	enable := func(p *service.PluginInstallation) error {
		if err := repo.BeginEnable(ctx, p.ID, p.BinarySHA256, service.PluginStateDisabled); err != nil {
			return err
		}
		bindings := append([]service.PluginBinding(nil), p.Bindings...)
		bindings[0].Enabled = true
		now := time.Now()
		return repo.UpdateBindingsAndState(ctx, p.ID, bindings, service.PluginStateEnabled, "", &now, service.PluginStateStarting, p.BinarySHA256)
	}
	transport := install("transport", service.PluginCapabilityOpenAIOAuthOutbound)
	extension := install("extension", pluginv2.CapabilityRequestPreprocess)
	conflict := install("conflict", pluginv2.CapabilityRequestPreprocess)
	require.NoError(t, enable(transport), "transport and preprocess occupy independent scopes")
	require.NoError(t, enable(extension))
	require.NoError(t, enable(conflict), "v2 hooks may overlap; routing chooses a deterministic priority winner")
	stored, err := repo.GetByID(ctx, conflict.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateEnabled, stored.State)
	require.True(t, stored.Bindings[0].Enabled)
	transportConflict := install("transport-conflict", service.PluginCapabilityOpenAIOAuthOutbound)
	require.Error(t, enable(transportConflict), "v1 transport scopes remain exclusive")
	conflicted, err := repo.GetByID(ctx, transportConflict.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateStarting, conflicted.State)
	require.False(t, conflicted.Bindings[0].Enabled, "the conflicting transaction must roll back")
	current, err := repo.GetByID(ctx, extension.ID)
	require.NoError(t, err)
	require.True(t, current.Bindings[0].Enabled)
	require.Equal(t, 25, current.Bindings[0].RolloutPercent)
}
