//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newCatalogPricingServiceFromRepoFile 用仓库内随版本发布的定价目录构造 PricingService。
// 与 newStubPricingServiceFromJSON 的区别：条目取自真实文件而非内联片段，因此目录改价
// （含 *_above_NNNk_tokens 绝对价）会直接驱动这里的断言。
func newCatalogPricingServiceFromRepoFile(t *testing.T) *PricingService {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	return newStubPricingServiceFromJSON(t, string(body))
}

// TestHaiku55LongContextSurchargeReachesAccountCost 钉住「长上下文附加费计入账号成本」：
// 同一笔超阈值的 claude-haiku-5-5 请求，客户售价（total_cost / actual_cost）与账号成本
// （account_stats_cost）必须同时含整单 5x 附加费，不允许只有一侧抬价。
//
// 两个定价源都要走一遍：目录条目用 *_above_100k_tokens 绝对价表达阶梯（由解析层折算），
// Go 兜底价直接携带阈值与倍率。远程目录不可用时账号成本不能悄悄回落到 1x。
func TestHaiku55LongContextSurchargeReachesAccountCost(t *testing.T) {
	const (
		model       = "claude-haiku-5-5"
		groupID     = int64(77)
		inputPrice  = 1e-7
		outputPrice = 5e-7
		write5m     = 1.25e-7
		readPrice   = 1e-8
		threshold   = 100_000
	)

	cfg := &config.Config{}
	sources := map[string]*BillingService{
		"catalog":  NewBillingService(cfg, newCatalogPricingServiceFromRepoFile(t)),
		"fallback": NewBillingService(cfg, nil),
	}

	// 阈值只看输入侧三项之和（input + cache_creation + cache_read），不含 output。
	cases := []struct {
		name        string
		inputTokens int
		surcharge   float64
	}{
		{name: "at_threshold", inputTokens: threshold - 3000, surcharge: 1},
		{name: "above_threshold", inputTokens: threshold - 3000 + 1, surcharge: 5},
	}

	for source, bs := range sources {
		for _, tc := range cases {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				tokens := UsageTokens{
					InputTokens:         tc.inputTokens,
					OutputTokens:        500,
					CacheCreationTokens: 1000,
					CacheReadTokens:     2000,
				}
				require.Equal(t, tc.surcharge > 1,
					tokens.InputTokens+tokens.CacheCreationTokens+tokens.CacheReadTokens > threshold,
					"用例自检：输入侧合计与期望的阶梯命中状态必须一致")

				// 整单 5x：输入、输出、缓存写入、缓存读取四个分项都按同一附加费抬价。
				wantTotal := (float64(tokens.InputTokens)*inputPrice +
					float64(tokens.OutputTokens)*outputPrice +
					float64(tokens.CacheCreationTokens)*write5m +
					float64(tokens.CacheReadTokens)*readPrice) * tc.surcharge

				groupRate, accountRate := 1.5, 0.6
				gid := groupID
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				svc.billingService = bs
				// 优先级 1/2 都不配置，账号成本落到优先级 3（模型定价文件）。
				svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, groupID, PlatformAnthropic)

				require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
					Result: &ForwardResult{
						RequestID: source + "-" + tc.name,
						Model:     model,
						Usage: ClaudeUsage{
							InputTokens:              tokens.InputTokens,
							OutputTokens:             tokens.OutputTokens,
							CacheCreationInputTokens: tokens.CacheCreationTokens,
							CacheReadInputTokens:     tokens.CacheReadTokens,
						},
					},
					APIKey:  &APIKey{ID: 881, GroupID: &gid, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: groupRate, Hydrated: true}},
					User:    &User{ID: 681},
					Account: &Account{ID: 781, Platform: PlatformAnthropic, RateMultiplier: &accountRate},
				}))

				log := usageRepo.lastLog
				require.NotNil(t, log)
				require.InDelta(t, wantTotal, log.TotalCost, 1e-10, "客户售价（倍率前）")
				require.InDelta(t, wantTotal*groupRate, log.ActualCost, 1e-10, "客户售价 = 官方价 × 分组倍率")
				require.Equal(t, tc.surcharge > 1, log.LongContextBillingApplied, "用量行须标记本次是否收了长上下文附加费")

				require.NotNil(t, log.AccountStatsCost, "优先级 3 必须算出账号成本")
				require.InDelta(t, wantTotal, *log.AccountStatsCost, 1e-10,
					"账号成本与客户售价同口径：附加费必须一并计入，不能只抬售价")
				require.NotNil(t, log.AccountRateMultiplier)
				require.InDelta(t, wantTotal*accountRate, *log.AccountStatsCost**log.AccountRateMultiplier, 1e-10,
					"归集口径：成本 = 账号成本 × 账号倍率")
			})
		}
	}

	// 账号成本优先级 1（渠道自定义规则）由 calculateTokenStatsCost 独立实现，不含长上下文
	// 阶梯。这是上游既有设计：自定义规则就是运营者给出的成本价，不再叠加官方附加费。
	// 钉住现状，避免被当成"附加费漏算"而误改。
	t.Run("custom_rule_is_flat_by_design", func(t *testing.T) {
		const rulePrice = 2e-7
		channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{
			GroupIDs: []int64{groupID},
			Pricing:  []ChannelModelPricing{{Models: []string{model}, InputPrice: testPtrFloat64(rulePrice)}},
		}}}
		cs := newTestChannelServiceForStats(t, channel, groupID, PlatformAnthropic)
		bs := NewBillingService(cfg, newCatalogPricingServiceFromRepoFile(t))
		tokens := UsageTokens{InputTokens: threshold + 1}

		usageLog := &UsageLog{}
		applyAccountStatsCost(context.Background(), usageLog, cs, bs,
			1, groupID, model, model, tokens, 0, time.Time{},
			accountStatsLongContextPricingEnabled(nil))

		require.NotNil(t, usageLog.AccountStatsCost)
		require.InDelta(t, float64(tokens.InputTokens)*rulePrice, *usageLog.AccountStatsCost, 1e-10,
			"自定义规则按规则单价平价计，不叠加官方长上下文附加费")
	})
}

// TestGemini36FlashFallbackMatchesCatalogRates 钉住 gemini-3.6-flash 的兜底价与目录价同源。
// 兜底价只在远程目录不可用时生效，一旦与目录脱节，降级期间的计费会静默偏离官方价。
func TestGemini36FlashFallbackMatchesCatalogRates(t *testing.T) {
	const model = "gemini-3.6-flash"

	catalog := newCatalogPricingServiceFromRepoFile(t).GetModelPricing(model)
	require.NotNil(t, catalog, "目录必须保留 gemini-3.6-flash 条目")

	fallback, err := NewBillingService(&config.Config{}, nil).GetModelPricing(model)
	require.NoError(t, err)

	require.InDelta(t, catalog.InputCostPerToken, fallback.InputPricePerToken, 1e-15, "标准档输入价")
	require.InDelta(t, catalog.OutputCostPerToken, fallback.OutputPricePerToken, 1e-15, "标准档输出价")
	require.InDelta(t, catalog.CacheReadInputTokenCost, fallback.CacheReadPricePerToken, 1e-15, "标准档缓存读取价")
	require.Zero(t, catalog.LongContextInputTokenThreshold, "该模型无长上下文阶梯")
	require.Zero(t, fallback.LongContextInputThreshold, "兜底价也不得凭空带阶梯")

	// 目录声明 supports_service_tier 并给出 priority 绝对价；兜底价必须跟随同一比例，
	// 否则降级期间 priority 请求会落到通用 2x 服务档倍率，与官方 1.8x 不符。
	require.Positive(t, catalog.InputCostPerTokenPriority, "目录 priority 档前提")
	require.InDelta(t, catalog.InputCostPerTokenPriority, fallback.InputPricePerTokenPriority, 1e-15, "priority 输入价")
	require.InDelta(t, catalog.OutputCostPerTokenPriority, fallback.OutputPricePerTokenPriority, 1e-15, "priority 输出价")
	require.InDelta(t, catalog.CacheReadInputTokenCostPriority, fallback.CacheReadPricePerTokenPriority, 1e-15, "priority 缓存读取价")

	// 两源落到同一笔费用：priority 请求在目录与兜底下金额一致。
	tokens := UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}
	for _, tier := range []string{"", "priority"} {
		fromCatalog, err := NewBillingService(&config.Config{}, newCatalogPricingServiceFromRepoFile(t)).
			CalculateCostWithServiceTier(model, tokens, 1, tier)
		require.NoError(t, err)
		fromFallback, err := NewBillingService(&config.Config{}, nil).
			CalculateCostWithServiceTier(model, tokens, 1, tier)
		require.NoError(t, err)
		require.InDelta(t, fromCatalog.TotalCost, fromFallback.TotalCost, 1e-12, "service_tier=%q 两源同价", tier)
	}
}

// TestCacheStorageOnlyBillsOneStorageFeeAcrossLedgers 钉住「仅存储费记录的三方金额一致」：
// Gemini 显式缓存续期不是一次模型请求，只产生存储费。该笔金额必须在用量行、实际扣费
// （余额 / API Key 配额 / 限流窗口 / 账号配额）和账号成本归集三处口径一致且只收一份。
func TestCacheStorageOnlyBillsOneStorageFeeAcrossLedgers(t *testing.T) {
	const (
		model        = "gemini-3.8-flash"
		groupID      = int64(41)
		storagePrice = 5e-7 // 目录声明的 cache_storage_cost_per_token_per_hour
		inputPrice   = 7.5e-7
		tokenHours   = 20_000.0
		inputTokens  = 10_000
	)
	storageFee := tokenHours * storagePrice

	newPricing := func(t *testing.T) *PricingService {
		return newStubPricingServiceFromJSON(t, `{"gemini-3.8-flash":{"litellm_provider":"gemini",
			"input_cost_per_token":0.00000075,"output_cost_per_token":0.00000375,
			"cache_storage_cost_per_token_per_hour":0.0000005}}`)
	}

	for _, tc := range []struct {
		name        string
		inputTokens int
		// wantStatsCost 为 nil 表示该笔不写 account_stats_cost，归集回落 total_cost。
		wantStatsCost *float64
	}{
		{name: "storage_only", inputTokens: 0, wantStatsCost: nil},
		{name: "tokens_plus_storage", inputTokens: inputTokens, wantStatsCost: testPtrFloat64(inputTokens*inputPrice + storageFee)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupRate, accountRate := 1.5, 0.6
			gid := groupID
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo,
				&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
			svc.billingService = NewBillingService(svc.cfg, newPricing(t))
			svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, groupID, PlatformGemini)

			require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: &ForwardResult{
					RequestID:              GeminiCachedContentCreateUsageRequestID(tc.name),
					Model:                  model,
					Usage:                  ClaudeUsage{InputTokens: tc.inputTokens},
					CacheStorageTokenHours: tokenHours,
				},
				APIKey: &APIKey{
					ID: 841, GroupID: &gid, Quota: 1000, RateLimit5h: 500,
					Group: &Group{ID: groupID, Platform: PlatformGemini, RateMultiplier: groupRate, Hydrated: true},
				},
				User: &User{ID: 641},
				Account: &Account{
					ID: 741, Platform: PlatformGemini, Type: AccountTypeAPIKey,
					RateMultiplier: &accountRate, Extra: map[string]any{"quota_limit": 1000.0},
				},
				APIKeyService: apiKeyQuotaUpdaterStub{},
			}))

			log := usageRepo.lastLog
			require.NotNil(t, log)
			wantTotal := float64(tc.inputTokens)*inputPrice + storageFee
			require.InDelta(t, wantTotal, log.TotalCost, 1e-12, "用量行售价（倍率前）须含且仅含一份存储费")
			require.InDelta(t, wantTotal*groupRate, log.ActualCost, 1e-12, "用量行售价 = 官方价 × 分组倍率")

			// 第一方：用量行。第二方：实际扣费命令。
			require.Equal(t, 1, billingRepo.calls, "一笔续期只应产生一条扣费命令")
			cmd := billingRepo.lastCmd
			require.NotNil(t, cmd)
			require.InDelta(t, log.ActualCost, cmd.BalanceCost, 1e-12, "余额扣费 = 用量行 actual_cost")
			require.Zero(t, cmd.SubscriptionCost, "余额模式不写订阅消费")
			require.InDelta(t, log.ActualCost, cmd.APIKeyQuotaCost, 1e-12, "API Key 配额 = 用量行 actual_cost")
			require.InDelta(t, log.ActualCost, cmd.APIKeyRateLimitCost, 1e-12, "API Key 限流窗口 = 用量行 actual_cost")
			require.InDelta(t, log.TotalCost*accountRate, cmd.AccountQuotaCost, 1e-12, "账号配额 = 倍率前售价 × 账号倍率")

			// 第三方：账号成本归集（account_economics 用 COALESCE(account_stats_cost, total_cost)）。
			if tc.wantStatsCost == nil {
				require.Nil(t, log.AccountStatsCost, "纯存储费不是一次模型请求，不写账号统计成本")
			} else {
				require.NotNil(t, log.AccountStatsCost)
				require.InDelta(t, *tc.wantStatsCost, *log.AccountStatsCost, 1e-12, "账号统计成本须含存储费且只计一次")
			}
			statsCost := log.TotalCost
			if log.AccountStatsCost != nil {
				statsCost = *log.AccountStatsCost
			}
			require.NotNil(t, log.AccountRateMultiplier)
			require.InDelta(t, wantTotal*accountRate, statsCost**log.AccountRateMultiplier, 1e-12,
				"归集成本 = COALESCE(account_stats_cost, total_cost) × 账号倍率，存储费不重不漏")
		})
	}
}
