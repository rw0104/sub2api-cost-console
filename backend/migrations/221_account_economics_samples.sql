-- Runtime economics samples are observations, not a second billing ledger.
-- Source-of-truth amounts remain in usage_logs and account_cost_loss_events.
CREATE TABLE IF NOT EXISTS account_economics_samples (
    id BIGSERIAL PRIMARY KEY,
    sample_bucket TIMESTAMPTZ NOT NULL,
    sampled_at TIMESTAMPTZ NOT NULL,
    scope_key VARCHAR(160) NOT NULL,
    membership_hash VARCHAR(64) NOT NULL,
    account_count INTEGER NOT NULL CHECK (account_count >= 0),
    normal_count INTEGER NOT NULL CHECK (normal_count >= 0),
    rate_limited_count INTEGER NOT NULL CHECK (rate_limited_count >= 0),
    error_count INTEGER NOT NULL CHECK (error_count >= 0),
    billed_usd_total NUMERIC(30, 12) NOT NULL CHECK (billed_usd_total >= 0),
    account_cost_usd_total NUMERIC(30, 12) NOT NULL CHECK (account_cost_usd_total >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scope_key, sample_bucket)
);

CREATE INDEX IF NOT EXISTS idx_account_economics_samples_scope_time
    ON account_economics_samples (scope_key, sampled_at DESC);

COMMENT ON TABLE account_economics_samples IS
    'Cumulative runtime observations for economics projection; not a billing or procurement ledger';
