//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestAccountEconomicsSumUsageTotals_CacheStorageCountedOnce 钉住 Gemini 显式缓存存储费
// 在经济采样这一方的归集口径（源计划 R4 的第三条腿：账号成本、用户扣费、经济样本三方一致）。
//
//   - 创建缓存：既有 token 用量又有存储费，账号成本由网关预先算定写入 account_stats_cost；
//   - 续期缓存：不是一次模型请求，token 全 0、account_stats_cost 为 NULL，
//     归集必须回落 total_cost（其中已含官方存储价）。
//
// 两行合计必须等于每行各自的成本基数乘账号倍率之和：NULL 回落不能把存储费漏掉，
// 也不能因为创建行已含存储费而重复累加。
//
// SumUsageTotals 只接受 *sql.DB，无法在 ent 事务里执行，因此前置数据写的是共享库。
// 用例结束必须把这些行删干净 —— 否则多出来的账号会泄漏给同包其它套件，账号列表、
// 可调度筛选与看板聚合都会各自多数出一个。
func TestAccountEconomicsSumUsageTotals_CacheStorageCountedOnce(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	logs := newUsageLogRepositoryWithSQL(client, integrationDB)
	stamp := time.Now().UnixNano()

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("cache-storage-economics-%d@example.com", stamp)})
	group := mustCreateGroup(t, client, &service.Group{
		Name: fmt.Sprintf("cache-storage-economics-%d", stamp), Platform: service.PlatformGemini, RateMultiplier: 1.5,
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: fmt.Sprintf("cache-storage-economics-%d", stamp), Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
	})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID})

	t.Cleanup(func() {
		for _, step := range []struct {
			query string
			arg   int64
		}{
			{"DELETE FROM usage_logs WHERE account_id = $1", account.ID},
			{"DELETE FROM api_keys WHERE id = $1", apiKey.ID},
			{"DELETE FROM accounts WHERE id = $1", account.ID},
			{"DELETE FROM groups WHERE id = $1", group.ID},
			{"DELETE FROM users WHERE id = $1", user.ID},
		} {
			if _, err := integrationDB.ExecContext(context.Background(), step.query, step.arg); err != nil {
				t.Errorf("清理共享库失败，会污染同包其它套件: %s: %v", step.query, err)
			}
		}
	})

	const (
		groupRate   = 1.5
		accountRate = 0.6
		storageFee  = 0.01
		tokenCost   = 0.0075
	)
	rate := accountRate

	// 创建缓存：token 费叠加存储费。账号成本已由 applyAccountStatsCost 与
	// addCacheStorageToAccountStatsCost 算定，落库时 account_stats_cost 非空。
	createStats := tokenCost + storageFee
	inserted, err := logs.Create(ctx, &service.UsageLog{
		RequestID:             fmt.Sprintf("cc-create-%d", stamp),
		UserID:                user.ID,
		APIKeyID:              apiKey.ID,
		AccountID:             account.ID,
		Model:                 "gemini-3.8-flash",
		InputTokens:           10000,
		TotalCost:             tokenCost + storageFee,
		ActualCost:            (tokenCost + storageFee) * groupRate,
		AccountStatsCost:      &createStats,
		AccountRateMultiplier: &rate,
	})
	require.NoError(t, err)
	require.True(t, inserted, "创建行必须落库")

	// 续期缓存：纯存储费。isCacheStorageOnlyResult 命中后整段跳过账号统计定价，
	// account_stats_cost 保持 NULL。
	inserted, err = logs.Create(ctx, &service.UsageLog{
		RequestID:             fmt.Sprintf("cc-renew-%d", stamp),
		UserID:                user.ID,
		APIKeyID:              apiKey.ID,
		AccountID:             account.ID,
		Model:                 "gemini-3.8-flash",
		TotalCost:             storageFee,
		ActualCost:            storageFee * groupRate,
		AccountRateMultiplier: &rate,
	})
	require.NoError(t, err)
	require.True(t, inserted, "续期行必须落库")

	totals, err := NewAccountEconomicsRepository(integrationDB).SumUsageTotals(ctx, []int64{account.ID})
	require.NoError(t, err)

	wantBilled := (tokenCost+storageFee)*groupRate + storageFee*groupRate
	wantAccountCost := createStats*accountRate + storageFee*accountRate

	require.InDelta(t, wantBilled, totals.BilledUSD, 1e-9, "用户扣费合计等于两行 actual_cost 之和")
	require.InDelta(t, wantAccountCost, totals.AccountCostUSD, 1e-9,
		"账号成本合计：创建行取 account_stats_cost，续期行回落 total_cost，存储费各计一次")

	// 显式检查存储费出现的次数：剔除 token 成本后应恰好剩两份存储费，不多不少。
	require.InDelta(t, 2*storageFee*accountRate, totals.AccountCostUSD-tokenCost*accountRate, 1e-9,
		"存储费在两行中各出现一次：NULL 回落不丢，预计算行不重复")
}
