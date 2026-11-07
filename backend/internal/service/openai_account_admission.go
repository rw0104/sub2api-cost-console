package service

import (
	"context"
	"errors"
)

// RecheckOpenAIAccountAdmission validates the account after acquiring its slot.
// A repository read is authoritative here: a scheduler snapshot can predate a
// 429 or an operator disabling the account while the request was queued.
// Model/protocol/group selection remains the scheduler's responsibility.
func (s *OpenAIGatewayService) RecheckOpenAIAccountAdmission(ctx context.Context, selected *Account, requestModels ...string) (*Account, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if selected == nil {
		return nil, "account_missing", nil
	}
	latest := selected
	if s != nil && s.accountRepo != nil {
		var err error
		latest, err = s.accountRepo.GetByID(ctx, selected.ID)
		if errors.Is(err, ErrAccountNotFound) || (err == nil && latest == nil) {
			return nil, "account_missing", nil
		}
		if err != nil {
			return nil, "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if latest.ID != selected.ID || latest.Platform != selected.Platform || latest.Type != selected.Type {
		return latest, "account_identity_changed", nil
	}
	requestedModel, _ := RequestedPublicModelFromContext(ctx)
	if len(requestModels) > 0 {
		requestedModel = requestModels[0]
	}
	if !latest.IsSchedulableForModelWithContext(ctx, requestedModel) {
		return latest, "account_not_schedulable", nil
	}
	if latest.IsOpenAI() {
		if paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, latest); paused {
			return latest, "account_quota_paused", nil
		}
	}
	if latest.IsGrok() {
		if paused, _ := shouldAutoPauseGrokAccountByQuota(latest); paused {
			return latest, "account_quota_paused", nil
		}
	}
	if s != nil {
		if s.isOpenAIAccountRequestRuntimeBlocked(latest, requestedModel) {
			return latest, "account_runtime_blocked", nil
		}
		if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, latest) {
			return latest, "account_quota_paused", nil
		}
		if !parentHealthyForShadow(latest, s.parentAccountLookup(ctx)) {
			return latest, "parent_unavailable", nil
		}
		if s.isOpenAIProxyStreamQuarantined(ctx, latest) {
			return latest, "proxy_quarantined", nil
		}
	}
	// Use the same authoritative row for profit admission, not another cache
	// read that could replace it with an older scheduling snapshot.
	if vetoed, reason := OpenAIProfitControlVeto(ctx, latest); vetoed {
		return latest, reason, nil
	}
	return latest, "", nil
}
