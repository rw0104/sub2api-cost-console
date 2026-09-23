package service

import (
	"strings"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

// pluginAccountSubscription only reads the selected account's persisted,
// non-secret plan fields. It never refreshes tokens, enumerates other accounts,
// or infers a subscription from names, state length, or organization presence.
func pluginAccountSubscription(account *Account) *pluginv2.AccountSubscription {
	if account == nil || account.ID <= 0 || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return nil
	}
	workspace := strings.TrimSpace(account.GetChatGPTAccountID())
	if len(workspace) > 256 {
		return nil
	}
	for _, source := range []struct {
		name   string
		fields map[string]any
	}{{"host_credentials", account.Credentials}, {"host_extra", account.Extra}} {
		for _, key := range []string{"plan_type", "chatgpt_plan_type", "subscription_tier"} {
			if raw, ok := source.fields[key].(string); ok && strings.TrimSpace(raw) != "" {
				return &pluginv2.AccountSubscription{PlanType: pluginv2.NormalizeOpenAIPlanType(raw), Source: source.name, WorkspaceID: workspace}
			}
		}
	}
	return nil
}
