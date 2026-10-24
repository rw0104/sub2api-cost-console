package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func protectionTestCapability() PluginCapability {
	return PluginCapability{ID: pluginv2.CapabilityProtectionTransport, Kind: pluginv2.CapabilityKindProvider, Platform: "openai", AccountType: "oauth", TimeoutMS: 120000, FailureMode: pluginv2.FailureModeClosed, Synchronous: true,
		Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionRequestBody, pluginv2.PermissionCredentialsForward, pluginv2.PermissionNetworkOutbound, pluginv2.PermissionAccountProtection, pluginv2.PermissionOriginalRequest}}
}
func TestAccountProtectionCapabilityBoundary(t *testing.T) {
	c := protectionTestCapability()
	require.NoError(t, supportedExtensionCapability(c))
	for _, permission := range c.Permissions {
		next := c
		next.Permissions = nil
		for _, p := range c.Permissions {
			if p != permission {
				next.Permissions = append(next.Permissions, p)
			}
		}
		require.Error(t, supportedExtensionCapability(next))
	}
	c.Permissions = append(c.Permissions, pluginv2.PermissionSecretBroker)
	require.Error(t, supportedExtensionCapability(c))
	c = protectionTestCapability()
	c.FailureMode = pluginv2.FailureModeOpen
	require.Error(t, supportedExtensionCapability(c))
	// Existing preprocess does not gain credential or network access.
	c = testPreprocessCapability()
	c.Permissions = append(c.Permissions, pluginv2.PermissionCredentialsForward)
	require.Error(t, supportedExtensionCapability(c))
}
func TestAccountProtectionMetadataIsolation(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"secret": "never", "codex_fingerprint_seed": "seed", "anti_degrade": map[string]any{"credential": "never"}}}
	// A nil forward client represents the unchanged legacy path.
	runtime := &pluginRuntime{}
	ctx := withPluginProtectionOriginal(context.Background(), account, []byte(`{"model":"test","input":"hello"}`))
	require.Nil(t, runtime.protectionOriginalBody(ctx, account))
	require.Nil(t, runtime.protectionAccountMetadata(ctx, account))
	manager := &PluginManager{}
	cap := protectionTestCapability()
	manager.publishExtensionRoutesLocked(&PluginInstallation{ID: 1, Manifest: PluginManifest{Capabilities: []PluginCapability{cap}}, Bindings: []PluginBinding{{Capability: cap.ID, Enabled: true, Platform: "openai", AccountType: "oauth", RolloutPercent: 100, UserIDs: []int64{7}}}}, nil, "")
	require.True(t, manager.hasProtectionTransport(account))
	require.Nil(t, manager.protectionTransportRoute(context.Background(), account, true))
	require.NotNil(t, manager.protectionTransportRoute(WithPluginPrincipal(context.Background(), 7, 0), account, true))
}

type protectionMemoryRepository struct {
	*extensionMemoryRepository
	failSave bool
}

func (r *protectionMemoryRepository) UpdateConfigCAS(_ context.Context, _ int64, next, hash, old string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSave || r.row.BinarySHA256 != hash || r.row.ConfigEncrypted != old {
		return ErrPluginStateChanged
	}
	r.row.ConfigEncrypted = next
	r.row.UpdatedAt = time.Now()
	return nil
}

// The package is produced by plugins/account-protection/cmd/pack. No production
// service is started or modified; signature checks and RPC use temporary paths.
func TestAccountProtectionSignedPackageProcess(t *testing.T) {
	path := os.Getenv("SUB2API_ACCOUNT_PROTECTION_PACKAGE")
	if path == "" {
		t.Skip("set SUB2API_ACCOUNT_PROTECTION_PACKAGE to the built package")
	}
	public := os.Getenv("SUB2API_ACCOUNT_PROTECTION_PUBLIC_KEY")
	require.NotEmpty(t, public)
	archive, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg := testPluginConfig(filepath.Join(t.TempDir(), "plugins"), false)
	cfg.Plugins.TrustedPublishers["local-account-protection-v1"] = public
	installation, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.5"}).Install(context.Background(), bytes.NewReader(archive), nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
	require.True(t, installation.Compatibility.Compatible)
	installation.ID = 1
	cap := installation.Manifest.Capabilities[0]
	installation.Bindings = []PluginBinding{{Capability: cap.ID, Platform: cap.Platform, AccountType: cap.AccountType, Enabled: true, RolloutPercent: 100}}
	installation.ConfigEncrypted = `ENC:{"enabled":true,"mode":"mode1","seed":"d18288f2-9278-4d35-b8e0-19b3d7e163e0","revision":0}`
	repo := &protectionMemoryRepository{extensionMemoryRepository: &extensionMemoryRepository{row: cloneExtensionInstallation(installation)}}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.2.5"})
	process, err := manager.prepareRuntime(context.Background(), installation, true)
	require.NoError(t, err)
	defer process.kill()
	require.NotNil(t, process.host)
	require.NotNil(t, process.transport)
	require.NoError(t, process.checkHealth(context.Background()))
	manager.mu.Lock()
	manager.publishRuntimeLocked(installation, process)
	manager.mu.Unlock()
	// Save a normalized config, transition while enabled, then revert the persisted snapshot.
	raw, err := manager.SaveConfig(context.Background(), 1, []byte(`{"enabled":true,"mode":"mode1","integrity":"enforce","seed":"d18288f2-9278-4d35-b8e0-19b3d7e163e0","revision":0}`))
	require.NoError(t, err)
	var saved map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.EqualValues(t, 1, saved["revision"])
	raw, err = manager.SaveConfig(context.Background(), 1, []byte(`{"action":{"account_id":12,"operation":"apply","mode":"legacy","expected_revision":1}}`))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"previous"`)
	_, err = manager.SaveConfig(context.Background(), 1, []byte(`{"action":{"account_id":12,"operation":"revert","expected_revision":1}}`))
	require.Error(t, err)
	raw, err = manager.SaveConfig(context.Background(), 1, []byte(`{"action":{"account_id":12,"operation":"revert","expected_revision":2}}`))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.EqualValues(t, 3, saved["revision"])
	tested, testErr := manager.Test(context.Background(), 1)
	require.NoError(t, testErr)
	require.True(t, tested.Success, "Host API must return the same applied configuration")
	repo.failSave = true
	_, err = manager.SaveConfig(context.Background(), 1, []byte(`{"enabled":false,"revision":3}`))
	require.Error(t, err)
	repo.failSave = false
	require.Equal(t, string(raw), string(process.host.config.Load().data), "failed persistence restored the exact saved snapshot")
	// A semantic rejection must happen before reaching any upstream and must remain a local policy error.
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"unrelated_secret": "never"}}
	original := []byte(`{"model":"test","input":"keep context"}`)
	ctx := withPluginProtectionOriginal(context.Background(), account, original)
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/responses", strings.NewReader(`{"model":"test","input":"lost context"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer synthetic")
	_, handled, err := manager.RoundTripOpenAIOAuth(ctx, req, "", account)
	require.True(t, handled)
	var denied *PluginPreprocessError
	require.ErrorAs(t, err, &denied)
	require.True(t, denied.Denied)
	require.Zero(t, calls)
	for range 4 {
		repeated, makeErr := http.NewRequestWithContext(ctx, "POST", server.URL+"/responses", strings.NewReader(`{"model":"test","input":"lost context"}`))
		require.NoError(t, makeErr)
		_, _, deniedErr := manager.RoundTripOpenAIOAuth(ctx, repeated, "", account)
		require.ErrorAs(t, deniedErr, &denied)
		require.True(t, denied.Denied)
	}
	require.False(t, manager.extensionStatus(1)[0].CircuitOpen, "policy denials must not open the failure circuit")
	require.NotContains(t, string(process.protectionAccountMetadata(ctx, account)), "unrelated_secret")
	other := *account
	other.ID = 13
	require.Nil(t, process.protectionOriginalBody(ctx, &other))
	require.Eventually(t, func() bool { return process.host.Snapshot().EventsAccepted > 0 }, time.Second, 10*time.Millisecond)
	// Valid semantic input reaches TLS; the synthetic untrusted certificate is rejected, never bypassed.
	req, err = http.NewRequestWithContext(ctx, "POST", server.URL+"/responses", bytes.NewReader(original))
	require.NoError(t, err)
	_, handled, err = manager.RoundTripOpenAIOAuth(ctx, req, "", account)
	require.True(t, handled)
	var transportErr *PluginTransportError
	require.ErrorAs(t, err, &transportErr)
	require.True(t, transportErr.RequestSent)
	require.Zero(t, calls)
	require.NoError(t, process.checkHealth(context.Background()))
}
