package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type admissionAccountRepo struct {
	AccountRepository
	account *Account
	err     error
	reads   int
}

func (r *admissionAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, r.err
}

func TestOpenAIAccountAdmission_RechecksAuthoritativeState(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	for _, tc := range []struct {
		name   string
		change func(*Account)
	}{
		{"new rate limit", func(a *Account) { until := time.Now().Add(time.Minute); a.RateLimitResetAt = &until }},
		{"new overload", func(a *Account) { until := time.Now().Add(time.Minute); a.OverloadUntil = &until }},
		{"disabled", func(a *Account) { a.Schedulable = false }},
		{"authentication failure", func(a *Account) { a.Status = StatusError }},
		{"provider changed", func(a *Account) { a.Platform = PlatformGrok }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			latest := *selected
			tc.change(&latest)
			repo := &admissionAccountRepo{account: &latest}
			svc := &OpenAIGatewayService{accountRepo: repo}
			_, reason, err := svc.RecheckOpenAIAccountAdmission(context.Background(), selected)
			require.NoError(t, err)
			require.NotEmpty(t, reason)
			require.Equal(t, 1, repo.reads)
			require.True(t, selected.IsSchedulable(), "selection snapshot must not be mutated")
		})
	}
	latest := *selected
	latest.Name = "fresh authoritative row"
	repo := &admissionAccountRepo{account: &latest}
	got, reason, err := (&OpenAIGatewayService{accountRepo: repo}).RecheckOpenAIAccountAdmission(context.Background(), selected)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Same(t, &latest, got)
}

func TestOpenAIAccountAdmission_RepositoryErrorAndCancellationFailClosed(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	repo := &admissionAccountRepo{err: errors.New("repository unavailable")}
	svc := &OpenAIGatewayService{accountRepo: repo}
	_, _, err := svc.RecheckOpenAIAccountAdmission(context.Background(), selected)
	require.ErrorIs(t, err, repo.err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = svc.RecheckOpenAIAccountAdmission(ctx, selected)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, repo.reads, "cancelled admission should not query storage")
}

func TestOpenAIAccountAdmission_ExplicitModelRechecksMappedCooldownWithoutCompositeContext(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"public-codex": "gpt-6.1-terra"}}}
	latest := *selected
	latest.Extra = map[string]any{"model_rate_limits": map[string]any{
		"gpt-6.1-terra": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
	}}
	repo := &admissionAccountRepo{account: &latest}
	svc := &OpenAIGatewayService{accountRepo: repo}
	_, reason, err := svc.RecheckOpenAIAccountAdmission(context.Background(), selected, "public-codex")
	require.NoError(t, err)
	require.Equal(t, "account_not_schedulable", reason)
	_, reason, err = svc.RecheckOpenAIAccountAdmission(context.Background(), selected, "gpt-6.1-sol")
	require.NoError(t, err)
	require.Empty(t, reason, "a model-scoped cooldown must not block a different model")
}

func TestOpenAIAccountAdmission_RejectsDifferentAccountBehindAcquiredSlot(t *testing.T) {
	selected := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	other := *selected
	other.ID = 13
	svc := &OpenAIGatewayService{accountRepo: &admissionAccountRepo{account: &other}}
	_, reason, err := svc.RecheckOpenAIAccountAdmission(context.Background(), selected, "gpt-6.1-terra")
	require.NoError(t, err)
	require.Equal(t, "account_identity_changed", reason)
	require.Equal(t, int64(12), selected.ID)
}
