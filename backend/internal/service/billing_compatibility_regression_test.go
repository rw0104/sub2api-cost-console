//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLegacyMaxReasoningEffortPricingCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		payload    string
		multiplier float64
		direct     bool
	}{
		{"legacy only", `{"max_reasoning_effort_multiplier":2.5}`, 2.5, false},
		{"explicit map wins", `{"max_reasoning_effort_multiplier":2.5,"reasoning_effort_multipliers":{"max":1.7}}`, 1.7, false},
		{"explicit empty map clears", `{"max_reasoning_effort_multiplier":2.5,"reasoning_effort_multipliers":{}}`, 1, false},
		{"no implicit default", `{}`, 1, false},
		{"legacy struct", ``, 2.5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []BillingMode{BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo} {
				t.Run(string(mode), func(t *testing.T) {
					var pricing ChannelModelPricing
					if tc.direct {
						pricing.MaxReasoningEffortMultiplier = testPtrFloat64(2.5)
					} else {
						require.NoError(t, json.Unmarshal([]byte(tc.payload), &pricing))
						require.Nil(t, pricing.MaxReasoningEffortMultiplier)
						roundTrip, err := json.Marshal(pricing)
						require.NoError(t, err)
						require.NotContains(t, string(roundTrip), "max_reasoning_effort_multiplier")
						pricing = ChannelModelPricing{}
						require.NoError(t, json.Unmarshal(roundTrip, &pricing))
					}
					pricing.Platform = PlatformOpenAI
					pricing.Models = []string{"claude-fable-5-1"}
					pricing.BillingMode = mode
					pricing.InputPrice = testPtrFloat64(1e-6)
					pricing.PerRequestPrice = testPtrFloat64(0.1)
					bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, []ChannelModelPricing{pricing}, nil)
					group := &Group{ID: 100, Platform: PlatformOpenAI}
					cost, err := bs.CalculateCostUnified(CostInput{
						Ctx: context.Background(), Model: pricing.Models[0], Group: group, GroupID: &group.ID,
						Tokens: UsageTokens{InputTokens: 1000}, RequestCount: 2, RateMultiplier: 0.8,
						ReasoningEffort: "max", Resolver: resolver,
					})
					require.NoError(t, err)
					base := 0.2
					if mode == BillingModeToken {
						base = 0.001
					}
					require.InDelta(t, base*tc.multiplier, cost.TotalCost, 1e-12)
					require.InDelta(t, base*tc.multiplier*0.8, cost.ActualCost, 1e-12)

					channel := &Channel{AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{1}, Pricing: []ChannelModelPricing{pricing}}}}
					statsCost := tryCustomRules(channel, 1, group.ID, PlatformOpenAI, pricing.Models[0], UsageTokens{InputTokens: 1000}, 2, "max")
					require.NotNil(t, statsCost)
					require.InDelta(t, base*tc.multiplier, *statsCost, 1e-12)

					tokenPricing, err := bs.GetModelPricingWithChannel(pricing.Models[0], &pricing)
					require.NoError(t, err)
					require.Equal(t, tc.multiplier, reasoningEffortBillingMultiplier("max", tokenPricing.ReasoningEffortMultipliers))
				})
			}
		})
	}
}

func TestOpus55ExplicitPriorityPricesPreserved(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pricing ModelPricing
		tokens  UsageTokens
		want    float64
	}{
		{"input", ModelPricing{InputPricePerToken: 4e-6, InputPricePerTokenPriority: 12e-6}, UsageTokens{InputTokens: 1000}, 0.012},
		{"output", ModelPricing{OutputPricePerToken: 20e-6, OutputPricePerTokenPriority: 70e-6}, UsageTokens{OutputTokens: 1000}, 0.070},
		{"cache creation", ModelPricing{CacheCreationPricePerToken: 5e-6, CacheCreationPricePerTokenPriority: 17e-6}, UsageTokens{CacheCreationTokens: 1000}, 0.017},
		{"cache read", ModelPricing{CacheReadPricePerToken: 0.2e-6, CacheReadPricePerTokenPriority: 0.8e-6}, UsageTokens{CacheReadTokens: 1000}, 0.0008},
		{"cache TTL breakdown", ModelPricing{CacheCreationPricePerToken: 5e-6, CacheCreationPricePerTokenPriority: 17e-6, SupportsCacheBreakdown: true, CacheCreation5mPrice: 5e-6, CacheCreation1hPrice: 8e-6}, UsageTokens{CacheCreationTokens: 1000, CacheCreation5mTokens: 400, CacheCreation1hTokens: 600}, 0.02312},
		{"cache TTL long context", ModelPricing{CacheCreationPricePerToken: 5e-6, CacheCreationPricePerTokenPriority: 17e-6, SupportsCacheBreakdown: true, CacheCreation5mPrice: 5e-6, CacheCreation1hPrice: 8e-6, LongContextInputThreshold: 100, LongContextInputMultiplier: 2}, UsageTokens{CacheCreationTokens: 1000, CacheCreation5mTokens: 400, CacheCreation1hTokens: 600}, 0.04624},
		{"default when absent", ModelPricing{InputPricePerToken: 4e-6}, UsageTokens{InputTokens: 1000}, 0.008},
		{"explicit fast multiplier wins", ModelPricing{InputPricePerToken: 4e-6, InputPricePerTokenPriority: 12e-6, FastMultiplier: testPtrFloat64(1.5)}, UsageTokens{InputTokens: 1000}, 0.006},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bs := newTestBillingService()
			bs.fallbackPrices["claude-opus-5-5"] = &tc.pricing
			for _, tier := range []string{"fast", "priority"} {
				cost, err := bs.CalculateCostWithServiceTier("claude-opus-5-5", tc.tokens, 1, tier)
				require.NoError(t, err)
				require.InDelta(t, tc.want, cost.TotalCost, 1e-12, tier)
			}
		})
	}
}
