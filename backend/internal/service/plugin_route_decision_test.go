package service

import (
	"context"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRouteReturnsStableReasonCodes(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*extensionRoute)
		account      *Account
		ctx          context.Context
		requireScope bool
		candidates   int
		want         string
	}{
		{name: "no enabled binding", mutate: func(route *extensionRoute) { route.binding.Enabled = false }, candidates: 0, want: PluginRouteReasonNoEnabledBinding},
		{name: "platform", account: &Account{ID: 10, Platform: "anthropic", Type: AccountTypeOAuth}, candidates: 1, want: PluginRouteReasonPlatformMismatch},
		{name: "account type", account: &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, candidates: 1, want: PluginRouteReasonAccountTypeMismatch},
		{name: "rollout", mutate: func(route *extensionRoute) { route.binding.RolloutPercent = 0 }, candidates: 1, want: PluginRouteReasonRolloutExcluded},
		{name: "account scope", mutate: func(route *extensionRoute) { route.binding.AccountIDs = []int64{11} }, account: &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, candidates: 1, want: PluginRouteReasonAccountScopeExcluded},
		{name: "user scope", mutate: func(route *extensionRoute) { route.binding.UserIDs = []int64{7} }, ctx: WithPluginPrincipal(context.Background(), 8, 0), requireScope: true, candidates: 1, want: PluginRouteReasonUserScopeExcluded},
		{name: "group scope", mutate: func(route *extensionRoute) { route.binding.GroupIDs = []int64{9} }, ctx: WithPluginPrincipal(context.Background(), 7, 8), requireScope: true, candidates: 1, want: PluginRouteReasonGroupScopeExcluded},
		{name: "runtime", mutate: func(route *extensionRoute) { route.runtime = nil }, candidates: 1, want: PluginRouteReasonRuntimeUnavailable},
		{name: "circuit", mutate: func(route *extensionRoute) {
			route.calls.mu.Lock()
			route.calls.openUntil = time.Now().Add(time.Minute)
			route.calls.mu.Unlock()
		}, candidates: 1, want: PluginRouteReasonCircuitOpen},
		{name: "selected", candidates: 1, want: PluginRouteReasonSelected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, route := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
				return pluginv2.PreprocessResponse{}, nil
			}})
			if test.mutate != nil {
				test.mutate(route)
			}
			account := test.account
			if account == nil {
				account = &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			}
			ctx := test.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			decision := manager.EvaluateRoute(ctx, testPreprocessCapability().ID, account, test.requireScope)
			require.Equal(t, test.want, decision.Reason)
			require.Equal(t, test.want == PluginRouteReasonSelected, decision.Selected)
			require.Equal(t, test.candidates, decision.CandidateCount)
			if decision.Selected || test.want == PluginRouteReasonRuntimeUnavailable || test.want == PluginRouteReasonCircuitOpen {
				require.EqualValues(t, 1, decision.PluginID)
				require.Equal(t, testPreprocessCapability().ID, decision.Capability)
			}
		})
	}
}

func TestEvaluateRouteCanDeferPrincipalScope(t *testing.T) {
	manager, route := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{}, nil
	}})
	route.binding.UserIDs = []int64{7}
	account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	decision := manager.EvaluateRoute(context.Background(), testPreprocessCapability().ID, account, false)
	require.Equal(t, PluginRouteReasonSelected, decision.Reason)
	require.True(t, decision.Selected)
}
