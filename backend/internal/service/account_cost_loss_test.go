//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memoryCostLossRepository struct {
	recorded    AccountCostLossDraft
	event       *AccountCostLossEvent
	states      []AccountCostLossState
	adjustments []AccountCostLossAdjustment
}

func (r *memoryCostLossRepository) RecordTerminalFailure(_ context.Context, draft AccountCostLossDraft, _ string) (*AccountCostLossEvent, bool, error) {
	r.recorded = draft
	return r.event, true, nil
}

func (r *memoryCostLossRepository) ListStates(context.Context) ([]AccountCostLossState, error) {
	return r.states, nil
}

func (r *memoryCostLossRepository) RecordAdjustment(_ context.Context, adjustment AccountCostLossAdjustment) (*AccountCostLossEvent, bool, error) {
	r.adjustments = append(r.adjustments, adjustment)
	return &AccountCostLossEvent{ID: int64(len(r.adjustments))}, true, nil
}

func TestBuildTerminalCostLossRecognizesRemainingPrepaidCycle(t *testing.T) {
	startedAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	failedAt := startedAt.Add(72 * time.Hour)
	account := &Account{
		ID:        42,
		Name:      "local-plus",
		Platform:  PlatformOpenAI,
		Type:      AccountTypeOAuth,
		CreatedAt: startedAt,
		UpdatedAt: startedAt,
		Extra: map[string]any{
			"cost_profile": map[string]any{
				"amount":            20.0,
				"currency":          "USD",
				"billing_cycle":     "monthly",
				"started_at":        startedAt.Format(time.RFC3339),
				"algorithm_version": "1.4.0",
			},
		},
	}

	draft, err := BuildTerminalCostLoss(account, TerminalFailure{
		Reason:     TerminalFailureTokenRevoked,
		StatusCode: 401,
		OccurredAt: failedAt,
	})

	require.NoError(t, err)
	require.InDelta(t, 20*72.0/730.0, draft.AccruedCost, 0.000001)
	require.InDelta(t, 20-(20*72.0/730.0), draft.LossAmount, 0.000001)
	require.InDelta(t, 20, draft.RecognizedCost, 0.000001)
	require.Equal(t, "USD", draft.Currency)
	require.Equal(t, "monthly", draft.CostProfile.BillingCycle)
}

func TestBuildTerminalCostLossOnlyAcceptsProcurementAccounts(t *testing.T) {
	startedAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	parentID := int64(9)
	profile := map[string]any{
		"cost_profile": map[string]any{
			"amount": 20.0, "currency": "USD", "billing_cycle": "monthly",
			"started_at": startedAt.Format(time.RFC3339),
		},
	}
	cases := []struct {
		name    string
		account *Account
	}{
		{name: "api key", account: &Account{ID: 1, Type: AccountTypeAPIKey, CreatedAt: startedAt, Extra: profile}},
		{name: "pool mode", account: &Account{ID: 2, Type: AccountTypeOAuth, CreatedAt: startedAt, Extra: profile, Credentials: map[string]any{"pool_mode": true}}},
		{name: "shadow", account: &Account{ID: 3, Type: AccountTypeOAuth, CreatedAt: startedAt, Extra: profile, ParentAccountID: &parentID}},
		{name: "CRS relay import", account: &Account{ID: 5, Type: AccountTypeOAuth, CreatedAt: startedAt, Extra: map[string]any{
			"cost_profile": profile["cost_profile"], "crs_account_id": "relay-account-5",
		}}},
		{name: "custom relay", account: &Account{ID: 4, Type: AccountTypeOAuth, CreatedAt: startedAt, Extra: map[string]any{
			"cost_profile": profile["cost_profile"], "custom_base_url_enabled": true, "custom_base_url": "https://relay.example.com",
		}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildTerminalCostLoss(tc.account, TerminalFailure{
				Reason: TerminalFailureAdminConfirmed, OccurredAt: startedAt.Add(time.Hour),
			})
			require.ErrorIs(t, err, ErrAccountCostLossIneligible)
		})
	}
}

func TestBuildTerminalCostLossUsesSubscriptionDefaultWhenProfileMissing(t *testing.T) {
	startedAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	account := &Account{
		ID: 7, Name: "default-plus", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		CreatedAt: startedAt, UpdatedAt: startedAt,
		Extra: map[string]any{"plan_type": "ChatGPT Plus"},
	}

	draft, err := BuildTerminalCostLoss(account, TerminalFailure{
		Reason: TerminalFailureAdminConfirmed, OccurredAt: startedAt.Add(24 * time.Hour),
	})

	require.NoError(t, err)
	require.Equal(t, 20.0, draft.CostProfile.Amount)
	require.Equal(t, "default", draft.CostProfile.Source)
	require.Equal(t, AccountCostLossAlgorithmVersion, draft.CostProfile.AlgorithmVersion)
	require.InDelta(t, 20, draft.RecognizedCost, 0.000001)
}

func TestAccountCostLossModuleConfirmsTerminalFailureThroughLedger(t *testing.T) {
	startedAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	repo := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 99}}
	module := NewAccountCostLossService(repo)
	account := &Account{
		ID: 18, Name: "procured", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		CreatedAt: startedAt, UpdatedAt: startedAt,
		Extra: map[string]any{"plan_type": "plus"},
	}

	event, created, err := module.ConfirmTerminalFailure(context.Background(), account, TerminalFailure{
		Reason: TerminalFailureTokenRevoked, StatusCode: 401, OccurredAt: startedAt.Add(48 * time.Hour),
	}, "Token revoked (401)")

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(99), event.ID)
	require.Equal(t, int64(18), repo.recorded.AccountID)
	require.Equal(t, TerminalFailureTokenRevoked, repo.recorded.Failure.Reason)
	require.NotEmpty(t, repo.recorded.IdempotencyKey)
}

func TestAccountCostLossModuleAppendsRefundAndRecoveryReversal(t *testing.T) {
	now := time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC)
	repo := &memoryCostLossRepository{states: []AccountCostLossState{
		{AccountIDSnapshot: 42, TerminalEventID: 7, Active: true, NetLoss: 12},
		{AccountIDSnapshot: 42, TerminalEventID: 8, Active: false, NetLoss: 0},
		{AccountIDSnapshot: 99, TerminalEventID: 9, Active: true, NetLoss: 5},
	}}
	module := NewAccountCostLossService(repo)

	_, created, err := module.RecordRefund(context.Background(), 7, 42, 3, now, "refund:provider:7", "provider refund")
	require.NoError(t, err)
	require.True(t, created)
	require.Len(t, repo.adjustments, 1)
	require.Equal(t, AccountCostLossEventRefund, repo.adjustments[0].EventType)
	require.Equal(t, 3.0, repo.adjustments[0].Amount)

	reversed, err := module.ReverseActiveLossesForAccount(context.Background(), 42, now.Add(time.Minute), "account recovered")
	require.NoError(t, err)
	require.Equal(t, 1, reversed)
	require.Len(t, repo.adjustments, 2)
	require.Equal(t, AccountCostLossEventReversal, repo.adjustments[1].EventType)
	require.Equal(t, int64(7), repo.adjustments[1].SourceEventID)
	require.Equal(t, "reversal:42:7", repo.adjustments[1].Idempotency)
}
