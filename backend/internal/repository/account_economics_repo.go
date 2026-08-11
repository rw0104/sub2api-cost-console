package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountEconomicsRepository struct {
	db *sql.DB
}

func NewAccountEconomicsRepository(db *sql.DB) service.AccountEconomicsRepository {
	return &accountEconomicsRepository{db: db}
}

func (r *accountEconomicsRepository) SumUsageTotals(ctx context.Context, accountIDs []int64) (service.AccountEconomicsUsageTotals, error) {
	if r == nil || r.db == nil {
		return service.AccountEconomicsUsageTotals{}, errors.New("account economics repository is unavailable")
	}
	if len(accountIDs) == 0 {
		return service.AccountEconomicsUsageTotals{}, nil
	}
	var totals service.AccountEconomicsUsageTotals
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0)
		FROM usage_logs
		WHERE account_id = ANY($1)
	`, pq.Array(accountIDs)).Scan(&totals.BilledUSD, &totals.AccountCostUSD)
	if err != nil {
		return service.AccountEconomicsUsageTotals{}, fmt.Errorf("query account economics usage totals: %w", err)
	}
	return totals, nil
}

func (r *accountEconomicsRepository) UpsertSample(ctx context.Context, sample service.AccountEconomicsSample) error {
	if r == nil || r.db == nil {
		return errors.New("account economics repository is unavailable")
	}
	bucket := accountEconomicsSampleBucket(sample.SampledAt)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO account_economics_samples (
			sample_bucket, sampled_at, scope_key, membership_hash,
			account_count, normal_count, rate_limited_count, error_count,
			billed_usd_total, account_cost_usd_total
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (scope_key, sample_bucket) DO UPDATE SET
			sampled_at = EXCLUDED.sampled_at,
			membership_hash = EXCLUDED.membership_hash,
			account_count = EXCLUDED.account_count,
			normal_count = EXCLUDED.normal_count,
			rate_limited_count = EXCLUDED.rate_limited_count,
			error_count = EXCLUDED.error_count,
			billed_usd_total = EXCLUDED.billed_usd_total,
			account_cost_usd_total = EXCLUDED.account_cost_usd_total
	`, bucket, sample.SampledAt.UTC(), sample.ScopeKey, sample.MembershipHash,
		sample.AccountCount, sample.NormalCount, sample.RateLimitedCount, sample.ErrorCount,
		sample.BilledUSDTotal, sample.AccountCostUSDTotal)
	if err != nil {
		return fmt.Errorf("upsert account economics sample: %w", err)
	}
	return nil
}

func accountEconomicsSampleBucket(sampledAt time.Time) time.Time {
	// Preserve the shortest supported event window without turning the always-on
	// sampler into a high-volume writer. The background job still runs once per
	// minute; foreground refreshes and state events can retain 5-second evidence.
	return sampledAt.UTC().Truncate(5 * time.Second)
}

func (r *accountEconomicsRepository) ListSamples(ctx context.Context, scopeKey string, since time.Time) ([]service.AccountEconomicsSample, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("account economics repository is unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT sampled_at, scope_key, membership_hash,
			account_count, normal_count, rate_limited_count, error_count,
			billed_usd_total, account_cost_usd_total
		FROM account_economics_samples
		WHERE scope_key = $1 AND sampled_at >= $2
		ORDER BY sampled_at ASC
	`, scopeKey, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("query account economics samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	samples := make([]service.AccountEconomicsSample, 0)
	for rows.Next() {
		var sample service.AccountEconomicsSample
		if err := rows.Scan(
			&sample.SampledAt, &sample.ScopeKey, &sample.MembershipHash,
			&sample.AccountCount, &sample.NormalCount, &sample.RateLimitedCount, &sample.ErrorCount,
			&sample.BilledUSDTotal, &sample.AccountCostUSDTotal,
		); err != nil {
			return nil, fmt.Errorf("scan account economics sample: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account economics samples: %w", err)
	}
	return samples, nil
}

func (r *accountEconomicsRepository) PruneSamples(ctx context.Context, before time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("account economics repository is unavailable")
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM account_economics_samples WHERE sampled_at < $1`, before.UTC()); err != nil {
		return fmt.Errorf("prune account economics samples: %w", err)
	}
	return nil
}
