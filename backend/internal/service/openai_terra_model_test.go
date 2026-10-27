package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestOfficialPickerModelsKeepDistinctUpstreamIDs(t *testing.T) {
	for _, model := range openai.RecommendedModelIDs() {
		require.Equal(t, model, normalizeCodexModel(model), model)
		require.Equal(t, model, normalizeKnownOpenAICodexModel(model), model)
	}
	for _, model := range []string{"openai/GPT_6.1_TERRA", "gpt-6.1-terra-none", "gpt-6.1-terra-max", "gpt-6.1-terra-openai-compact"} {
		require.Equal(t, "gpt-6.1-terra", normalizeCodexModel(model), model)
	}
	for _, model := range []string{"gpt-6.1-terrain", "gpt-6.1-terra-preview", "gpt-6.2-terra"} {
		require.Equal(t, model, normalizeCodexModel(model), model)
		require.False(t, isOpenAIGPT6Model(model), model)
	}
}

func TestGPT61TerraCodexDescriptorUsesOwnOfficialCapabilities(t *testing.T) {
	descriptor := newConfiguredCodexModelDescriptor("gpt-6.1-terra")
	require.Equal(t, "GPT-6.1 Terra", descriptor.DisplayName)
	require.Equal(t, "medium", *descriptor.DefaultReasoningLevel)
	require.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, effortsFromConfiguredCodexLevels(descriptor.SupportedReasoningLevels))
	require.EqualValues(t, 1_050_000, descriptor.ContextWindow)
	require.EqualValues(t, 1_050_000, descriptor.MaxContextWindow)
	require.Nil(t, descriptor.MultiAgentReasoningEffort)
	require.Empty(t, descriptor.MultiAgentVersion)
	require.False(t, descriptor.UseResponsesLite)
	require.Empty(t, descriptor.officialMetadata)
	require.Equal(t, []configuredCodexServiceTier{{ID: OpenAIFastTierPriority, Name: "Fast", Description: "Priority processing for lower latency."}}, descriptor.ServiceTiers)
	manifest, err := BuildCodexModelsManifest([]string{"gpt-6.1-terra"})
	require.NoError(t, err)
	models := decodeCodexManifestModels(t, manifest)
	// Standalone custom-provider catalogs keep the existing conservative default.
	require.Equal(t, []any{"text"}, models[0]["input_modalities"])
}

func TestGPT61TerraCodexMetadataKeepsExplicitUpstreamCapabilities(t *testing.T) {
	svc := &OpenAIGatewayService{}
	manifest := &OpenAIModelsResponse{Body: []byte(`{"models":[{"slug":"gpt-6.1-terra","display_name":"Account Terra","context_window":500000,"max_context_window":900000,"supported_reasoning_levels":[{"effort":"high"}],"default_reasoning_level":"high","supports_search_tool":false,"multi_agent_version":"account-version"}]}`)}
	account := newCodexModelsAPIKeyTestAccount("https://api.openai.com/v1")
	require.NoError(t, svc.CompleteAPIKeyCodexModelsManifestForClient(manifest, account))
	models := decodeCodexManifestModels(t, manifest.Body)
	require.Equal(t, "Account Terra", models[0]["display_name"])
	require.EqualValues(t, 500_000, models[0]["context_window"])
	require.EqualValues(t, 900_000, models[0]["max_context_window"])
	require.Equal(t, []string{"high"}, effortsFromManifestModel(t, models[0]))
	require.Equal(t, "high", models[0]["default_reasoning_level"])
	require.Equal(t, false, models[0]["supports_search_tool"])
	require.Equal(t, "account-version", models[0]["multi_agent_version"])
	require.Equal(t, []any{"text", "image"}, models[0]["input_modalities"])
}

func TestGPT61TerraPricingUsesOwnRatesAcrossCatalogAndFallbacks(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	var catalog map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.Contains(t, catalog, "gpt-6.1-terra")
	parsed := &PricingService{}
	parsed.pricingData, err = parsed.parsePricingData(body)
	require.NoError(t, err)
	for name, pricingService := range map[string]*PricingService{
		"catalog":                 parsed,
		"empty_catalog":           {pricingData: map[string]*LiteLLMModelPricing{}},
		"without_pricing_service": nil,
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewBillingService(&config.Config{}, pricingService)
			pricing, err := svc.GetModelPricing("gpt-6.1-terra")
			require.NoError(t, err)
			require.InDelta(t, 1e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, 5e-6, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, 0.05e-6, pricing.CacheReadPricePerToken, 1e-12)
			require.InDelta(t, 1.25e-6, pricing.CacheCreationPricePerToken, 1e-12)
			for _, tier := range []struct {
				name  string
				scale float64
			}{{"", 1}, {"priority", 2}, {"flex", 0.5}} {
				for _, input := range []int{272_000, 272_001} {
					cost, err := svc.CalculateCostWithServiceTier("gpt-6.1-terra", UsageTokens{InputTokens: input - 2000, CacheReadTokens: 1000, CacheCreationTokens: 1000, OutputTokens: 10}, 1, tier.name)
					require.NoError(t, err)
					inputScale, outputScale := 1.0, 1.0
					if input > 272_000 {
						inputScale, outputScale = 2, 1.5
					}
					require.Equal(t, input > 272_000, cost.LongContextBillingApplied)
					require.InDelta(t, float64(input-2000)*1e-6*tier.scale*inputScale, cost.InputCost, 1e-12)
					require.InDelta(t, 1000*0.05e-6*tier.scale*inputScale, cost.CacheReadCost, 1e-12)
					require.InDelta(t, 1000*1.25e-6*tier.scale*inputScale, cost.CacheCreationCost, 1e-12)
					require.InDelta(t, 10*5e-6*tier.scale*outputScale, cost.OutputCost, 1e-12)
				}
			}
		})
	}
}

func TestGPT61TerraPricingRetainsExplicitCatalogOverrides(t *testing.T) {
	pricing := newStubPricingServiceFromJSON(t, `{"gpt-6.1-terra":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000007,"cache_creation_input_token_cost":0,"cache_read_input_token_cost":0.0000002}}`)
	svc := NewBillingService(&config.Config{}, pricing)
	for _, model := range []string{"gpt-6.1-terra", "openai/gpt-6.1-terra-max"} {
		got, err := svc.GetModelPricing(model)
		require.NoError(t, err)
		require.InDelta(t, 3e-6, got.InputPricePerToken, 1e-12)
		require.InDelta(t, 6e-6, got.InputPricePerTokenPriority, 1e-12)
		require.Zero(t, got.CacheCreationPricePerToken)
		require.Zero(t, got.CacheCreationPricePerTokenPriority)
	}
}
