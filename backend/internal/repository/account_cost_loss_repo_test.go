package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountCostLossRepositoryRecordsTerminalLossAndDisablesAccountAtomically(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	occurredAt := time.Date(2026, time.August, 9, 15, 0, 0, 0, time.UTC)
	createdAt := occurredAt.Add(time.Second)
	draft := service.AccountCostLossDraft{
		AccountID:   42,
		AccountName: "local-plus",
		Platform:    service.PlatformOpenAI,
		AccountType: service.AccountTypeOAuth,
		Failure: service.TerminalFailure{
			Reason:       service.TerminalFailureTokenRevoked,
			StatusCode:   401,
			UpstreamCode: "token_revoked",
			Message:      "token revoked",
			OccurredAt:   occurredAt,
		},
		CostProfile: service.AccountCostProfileSnapshot{
			Amount: 20, Currency: "USD", BillingCycle: "monthly",
			StartedAt: occurredAt.Add(-365 * time.Hour), Source: "custom",
			AlgorithmVersion: service.AccountCostLossAlgorithmVersion,
		},
		Currency:        "USD",
		BillingPeriodAt: occurredAt.Add(-5 * time.Hour),
		BillingPeriodTo: occurredAt.Add(725 * time.Hour),
		AccruedCost:     10,
		LossAmount:      10,
		RecognizedCost:  20,
		Algorithm:       service.AccountCostLossAlgorithmVersion,
		IdempotencyKey:  "terminal:42:v1:token_revoked",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO account_cost_loss_events .*ON CONFLICT \(idempotency_key\) DO NOTHING.*RETURNING id, created_at`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(7), createdAt))
	mock.ExpectExec(`(?s)UPDATE accounts.*status = \$1.*error_message = \$2.*schedulable = FALSE.*WHERE id = \$3`).
		WithArgs(service.StatusError, "terminal account failure: token revoked", int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO scheduler_outbox`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := NewAccountCostLossRepository(db)
	event, created, err := repo.RecordTerminalFailure(context.Background(), draft, "terminal account failure: token revoked")

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(7), event.ID)
	require.Equal(t, draft.LossAmount, event.Amount)
	require.Equal(t, createdAt, event.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountCostLossRepositoryAppendsBoundedRefund(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	occurredAt := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	sourceCreatedAt := occurredAt.Add(-24 * time.Hour)
	profile := `{"amount":20,"currency":"USD","billing_cycle":"monthly","started_at":"2026-08-01T00:00:00Z","source":"custom","algorithm_version":"1.5.0"}`

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)FROM account_cost_loss_events.*WHERE idempotency_key = \$1`).
		WithArgs("refund:provider:7").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`(?s)FROM account_cost_loss_events.*WHERE id = \$1 AND event_type = 'terminal_loss'.*FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(accountCostLossEventColumns()).AddRow(
			int64(7), int64(42), int64(42), "local-plus", service.PlatformOpenAI, service.AccountTypeOAuth,
			service.AccountCostLossEventTerminal, service.TerminalFailureTokenRevoked, 401, "token_revoked", "revoked", occurredAt.Add(-24*time.Hour),
			"USD", 10.0, 10.0, 20.0, occurredAt.Add(-48*time.Hour), occurredAt.Add(682*time.Hour), []byte(profile),
			nil, "terminal:42:v1", service.AccountCostLossAlgorithmVersion, sourceCreatedAt,
		))
	mock.ExpectQuery(`(?s)SELECT COALESCE\(SUM\(amount\), 0\).*source_event_id = \$1`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(-2.0))
	mock.ExpectQuery(`(?s)INSERT INTO account_cost_loss_events .*RETURNING id, created_at`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(12), occurredAt.Add(time.Second)))
	mock.ExpectCommit()

	repo := NewAccountCostLossRepository(db)
	event, created, err := repo.RecordAdjustment(context.Background(), service.AccountCostLossAdjustment{
		SourceEventID: 7,
		AccountID:     42,
		EventType:     service.AccountCostLossEventRefund,
		Amount:        3,
		OccurredAt:    occurredAt,
		Idempotency:   "refund:provider:7",
		Message:       "provider refund",
	})

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, -3.0, event.Amount)
	require.Equal(t, int64(7), *event.SourceEventID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func accountCostLossEventColumns() []string {
	return []string{
		"id", "account_id", "account_id_snapshot", "account_name", "platform", "account_type",
		"event_type", "reason", "status_code", "upstream_code", "message", "occurred_at",
		"currency", "amount", "accrued_cost", "recognized_cost", "billing_period_start",
		"billing_period_end", "cost_profile", "source_event_id", "idempotency_key",
		"algorithm_version", "created_at",
	}
}

func TestAccountCostLossRepositoryListsNetStateIncludingDeletedAccounts(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	occurredAt := time.Date(2026, time.August, 9, 15, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)FROM account_cost_loss_events t.*LEFT JOIN account_cost_loss_events adjustment.*LEFT JOIN accounts a`).
		WillReturnRows(sqlmock.NewRows([]string{
			"account_id_snapshot", "account_name", "platform", "account_type", "terminal_event_id",
			"occurred_at", "currency", "accrued_cost", "gross_loss", "refund_amount",
			"reversal_amount", "net_loss", "recognized_cost", "cost_profile", "active", "account_deleted",
		}).AddRow(
			42, "deleted-plus", service.PlatformOpenAI, service.AccountTypeOAuth, 7,
			occurredAt, "USD", 8.0, 12.0, 2.0, 0.0, 10.0, 18.0,
			[]byte(`{"amount":20,"currency":"USD","billing_cycle":"monthly","started_at":"2026-08-01T00:00:00Z","source":"custom","algorithm_version":"1.6.0"}`),
			true, true,
		))

	repo := NewAccountCostLossRepository(db)
	states, err := repo.ListStates(context.Background())

	require.NoError(t, err)
	require.Len(t, states, 1)
	require.True(t, states[0].Active)
	require.True(t, states[0].AccountDeleted)
	require.Equal(t, 10.0, states[0].NetLoss)
	require.Equal(t, 18.0, states[0].RecognizedCost)
	require.Equal(t, "monthly", states[0].CostProfile.BillingCycle)
	require.Equal(t, 20.0, states[0].CostProfile.Amount)
	require.NoError(t, mock.ExpectationsWereMet())
}
