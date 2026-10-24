//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Exercise the production providers: manually setting dependencies in a service
// test cannot detect a Wire provider that silently drops the runtime handler.
func TestAccountServiceProviders_ProbeRecordsTerminalFailure(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 90}}
	cfg := &config.Config{}
	rateLimits := ProvideRateLimitService(repo, nil, cfg, nil, nil, nil, nil, nil, nil, NewAccountCostLossService(ledger), nil)
	probe := ProvideAccountTestService(repo, nil, nil, nil, nil, nil, cfg, nil, nil, nil, rateLimits, nil)
	startedAt := time.Now().UTC().Add(-24 * time.Hour)
	account := &Account{
		ID: 80, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		CreatedAt: startedAt, UpdatedAt: startedAt, Extra: map[string]any{"plan_type": "team"},
	}

	probe.reconcileOpenAIProbeFailure(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"code":"deactivated_workspace","message":"Workspace has been deactivated"}}`))

	require.Equal(t, account.ID, ledger.recorded.AccountID)
	require.Equal(t, TerminalFailureWorkspaceDeactivated, ledger.recorded.Failure.Reason)
	require.Zero(t, repo.setErrorCalls, "the ledger transaction must own the account state update")
}

func TestRateLimitServiceProvider_RecoveryReversesTerminalFailure(t *testing.T) {
	repo := &rateLimitClearRepoStub{getByIDAccount: &Account{ID: 80, Status: StatusError}}
	ledger := &memoryCostLossRepository{states: []AccountCostLossState{{
		AccountIDSnapshot: 80, TerminalEventID: 90, Active: true, NetLoss: 12.5,
	}}}
	svc := ProvideRateLimitService(repo, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, NewAccountCostLossService(ledger), nil)

	result, err := svc.RecoverAccountAfterSuccessfulTest(context.Background(), 80)

	require.NoError(t, err)
	require.True(t, result.ClearedError)
	require.Len(t, ledger.adjustments, 1)
	require.Equal(t, AccountCostLossEventReversal, ledger.adjustments[0].EventType)
	require.Equal(t, 12.5, ledger.adjustments[0].Amount)
}

func TestRateLimitService_TerminalLedgerFailureStillDisablesAccount(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := ProvideRateLimitService(repo, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, NewAccountCostLossService(nil), nil)
	account := &Account{ID: 81, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	disabled := svc.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, http.Header{},
		[]byte(`{"error":{"code":"token_revoked","message":"revoked"}}`))

	require.True(t, disabled)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Contains(t, repo.lastErrorMsg, "Token revoked")
}
