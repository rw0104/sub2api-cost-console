package repository

import (
	"context"
	"testing"

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
