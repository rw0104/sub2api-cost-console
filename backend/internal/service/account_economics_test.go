package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type economicsAccountReaderStub struct{ accounts []Account }

func (s *economicsAccountReaderStub) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return append([]Account(nil), s.accounts...), nil
}

type economicsRepositoryStub struct {
	totals   AccountEconomicsUsageTotals
	samples  []AccountEconomicsSample
	profiles []AccountProcurementProfile
}

type economicsCostLossRepositoryStub struct {
	states []AccountCostLossState
}

func (s *economicsCostLossRepositoryStub) RecordTerminalFailure(context.Context, AccountCostLossDraft, string) (*AccountCostLossEvent, bool, error) {
	return nil, false, nil
}

func (s *economicsCostLossRepositoryStub) ListStates(context.Context) ([]AccountCostLossState, error) {
	return append([]AccountCostLossState(nil), s.states...), nil
}

func (s *economicsCostLossRepositoryStub) RecordAdjustment(context.Context, AccountCostLossAdjustment) (*AccountCostLossEvent, bool, error) {
	return nil, false, nil
}

func (s *economicsRepositoryStub) SumUsageTotals(context.Context, []int64) (AccountEconomicsUsageTotals, error) {
	return s.totals, nil
}

func (s *economicsRepositoryStub) ListProcurementProfiles(context.Context) ([]AccountProcurementProfile, error) {
	return append([]AccountProcurementProfile(nil), s.profiles...), nil
}

func (s *economicsRepositoryStub) UpsertSample(_ context.Context, sample AccountEconomicsSample) error {
	s.samples = append(s.samples, sample)
	return nil
}

func (s *economicsRepositoryStub) ListSamples(_ context.Context, _ string, since, until time.Time) ([]AccountEconomicsSample, error) {
	result := make([]AccountEconomicsSample, 0, len(s.samples))
	for _, sample := range s.samples {
		if !sample.SampledAt.Before(since) && !sample.SampledAt.After(until) {
			result = append(result, sample)
		}
	}
	return result, nil
}

func (s *economicsRepositoryStub) PruneSamples(context.Context, time.Time) error { return nil }

func TestBuildAccountEconomicsProjectionUsesOnlyStableSampleIntervals(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	samples := []AccountEconomicsSample{
		{SampledAt: base, MembershipHash: "pool-a", AccountCount: 2, BilledUSDTotal: 10, AccountCostUSDTotal: 4},
		{SampledAt: base.Add(30 * time.Minute), MembershipHash: "pool-a", AccountCount: 2, BilledUSDTotal: 13, AccountCostUSDTotal: 5},
		{SampledAt: base.Add(60 * time.Minute), MembershipHash: "pool-a", AccountCount: 2, BilledUSDTotal: 16, AccountCostUSDTotal: 6},
	}

	projection := BuildAccountEconomicsProjection(samples)

	require.Equal(t, "high", projection.Confidence)
	require.Equal(t, 2, projection.ValidIntervals)
	require.Equal(t, 0, projection.ResetIntervals)
	require.InDelta(t, 6, *projection.BilledUSDPerHour, 1e-9)
	require.InDelta(t, 2, *projection.AccountCostUSDPerHour, 1e-9)
	require.InDelta(t, 1, projection.CoverageHours, 1e-9)
}

func TestBuildAccountEconomicsProjectionDoesNotInventRateAcrossPoolChanges(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	samples := []AccountEconomicsSample{
		{SampledAt: base, MembershipHash: "pool-a", AccountCount: 2, BilledUSDTotal: 10},
		{SampledAt: base.Add(30 * time.Minute), MembershipHash: "pool-b", AccountCount: 3, BilledUSDTotal: 30},
		{SampledAt: base.Add(60 * time.Minute), MembershipHash: "pool-b", AccountCount: 3, BilledUSDTotal: 2},
	}

	projection := BuildAccountEconomicsProjection(samples)

	require.Equal(t, "unavailable", projection.Confidence)
	require.Nil(t, projection.BilledUSDPerHour)
	require.Equal(t, 0, projection.ValidIntervals)
	require.Equal(t, 2, projection.ResetIntervals)
	require.NotEmpty(t, projection.Warning)
}

func TestBuildAccountEconomicsProjectionAdjustsForecastForCurrentHealthyCapacity(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	samples := []AccountEconomicsSample{
		{SampledAt: base, MembershipHash: "pool-a", AccountCount: 2, NormalCount: 2, BilledUSDTotal: 10, AccountCostUSDTotal: 4},
		{SampledAt: base.Add(30 * time.Minute), MembershipHash: "pool-a", AccountCount: 2, NormalCount: 2, BilledUSDTotal: 13, AccountCostUSDTotal: 5},
		{SampledAt: base.Add(60 * time.Minute), MembershipHash: "pool-a", AccountCount: 2, NormalCount: 1, RateLimitedCount: 1, BilledUSDTotal: 16, AccountCostUSDTotal: 6},
	}

	projection := BuildAccountEconomicsProjection(samples)

	require.InDelta(t, 2.0/3.0, projection.CapacityAdjustment, 1e-9)
	require.InDelta(t, 4, *projection.CapacityAdjustedBilledUSDPerHour, 1e-9)
	require.InDelta(t, 4.0/3.0, *projection.CapacityAdjustedAccountCostUSDPerHour, 1e-9)
}

func TestBuildAccountPoolUnitEconomicsSeparatesUSDUsageFromCNYProcurement(t *testing.T) {
	billedRate, accountCostRate := 10.0, 2.0
	result := BuildAccountPoolUnitEconomics(AccountPoolUnitEconomicsInput{
		BilledUSD:             20,
		AccountCostUSD:        5,
		ProcurementAccruedCNY: 200,
		ImpairmentLossCNY:     30,
		ProcurementHourlyCNY:  1,
		CNYPerUSD:             7,
		BilledUSDPerHour:      &billedRate,
		AccountCostUSDPerHour: &accountCostRate,
	})

	require.InDelta(t, 230, result.EconomicCostCNY, 1e-9)
	require.InDelta(t, 11.5, *result.CNYPerBilledUSD, 1e-9)
	require.InDelta(t, 105.0/230.0, *result.PaybackRatio, 1e-9)
	require.InDelta(t, -125, result.ContributionMarginCNY, 1e-9)
	require.InDelta(t, 55, *result.ProjectedContributionCNYPerHour, 1e-9)
	require.InDelta(t, 125.0/55.0, *result.EstimatedPaybackHours, 1e-9)
}

func TestBuildAccountPoolUnitEconomicsLeavesUnsupportedForecastUnavailable(t *testing.T) {
	result := BuildAccountPoolUnitEconomics(AccountPoolUnitEconomicsInput{
		ProcurementAccruedCNY: 100,
		CNYPerUSD:             7,
	})

	require.Nil(t, result.CNYPerBilledUSD)
	require.Nil(t, result.PaybackRatio)
	require.Nil(t, result.ProjectedContributionCNYPerHour)
	require.Nil(t, result.EstimatedPaybackHours)
}

func TestSummarizeProcurementEconomicsPreservesConfiguredCurrency(t *testing.T) {
	startedAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	now := startedAt.Add(24 * time.Hour)
	accounts := []Account{{
		ID: 1, Name: "cny-account", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		CreatedAt: startedAt, Extra: map[string]any{"cost_profile": map[string]any{
			"amount": 2.5, "currency": "CNY", "billing_cycle": "one_time",
			"started_at": startedAt.Format(time.RFC3339), "algorithm_version": "1.6.0",
		}}, Credentials: map[string]any{},
	}, {
		ID: 2, Name: "usd-account", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		CreatedAt: startedAt, Extra: map[string]any{"cost_profile": map[string]any{
			"amount": 2.5, "currency": "USD", "billing_cycle": "one_time",
			"started_at": startedAt.Format(time.RFC3339), "algorithm_version": "1.6.0",
		}}, Credentials: map[string]any{},
	}}

	procurement, impairment, hourly, invalid := summarizeProcurementEconomics(accounts, nil, "openai", 7, now, true)

	require.InDelta(t, 20, procurement, 1e-9)
	require.Zero(t, impairment)
	require.Zero(t, hourly)
	require.Zero(t, invalid)
}

func TestSummarizeProcurementEconomicsDoesNotCountImpairmentTwice(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	states := []AccountCostLossState{{
		AccountIDSnapshot: 427,
		Platform:          PlatformOpenAI,
		Currency:          "CNY",
		AccruedCost:       4.17,
		NetLoss:           168.37,
		RecognizedCost:    172.54,
		Active:            true,
		OccurredAt:        now.Add(-time.Hour),
	}}

	procurement, impairment, hourly, invalid := summarizeProcurementEconomics(nil, states, "openai", 7, now, true)
	result := BuildAccountPoolUnitEconomics(AccountPoolUnitEconomicsInput{
		ProcurementAccruedCNY: procurement,
		ImpairmentLossCNY:     impairment,
		CNYPerUSD:             7,
	})

	require.InDelta(t, 4.17, procurement, 1e-9)
	require.InDelta(t, 168.37, impairment, 1e-9)
	require.InDelta(t, 172.54, result.EconomicCostCNY, 1e-9)
	require.Zero(t, hourly)
	require.Zero(t, invalid)
}

func TestSummarizeAccountEconomicsHealthUsesCurrentRuntimeWindows(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(time.Hour)
	accounts := []Account{
		{Status: StatusActive, Schedulable: true},
		{Status: StatusActive, Schedulable: true, RateLimitResetAt: &resetAt},
		{Status: StatusError, Schedulable: false, ErrorMessage: "workspace deactivated (402)"},
	}

	normal, limited, failed := summarizeAccountEconomicsHealth(accounts, now)

	require.Equal(t, 1, normal)
	require.Equal(t, 1, limited)
	require.Equal(t, 1, failed)
}

func TestEconomicsUsageAccountIDsIncludesActiveArchivedLossAccounts(t *testing.T) {
	ids := economicsUsageAccountIDs(
		[]Account{{ID: 7}, {ID: 2}},
		[]AccountCostLossState{
			{AccountIDSnapshot: 9, Platform: PlatformOpenAI, Active: true},
			{AccountIDSnapshot: 10, Platform: PlatformOpenAI, Active: false},
			{AccountIDSnapshot: 11, Platform: PlatformAnthropic, Active: true},
		},
		PlatformOpenAI,
	)

	require.Equal(t, []int64{2, 7, 9}, ids)
}

func TestAccountEconomicsServiceBuildsVersionedSnapshotFromExistingLedgers(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	account := Account{
		ID: 7, Name: "cny-purchase", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, CreatedAt: now.Add(-24 * time.Hour),
		Credentials: map[string]any{}, Extra: map[string]any{"cost_profile": map[string]any{
			"amount": 2.5, "currency": "CNY", "billing_cycle": "one_time",
			"started_at": now.Add(-24 * time.Hour).Format(time.RFC3339), "algorithm_version": CostAlgorithmVersion,
		}},
	}
	repo := &economicsRepositoryStub{
		totals: AccountEconomicsUsageTotals{BilledUSD: 10, AccountCostUSD: 2},
		samples: []AccountEconomicsSample{
			{SampledAt: now.Add(-time.Hour), ScopeKey: "account-pool:all", MembershipHash: economicsMembershipHash([]int64{7}), AccountCount: 1, NormalCount: 1, BilledUSDTotal: 0, AccountCostUSDTotal: 0},
			{SampledAt: now.Add(-30 * time.Minute), ScopeKey: "account-pool:all", MembershipHash: economicsMembershipHash([]int64{7}), AccountCount: 1, NormalCount: 1, BilledUSDTotal: 5, AccountCostUSDTotal: 1},
		},
	}
	service := NewAccountEconomicsService(
		&economicsAccountReaderStub{accounts: []Account{account}},
		repo,
		NewAccountCostLossService(&economicsCostLossRepositoryStub{}),
	)

	snapshot, err := service.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Scope: "all", CNYPerUSD: 7, ExchangeRateSource: "test", Window: time.Hour, Now: now,
	})

	require.NoError(t, err)
	require.Equal(t, CostAlgorithmVersion, snapshot.AlgorithmVersion)
	require.Equal(t, EconomicsProjectionVersion, snapshot.ProjectionVersion)
	require.InDelta(t, 2.5, snapshot.Actual.EconomicCostCNY, 1e-9)
	require.InDelta(t, 10, *snapshot.Projection.CapacityAdjustedBilledUSDPerHour, 1e-9)
	require.Equal(t, "complete", snapshot.DataQuality.Status)
}

func TestSummarizeMonthlyProcurementIncludesDeletedAccountProfiles(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, location)
	profiles := []AccountProcurementProfile{
		{AccountID: 17, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{
			Amount: 2.5, Currency: "CNY", BillingCycle: "one_time", StartedAt: now.Add(-48 * time.Hour),
		}},
		{AccountID: 422, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{
			Amount: 1.5, Currency: "CNY", BillingCycle: "one_time", StartedAt: now.Add(-24 * time.Hour),
		}},
		{AccountID: 500, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{
			Amount: 20, Currency: "USD", BillingCycle: "monthly", StartedAt: now.Add(-24 * time.Hour),
		}},
		{AccountID: 501, Platform: PlatformAnthropic, CostProfile: AccountCostProfileSnapshot{
			Amount: 3, Currency: "CNY", BillingCycle: "one_time", StartedAt: now.Add(-24 * time.Hour),
		}},
	}

	oneTime, oneTimeCount, deletedOneTime, deletedRecurring, deletedRecurringCount := summarizeMonthlyProcurementProfiles(profiles, PlatformOpenAI, nil, 7.2, now, true)

	require.Equal(t, 4.0, oneTime)
	require.Equal(t, 2, oneTimeCount)
	require.Equal(t, 2, deletedOneTime)
	require.Equal(t, 144.0, deletedRecurring)
	require.Equal(t, 1, deletedRecurringCount)
}

func TestSummarizeWindowEconomicsExcludesHistoricalLossAndCountsWindowPurchases(t *testing.T) {
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-6 * time.Hour)
	profiles := []AccountProcurementProfile{
		{AccountID: 17, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{
			Amount: 2.5, Currency: "CNY", BillingCycle: "one_time", StartedAt: now.Add(-48 * time.Hour),
		}},
		{AccountID: 422, Platform: PlatformOpenAI, Deleted: true, CostProfile: AccountCostProfileSnapshot{
			Amount: 1.5, Currency: "CNY", BillingCycle: "one_time", StartedAt: now.Add(-2 * time.Hour),
		}},
	}
	states := []AccountCostLossState{
		{AccountIDSnapshot: 17, Platform: PlatformOpenAI, AccountDeleted: true, Active: true, OccurredAt: now.Add(-24 * time.Hour), Currency: "CNY", NetLoss: 20},
		{AccountIDSnapshot: 422, Platform: PlatformOpenAI, AccountDeleted: true, Active: true, OccurredAt: now.Add(-time.Hour), Currency: "CNY", NetLoss: 3},
	}

	procurement, impairment := summarizeWindowEconomics(nil, profiles, states, PlatformOpenAI, nil, 7.2, windowStart, now, true)

	require.Equal(t, 1.5, procurement)
	require.Equal(t, 3.0, impairment)
}

func TestBuildAccountEconomicsSeriesKeepsStableRatesAndResetEvents(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	samples := []AccountEconomicsSample{
		{SampledAt: now.Add(-3 * time.Minute), MembershipHash: "a", AccountCount: 2, NormalCount: 2, BilledUSDTotal: 1, AccountCostUSDTotal: 0.4},
		{SampledAt: now.Add(-2 * time.Minute), MembershipHash: "a", AccountCount: 2, NormalCount: 1, RateLimitedCount: 1, BilledUSDTotal: 2, AccountCostUSDTotal: 0.6},
		{SampledAt: now.Add(-time.Minute), MembershipHash: "b", AccountCount: 3, NormalCount: 2, ErrorCount: 1, BilledUSDTotal: 2.5, AccountCostUSDTotal: 0.8},
	}

	series, events := BuildAccountEconomicsSeries(samples, time.Hour)

	require.Len(t, series, 2)
	require.True(t, series[0].Stable)
	require.InDelta(t, 60, *series[0].BilledUSDPerHour, 1e-9)
	require.False(t, series[1].Stable)
	require.Nil(t, series[1].BilledUSDPerHour)
	require.Len(t, events, 1)
	require.Equal(t, "pool_membership_changed", events[0].Kind)
}

func TestEconomicsSeriesBucketUsesEventScaleDensityForOneMinute(t *testing.T) {
	require.Equal(t, 5*time.Second, economicsSeriesBucket(time.Minute))
	require.Equal(t, 15*time.Second, economicsSeriesBucket(5*time.Minute))
	require.Equal(t, time.Minute, economicsSeriesBucket(30*time.Minute))
}

func TestAccountEconomicsSnapshotExcludesSamplesOutsideRequestedWindow(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	account := Account{ID: 7, Status: StatusActive, Schedulable: true}
	repo := &economicsRepositoryStub{
		totals: AccountEconomicsUsageTotals{BilledUSD: 10, AccountCostUSD: 2},
		samples: []AccountEconomicsSample{
			{SampledAt: now.Add(-7 * time.Hour), ScopeKey: "account-pool:all", MembershipHash: economicsMembershipHash([]int64{7}), AccountCount: 1, NormalCount: 1, BilledUSDTotal: 1},
			{SampledAt: now.Add(-5 * time.Hour), ScopeKey: "account-pool:all", MembershipHash: economicsMembershipHash([]int64{7}), AccountCount: 1, NormalCount: 1, BilledUSDTotal: 2},
		},
	}
	service := NewAccountEconomicsService(
		&economicsAccountReaderStub{accounts: []Account{account}}, repo,
		NewAccountCostLossService(&economicsCostLossRepositoryStub{}),
	)

	snapshot, err := service.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Scope: "all", CNYPerUSD: 7, Window: 6 * time.Hour, Now: now,
	})

	require.NoError(t, err)
	require.Equal(t, 2, snapshot.DataQuality.SampleCount, "one in-window history sample plus the current sample")
	for _, point := range snapshot.Series {
		require.False(t, point.SampledAt.Before(now.Add(-6*time.Hour)))
		require.False(t, point.SampledAt.After(now))
	}
}
