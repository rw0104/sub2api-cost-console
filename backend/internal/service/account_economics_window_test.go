package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEconomicsWindowHonorsCalendarTimezoneAndDST(t *testing.T) {
	for _, tc := range []struct {
		name, start, now string
		hours            float64
	}{
		{"spring-forward", "2026-03-08T00:00:00-05:00", "2026-03-08T12:00:00-04:00", 11},
		{"fall-back", "2026-11-01T00:00:00-04:00", "2026-11-01T12:00:00-05:00", 13},
		{"midnight", "2026-09-16T00:00:00-04:00", "2026-09-16T00:00:00-04:00", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, err := time.Parse(time.RFC3339, tc.start)
			require.NoError(t, err)
			now, err := time.Parse(time.RFC3339, tc.now)
			require.NoError(t, err)
			from, to, zone, err := ResolveEconomicsWindow(AccountEconomicsQuery{
				StartTime: start, EndTime: now, Window: 24 * time.Hour, Timezone: "America/New_York",
			}, now)
			require.NoError(t, err)
			require.True(t, from.Equal(start))
			require.True(t, to.Equal(now))
			require.Equal(t, tc.hours, to.Sub(from).Hours())
			require.Equal(t, "America/New_York", zone.String())
		})
	}
}

func TestEconomicsWindowRejectsInvalidBoundsAndCapsFutureEnd(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for _, query := range []AccountEconomicsQuery{
		{StartTime: now}, {EndTime: now}, {StartTime: now, EndTime: now.Add(-time.Hour)},
		{StartTime: now.Add(-31 * 24 * time.Hour), EndTime: now}, {Window: -time.Hour},
		{Window: 31 * 24 * time.Hour}, {Timezone: "invalid/zone"},
		{StartTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour)},
	} {
		_, _, _, err := ResolveEconomicsWindow(query, now)
		require.ErrorIs(t, err, ErrInvalidEconomicsWindow)
	}
	start, end, _, err := ResolveEconomicsWindow(AccountEconomicsQuery{StartTime: now.Add(-12 * time.Hour), EndTime: now.Add(12 * time.Hour)}, now)
	require.NoError(t, err)
	require.Equal(t, 12.0, end.Sub(start).Hours())
}

func TestEconomicsSnapshotUsesExplicitWindowForPurchasesAndLosses(t *testing.T) {
	start := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	account := costRegressionAccount(AccountTypeOAuth, "daily", 24, start.Add(-24*time.Hour))
	reader := &economicsAccountReaderStub{accounts: []Account{account}}
	repo := &economicsRepositoryStub{}
	loss := &economicsCostLossRepositoryStub{states: []AccountCostLossState{{
		AccountIDSnapshot: 999, TerminalEventID: 1, Platform: PlatformOpenAI,
		OccurredAt: start.Add(-time.Hour), Currency: "CNY", NetLoss: 50, Active: true, AccountDeleted: true,
	}}}
	svc := NewAccountEconomicsService(reader, repo, NewAccountCostLossService(loss))
	result, err := svc.GetSnapshot(context.Background(), AccountEconomicsQuery{
		Now: start.Add(12 * time.Hour), StartTime: start, EndTime: start.Add(24 * time.Hour),
		Window: 24 * time.Hour, CNYPerUSD: 7, Timezone: "UTC",
	})
	require.NoError(t, err)
	require.Equal(t, 12.0, result.Actual.WindowProcurementCNY)
	require.Zero(t, result.Actual.WindowImpairmentLossCNY)
	require.True(t, result.WindowStart.Equal(start))
	require.True(t, result.WindowEnd.Equal(start.Add(12*time.Hour)))
}

func TestEconomicsSnapshotMonthlyPurchasesUseClientTimezone(t *testing.T) {
	start := time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)
	account := costRegressionAccount(AccountTypeAPIKey, "one_time", 100, start)
	profile, err := resolveAccountCostProfileSnapshot(&account)
	require.NoError(t, err)
	repo := &economicsRepositoryStub{profiles: []AccountProcurementProfile{{AccountID: account.ID, Platform: account.Platform, CostProfile: profile}}}
	svc := NewAccountEconomicsService(&economicsAccountReaderStub{accounts: []Account{account}}, repo, NewAccountCostLossService(&economicsCostLossRepositoryStub{}))
	query := AccountEconomicsQuery{Now: start.Add(2 * time.Hour), Window: 24 * time.Hour, CNYPerUSD: 7, Timezone: "America/Los_Angeles"}
	local, err := svc.GetSnapshot(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, 100.0, local.Actual.MonthOneTimeProcurementCNY)
	query.Timezone = "UTC"
	utc, err := svc.GetSnapshot(context.Background(), query)
	require.NoError(t, err)
	require.Zero(t, utc.Actual.MonthOneTimeProcurementCNY)
}
