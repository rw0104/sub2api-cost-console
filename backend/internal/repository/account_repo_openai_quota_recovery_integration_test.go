//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepositoryRecoverOpenAIQuotaIfObserved(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	newAccount := func(name, platform, kind string) *service.Account {
		return mustCreateAccount(t, tx.Client(), &service.Account{
			Name: name, Platform: platform, Type: kind, Status: service.StatusActive,
			Credentials: map[string]any{"access_token": "synthetic"}, Extra: map[string]any{"keep": "value"},
		})
	}
	a := newAccount("openai-quota-observed", service.PlatformOpenAI, service.AccountTypeOAuth)
	until := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Microsecond)
	require.NoError(t, repo.SetRateLimited(ctx, a.ID, until))
	_, err := tx.Client().Account.UpdateOneID(a.ID).SetOverloadUntil(until).SetTempUnschedulableUntil(until).Save(ctx)
	require.NoError(t, err)
	observed, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	updates := map[string]any{"codex_5h_used_percent": float64(25)}
	ok, err := repo.RecoverOpenAIQuotaIfObserved(ctx, a.ID, observed.UpdatedAt, observed.RateLimitedAt, observed.RateLimitResetAt, updates)
	require.NoError(t, err)
	require.True(t, ok)
	after, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, after.RateLimitedAt)
	require.Nil(t, after.RateLimitResetAt)
	require.Equal(t, until, after.OverloadUntil.UTC())
	require.Equal(t, until, after.TempUnschedulableUntil.UTC())
	require.Equal(t, "value", after.Extra["keep"])
	require.Equal(t, float64(25), after.Extra["codex_5h_used_percent"])

	for _, sameDeadline := range []bool{true, false} {
		require.NoError(t, repo.SetRateLimited(ctx, a.ID, until))
		old, err := repo.GetByID(ctx, a.ID)
		require.NoError(t, err)
		newLimited := old.RateLimitedAt.Add(time.Millisecond)
		newReset := *old.RateLimitResetAt
		if !sameDeadline {
			newReset = newReset.Add(-time.Minute)
		}
		_, err = tx.Client().Account.UpdateOneID(a.ID).
			SetRateLimitedAt(newLimited).SetRateLimitResetAt(newReset).
			SetUpdatedAt(old.UpdatedAt.Add(time.Millisecond)).Save(ctx)
		require.NoError(t, err)
		ok, err := repo.RecoverOpenAIQuotaIfObserved(ctx, a.ID, old.UpdatedAt, old.RateLimitedAt, old.RateLimitResetAt,
			map[string]any{"codex_5h_used_percent": float64(0)})
		require.NoError(t, err)
		require.False(t, ok)
		current, err := repo.GetByID(ctx, a.ID)
		require.NoError(t, err)
		require.True(t, current.RateLimitedAt.Equal(newLimited))
		require.True(t, current.RateLimitResetAt.Equal(newReset))
		require.Equal(t, float64(25), current.Extra["codex_5h_used_percent"], "stale recovery must not overwrite newer quota metadata")
	}

	for _, other := range []*service.Account{
		newAccount("apikey-quota-no-recovery", service.PlatformOpenAI, service.AccountTypeAPIKey),
		newAccount("grok-quota-no-recovery", service.PlatformGrok, service.AccountTypeOAuth),
	} {
		ok, err := repo.RecoverOpenAIQuotaIfObserved(ctx, other.ID, other.UpdatedAt, nil, nil, updates)
		require.NoError(t, err)
		require.False(t, ok)
	}
}
