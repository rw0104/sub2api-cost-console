package service

import (
	"context"
	"net/http"
	"testing"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestPluginFallbackPolicyDefaultsClosedAndRequiresManifestOptIn(t *testing.T) {
	var binding PluginBinding
	require.Equal(t, PluginFallbackPolicyFailClosed, binding.EffectiveFallbackPolicy())
	require.NoError(t, binding.EffectiveFallbackPolicy().Validate())
	require.Error(t, PluginFallbackPolicy("unknown").Validate())

	capability := PluginCapability{ID: pluginv2.CapabilityRequestPreprocess}
	require.NoError(t, capability.ValidateFallbackPolicy(PluginFallbackPolicyFailClosed))
	require.Error(t, capability.ValidateFallbackPolicy(PluginFallbackPolicyNextPlugin))
	capability.FallbackPolicies = []PluginFallbackPolicy{PluginFallbackPolicyNextPlugin, PluginFallbackPolicyBuiltin}
	require.NoError(t, capability.ValidateFallbackPolicy(PluginFallbackPolicyNextPlugin))
	require.NoError(t, capability.ValidateFallbackPolicy(PluginFallbackPolicyBuiltin))
}

func TestPluginRouteCandidateCarriesFallbackPolicy(t *testing.T) {
	route := &extensionRoute{
		pluginID:   9,
		capability: PluginCapability{ID: pluginv2.CapabilityRequestPreprocess},
		binding: PluginBinding{
			ID:             4,
			Priority:       20,
			FallbackPolicy: PluginFallbackPolicyNextPlugin,
			Enabled:        true,
		},
	}
	candidate := route.Candidate()
	require.EqualValues(t, 9, candidate.PluginID)
	require.EqualValues(t, 4, candidate.BindingID)
	require.Equal(t, PluginFallbackPolicyNextPlugin, candidate.FallbackPolicy)
	require.False(t, candidate.RuntimeAvailable)
}

func TestPreprocessNextPluginFallbackRunsOnlyBeforeUpstreamSend(t *testing.T) {
	cap := testPreprocessCapability()
	cap.Permissions = []pluginv2.Permission{pluginv2.PermissionRequestMetadata}
	cap.FallbackPolicies = []PluginFallbackPolicy{PluginFallbackPolicyNextPlugin}
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	add := func(id int64, priority int, handler pluginv2.ExtensionHandler) {
		installation := &PluginInstallation{ID: id, Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{cap}},
			Bindings: []PluginBinding{{ID: id, Capability: cap.ID, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true,
				RolloutPercent: 100, Priority: priority, AccountIDs: []int64{10}, FallbackPolicy: PluginFallbackPolicyNextPlugin}}}
		manager.publishRuntimeLocked(installation, &pluginRuntime{installation: installation, extension: handler, done: make(chan struct{})})
	}
	add(1, 20, &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{}, context.DeadlineExceeded
	}})
	add(2, 10, &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionPass}, nil
	}})
	request, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)
	account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	request.Body = http.NoBody
	out, err := manager.PreprocessOpenAI(context.Background(), request, account)
	require.NoError(t, err)
	require.Same(t, request, out)
	routes := manager.extensions.Load().routes
	require.Len(t, routes, 2)
	require.EqualValues(t, 1, routes[0].calls.total.Load())
	require.EqualValues(t, 1, routes[1].calls.total.Load())
}
