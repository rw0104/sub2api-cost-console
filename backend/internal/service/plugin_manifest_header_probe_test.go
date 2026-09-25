package service

import (
	"testing"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestSupportedExtensionCapabilityHeaderProbeIsReadOnlyFailOpen(t *testing.T) {
	c := PluginCapability{
		ID: pluginv2.CapabilityRequestHeaderProbe, Kind: pluginv2.CapabilityKindHook,
		Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, TimeoutMS: 500,
		FailureMode: pluginv2.FailureModeOpen, Synchronous: true,
		Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionHeaderObservation},
	}
	require.NoError(t, supportedExtensionCapability(c))
	c.FailureMode = pluginv2.FailureModeClosed
	require.Error(t, supportedExtensionCapability(c))
}
