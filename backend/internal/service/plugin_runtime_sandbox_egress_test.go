package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestContainerProtectionTransportRequiresEgressBroker(t *testing.T) {
	installation := &PluginInstallation{Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{protectionTestCapability()}}}
	_, err := startPluginRuntimeWithSandboxAndHost(context.Background(), installation, time.Second, t.TempDir(), config.PluginSandboxConfig{Mode: "container"}, nil)
	require.ErrorContains(t, err, "egress broker")
}

func TestContainerProtectionTransportEgressBrokerRemainsFailClosedUntilDataPlaneExists(t *testing.T) {
	installation := &PluginInstallation{Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{protectionTestCapability()}}}
	sandbox := config.PluginSandboxConfig{Mode: "container", EgressBroker: config.PluginSandboxEgressBrokerConfig{
		Enabled: true, SocketPath: "/run/sub2api/egress.sock", AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true}}
	_, err := startPluginRuntimeWithSandboxAndHost(context.Background(), installation, time.Second, t.TempDir(), sandbox, nil)
	require.ErrorContains(t, err, "data plane is not implemented")
	// The check happens before checksum validation or Docker startup, so a
	// configured-but-unserved broker cannot accidentally open a network path.
	require.NotContains(t, err.Error(), "Docker")
}

func TestContainerPreprocessCanKeepNetworkDisabledWithBrokerPolicyOff(t *testing.T) {
	installation := &PluginInstallation{Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{testPreprocessCapability()}}}
	_, err := startPluginRuntimeWithSandboxAndHost(context.Background(), installation, time.Second, t.TempDir(), config.PluginSandboxConfig{Mode: "container"}, nil)
	require.Error(t, err)
	// The test intentionally reaches binary verification; no network capability
	// is implicitly enabled for ordinary preprocess hooks.
	require.NotContains(t, err.Error(), "egress broker")
}
