package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludeBareGPT56Alias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
}

func TestDefaultModelsIncludeGPT6Astra(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-6-astra")
	require.Contains(t, DefaultModelIDs(), "gpt-6")
	var displayName string
	for _, model := range DefaultModels {
		if model.ID == "gpt-6-astra" {
			displayName = model.DisplayName
			break
		}
	}
	require.Equal(t, "GPT-6 Astra", displayName)
}

func TestDefaultModelsPreferConcreteGPT56SolForAccountTests(t *testing.T) {
	require.NotEmpty(t, DefaultModels)
	require.Equal(t, "gpt-5.6-sol", DefaultModels[0].ID)
}

func TestDefaultModelsIncludeGPTImage25(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-image-2.5-flare")
	require.Contains(t, DefaultModelIDs(), "gpt-image-2.5-sunburst")
}

func TestGPT6SolLunaModelIdentity(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		require.Contains(t, DefaultModelIDs(), model)
		require.True(t, IsGPT6SolOrLunaModelSpelling(model))
	}
	require.False(t, IsGPT6SolOrLunaModelSpelling("gpt-6-astra"))
	require.False(t, IsGPT6SolOrLunaModelSpelling("gpt-6-solitude"))
	require.False(t, IsGPT6SolOrLunaModelSpelling("gpt-6-luna-preview"))
}

func TestGPT61SolModelIdentity(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-6.1-sol")

	var displayName string
	for _, model := range DefaultModels {
		if model.ID == "gpt-6.1-sol" {
			displayName = model.DisplayName
			break
		}
	}
	require.Equal(t, "GPT-6.1 Sol", displayName)

	for _, model := range []string{
		"gpt-6.1-sol", "GPT-6.1-SOL", "openai/gpt-6.1-sol", "gpt-6.1sol",
		"gpt-6.1-sol-high", "gpt-6.1-sol-max", "gpt-6.1-sol-openai-compact",
	} {
		require.True(t, IsGPT61SolModelSpelling(model), model)
	}
	require.Equal(t, "gpt-6.1-sol", CanonicalizeOpenAIModelAliasSpelling("gpt-6.1sol"))

	// Keep gpt-6.1-sol out of the gpt-6-sol/luna family so it canonicalizes to
	// its own billing/catalog identity.
	require.False(t, IsGPT6SolOrLunaModelSpelling("gpt-6.1-sol"))
	require.False(t, IsGPT61SolModelSpelling("gpt-6-sol"))
	require.False(t, IsGPT61SolModelSpelling("gpt-6.1-solitude"))
	require.False(t, IsGPT61SolModelSpelling("gpt-6.1-sol-preview"))
}
