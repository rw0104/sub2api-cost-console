package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPluginSandboxDefaultsAndValidation(t *testing.T) {
	defaults := PluginSandboxConfig{}.WithDefaults()
	require.Equal(t, "process", defaults.Mode)
	require.Equal(t, 256, defaults.MemoryMB)
	require.False(t, defaults.EgressBroker.Enabled)
	require.NoError(t, defaults.Validate())
	for _, invalid := range []PluginSandboxConfig{
		{Mode: "best_effort"}, {MemoryMB: -1}, {MemoryMB: 8192}, {CPUMilli: 50}, {CPUMilli: 8000}, {PidsLimit: 10}, {Image: "--privileged"},
	} {
		require.Error(t, invalid.Validate())
	}
	require.NoError(t, (PluginSandboxConfig{Mode: "container", MemoryMB: 64, CPUMilli: 100, PidsLimit: 32}).Validate())
}

func TestPluginSandboxEgressBrokerRequiresExplicitPolicy(t *testing.T) {
	valid := PluginSandboxConfig{Mode: "container", MemoryMB: 64, CPUMilli: 100, PidsLimit: 32,
		EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true, SocketPath: "/run/sub2api/egress.sock",
			AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}
	require.NoError(t, valid.Validate())
	require.True(t, valid.WithDefaults().EgressBroker.RequireTLS)

	for _, invalid := range []PluginSandboxConfig{
		{Mode: "container", EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true}},
		{Mode: "container", EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true, SocketPath: "relative.sock", AllowedHosts: []string{"api.openai.com"}}},
		{Mode: "container", EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true, SocketPath: "/run/egress.sock", AllowedHosts: []string{"*"}}},
		{Mode: "container", EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true, SocketPath: "/run/egress.sock", AllowedHosts: []string{"127.0.0.1"}}},
		{Mode: "container", EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true, SocketPath: "/run/egress.sock", AllowedHosts: []string{"api.openai.com"}, RequireTLS: false}},
		{Mode: "container", EgressBroker: PluginSandboxEgressBrokerConfig{SocketPath: "/run/egress.sock"}},
		{Mode: "process", EgressBroker: PluginSandboxEgressBrokerConfig{Enabled: true, SocketPath: "/run/egress.sock", AllowedHosts: []string{"api.openai.com"}}},
	} {
		require.Error(t, invalid.Validate())
	}
}
