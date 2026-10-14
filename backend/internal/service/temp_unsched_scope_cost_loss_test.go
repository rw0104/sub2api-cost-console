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

// Upstream v0.2.16 lets a temporary-unschedulable rule declare a model scope.
// This fork records a terminal cost-loss ledger entry for confirmed dead
// accounts. The two features meet on the same 401/402 responses, so these
// tests pin which one owns each case: a model-scoped rule must never swallow a
// confirmed terminal failure, and a recoverable model-level cooldown must
// never produce a ledger entry.

func tempUnschedScopeRule(statusCode int, keyword, scope string) map[string]any {
	rule := map[string]any{
		"error_code":       float64(statusCode),
		"keywords":         []any{keyword},
		"duration_minutes": float64(10),
	}
	if scope != "" {
		rule["scope"] = scope
	}
	return rule
}

// newLedgerEligibleOAuthAccount returns a procurement account that
// BuildTerminalCostLoss accepts, so a missing ledger write is a real finding
// rather than an eligibility rejection.
func newLedgerEligibleOAuthAccount(rules ...map[string]any) *Account {
	startedAt := time.Now().UTC().Add(-24 * time.Hour)
	ruleValues := make([]any, 0, len(rules))
	for _, rule := range rules {
		ruleValues = append(ruleValues, rule)
	}
	return &Account{
		ID: 310, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		CreatedAt: startedAt, UpdatedAt: startedAt,
		Extra: map[string]any{"plan_type": "team"},
		Credentials: map[string]any{
			"refresh_token":              "rt",
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules":   ruleValues,
		},
	}
}

func newModelScopedAPIKeyAccount(rules ...map[string]any) *Account {
	ruleValues := make([]any, 0, len(rules))
	for _, rule := range rules {
		ruleValues = append(ruleValues, rule)
	}
	return &Account{
		ID: 311, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules":   ruleValues,
		},
	}
}

func newCostLossRateLimitService(repo AccountRepository, ledger *memoryCostLossRepository) *RateLimitService {
	return &RateLimitService{
		accountRepo:     repo,
		cfg:             &config.Config{},
		accountCostLoss: NewAccountCostLossService(ledger),
	}
}

// A model-scoped 401 on an API-key account is a recoverable, model-local
// cooldown. It must not reach the terminal ledger or disable the account.
func TestModelScoped401CoolsTheModelWithoutTerminalLedger(t *testing.T) {
	repo := &tempUnschedScopeRepoStub{}
	ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 401}}
	svc := newCostLossRateLimitService(repo, ledger)
	account := newModelScopedAPIKeyAccount(tempUnschedScopeRule(http.StatusUnauthorized, "unauthorized", TempUnschedScopeModel))

	handled := svc.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, http.Header{},
		[]byte(`{"error":{"message":"unauthorized"}}`), "gpt-5.4")

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1, "the rule must cool only the requested model")
	require.Equal(t, "gpt-5.4", repo.modelRateLimitCalls[0].scope)
	require.Zero(t, repo.errorCalls, "a model-local cooldown must not disable the account")
	require.Zero(t, repo.tempCalls, "a model-scoped hit must not pause the whole account")
	require.Zero(t, ledger.recorded.AccountID, "a recoverable cooldown must not write a cost-loss ledger entry")
}

// A revoked credential is terminal even when the operator configured a
// model-scoped 401 rule: the rule cannot keep a dead account schedulable.
func TestCredentialLevel401RecordsTerminalLedgerDespiteModelScopedRule(t *testing.T) {
	for _, upstreamCode := range []string{"token_revoked", "token_invalidated"} {
		t.Run(upstreamCode, func(t *testing.T) {
			repo := &tempUnschedScopeRepoStub{}
			ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 402}}
			svc := newCostLossRateLimitService(repo, ledger)
			account := newLedgerEligibleOAuthAccount(tempUnschedScopeRule(http.StatusUnauthorized, "unauthorized", TempUnschedScopeModel))
			body := []byte(`{"error":{"code":"` + upstreamCode + `","message":"unauthorized"}}`)

			disabled := svc.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, http.Header{}, body, "gpt-5.4")

			require.True(t, disabled)
			require.Equal(t, account.ID, ledger.recorded.AccountID, "a revoked credential must reach the cost-loss ledger")
			require.Equal(t, TerminalFailureTokenRevoked, ledger.recorded.Failure.Reason)
			require.Empty(t, repo.modelRateLimitCalls, "a credential failure must not be isolated to one model")
		})
	}
}

// 402 deactivated_workspace is confirmed dead upstream. It is handled before
// any rule evaluation, so a model-scoped 402 rule must not intercept it.
func TestWorkspaceDeactivatedRecordsTerminalLedgerBeforeModelScopedRule(t *testing.T) {
	repo := &tempUnschedScopeRepoStub{}
	ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 403}}
	svc := newCostLossRateLimitService(repo, ledger)
	account := newLedgerEligibleOAuthAccount(tempUnschedScopeRule(http.StatusPaymentRequired, "deactivated", TempUnschedScopeModel))

	disabled := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"code":"deactivated_workspace","message":"workspace has been deactivated"}}`), "gpt-5.4")

	require.True(t, disabled)
	require.Equal(t, account.ID, ledger.recorded.AccountID)
	require.Equal(t, TerminalFailureWorkspaceDeactivated, ledger.recorded.Failure.Reason)
	require.Empty(t, repo.modelRateLimitCalls, "a dead workspace must not be reduced to a model cooldown")
	require.Zero(t, repo.tempCalls, "a dead workspace must not be reduced to a temporary pause")
}

// Pool mode normally skips local account-state handling, and upstream now lets
// explicit rules run inside it. A dead workspace still has to stop scheduling.
func TestPoolModeWorkspaceDeactivatedStillStopsSchedulingWithModelScopedRule(t *testing.T) {
	repo := &tempUnschedScopeRepoStub{}
	ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 404}}
	svc := newCostLossRateLimitService(repo, ledger)
	account := newLedgerEligibleOAuthAccount(tempUnschedScopeRule(http.StatusPaymentRequired, "deactivated", TempUnschedScopeModel))
	account.Credentials["pool_mode"] = true

	disabled := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"code":"deactivated_workspace","message":"workspace has been deactivated"}}`), "gpt-5.4")

	require.True(t, disabled)
	// A pool-mode account is not a procurement account, so the ledger declines
	// it; the terminal path must still fall through to disabling the account
	// instead of returning through the pool-mode skip.
	require.Zero(t, ledger.recorded.AccountID, "pool mode is ineligible for the procurement ledger")
	require.Equal(t, 1, repo.errorCalls, "an ineligible ledger write must still stop scheduling")
	require.Empty(t, repo.modelRateLimitCalls)
}

// The OpenAI fastpath calls the same rule and ledger code with a known model.
// Entering through it must not change which feature owns the response.
func TestFastpathModelScopeKeepsTerminalLedgerSemantics(t *testing.T) {
	t.Run("model-scoped 401 cools the model only", func(t *testing.T) {
		repo := &tempUnschedScopeRepoStub{}
		ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 405}}
		gateway := &OpenAIGatewayService{rateLimitService: newCostLossRateLimitService(repo, ledger)}
		account := newModelScopedAPIKeyAccount(tempUnschedScopeRule(http.StatusUnauthorized, "unauthorized", TempUnschedScopeModel))

		handled := gateway.handleOpenAIAccountUpstreamError(context.Background(), account,
			http.StatusUnauthorized, http.Header{}, []byte(`{"error":{"message":"unauthorized"}}`), "gpt-5.4")

		require.True(t, handled)
		require.Len(t, repo.modelRateLimitCalls, 1)
		require.Zero(t, ledger.recorded.AccountID)
		require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account),
			"a model-local cooldown must leave the account schedulable for other models")
	})

	t.Run("revoked credential records the ledger and blocks the account", func(t *testing.T) {
		repo := &tempUnschedScopeRepoStub{}
		ledger := &memoryCostLossRepository{event: &AccountCostLossEvent{ID: 406}}
		gateway := &OpenAIGatewayService{rateLimitService: newCostLossRateLimitService(repo, ledger)}
		account := newLedgerEligibleOAuthAccount(tempUnschedScopeRule(http.StatusUnauthorized, "unauthorized", TempUnschedScopeModel))

		gateway.handleOpenAIAccountUpstreamError(context.Background(), account,
			http.StatusUnauthorized, http.Header{}, []byte(`{"error":{"code":"token_revoked","message":"unauthorized"}}`), "gpt-5.4")

		require.Equal(t, account.ID, ledger.recorded.AccountID)
		require.Equal(t, TerminalFailureTokenRevoked, ledger.recorded.Failure.Reason)
		require.Empty(t, repo.modelRateLimitCalls)
		require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	})
}
