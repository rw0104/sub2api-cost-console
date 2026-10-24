//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestPluginRepositorySecretGrantsEncryptScopeExpireAndRevoke(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	key := "local.test.secrets-" + time.Now().Format("150405.000000000")
	manifest := service.PluginManifest{ID: key, Name: "Secret test", Version: "0.1.0", SchemaVersion: 2,
		Capabilities: []service.PluginCapability{{ID: pluginv2.CapabilityRequestPreprocess, Kind: pluginv2.CapabilityKindHook, Platform: "openai", AccountType: "oauth",
			Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionSecretBroker}, TimeoutMS: 1000, Synchronous: true, FailureMode: pluginv2.FailureModeClosed}}}
	installation, err := repo.Install(ctx, &service.PluginInstallation{PluginKey: key, Name: manifest.Name, Version: manifest.Version, Manifest: manifest,
		ArtifactData: []byte("test"), ArtifactPath: "/tmp/secret-test.s2plugin", InstallPath: "/tmp/secret-test", BinaryPath: "/tmp/secret-test/plugin",
		BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted, State: service.PluginStateDisabled}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE id=$1", installation.ID)
	})
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("1a", 32)}}
	encryptor, err := NewAESEncryptor(cfg)
	require.NoError(t, err)
	manager := service.NewPluginManager(repo, encryptor, cfg, service.PluginHostInfo{Version: "0.1.179"})
	capability := pluginv2.CapabilityRequestPreprocess
	const marker = "private-broker-test-value"
	require.NoError(t, manager.PutSecretGrant(ctx, installation.ID, capability, "policy_key", marker, 60))
	stored, err := repo.GetSecretGrant(ctx, installation.ID, capability, "policy_key")
	require.NoError(t, err)
	require.NotContains(t, stored.EncryptedValue, marker)
	plain, err := encryptor.Decrypt(stored.EncryptedValue)
	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(plain), &envelope))
	require.Equal(t, "sub2api.plugin-secret.v1", envelope["purpose"])
	require.Equal(t, marker, envelope["value"])
	require.EqualValues(t, installation.ID, envelope["plugin_id"])
	grants, err := manager.ListSecretGrants(ctx, installation.ID)
	require.NoError(t, err)
	encoded, _ := json.Marshal(grants)
	require.NotContains(t, string(encoded), marker)
	require.NotContains(t, string(encoded), stored.EncryptedValue)
	_, err = repo.GetSecretGrant(ctx, installation.ID+1, capability, "policy_key")
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = repo.GetSecretGrant(ctx, installation.ID, "other.v1", "policy_key")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.Error(t, manager.PutSecretGrant(ctx, installation.ID, capability, "invalid_alias", "x", 3601))
	_, err = integrationDB.ExecContext(ctx, "UPDATE sub2api_plugin_secret_grants SET expires_at=NOW()-INTERVAL '1 second' WHERE plugin_id=$1", installation.ID)
	require.NoError(t, err)
	_, err = repo.GetSecretGrant(ctx, installation.ID, capability, "policy_key")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, repo.PruneSecretGrants(ctx))
	var retained int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sub2api_plugin_secret_grants WHERE plugin_id=$1", installation.ID).Scan(&retained))
	require.Zero(t, retained, "expired ciphertext must be removed, not just hidden")
	require.NoError(t, manager.PutSecretGrant(ctx, installation.ID, capability, "policy_key", marker, 60))
	require.NoError(t, manager.DeleteSecretGrant(ctx, installation.ID, capability, "policy_key"))
	_, err = repo.GetSecretGrant(ctx, installation.ID, capability, "policy_key")
	require.ErrorIs(t, err, sql.ErrNoRows)
	for i := 0; i < 32; i++ {
		require.NoError(t, manager.PutSecretGrant(ctx, installation.ID, capability, fmt.Sprintf("key_%02d", i), marker, 60))
	}
	require.ErrorContains(t, manager.PutSecretGrant(ctx, installation.ID, capability, "too_many", marker, 60), "32")
}
