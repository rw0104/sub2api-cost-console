package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAICacheWriteTTLWithLegacyHourPrice(t *testing.T) {
	service := NewBillingService(nil, nil)
	for _, tier := range []string{"default", "fast", "priority"} {
		for _, longContext := range []bool{false, true} {
			t.Run(tier+"/long="+map[bool]string{false: "no", true: "yes"}[longContext], func(t *testing.T) {
				pricing := &ModelPricing{
					InputPricePerToken: 1, InputPricePerTokenPriority: 2,
					CacheCreationPricePerToken: 1, CacheCreationPricePerTokenPriority: 2,
					CacheCreation5mPrice: 1, CacheCreation1hPrice: 5,
					CacheCreation12hPricePerToken: float64Ptr(2), SupportsCacheBreakdown: true,
					LongContextInputThreshold: 1, LongContextInputMultiplier: 2,
				}
				tokens := UsageTokens{InputTokens: 2, CacheCreationTokens: 2, CacheCreation30mTokens: 1, CacheCreation12hTokens: 1}
				cost := service.computeTokenBreakdown(pricing, tokens, 1, tier, longContext)
				want := 3.0
				if tier != "default" {
					want *= 2
				}
				if longContext {
					want *= 2
				}
				require.Equal(t, want, cost.CacheCreationCost, "OpenAI TTL prices already include the explicit priority rate")
				require.Equal(t, 2.0, *pricing.CacheCreation12hPricePerToken, "shared pricing must remain immutable")

				// A legacy Anthropic 5m/1h write still uses its own base rates.
				tokens.CacheCreation30mTokens, tokens.CacheCreation12hTokens = 0, 0
				tokens.CacheCreation5mTokens, tokens.CacheCreation1hTokens = 1, 1
				legacy := service.computeTokenBreakdown(pricing, tokens, 1, tier, longContext)
				require.Equal(t, want*2, legacy.CacheCreationCost)
			})
		}
	}
}
