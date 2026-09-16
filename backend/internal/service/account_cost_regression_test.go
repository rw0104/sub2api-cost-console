package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Accounting regressions run in the regular test suite.
func costRegressionAccount(kind, cycle string, amount float64, start time.Time) Account {
	return Account{
		ID: 42, Name: "audit-account", Type: kind, Platform: PlatformOpenAI,
		CreatedAt: start, UpdatedAt: start, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{"cost_profile": map[string]any{
			"amount": amount, "currency": "CNY", "billing_cycle": cycle,
			"started_at": start.Format(time.RFC3339), "algorithm_version": "1.6.0",
		}},
	}
}

func TestCostRegressionMeteredFixedOverheadIsIncludedInLifetimeCost(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	account := costRegressionAccount(AccountTypeAPIKey, "one_time", 100, start)
	profile, err := resolveAccountCostProfileSnapshot(&account)
	require.NoError(t, err)
	repo := &economicsRepositoryStub{profiles: []AccountProcurementProfile{{AccountID: account.ID, Platform: account.Platform, CostProfile: profile}}}
	svc := NewAccountEconomicsService(&economicsAccountReaderStub{accounts: []Account{account}}, repo, NewAccountCostLossService(&economicsCostLossRepositoryStub{}))
	result, err := svc.GetSnapshot(context.Background(), AccountEconomicsQuery{Now: start.Add(time.Hour), Window: 2 * time.Hour, CNYPerUSD: 7})
	require.NoError(t, err)
	t.Logf("explicit API-key overhead: window=%g lifetime=%g quality=%s", result.Actual.WindowProcurementCNY, result.Actual.ProcurementAccruedCNY, result.DataQuality.Status)
	require.Equal(t, 100.0, result.Actual.WindowProcurementCNY)
	assert.Equal(t, 100.0, result.Actual.ProcurementAccruedCNY, "explicit fixed overhead must survive in lifetime economics")
}

func TestCostRegressionFixedOverheadAndDefaultPricingAreIndependentOfLossEligibility(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeUpstream, AccountTypeBedrock, AccountTypeServiceAccount, AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(kind, func(t *testing.T) {
			account := costRegressionAccount(kind, "daily", 24, start)
			account.Extra["plan_type"] = "pro"
			procurement, _, hourly, invalid := summarizeProcurementEconomics([]Account{account}, nil, "openai", 7, start.Add(12*time.Hour), false)
			require.Equal(t, 12.0, procurement)
			require.Equal(t, 1.0, hourly)
			require.Zero(t, invalid)
			delete(account.Extra, "cost_profile")
			profile, err := resolveAccountCostProfileSnapshot(&account)
			require.NoError(t, err)
			if kind == AccountTypeOAuth || kind == AccountTypeSetupToken {
				require.Equal(t, 100.0, profile.Amount)
			} else {
				require.Zero(t, profile.Amount)
			}
		})
	}
}

func TestCostRegressionDeletingAccountSeparatesCurrentPoolFromMonthlyHistory(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	account := costRegressionAccount(AccountTypeOAuth, "one_time", 100, start)
	profile, err := resolveAccountCostProfileSnapshot(&account)
	require.NoError(t, err)
	reader := &economicsAccountReaderStub{accounts: []Account{account}}
	repo := &economicsRepositoryStub{profiles: []AccountProcurementProfile{{AccountID: account.ID, Platform: account.Platform, CostProfile: profile}}}
	svc := NewAccountEconomicsService(reader, repo, NewAccountCostLossService(&economicsCostLossRepositoryStub{}))
	query := AccountEconomicsQuery{Now: start.Add(time.Hour), Window: 2 * time.Hour, CNYPerUSD: 7}
	before, err := svc.GetSnapshot(context.Background(), query)
	require.NoError(t, err)
	reader.accounts = nil
	repo.profiles[0].Deleted = true
	after, err := svc.GetSnapshot(context.Background(), query)
	require.NoError(t, err)
	t.Logf("deleted paid account: current pool before=%g after=%g retained monthly purchases=%g", before.Actual.EconomicCostCNY, after.Actual.EconomicCostCNY, after.Actual.MonthOneTimeProcurementCNY)
	require.Equal(t, 100.0, before.Actual.EconomicCostCNY)
	require.Equal(t, 100.0, after.Actual.MonthOneTimeProcurementCNY)
	// The UI explicitly labels this as current-pool economics, not an all-time ledger.
	// Retained monthly purchases are the separate historical measure.
	assert.Zero(t, after.Actual.EconomicCostCNY)
}

func TestCostRegressionRepeatedTerminalConfirmationPreservesRefund(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	account := costRegressionAccount(AccountTypeOAuth, "monthly", 730, start)
	failure := TerminalFailure{Reason: TerminalFailureAdminConfirmed, OccurredAt: start.Add(100 * time.Hour)}
	first, err := BuildTerminalCostLoss(&account, failure)
	require.NoError(t, err)
	state := AccountCostLossState{AccountIDSnapshot: account.ID, Platform: account.Platform,
		TerminalEventID: 1, OccurredAt: failure.OccurredAt, Currency: "CNY", Active: true,
		AccruedCost: first.AccruedCost, GrossLoss: first.LossAmount, RefundAmount: 30,
		NetLoss: first.LossAmount - 30, CostProfile: first.CostProfile}
	// RecordTerminalFailure updates accounts.updated_at without opening a new lifecycle.
	account.UpdatedAt = failure.OccurredAt
	account.Status = StatusError
	account.Schedulable = false
	failure.OccurredAt = failure.OccurredAt.Add(time.Hour)
	second, err := BuildTerminalCostLoss(&account, failure)
	require.NoError(t, err)
	require.Empty(t, first.IdempotencyKey, "the repository owns lifecycle idempotency")
	require.Empty(t, second.IdempotencyKey)
	duplicate := AccountCostLossState{AccountIDSnapshot: account.ID, Platform: account.Platform,
		TerminalEventID: 2, OccurredAt: failure.OccurredAt, Currency: "CNY", Active: true,
		AccruedCost: second.AccruedCost, GrossLoss: second.LossAmount, NetLoss: second.LossAmount, CostProfile: second.CostProfile}
	procurement, impairment, _, _ := summarizeProcurementEconomics([]Account{account}, []AccountCostLossState{state, duplicate}, "openai", 7, failure.OccurredAt, true)
	t.Logf("same terminal lifecycle: after refund=%g after repeated confirmation=%g", first.RecognizedCost-30, procurement+impairment)
	assert.Equal(t, first.RecognizedCost-30, procurement+impairment, "reconfirming the same failure must not cancel the recorded refund")
}
