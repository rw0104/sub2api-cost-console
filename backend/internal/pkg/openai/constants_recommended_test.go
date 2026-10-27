package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecommendedModelIDsMatchOfficialPickerAndKeepCompatibilityDefaults(t *testing.T) {
	want := []string{
		"gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-sol", "gpt-6-luna",
		"gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-5.6-sol",
	}
	require.Equal(t, want, RecommendedModelIDs())
	for _, id := range want {
		require.Contains(t, DefaultModelIDs(), id)
	}
	require.Equal(t, "gpt-5.6-sol", DefaultModels[0].ID)
	require.Contains(t, DefaultModelIDs(), "gpt-6")
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
	modified := RecommendedModelIDs()
	modified[0] = "changed"
	require.Equal(t, want, RecommendedModelIDs())
}

func TestGPT61TerraModelIdentity(t *testing.T) {
	for _, model := range []string{"gpt-6.1-terra", "openai/GPT_6.1_TERRA", "gpt-6.1-terra-none", "gpt-6.1-terra-max", "gpt-6.1-terra-openai-compact"} {
		require.True(t, IsGPT61TerraModelSpelling(model), model)
		require.False(t, IsGPT61SolModelSpelling(model), model)
	}
	for _, model := range []string{"gpt-6.1", "gpt-6.1-terrain", "gpt-6.1-terra-preview", "gpt-5.6-terra", "gpt-6.1-sol"} {
		require.False(t, IsGPT61TerraModelSpelling(model), model)
	}
}
