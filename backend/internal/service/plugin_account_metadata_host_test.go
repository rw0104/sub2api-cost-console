package service

import (
	"context"
	"encoding/json"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
)

type scopedMetadataDirectoryStub struct {
	infos []PluginAccountInfo
	scope PluginAccountScope
}

func (s *scopedMetadataDirectoryStub) ListPluginAccounts(_ context.Context, args ...any) ([]PluginAccountInfo, error) {
	if len(args) == 3 {
		s.scope, _ = args[0].(PluginAccountScope)
	}
	return s.infos, nil
}

func (s *scopedMetadataDirectoryStub) ResolvePluginOutboundIdentityScoped(_ context.Context, scope PluginAccountScope, accountID int64) (*PluginOutboundIdentity, error) {
	s.scope = scope
	return nil, nil
}

func TestPluginHostServiceServer_ReturnsScopedStructuredAccountMetadata(t *testing.T) {
	metadata, err := json.Marshal(map[string]any{
		"subscription": map[string]string{
			"plan_type":    "pro",
			"source":       "host_credentials",
			"workspace_id": "workspace-a",
		},
		"safe": "value",
	})
	require.NoError(t, err)
	directory := &scopedMetadataDirectoryStub{infos: []PluginAccountInfo{{
		ID: 7, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Name: "selected",
		Status: StatusActive, Schedulable: true, MetadataJSON: metadata,
	}}}
	scope := newPluginAccountScope(pluginAccountScopeEntry{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, AccountIDs: []int64{7}})
	server := newPluginHostServiceServer("metadata.example", newFakePluginKVStore(), directory, scope)

	response, err := server.ListAccounts(context.Background(), &pluginv1.ListAccountsRequest{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
	require.NoError(t, err)
	require.Equal(t, []int64{7}, response.AccountIds, "legacy ids remain populated")
	require.Len(t, response.Accounts, 1)
	account := response.Accounts[0]
	require.Equal(t, int64(7), account.Id)
	require.Equal(t, PlatformOpenAI, account.Platform)
	require.Equal(t, AccountTypeOAuth, account.AccountType)
	require.Equal(t, "selected", account.Name)
	require.Equal(t, StatusActive, account.Status)
	require.True(t, account.Schedulable)
	asserted := map[string]any{}
	require.NoError(t, json.Unmarshal(account.MetadataJson, &asserted))
	require.Equal(t, "pro", asserted["subscription"].(map[string]any)["plan_type"])
	require.Equal(t, "host_credentials", asserted["subscription"].(map[string]any)["source"])
	require.Equal(t, "workspace-a", asserted["subscription"].(map[string]any)["workspace_id"])
	require.Equal(t, scope, directory.scope, "directory receives host binding scope")
}

func TestPluginHostServiceServer_MetadataDoesNotCreateCredentialChannel(t *testing.T) {
	directory := &scopedMetadataDirectoryStub{infos: []PluginAccountInfo{{
		ID: 3, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth,
		MetadataJSON: []byte(`{"subscription":{"plan_type":"unknown"}}`),
	}}}
	server := newPluginHostServiceServer("metadata.example", newFakePluginKVStore(), directory,
		newPluginAccountScope(pluginAccountScopeEntry{Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, AccountIDs: []int64{3}}))
	response, err := server.ResolveOutboundIdentity(context.Background(), &pluginv1.ResolveOutboundIdentityRequest{AccountId: 99})
	require.NoError(t, err)
	require.False(t, response.Found)
}

func TestAccountReadableSnapshotIncludesNormalizedSubscription(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type":          "ChatGPT Pro",
			"chatgpt_account_id": "workspace-a",
			"refresh_token":      "must-not-appear",
		},
		Extra: map[string]any{"plan_type": "free"},
	}
	raw := accountReadableSnapshotJSON(account)
	require.NotContains(t, string(raw), "must-not-appear")
	var decoded struct {
		Subscription map[string]string `json:"subscription"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, map[string]string{
		"plan_type":    "pro",
		"source":       "host_credentials",
		"workspace_id": "workspace-a",
	}, decoded.Subscription)
}
