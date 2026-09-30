package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeKnownOpenAICodexModelGPT6Astra(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "openai/gpt-6-astra", "OPENAI/GPT-6_ASTRA", "gpt-6", "openai/gpt-6"} {
		require.Equal(t, "gpt-6-astra", normalizeKnownOpenAICodexModel(model))
	}
}

func TestNormalizeKnownOpenAICodexModelGPT61Sol(t *testing.T) {
	for _, model := range []string{
		"gpt-6.1-sol", "openai/gpt-6.1-sol", "GPT-6.1-SOL", "gpt-6.1sol",
		"gpt-6.1-sol-max", "openai/gpt-6.1-sol-high", "gpt-6.1-sol-openai-compact",
	} {
		require.Equal(t, "gpt-6.1-sol", normalizeKnownOpenAICodexModel(model), model)
		require.True(t, isOpenAIGPT6Model(model), model)
	}
	// GPT-6.1 Sol must not collapse into the gpt-6-sol / gpt-6-luna identity.
	require.NotEqual(t, "gpt-6-sol", normalizeKnownOpenAICodexModel("gpt-6.1-sol"))
	require.NotEqual(t, "gpt-6-luna", normalizeKnownOpenAICodexModel("gpt-6.1-sol"))
}

func TestNormalizeKnownOpenAICodexModel_BareGPT56RoutesToSol(t *testing.T) {
	tests := map[string]string{
		"gpt-5.6":            "gpt-5.6-sol",
		"openai/gpt-5.6":     "gpt-5.6-sol",
		"gpt5.6":             "gpt-5.6-sol",
		"gpt-5.6-high":       "gpt-5.6-sol",
		"gpt-5.6-max":        "gpt-5.6-sol",
		"gpt-5.6-2026-07-09": "gpt-5.6-sol",
		"openai/gpt-5.6-max": "gpt-5.6-sol",
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, normalizeKnownOpenAICodexModel(input))
		})
	}
}

func TestUsageBillingModelCandidates_BareGPT56IncludesSol(t *testing.T) {
	require.Equal(t,
		[]string{"gpt-5.6", "gpt-5.6-sol"},
		usageBillingModelCandidates("gpt-5.6"),
	)
	require.Equal(t,
		[]string{"openai/gpt-5.6", "gpt-5.6", "gpt-5.6-sol"},
		usageBillingModelCandidates("openai/gpt-5.6"),
	)
}
