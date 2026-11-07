package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type quotaObservedRepo struct {
	stubQuotaAccountRepo
	recoveryCalls int
	recoveryErr   error
}

func (r *quotaObservedRepo) RecoverOpenAIQuotaIfObserved(_ context.Context, id int64, updated time.Time,
	limited, reset *time.Time, updates map[string]any,
) (bool, error) {
	r.recoveryCalls++
	if r.recoveryErr != nil {
		return false, r.recoveryErr
	}
	equal := func(a, b *time.Time) bool {
		return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
	}
	a := r.accounts[id]
	if !a.UpdatedAt.Equal(updated) || !equal(a.RateLimitedAt, limited) || !equal(a.RateLimitResetAt, reset) {
		return false, nil
	}
	a.RateLimitedAt, a.RateLimitResetAt = nil, nil
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	for key, value := range updates {
		a.Extra[key] = value
	}
	a.UpdatedAt = a.UpdatedAt.Add(time.Millisecond)
	return true, nil
}

func quotaRecoveryTestUsage() *OpenAIQuotaUsage {
	return &OpenAIQuotaUsage{FetchedAt: time.Now().Unix(), RateLimit: &OpenAIRateLimit{
		Allowed: true, PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 25, LimitWindowSeconds: 18000, ResetAfterSeconds: 3600},
	}}
}

func quotaRecoveryTestRepo() (*quotaObservedRepo, *Account) {
	limited := time.Now().Add(-time.Minute).UTC()
	reset := limited.Add(2 * time.Hour)
	a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		UpdatedAt: limited, RateLimitedAt: &limited, RateLimitResetAt: &reset,
		Credentials: map[string]any{"chatgpt_account_id": "synthetic-workspace"}}
	return &quotaObservedRepo{stubQuotaAccountRepo: stubQuotaAccountRepo{accounts: map[int64]*Account{42: a}}}, a
}

func TestOpenAIQuotaRecovery_Concurrent429SurvivesActualQuery(t *testing.T) {
	for _, sameReset := range []bool{false, true} {
		t.Run(map[bool]string{true: "same deadline new generation", false: "shorter deadline new generation"}[sameReset], func(t *testing.T) {
			repo, account := quotaRecoveryTestRepo()
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "synthetic-token"}}
			entered, release := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/backend-api/wham/usage" {
					close(entered)
					<-release
					_, _ = w.Write([]byte(`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":3600}}}`))
					return
				}
				_, _ = w.Write([]byte(`{"credits":[]}`))
			}))
			defer srv.Close()
			svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(srv), nil)
			type outcome struct {
				usage *OpenAIQuotaUsage
				err   error
			}
			done := make(chan outcome, 1)
			go func() { usage, err := svc.QueryUsage(context.Background(), 42); done <- outcome{usage, err} }()
			<-entered
			newLimited := account.RateLimitedAt.Add(time.Millisecond)
			newReset := *account.RateLimitResetAt
			if !sameReset {
				newReset = newReset.Add(-time.Minute)
			}
			account.RateLimitedAt, account.RateLimitResetAt, account.UpdatedAt = &newLimited, &newReset, newLimited
			close(release)
			result := <-done
			require.NoError(t, result.err)
			require.Zero(t, repo.recoveryCalls, "GET/query must remain read-only")
			recovered, err := svc.RecoverQuotaState(context.Background(), 42, result.usage)
			require.NoError(t, err)
			require.False(t, recovered)
			require.Equal(t, newLimited, *account.RateLimitedAt)
			require.Equal(t, newReset, *account.RateLimitResetAt)
			encoded, err := json.Marshal(result.usage)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "recoveryObservation")
			require.NotContains(t, string(encoded), "updatedAt")
		})
	}
}

func TestOpenAIQuotaRecovery_MatchingStatePreservesOtherBlocks(t *testing.T) {
	repo, account := quotaRecoveryTestRepo()
	overload, temp := time.Now().Add(time.Minute), time.Now().Add(2*time.Minute)
	account.OverloadUntil, account.TempUnschedulableUntil = &overload, &temp
	account.Schedulable = false
	svc := &OpenAIQuotaService{accountRepo: repo}
	usage := quotaRecoveryTestUsage()
	usage.recoveryObservation = svc.observeQuotaRecovery(context.Background(), 42)
	recovered, err := svc.RecoverQuotaState(context.Background(), 42, usage)
	require.NoError(t, err)
	require.True(t, recovered)
	require.Nil(t, account.RateLimitedAt)
	require.Nil(t, account.RateLimitResetAt)
	require.Equal(t, overload, *account.OverloadUntil)
	require.Equal(t, temp, *account.TempUnschedulableUntil)
	require.False(t, account.Schedulable)
	require.Equal(t, float64(25), account.Extra["codex_5h_used_percent"])
	usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{}
	require.NoError(t, svc.CachePostResetSnapshot(context.Background(), 42, usage))
	require.NotContains(t, repo.extraUpdates[42], "codex_5h_used_percent", "post-CAS display refresh must not overwrite a later 429's usage windows")
}

func TestOpenAIQuotaRecovery_RequiresKnownUsableWindows(t *testing.T) {
	for _, mutate := range []func(*OpenAIQuotaUsage){
		func(u *OpenAIQuotaUsage) { u.RateLimit = nil },
		func(u *OpenAIQuotaUsage) { u.RateLimit.Allowed = false },
		func(u *OpenAIQuotaUsage) { u.RateLimit.LimitReached = true },
		func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow = nil },
		func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow.UsedPercent = 100 },
		func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow.UsedPercent = math.NaN() },
		func(u *OpenAIQuotaUsage) { u.RateLimit.PrimaryWindow.LimitWindowSeconds = 0 },
	} {
		repo, _ := quotaRecoveryTestRepo()
		svc := &OpenAIQuotaService{accountRepo: repo}
		u := quotaRecoveryTestUsage()
		u.recoveryObservation = svc.observeQuotaRecovery(context.Background(), 42)
		mutate(u)
		ok, err := svc.RecoverQuotaState(context.Background(), 42, u)
		require.NoError(t, err)
		require.False(t, ok)
		require.Zero(t, repo.recoveryCalls)
	}
	for _, raw := range []string{`{}`, `{"rate_limit":{"allowed":true}}`,
		`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"limit_window_seconds":18000}}}`,
		`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":0}}}`} {
		require.False(t, openAIQuotaRecoveryEvidenceComplete([]byte(raw)))
	}
	require.True(t, openAIQuotaRecoveryEvidenceComplete([]byte(`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":0,"limit_window_seconds":18000}}}`)))
}

func TestOpenAIQuotaRecovery_ShadowAndDifferentAccountNeverClear(t *testing.T) {
	repo, account := quotaRecoveryTestRepo()
	svc := &OpenAIQuotaService{accountRepo: repo}
	u := quotaRecoveryTestUsage()
	u.recoveryObservation = svc.observeQuotaRecovery(context.Background(), 42)
	ok, err := svc.RecoverQuotaState(context.Background(), 43, u)
	require.NoError(t, err)
	require.False(t, ok)
	parent := int64(41)
	account.ParentAccountID = &parent
	require.Nil(t, svc.observeQuotaRecovery(context.Background(), 42))
	require.Zero(t, repo.recoveryCalls)
}

type observedResetQuotaStub struct {
	order    []string
	usable   bool
	queryErr error
}

func (s *observedResetQuotaStub) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	s.order = append(s.order, "query")
	return quotaRecoveryTestUsage(), s.queryErr
}
func (s *observedResetQuotaStub) RecoverQuotaState(context.Context, int64, *OpenAIQuotaUsage) (bool, error) {
	s.order = append(s.order, "recover")
	return s.usable, nil
}
func (s *observedResetQuotaStub) CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error {
	s.order = append(s.order, "cache")
	return nil
}

func TestOpenAIQuotaRecovery_ResetWorkflowNeverBlindlyClears(t *testing.T) {
	for _, usable := range []bool{true, false} {
		quota := &observedResetQuotaStub{usable: usable}
		result := RunOpenAIQuotaResetPostProcess(context.Background(), 42, quota, nil, nil)
		require.Equal(t, []string{"query", "recover", "cache"}, quota.order)
		require.Equal(t, usable, result.AccountStateRecovered)
		if !usable {
			require.Equal(t, OpenAIQuotaResetWarningStateChanged, result.WarningCode)
		}
	}
	quota := &observedResetQuotaStub{queryErr: errors.New("upstream unavailable")}
	result := RunOpenAIQuotaResetPostProcess(context.Background(), 42, quota, nil, nil)
	require.Equal(t, []string{"query"}, quota.order)
	require.False(t, result.AccountStateRecovered)
	require.Equal(t, OpenAIQuotaResetWarningCacheRefreshFailed, result.WarningCode)
}
