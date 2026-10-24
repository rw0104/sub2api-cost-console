package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

// TestCCodexSleepStateSignedPackageProcess exercises the real host installer,
// checksum verification, v2 handshake, Host API attachment, configuration
// application, health and test calls. It is opt-in because it needs a locally
// built signed package and must never use a production data directory.
func TestCCodexSleepStateSignedPackageProcess(t *testing.T) {
	packagePath := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PACKAGE")
	publicKey := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY")
	keyID := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_KEY_ID")
	if packagePath == "" || publicKey == "" || keyID == "" {
		t.Skip("set SUB2API_CCODEX_SLEEP_STATE_PACKAGE, SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY and SUB2API_CCODEX_SLEEP_STATE_KEY_ID")
	}
	archive, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	root := t.TempDir()
	cfg := testPluginConfig(filepath.Join(root, "plugins"), false)
	// The full Mihomo build carries three 47-51 MiB runtimes. Production uses
	// the 256 MiB default; keep this package integration aligned with it.
	cfg.Plugins.MaxUncompressedBytes = 256 * 1024 * 1024
	cfg.Plugins.TrustedPublishers[keyID] = publicKey
	installation, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.7"}).Install(context.Background(), bytes.NewReader(archive), nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
	require.True(t, installation.Compatibility.Compatible)
	require.FileExists(t, filepath.Join(installation.InstallPath, "ui", "index.html"))
	require.FileExists(t, filepath.Join(installation.InstallPath, "ui", "app.js"))
	require.FileExists(t, filepath.Join(installation.InstallPath, "ui", "style.css"))

	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, filepath.Join(root, "sockets"))
	require.NoError(t, err)
	defer runtime.kill()
	require.NotNil(t, runtime.transport)
	host := newPluginHostServices(installation, func(context.Context, string, string) (pluginv2.SecretValue, error) {
		return pluginv2.SecretValue{}, errors.New("secrets are not part of this test")
	})
	runtime.host = host
	defer host.Close()
	connector, ok := runtime.extension.(pluginv2.HostConnector)
	require.True(t, ok)
	require.NoError(t, connector.AttachHost(context.Background(), host))
	require.NotNil(t, runtime.host)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), []byte(`{"enabled":false,"models":["gpt-6-astra"],"cooldown_seconds":30}`)))
	require.NoError(t, runtime.checkHealth(context.Background()))
	tested, err := runtime.testConfig(context.Background(), []byte(`{"enabled":false,"models":["gpt-6-astra"],"cooldown_seconds":30}`))
	require.NoError(t, err)
	require.True(t, tested.Success)
	installation.ID = 99
	capability := installation.Manifest.Capabilities[0]
	installation.Bindings = []PluginBinding{{Capability: capability.ID, Platform: capability.Platform, AccountType: capability.AccountType,
		Enabled: true, RolloutPercent: 100, AccountIDs: []int64{1}}}
	manager := &PluginManager{}
	manager.publishExtensionRoutesLocked(installation, runtime, "")
	require.True(t, manager.hasProtectionTransport(&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.False(t, manager.hasProtectionTransport(&Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	stream, err := runtime.transport.Forward(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{
		RequestId: "host-integration", Method: "POST", Url: "http://invalid.example/responses", AccountId: 1,
		Platform: "openai", AccountType: "oauth", ContentLength: 0, HasBody: false,
	}}}))
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}))
	require.NoError(t, stream.CloseSend())
	response, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "INVALID_TARGET", response.GetError().Code)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://127.0.0.1:1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":[]}`))
	require.NoError(t, err)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	require.True(t, runtime.beginRequest())
	_, err = runtime.roundTrip(context.Background(), request, "", account)
	runtime.finishRequest()
	var localRefusal *PluginTransportError
	require.ErrorAs(t, err, &localRefusal)
	require.Equal(t, "PROTECTION_BUSY", localRefusal.Code)
	require.False(t, localRefusal.RequestSent, "local refusal must not masquerade as an upstream response")
	// The embedded upstream core rejects missing authentication before network
	// dispatch. Supply a synthetic credential for the separate closed-loopback
	// transport check; never borrow any real user account or call a model.
	request, err = http.NewRequestWithContext(context.Background(), http.MethodPost, "https://127.0.0.1:1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":[]}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer integration-fixture-credential")
	request.Header.Set("Content-Type", "application/json")
	require.True(t, runtime.beginRequest())
	_, err = runtime.roundTrip(context.Background(), request, "", account)
	runtime.finishRequest()
	var transportErr *PluginTransportError
	require.ErrorAs(t, err, &transportErr)
	require.True(t, transportErr.RequestSent)
}
