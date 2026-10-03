package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIConfiguredCodexModelIDsIdentityWhitelistDefersToOfficialManifest(t *testing.T) {
	identity := map[string]any{"model_mapping": map[string]any{
		"gpt-6.1-sol":          "gpt-6.1-sol",
		"gpt-5.2-2025-12-11":   "gpt-5.2-2025-12-11",
		"gpt-4o-audio-preview": "gpt-4o-audio-preview",
	}}
	oauth := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: identity}

	// Only same-name entries on OAuth accounts: no local catalog.
	require.Empty(t, openAIConfiguredCodexModelIDs([]Account{oauth, oauth}))
	require.Empty(t, openAIConfiguredCodexModelIDsForGroup([]Account{oauth}, &Group{Platform: PlatformOpenAI}))

	// A real alias keeps the whole configured catalog, whitelist included.
	alias := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{"team-default": "gpt-6.1-sol"},
	}}
	require.Equal(t,
		[]string{"gpt-4o-audio-preview", "gpt-5.2-2025-12-11", "gpt-6.1-sol", "team-default"},
		openAIConfiguredCodexModelIDs([]Account{oauth, alias}),
	)

	// API key upstreams have no official manifest; their list stays configured.
	apiKey := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: identity}
	require.Len(t, openAIConfiguredCodexModelIDs([]Account{apiKey}), 3)

	// Shadow accounts scope a quota dimension with a same-name mapping.
	parentID := int64(1)
	shadow := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, Credentials: map[string]any{
		"model_mapping": map[string]any{"gpt-5.3-codex-spark": "gpt-5.3-codex-spark"},
	}}
	require.Equal(t, []string{"gpt-5.3-codex-spark"}, openAIConfiguredCodexModelIDs([]Account{shadow}))
}
