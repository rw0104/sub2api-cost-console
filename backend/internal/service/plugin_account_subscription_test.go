package service

import (
	"context"
	"encoding/json"
	"testing"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestPluginAccountSubscriptionMixedAccounts(t *testing.T) {
	for i, plan := range []string{"plus", "pro", "team", "prolite", "self_serve_business_prolite", "enterprise", "edu", "free"} {
		account := &Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": plan, "chatgpt_account_id": "selected-workspace", "access_token": "never-access-token", "refresh_token": "never-refresh-token"}, Extra: map[string]any{"plan_type": "team", "private": "never-extra"}}
		hint := pluginAccountSubscription(account)
		require.Equal(t, pluginv2.NormalizeOpenAIPlanType(plan), hint.PlanType)
		require.Equal(t, "host_credentials", hint.Source)
		require.Equal(t, "selected-workspace", hint.WorkspaceID)
		raw, err := json.Marshal(hint)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "never-")
		// The legacy transport gets no newly exposed account metadata.
		require.Nil(t, (&pluginRuntime{}).protectionAccountMetadata(context.Background(), account))
	}
}

func TestPluginAccountSubscriptionFallbacksAndBoundary(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"plan_type": "Pro 5x"}}
	require.Equal(t, &pluginv2.AccountSubscription{PlanType: "pro_lite", Source: "host_extra"}, pluginAccountSubscription(account))
	account.Credentials = map[string]any{"plan_type": "unrecognized-secret-value"}
	require.Equal(t, "unknown", pluginAccountSubscription(account).PlanType)
	account.Credentials = map[string]any{"plan_type": 12}
	require.Equal(t, "pro_lite", pluginAccountSubscription(account).PlanType)
	account.Extra = nil
	require.Nil(t, pluginAccountSubscription(account))
	account.Type = AccountTypeAPIKey
	account.Credentials = map[string]any{"plan_type": "pro"}
	require.Nil(t, pluginAccountSubscription(account))
	account.Type = AccountTypeOAuth
	account.Platform = "anthropic"
	require.Nil(t, pluginAccountSubscription(account))
	require.Nil(t, pluginAccountSubscription(nil))
}
