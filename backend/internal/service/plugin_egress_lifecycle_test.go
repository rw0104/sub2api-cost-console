package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestCreatePluginEgressOwnerRequiresProtectionCapability(t *testing.T) {
	manager := &PluginManager{installer: NewPluginPackageInstaller(testPluginConfig(filepath.Join(t.TempDir(), "plugins"), true), PluginHostInfo{Version: "test"})}
	sandbox := config.PluginSandboxConfig{Mode: "container", EgressBroker: config.PluginSandboxEgressBrokerConfig{Enabled: true,
		SocketPath: "/ignored.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}
	installation := &PluginInstallation{PluginKey: "example.plugin", Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{testPreprocessCapability()}}}
	_, err := manager.createPluginEgressOwner(installation, sandbox, "example.plugin-1")
	require.ErrorContains(t, err, "protection transport capability")
}

func TestCreatePluginEgressOwnerUsesScopedUnixSocketAndCleansIt(t *testing.T) {
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(os.PathSeparator)
	shortRoot, err := os.MkdirTemp(volumeRoot, "s2e-")
	if err != nil {
		t.Skipf("short socket root unavailable: %v", err)
	}
	defer os.RemoveAll(shortRoot)
	cfg := testPluginConfig(filepath.Join(shortRoot, "plugins"), true)
	manager := &PluginManager{cfg: cfg, installer: NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "test"})}
	sandbox := config.PluginSandboxConfig{Mode: "container", EgressBroker: config.PluginSandboxEgressBrokerConfig{Enabled: true,
		SocketPath: "/ignored.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}
	installation := &PluginInstallation{PluginKey: "example.plugin", Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{protectionTestCapability()}},
		Bindings: []PluginBinding{{ID: 7, Capability: pluginv2.CapabilityProtectionTransport, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, AccountIDs: []int64{42}}}}
	owner, err := manager.createPluginEgressOwner(installation, sandbox, "example.plugin-1")
	if err != nil && os.PathSeparator == '\\' {
		t.Skipf("Unix sockets unavailable on this Windows environment: %v", err)
	}
	require.NoError(t, err)
	require.NotEmpty(t, filepath.Base(owner.SocketPath()))
	info, err := os.Stat(owner.SocketPath())
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSocket)
	require.NoError(t, owner.Close())
	_, err = os.Stat(owner.SocketPath())
	require.Error(t, err)
}

func TestPluginRuntimeKillClosesEgressOwner(t *testing.T) {
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(os.PathSeparator)
	shortRoot, err := os.MkdirTemp(volumeRoot, "s2e-")
	if err != nil {
		t.Skipf("short socket root unavailable: %v", err)
	}
	defer os.RemoveAll(shortRoot)
	manager := &PluginManager{installer: NewPluginPackageInstaller(testPluginConfig(filepath.Join(shortRoot, "plugins"), true), PluginHostInfo{Version: "test"})}
	sandbox := config.PluginSandboxConfig{Mode: "container", EgressBroker: config.PluginSandboxEgressBrokerConfig{Enabled: true,
		SocketPath: "/ignored.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}
	installation := &PluginInstallation{PluginKey: "example.plugin", Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{protectionTestCapability()}},
		Bindings: []PluginBinding{{ID: 7, Capability: pluginv2.CapabilityProtectionTransport, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, AccountIDs: []int64{42}}}}
	owner, err := manager.createPluginEgressOwner(installation, sandbox, "example.plugin-1")
	if err != nil {
		t.Skipf("Unix sockets unavailable on this Windows environment: %v", err)
	}
	runtime := &pluginRuntime{egressOwner: owner}
	runtime.kill()
	_, err = os.Stat(owner.SocketPath())
	require.Error(t, err)
}

func TestEgressRuntimeCompatibilityForcesOwnerReplacementOnScopeChange(t *testing.T) {
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(os.PathSeparator)
	shortRoot, err := os.MkdirTemp(volumeRoot, "s2e-")
	if err != nil {
		t.Skipf("short socket root unavailable: %v", err)
	}
	defer os.RemoveAll(shortRoot)
	cfg := testPluginConfig(filepath.Join(shortRoot, "plugins"), true)
	manager := &PluginManager{cfg: cfg, installer: NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "test"})}
	sandbox := config.PluginSandboxConfig{Mode: "container", EgressBroker: config.PluginSandboxEgressBrokerConfig{Enabled: true,
		SocketPath: "/ignored.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}
	manager.cfg.Plugins.V2Sandbox = sandbox
	installation := &PluginInstallation{PluginKey: "example.plugin", Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{protectionTestCapability()}},
		Bindings: []PluginBinding{{ID: 7, Capability: pluginv2.CapabilityProtectionTransport, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, AccountIDs: []int64{42}}}}
	owner, err := manager.createPluginEgressOwner(installation, sandbox, "example.plugin-1")
	if err != nil {
		t.Skipf("Unix sockets unavailable on this Windows environment: %v", err)
	}
	defer owner.Close()
	runtime := &pluginRuntime{egressOwner: owner, egressPolicyDigest: pluginEgressPolicyDigest(sandbox.EgressBroker)}
	require.True(t, manager.egressRuntimeCompatible(runtime, installation))
	installation.Bindings[0].AccountIDs = []int64{99}
	require.False(t, manager.egressRuntimeCompatible(runtime, installation))
}
