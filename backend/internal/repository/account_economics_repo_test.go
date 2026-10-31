package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAccountEconomicsRepositoryReadsFactualUserAndAccountCostColumns(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)SELECT.*SUM\(actual_cost\).*SUM\(COALESCE\(account_stats_cost, total_cost\).*WHERE account_id = ANY`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"billed", "account_cost"}).AddRow(12.5, 4.25))

	repo := NewAccountEconomicsRepository(db)
	totals, err := repo.SumUsageTotals(context.Background(), []int64{1, 2})

	require.NoError(t, err)
	require.InDelta(t, 12.5, totals.BilledUSD, 1e-9)
	require.InDelta(t, 4.25, totals.AccountCostUSD, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountEconomicsRepositoryReadsDeletedOneTimeProcurementProfiles(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	deletedAt := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT id, platform, deleted_at, created_at, extra->'cost_profile'.*WHERE extra \? 'cost_profile'`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "deleted_at", "created_at", "cost_profile"}).AddRow(
			42, "openai", deletedAt, createdAt,
			[]byte(`{"amount":2.5,"currency":"CNY","billing_cycle":"one_time","started_at":"2026-08-10T15:06:51+08:00","source":"custom","algorithm_version":"1.6.0"}`),
		))

	repo := NewAccountEconomicsRepository(db)
	profiles, err := repo.ListProcurementProfiles(context.Background())

	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.True(t, profiles[0].Deleted)
	require.NotNil(t, profiles[0].DeletedAt)
	require.True(t, deletedAt.Equal(*profiles[0].DeletedAt))
	require.False(t, profiles[0].Invalid)
	require.Equal(t, "one_time", profiles[0].CostProfile.BillingCycle)
	require.Equal(t, 2.5, profiles[0].CostProfile.Amount)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 成本算法 1.7：单条格式异常的成本档案只标记为无效，不能让整份经济快照失败；
// 已删除账号的档案与现存账号使用同一套解析与归一化规则。
func TestAccountEconomicsRepositoryMarksMalformedProcurementProfilesInvalid(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT id, platform, deleted_at, created_at, extra->'cost_profile'.*WHERE extra \? 'cost_profile'`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "deleted_at", "created_at", "cost_profile"}).
			AddRow(1, "openai", nil, createdAt, []byte(`{"amount":24,"currency":"CNY","billing_cycle":"daily","started_at":""}`)).
			AddRow(2, "openai", createdAt.Add(time.Hour), createdAt, []byte(`{"amount":"24","currency":"CNY","billing_cycle":"daily","started_at":"2026-09-01T00:00:00Z"}`)).
			AddRow(3, "openai", createdAt.Add(time.Hour), createdAt, []byte(`{"amount":24,"currency":"cny","billing_cycle":"One-Time","started_at":"2026-09-01T00:00:00Z"}`)).
			// JSON null means "no cost profile", as for live accounts; it is neither returned nor invalid.
			AddRow(4, "openai", createdAt.Add(time.Hour), createdAt, []byte(`null`)))

	repo := NewAccountEconomicsRepository(db)
	profiles, err := repo.ListProcurementProfiles(context.Background())

	require.NoError(t, err)
	require.Len(t, profiles, 3)
	require.True(t, profiles[0].Invalid)
	require.False(t, profiles[0].Deleted)
	require.Nil(t, profiles[0].DeletedAt)
	require.True(t, profiles[1].Invalid)
	require.True(t, profiles[1].Deleted)
	require.False(t, profiles[2].Invalid)
	require.Equal(t, "one_time", profiles[2].CostProfile.BillingCycle)
	require.Equal(t, "CNY", profiles[2].CostProfile.Currency)
	require.Equal(t, createdAt, profiles[2].CostProfile.StartedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountEconomicsSampleBucketPreservesFiveSecondEvidence(t *testing.T) {
	sampledAt := time.Date(2026, 8, 11, 12, 34, 58, 700_000_000, time.FixedZone("offset", 8*60*60))

	require.Equal(
		t,
		time.Date(2026, 8, 11, 4, 34, 55, 0, time.UTC),
		accountEconomicsSampleBucket(sampledAt),
	)
}

func TestAccountEconomicsRepositoryBoundsSamplesOnBothSides(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	since := time.Date(2026, 8, 12, 6, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)FROM account_economics_samples.*scope_key = \$1 AND sampled_at >= \$2 AND sampled_at <= \$3`).
		WithArgs("account-pool:all", since, until).
		WillReturnRows(sqlmock.NewRows([]string{
			"sampled_at", "scope_key", "membership_hash", "account_count", "normal_count",
			"rate_limited_count", "error_count", "billed_usd_total", "account_cost_usd_total",
		}))

	repo := NewAccountEconomicsRepository(db)
	samples, err := repo.ListSamples(context.Background(), "account-pool:all", since, until)

	require.NoError(t, err)
	require.Empty(t, samples)
	require.NoError(t, mock.ExpectationsWereMet())
}
