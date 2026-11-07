package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// This narrow capability deliberately leaves AccountRepository and the manual
// ClearRateLimit contract unchanged. The repository owns the atomic comparison.
type openAIQuotaRecoveryRepository interface {
	RecoverOpenAIQuotaIfObserved(context.Context, int64, time.Time, *time.Time, *time.Time, map[string]any) (bool, error)
}

type openAIQuotaRecoveryObservation struct {
	accountID int64
	updatedAt time.Time
	limitedAt *time.Time
	resetAt   *time.Time
}

// OpenAIQuotaStateRecoverer is optional for older quota-service implementations.
// QueryUsage remains read-only; explicit refresh/reset callers opt into recovery.
type OpenAIQuotaStateRecoverer interface {
	RecoverQuotaState(context.Context, int64, *OpenAIQuotaUsage) (bool, error)
}

func openAIQuotaRecoveryEvidenceComplete(raw []byte) bool {
	type window struct {
		UsedPercent *float64 `json:"used_percent"`
		Seconds     *int64   `json:"limit_window_seconds"`
	}
	var evidence struct {
		RateLimit *struct {
			Allowed      *bool   `json:"allowed"`
			LimitReached *bool   `json:"limit_reached"`
			Primary      *window `json:"primary_window"`
			Secondary    *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if json.Unmarshal(raw, &evidence) != nil || evidence.RateLimit == nil {
		return false
	}
	limit := evidence.RateLimit
	if limit.Allowed == nil || limit.LimitReached == nil {
		return false
	}
	found := false
	for _, value := range []*window{limit.Primary, limit.Secondary} {
		if value != nil {
			if value.UsedPercent == nil || value.Seconds == nil {
				return false
			}
			found = true
		}
	}
	return found
}

func (s *OpenAIQuotaService) observeQuotaRecovery(ctx context.Context, accountID int64) *openAIQuotaRecoveryObservation {
	if _, ok := s.accountRepo.(openAIQuotaRecoveryRepository); !ok {
		return nil
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		slog.Warn("openai_quota_recovery_observation_failed", "account_id", accountID, "error", err)
		return nil
	}
	// Parent and Spark windows are independent. Do not recover a shadow using
	// the parent's main quota, or clear unrelated credential/runtime failures.
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() || account.Status != StatusActive {
		return nil
	}
	copyTime := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	return &openAIQuotaRecoveryObservation{
		accountID: accountID, updatedAt: account.UpdatedAt,
		limitedAt: copyTime(account.RateLimitedAt), resetAt: copyTime(account.RateLimitResetAt),
	}
}

func openAIQuotaConfirmsUsable(usage *OpenAIQuotaUsage) bool {
	if usage == nil || usage.RateLimit == nil || !usage.RateLimit.Allowed || usage.RateLimit.LimitReached {
		return false
	}
	observedWindow := false
	for _, window := range []*OpenAIRateLimitWindow{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
		if window == nil {
			continue
		}
		if window.LimitWindowSeconds <= 0 || math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) || window.UsedPercent < 0 || window.UsedPercent >= 100 {
			return false
		}
		observedWindow = true
	}
	return observedWindow
}

// RecoverQuotaState commits the confirmed usable windows and clears only the
// exact global quota generation observed BEFORE the upstream query. A false
// result is normal when a newer 429, credential edit or admin action won the race.
func (s *OpenAIQuotaService) RecoverQuotaState(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage) (bool, error) {
	if usage == nil {
		return false, nil
	}
	usage.recoveryHandled = true
	observation := usage.recoveryObservation
	if observation == nil || observation.accountID != accountID || !openAIQuotaConfirmsUsable(usage) {
		return false, nil
	}
	if err := scopedAccountIDAccess(ctx, s.accountRepo, accountID, scopedAccountOperation(ctx, "openai.quota.read"), true); err != nil {
		return false, err
	}
	repo, ok := s.accountRepo.(openAIQuotaRecoveryRepository)
	if !ok {
		return false, nil
	}
	now := time.Now()
	if usage.FetchedAt > 0 {
		now = time.Unix(usage.FetchedAt, 0)
	}
	updated, err := repo.RecoverOpenAIQuotaIfObserved(ctx, accountID, observation.updatedAt,
		observation.limitedAt, observation.resetAt, buildOpenAIAutoResetUsageUpdates(usage, now))
	if err != nil {
		return false, infraerrors.InternalServer("OPENAI_QUOTA_RECOVERY_FAILED", "failed to recover quota scheduling state").WithCause(err)
	}
	if updated {
		slog.Info("openai_quota_state_recovered", "account_id", accountID)
	}
	return updated, nil
}

// Recovery can remain unconfirmed because the account changed concurrently or
// the upstream response did not provide sufficient usable-quota evidence.
const OpenAIQuotaResetWarningStateChanged = "quota_recovery_not_confirmed"

func runObservedOpenAIQuotaResetPostProcess(ctx context.Context, accountID int64, quota openAIQuotaResetWorkflowQuota,
	recovery OpenAIQuotaStateRecoverer, loadAccount func(context.Context, int64) (*Account, error),
) OpenAIQuotaResetPostProcessResult {
	result := OpenAIQuotaResetPostProcessResult{}
	usage, err := quota.QueryUsage(ctx, accountID)
	if err != nil || usage == nil {
		result.WarningCode = OpenAIQuotaResetWarningCacheRefreshFailed
	} else {
		result.Quota = usage
		recovered, recoveryErr := recovery.RecoverQuotaState(ctx, accountID, usage)
		result.AccountStateRecovered = recovered
		if recoveryErr != nil {
			result.WarningCode = OpenAIQuotaResetWarningAccountRecoveryFailed
		} else if !recovered {
			result.WarningCode = OpenAIQuotaResetWarningStateChanged
		}
		// The observation-aware service has already atomically handled usage
		// windows; this only refreshes independent display caches after the CAS.
		if cacheErr := quota.CachePostResetSnapshot(ctx, accountID, usage); cacheErr != nil {
			if result.WarningCode == "" {
				result.WarningCode = OpenAIQuotaResetWarningCacheRefreshFailed
			}
		} else {
			result.CacheRefreshed = true
		}
	}
	if loadAccount != nil {
		account, loadErr := loadAccount(ctx, accountID)
		if loadErr != nil {
			if result.WarningCode == "" {
				result.WarningCode = OpenAIQuotaResetWarningAccountRefreshFailed
			}
		} else {
			result.Account = account
		}
	}
	return result
}
