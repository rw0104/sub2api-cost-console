package service

import (
	"bytes"
	"context"
	"runtime"
	"testing"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func testExtensionManifest() PluginManifest {
	m := testPluginManifest(nil)
	m.SchemaVersion = 2
	m.Requires.PluginProtocol = 2
	m.Requires.TransportAPI = 0
	m.Requires.ExtensionAPI = 1
	m.Capabilities = []PluginCapability{testPreprocessCapability()}
	return m
}
func TestPluginExtensionManifestCompatibility(t *testing.T) {
	m := testExtensionManifest()
	require.NoError(t, m.Validate())
	require.True(t, EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.1.179"}).Compatible)
	for _, test := range []struct {
		name   string
		mutate func(*PluginManifest)
	}{
		{"unknown", func(m *PluginManifest) { m.Capabilities[0].ID = "event.sink.v1" }},
		{"network permission", func(m *PluginManifest) {
			m.Capabilities[0].Permissions = append(m.Capabilities[0].Permissions, pluginv2.PermissionNetworkOutbound)
		}},
		{"missing metadata", func(m *PluginManifest) { m.Capabilities[0].Permissions = nil }},
		{"async hook", func(m *PluginManifest) { m.Capabilities[0].Synchronous = false }},
		{"excessive deadline", func(m *PluginManifest) { m.Capabilities[0].TimeoutMS = 6000 }},
		{"wrong platform", func(m *PluginManifest) { m.Capabilities[0].Platform = "anthropic" }},
		{"missing API version", func(m *PluginManifest) { m.Requires.ExtensionAPI = 0 }},
		{"mixed protocols", func(m *PluginManifest) { m.Requires.TransportAPI = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := testExtensionManifest()
			test.mutate(&m)
			require.False(t, EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.1.179"}).Compatible)
		})
	}
}
func TestPluginExtensionUnknownCapabilityInstallsIncompatible(t *testing.T) {
	m := testExtensionManifest()
	m.Capabilities[0].ID = "future.extension.v1"
	installer := NewPluginPackageInstaller(testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.1.179"})
	installed, err := installer.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, m, nil, "", nil)), nil)
	require.NoError(t, err)
	require.Equal(t, PluginStateIncompatible, installed.State)
	require.Contains(t, installed.Compatibility.Message, "future.extension.v1")
}

func TestPluginContainerInstallerSelectsLinuxRuntime(t *testing.T) {
	m := testExtensionManifest()
	m.Runtimes = map[string]PluginRuntime{"linux-" + runtime.GOARCH: {Path: "bin/plugin"}}
	cfg := testPluginConfig(t.TempDir(), true)
	cfg.Plugins.V2Sandbox.Mode = "container"
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"})
	installed, err := installer.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, m, nil, "", nil)), nil)
	require.NoError(t, err)
	require.Equal(t, "linux-"+runtime.GOARCH, installer.runtimeKey(installed.Manifest))
	require.NoError(t, verifyLocalPluginBinary(installed, installer.RootDir(), installer.runtimeKey(installed.Manifest)))
}

type extensionInfoStub struct {
	pluginv2.ExtensionHandler
	info pluginv2.PluginInfo
}

func (s *extensionInfoStub) GetInfo(context.Context) (pluginv2.PluginInfo, error) { return s.info, nil }
func TestPluginExtensionRuntimeRejectsPermissionDrift(t *testing.T) {
	m := testExtensionManifest()
	i := &PluginInstallation{PluginKey: m.ID, Version: m.Version, Manifest: m}
	client := &extensionInfoStub{info: pluginv2.PluginInfo{PluginID: m.ID, PluginVersion: m.Version, ProtocolVersion: 2,
		Capabilities: []pluginv2.Capability{m.Capabilities[0].ExtensionCapability()}}}
	r := &pluginRuntime{installation: i}
	require.NoError(t, r.initializeAPI(context.Background(), client))
	client.info.Capabilities[0].Permissions = append(client.info.Capabilities[0].Permissions, pluginv2.PermissionSecretBroker)
	require.ErrorContains(t, r.initializeAPI(context.Background(), client), "权限")
}
