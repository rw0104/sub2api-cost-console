package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 成本算法 1.7.0 回归：前后端成本档案口径一致、影子账号不重复计入母账号订阅、
// 单条异常档案不拖垮整份经济快照、预测只在足够证据下输出。

func TestResolveCostProfileNormalizesBillingCycleSpelling(t *testing.T) {
	createdAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for raw, want := range map[string]string{
		"One-Time":  "one_time",
		" Monthly ": "monthly",
		"one time":  "one_time",
		"WEEKLY":    "weekly",
		"daily":     "daily",
		"Hourly\t":  "hourly",
		"one--time": "one_time",
		// CJK 输入法常见的全角空格，与前端 JavaScript `\s` 的结果一致。
		"one\u3000time": "one_time",
		"\u00a0Monthly": "monthly",
		"month_ly ":     "",
		"biweekly":      "",
		"":              "",
	} {
		account := Account{ID: 1, Type: AccountTypeOAuth, CreatedAt: createdAt, Extra: map[string]any{"cost_profile": map[string]any{
			"amount": 10.0, "currency": "CNY", "billing_cycle": raw, "started_at": createdAt.Format(time.RFC3339),
		}}}
		profile, err := resolveAccountCostProfileSnapshot(&account)
		if want == "" {
			require.ErrorIs(t, err, ErrInvalidCostProfile, raw)
			continue
		}
		require.NoError(t, err, raw)
		require.Equal(t, want, profile.BillingCycle, raw)
	}
}

func TestResolveCostProfileDoesNotChargeShadowAccountsForParentSubscription(t *testing.T) {
	createdAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	parentID := int64(99)
	shadow := Account{
		ID: 2, Type: AccountTypeOAuth, Platform: PlatformOpenAI, CreatedAt: createdAt, ParentAccountID: &parentID,
		Extra: map[string]any{"plan_type": "plus"}, Credentials: map[string]any{"plan_type": "pro"},
	}
	profile, err := resolveAccountCostProfileSnapshot(&shadow)
	require.NoError(t, err)
	require.Zero(t, profile.Amount, "spark shadow borrows the parent subscription")
	require.Equal(t, "default", profile.Source)

	parent := Account{ID: 99, Type: AccountTypeOAuth, Platform: PlatformOpenAI, CreatedAt: createdAt, Extra: map[string]any{"plan_type": "plus"}}
	parentProfile, err := resolveAccountCostProfileSnapshot(&parent)
	require.NoError(t, err)
	require.Equal(t, 20.0, parentProfile.Amount)

	procurement, _, hourly, invalid := summarizeProcurementEconomics([]Account{parent, shadow}, nil, PlatformOpenAI, 7, createdAt.Add(73*time.Hour), false)
	require.Zero(t, invalid)
	require.InDelta(t, 20.0*7*73/730, procurement, 1e-9, "only the parent subscription accrues")
	require.InDelta(t, 20.0*7/730, hourly, 1e-12)

	// 管理员为影子账号显式配置的固定成本仍然生效。
	shadow.Extra["cost_profile"] = map[string]any{"amount": 5.0, "currency": "CNY", "billing_cycle": "monthly", "started_at": createdAt.Format(time.RFC3339)}
	explicit, err := resolveAccountCostProfileSnapshot(&shadow)
	require.NoError(t, err)
	require.Equal(t, 5.0, explicit.Amount)
	require.Equal(t, "custom", explicit.Source)
}

func TestParseAccountProcurementProfileRejectsMalformedProfiles(t *testing.T) {
	createdAt := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	profile, err := ParseAccountProcurementProfile([]byte(`{"amount":24,"currency":"cny","billing_cycle":"Daily","started_at":"2026-09-01T00:00:00Z"}`), createdAt)
	require.NoError(t, err)
	require.Equal(t, "CNY", profile.Currency)
	require.Equal(t, "daily", profile.BillingCycle)
	require.Equal(t, createdAt, profile.StartedAt, "start is bounded by account creation like live accounts")

	for _, raw := range []string{
		`{"amount":24,"currency":"CNY","billing_cycle":"daily","started_at":""}`,
		`{"amount":"24","currency":"CNY","billing_cycle":"daily","started_at":"2026-09-01T00:00:00Z"}`,
		`{"amount":-1,"currency":"CNY","billing_cycle":"daily","started_at":"2026-09-01T00:00:00Z"}`,
		`{"amount":24,"currency":"EUR","billing_cycle":"daily","started_at":"2026-09-01T00:00:00Z"}`,
		`[]`,
		`null`,
		`not-json`,
	} {
		_, err := ParseAccountProcurementProfile([]byte(raw), createdAt)
		require.ErrorIs(t, err, ErrInvalidCostProfile, raw)
	}
}

func TestBuildAccountEconomicsProjectionExcludesSamplingGaps(t *testing.T) {
	base := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	samples := []AccountEconomicsSample{
		{SampledAt: base, MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 0},
		{SampledAt: base.Add(10 * time.Minute), MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 1},
		{SampledAt: base.Add(20 * time.Minute), MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 2},
		// 内核停机 8 小时：这段时间没有产出，不能按零产出摊薄速率。
		{SampledAt: base.Add(8*time.Hour + 20*time.Minute), MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 2},
		{SampledAt: base.Add(8*time.Hour + 30*time.Minute), MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 3},
	}

	projection := BuildAccountEconomicsProjection(samples)

	require.Equal(t, 3, projection.ValidIntervals)
	require.Equal(t, 1, projection.GapIntervals)
	require.Equal(t, 0, projection.ResetIntervals)
	require.InDelta(t, 0.5, projection.CoverageHours, 1e-9)
	require.InDelta(t, 6, *projection.BilledUSDPerHour, 1e-9)
	require.Equal(t, "medium", projection.Confidence)
	require.Contains(t, projection.Warning, "sampling gaps")
}

func TestBuildAccountEconomicsProjectionWithholdsLowConfidenceForecast(t *testing.T) {
	base := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	samples := []AccountEconomicsSample{
		{SampledAt: base, MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 0, AccountCostUSDTotal: 0},
		{SampledAt: base.Add(time.Minute), MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 1, AccountCostUSDTotal: 0.5},
		{SampledAt: base.Add(2 * time.Minute), MembershipHash: "pool", AccountCount: 1, NormalCount: 1, BilledUSDTotal: 2, AccountCostUSDTotal: 1},
	}

	projection := BuildAccountEconomicsProjection(samples)

	require.Equal(t, "low", projection.Confidence)
	require.NotNil(t, projection.BilledUSDPerHour, "the measured rate stays visible")
	require.InDelta(t, 60, *projection.BilledUSDPerHour, 1e-9)
	require.Nil(t, projection.CapacityAdjustedBilledUSDPerHour, "forecasts need at least 15 minutes of stable coverage")
	require.Nil(t, projection.CapacityAdjustedAccountCostUSDPerHour)
	require.Contains(t, projection.Warning, "forecast withheld")

	unit := BuildAccountPoolUnitEconomics(AccountPoolUnitEconomicsInput{
		BilledUSD: 2, AccountCostUSD: 1, ProcurementAccruedCNY: 100, CNYPerUSD: 7,
		BilledUSDPerHour:      projection.CapacityAdjustedBilledUSDPerHour,
		AccountCostUSDPerHour: projection.CapacityAdjustedAccountCostUSDPerHour,
	})
	require.Nil(t, unit.EstimatedPaybackHours)
	require.Nil(t, unit.ProjectedContributionCNYPerHour)
}

func TestBuildAccountEconomicsProjectionBoundsCapacityAdjustment(t *testing.T) {
	base := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	build := func(normal []int) AccountEconomicsProjection {
		samples := make([]AccountEconomicsSample, 0, len(normal))
		for index, count := range normal {
			samples = append(samples, AccountEconomicsSample{
				SampledAt: base.Add(time.Duration(index) * 20 * time.Minute), MembershipHash: "pool", AccountCount: 10,
				NormalCount: count, BilledUSDTotal: float64(index), AccountCostUSDTotal: float64(index) / 2,
			})
		}
		return BuildAccountEconomicsProjection(samples)
	}

	capped := build([]int{2, 2, 2, 10})
	require.Equal(t, maximumCapacityAdjustment, capped.CapacityAdjustment)
	require.InDelta(t, 3*maximumCapacityAdjustment, *capped.CapacityAdjustedBilledUSDPerHour, 1e-9)
	require.Contains(t, capped.Warning, "capacity adjustment capped")

	// 平均健康账号不足 1 个时无法可靠推算单账号产出，不做放大。
	sparse := build([]int{0, 0, 1, 1})
	require.Equal(t, 1.0, sparse.CapacityAdjustment)

	// 当前没有健康账号时预测产出为 0。
	exhausted := build([]int{3, 3, 3, 0})
	require.Equal(t, 0.0, exhausted.CapacityAdjustment)
	require.InDelta(t, 0, *exhausted.CapacityAdjustedBilledUSDPerHour, 1e-12)

	// 正常范围内保持原有比例。
	normal := build([]int{2, 2, 2, 3})
	require.InDelta(t, 3.0/(7.0/3.0), normal.CapacityAdjustment, 1e-9)
}

func TestSummarizeMonthlyProcurementCountsPaidCyclesForDeletedRecurringProfiles(t *testing.T) {
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	started := time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)
	deletedAfter := func(hours float64) *time.Time {
		value := started.Add(time.Duration(hours * float64(time.Hour)))
		return &value
	}
	profiles := []AccountProcurementProfile{
		// 按日付费 5.5 天后删除：已付 6 个日周期。
		{AccountID: 1, Platform: PlatformOpenAI, Deleted: true, DeletedAt: deletedAfter(5.5 * 24), CostProfile: AccountCostProfileSnapshot{Amount: 24, Currency: "CNY", BillingCycle: "daily", StartedAt: started}},
		// 按周付费 8 天后删除：已付 2 个周周期。
		{AccountID: 2, Platform: PlatformOpenAI, Deleted: true, DeletedAt: deletedAfter(8 * 24), CostProfile: AccountCostProfileSnapshot{Amount: 10, Currency: "USD", BillingCycle: "weekly", StartedAt: started}},
		// 月付立即删除：仍是 1 个已付周期，与 1.6 一致。
		{AccountID: 3, Platform: PlatformOpenAI, Deleted: true, DeletedAt: deletedAfter(0), CostProfile: AccountCostProfileSnapshot{Amount: 20, Currency: "USD", BillingCycle: "monthly", StartedAt: started}},
		// 缺少删除时间时按当前时刻计：11.5 天 = 12 个日周期。
		{AccountID: 4, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{Amount: 1, Currency: "CNY", BillingCycle: "daily", StartedAt: started}},
		// 异常档案不参与统计。
		{AccountID: 5, Platform: PlatformOpenAI, Deleted: true, Invalid: true},
		// 第 2 天确认终局、第 9 天才删除：终局后不再续付，计 2 个日周期。
		{AccountID: 6, Platform: PlatformOpenAI, Deleted: true, DeletedAt: deletedAfter(9 * 24), CostProfile: AccountCostProfileSnapshot{Amount: 3, Currency: "CNY", BillingCycle: "daily", StartedAt: started}},
		// 在起算时间之前删除：从未付费。
		{AccountID: 7, Platform: PlatformOpenAI, Deleted: true, DeletedAt: deletedAfter(-1), CostProfile: AccountCostProfileSnapshot{Amount: 99, Currency: "CNY", BillingCycle: "daily", StartedAt: started}},
	}
	states := []AccountCostLossState{
		{AccountIDSnapshot: 6, Platform: PlatformOpenAI, Active: true, AccountDeleted: true, OccurredAt: started.Add(36 * time.Hour), Currency: "CNY"},
	}

	oneTime, oneTimeCount, _, deletedRecurring, deletedRecurringCount := summarizeMonthlyProcurementProfiles(profiles, states, PlatformOpenAI, nil, 7, now, true)

	require.Zero(t, oneTime)
	require.Zero(t, oneTimeCount)
	require.Equal(t, 5, deletedRecurringCount)
	require.InDelta(t, 6*24+2*10*7+20*7+12*1+2*3, deletedRecurring, 1e-9)
}

func TestSummarizeWindowEconomicsStopsDeletedProfilesAtDeletion(t *testing.T) {
	now := time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-7 * 24 * time.Hour)
	deletedAt := windowStart.Add(3 * 24 * time.Hour)
	profiles := []AccountProcurementProfile{
		{AccountID: 9, Platform: PlatformOpenAI, Deleted: true, DeletedAt: &deletedAt, CostProfile: AccountCostProfileSnapshot{
			Amount: 168, Currency: "CNY", BillingCycle: "weekly", StartedAt: windowStart.Add(-24 * time.Hour),
		}},
	}

	procurement, impairment := summarizeWindowEconomics(nil, profiles, nil, PlatformOpenAI, nil, 7, windowStart, now, true)

	require.InDelta(t, 3*24, procurement, 1e-9, "1 CNY/hour for the 3 days before deletion")
	require.Zero(t, impairment)
}

func TestAccountEconomicsSnapshotProjectsShortRangesFromOneHourLookback(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	account := Account{ID: 7, Status: StatusActive, Schedulable: true, CreatedAt: now.Add(-2 * time.Hour)}
	hash := economicsMembershipHash([]int64{7})
	repo := &economicsRepositoryStub{totals: AccountEconomicsUsageTotals{BilledUSD: 6}}
	for minute := 60; minute >= 1; minute-- {
		repo.samples = append(repo.samples, AccountEconomicsSample{
			SampledAt: now.Add(-time.Duration(minute) * time.Minute), ScopeKey: "account-pool:all", MembershipHash: hash,
			AccountCount: 1, NormalCount: 1, BilledUSDTotal: float64(60-minute) / 10,
		})
	}
	service := NewAccountEconomicsService(
		&economicsAccountReaderStub{accounts: []Account{account}}, repo,
		NewAccountCostLossService(&economicsCostLossRepositoryStub{}),
	)

	snapshot, err := service.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Scope: "all", CNYPerUSD: 7, ExchangeRateSource: "network", Window: 5 * time.Minute, Now: now,
	})

	require.NoError(t, err)
	require.Equal(t, "high", snapshot.Projection.Confidence, "a 5 minute range still forecasts from the last hour")
	require.NotNil(t, snapshot.Projection.CapacityAdjustedBilledUSDPerHour)
	require.InDelta(t, 6, *snapshot.Projection.CapacityAdjustedBilledUSDPerHour, 1e-9)
	require.Equal(t, 6, snapshot.DataQuality.SampleCount, "five in-window history samples plus the current sample")
	for _, point := range snapshot.Series {
		require.False(t, point.SampledAt.Before(now.Add(-5*time.Minute)))
	}
}

func TestAccountEconomicsSnapshotMarksFallbackExchangeRatePartial(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	service := NewAccountEconomicsService(
		&economicsAccountReaderStub{accounts: []Account{{ID: 7, Status: StatusActive, Schedulable: true, CreatedAt: now.Add(-time.Hour)}}},
		&economicsRepositoryStub{},
		NewAccountCostLossService(&economicsCostLossRepositoryStub{}),
	)

	snapshot, err := service.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Scope: "all", CNYPerUSD: 7.2, ExchangeRateSource: "fallback", Window: time.Hour, Now: now,
	})
	require.NoError(t, err)
	require.Equal(t, "partial", snapshot.DataQuality.Status)
	require.Equal(t, "fallback", snapshot.DataQuality.ExchangeRateSource)

	snapshot, err = service.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Scope: "all", CNYPerUSD: 7.1, ExchangeRateSource: "network", Window: time.Hour, Now: now,
	})
	require.NoError(t, err)
	require.Equal(t, "complete", snapshot.DataQuality.Status)
}

func TestAccountEconomicsSnapshotSkipsInvalidDeletedProcurementProfiles(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	repo := &economicsRepositoryStub{profiles: []AccountProcurementProfile{
		{AccountID: 40, Platform: PlatformOpenAI, Deleted: true, Invalid: true},
		{AccountID: 41, Platform: PlatformAnthropic, Deleted: true, Invalid: true},
		{AccountID: 42, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{
			Amount: 3, Currency: "CNY", BillingCycle: "one_time", StartedAt: now.Add(-time.Hour),
		}},
	}}
	service := NewAccountEconomicsService(
		&economicsAccountReaderStub{accounts: []Account{{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, CreatedAt: now.Add(-time.Hour)}}},
		repo,
		NewAccountCostLossService(&economicsCostLossRepositoryStub{}),
	)

	snapshot, err := service.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Scope: "all", Platform: PlatformOpenAI, CNYPerUSD: 7, ExchangeRateSource: "network", Window: 6 * time.Hour, Now: now,
	})

	require.NoError(t, err)
	require.Equal(t, 1, snapshot.DataQuality.InvalidCostProfileCount, "only the in-scope malformed profile is reported")
	require.Equal(t, "partial", snapshot.DataQuality.Status)
	require.InDelta(t, 3, snapshot.Actual.WindowProcurementCNY, 1e-9)
	require.InDelta(t, 3, snapshot.Actual.MonthOneTimeProcurementCNY, 1e-9)
}
