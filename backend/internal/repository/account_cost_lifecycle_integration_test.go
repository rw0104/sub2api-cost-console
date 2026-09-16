//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newCostLifecycleAccount(t *testing.T) *service.Account {
	t.Helper()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	account := &service.Account{Name: t.Name(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		CreatedAt: start, UpdatedAt: start, Extra: map[string]any{"cost_profile": map[string]any{
			"amount": 730, "currency": "CNY", "billing_cycle": "monthly", "started_at": start.Format(time.RFC3339),
		}}}
	profile, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(context.Background(), `INSERT INTO accounts
		(name, platform, type, credentials, extra, status, schedulable, created_at, updated_at)
		VALUES ($1, 'openai', 'oauth', '{}', $2::jsonb, 'active', TRUE, $3, $3) RETURNING id`, account.Name, profile, start).Scan(&account.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM account_cost_loss_events WHERE account_id_snapshot = $1`, account.ID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id = $1`, account.ID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM accounts WHERE id = $1`, account.ID)
		require.NoError(t, err)
	})
	return account
}

func costLifecycleState(t *testing.T, repo service.AccountCostLossRepository, accountID int64) service.AccountCostLossState {
	t.Helper()
	states, err := repo.ListStates(context.Background())
	require.NoError(t, err)
	var active []service.AccountCostLossState
	for _, state := range states {
		if state.AccountIDSnapshot == accountID && state.Active {
			active = append(active, state)
		}
	}
	require.Len(t, active, 1)
	return active[0]
}

func TestCostLifecycleConcurrentConfirmationRefundAndRecovery(t *testing.T) {
	account := newCostLifecycleAccount(t)
	repo := NewAccountCostLossRepository(integrationDB)
	module := service.NewAccountCostLossService(repo)
	ctx := context.Background()
	failedAt := account.CreatedAt.Add(100 * time.Hour)
	type confirmation struct {
		event   *service.AccountCostLossEvent
		created bool
		err     error
	}
	results := make(chan confirmation, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(index int) {
			<-start
			event, created, err := module.ConfirmTerminalFailure(ctx, account, service.TerminalFailure{
				Reason: service.TerminalFailureAdminConfirmed, OccurredAt: failedAt,
				Idempotency: fmt.Sprintf("cost-confirm:%d:%d", account.ID, index),
			}, "confirmed")
			results <- confirmation{event, created, err}
		}(i)
	}
	close(start)
	var original *service.AccountCostLossEvent
	createdCount := 0
	for i := 0; i < 8; i++ {
		result := <-results
		require.NoError(t, result.err)
		if original == nil {
			original = result.event
		}
		require.Equal(t, original.ID, result.event.ID)
		if result.created {
			createdCount++
		}
	}
	require.Equal(t, 1, createdCount, "different retry keys still share one lifecycle")
	_, _, err := module.RecordRefund(ctx, original.ID, account.ID, 30, failedAt.Add(time.Minute), fmt.Sprintf("cost-refund:%d", account.ID), "refund")
	require.NoError(t, err)
	account.UpdatedAt = failedAt.Add(2 * time.Minute)
	// An administrative status edit is not a financial recovery. A confirmed
	// terminal must still block scheduling while reusing the original expense.
	_, err = integrationDB.Exec(`UPDATE accounts SET status='active', schedulable=TRUE WHERE id=$1`, account.ID)
	require.NoError(t, err)
	retry, created, err := module.ConfirmTerminalFailure(ctx, account, service.TerminalFailure{
		Reason: service.TerminalFailureTokenRevoked, OccurredAt: failedAt.Add(time.Hour),
	}, "repeated")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, original.ID, retry.ID)
	var schedulable bool
	require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, account.ID).Scan(&schedulable))
	require.False(t, schedulable)
	require.InDelta(t, 700, costLifecycleState(t, repo, account.ID).RecognizedCost, 1e-8)

	count, err := module.ReverseActiveLossesForAccount(ctx, account.ID, failedAt.Add(2*time.Hour), "recovered")
	require.NoError(t, err)
	require.Equal(t, 1, count)
	// The account timestamp stays unchanged; only a recorded recovery opens a new lifecycle.
	next, created, err := module.ConfirmTerminalFailure(ctx, account, service.TerminalFailure{
		Reason: service.TerminalFailureAdminConfirmed, OccurredAt: failedAt.Add(3 * time.Hour),
	}, "new failure")
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, original.ID, next.ID)
	require.Contains(t, next.IdempotencyKey, fmt.Sprintf("terminal:%d:recovery:", account.ID))

	_, err = integrationDB.Exec(`DELETE FROM accounts WHERE id = $1`, account.ID)
	require.NoError(t, err)
	_, _, err = module.RecordRefund(ctx, next.ID, account.ID, 5, failedAt.Add(4*time.Hour), fmt.Sprintf("cost-deleted-refund:%d", account.ID), "deleted account refund")
	require.NoError(t, err)
	require.True(t, costLifecycleState(t, repo, account.ID).AccountDeleted)
}

func TestCostLifecycleConcurrentRefundsCannotExceedRemainingLoss(t *testing.T) {
	account := newCostLifecycleAccount(t)
	repo := NewAccountCostLossRepository(integrationDB)
	module := service.NewAccountCostLossService(repo)
	now := account.CreatedAt.Add(100 * time.Hour)
	event, _, err := module.ConfirmTerminalFailure(context.Background(), account, service.TerminalFailure{
		Reason: service.TerminalFailureAdminConfirmed, OccurredAt: now,
	}, "confirmed")
	require.NoError(t, err)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			_, _, err := module.RecordRefund(context.Background(), event.ID, account.ID, 400, now.Add(time.Minute), fmt.Sprintf("concurrent-refund:%d:%d", account.ID, index), "refund")
			results <- err
		}(i)
	}
	succeeded := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, service.ErrInvalidCostLossAdjustment)
		}
	}
	require.Equal(t, 1, succeeded)
	require.InDelta(t, 230, costLifecycleState(t, repo, account.ID).NetLoss, 1e-8)
}

func TestCostLifecyclePreservesLegacyDuplicateRefundsAndClosesAllOnRecovery(t *testing.T) {
	account := newCostLifecycleAccount(t)
	repo := NewAccountCostLossRepository(integrationDB)
	module := service.NewAccountCostLossService(repo)
	ctx := context.Background()
	now := account.CreatedAt.Add(100 * time.Hour)
	first, _, err := module.ConfirmTerminalFailure(ctx, account, service.TerminalFailure{Reason: service.TerminalFailureAdminConfirmed, OccurredAt: now}, "confirmed")
	require.NoError(t, err)
	_, _, err = module.RecordRefund(ctx, first.ID, account.ID, 30, now.Add(time.Minute), fmt.Sprintf("legacy-refund-a:%d", account.ID), "refund")
	require.NoError(t, err)
	var duplicateID int64
	err = integrationDB.QueryRowContext(ctx, `INSERT INTO account_cost_loss_events (
		account_id, account_id_snapshot, account_name, platform, account_type, event_type, reason,
		occurred_at, currency, amount, accrued_cost, recognized_cost, cost_profile, idempotency_key, algorithm_version)
		SELECT account_id, account_id_snapshot, account_name, platform, account_type, event_type, reason,
		occurred_at + INTERVAL '1 hour', currency, amount - 1, accrued_cost + 1, recognized_cost, cost_profile,
		idempotency_key || ':legacy-duplicate', algorithm_version FROM account_cost_loss_events WHERE id = $1 RETURNING id`, first.ID).Scan(&duplicateID)
	require.NoError(t, err)
	_, _, err = module.RecordRefund(ctx, duplicateID, account.ID, 10, now.Add(2*time.Hour), fmt.Sprintf("legacy-refund-b:%d", account.ID), "refund on duplicate")
	require.NoError(t, err)
	state := costLifecycleState(t, repo, account.ID)
	require.Equal(t, first.ID, state.TerminalEventID)
	require.Equal(t, []int64{first.ID, duplicateID}, state.TerminalEventIDs)
	require.Equal(t, 40.0, state.RefundAmount)
	require.InDelta(t, 690, state.RecognizedCost, 1e-8)
	_, err = module.ReverseActiveLossesForAccount(ctx, account.ID, now.Add(3*time.Hour), "recovered")
	require.NoError(t, err)
	var recovered int
	err = integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_cost_loss_events WHERE account_id_snapshot=$1 AND event_type='reversal'`, account.ID).Scan(&recovered)
	require.NoError(t, err)
	require.Equal(t, 2, recovered)
	states, err := repo.ListStates(ctx)
	require.NoError(t, err)
	for _, state := range states {
		if state.AccountIDSnapshot == account.ID {
			require.False(t, state.Active)
		}
	}
}
