package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// RecoverOpenAIQuotaIfObserved never delegates to the broad administrative
// ClearRateLimit operation: overload, temp-unschedulable and error state must
// survive a successful quota read. Usage and the observed quota generation are
// updated in one SQL statement so a late read cannot erase a newer 429.
func (r *accountRepository) RecoverOpenAIQuotaIfObserved(ctx context.Context, id int64, expectedUpdatedAt time.Time,
	expectedLimitedAt, expectedResetAt *time.Time, usageUpdates map[string]any,
) (bool, error) {
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.RecoverOpenAIQuotaIfObserved(ctx, id, expectedUpdatedAt, expectedLimitedAt, expectedResetAt, usageUpdates)
	}
	if usageUpdates == nil {
		usageUpdates = map[string]any{}
	}
	raw, err := json.Marshal(usageUpdates)
	if err != nil {
		return false, err
	}
	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(ctx, `UPDATE accounts
		SET rate_limited_at = NULL, rate_limit_reset_at = NULL,
			extra = COALESCE(extra, '{}'::jsonb) || $5::jsonb, updated_at = clock_timestamp()
		WHERE id = $1 AND updated_at = $2
			AND rate_limited_at IS NOT DISTINCT FROM $3::timestamptz
			AND rate_limit_reset_at IS NOT DISTINCT FROM $4::timestamptz
			AND platform = 'openai' AND type = 'oauth' AND status = 'active'
			AND parent_account_id IS NULL AND deleted_at IS NULL`,
		id, expectedUpdatedAt, expectedLimitedAt, expectedResetAt, raw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if err := enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
			logger.LegacyPrintf("repository.account", "[SchedulerOutbox] enqueue observed OpenAI quota recovery failed: account=%d err=%v", id, err)
		}
	}
	// Even a rejected CAS refreshes stale local state, never announcing a clear.
	r.syncSchedulerAccountSnapshot(ctx, id)
	return affected > 0, nil
}
