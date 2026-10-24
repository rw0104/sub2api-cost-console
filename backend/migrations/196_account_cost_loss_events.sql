-- Independent append-only ledger for terminal procurement-account cost losses.
-- Account identity and cost-profile snapshots deliberately survive soft/hard deletes.
CREATE TABLE IF NOT EXISTS account_cost_loss_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    account_id_snapshot BIGINT NOT NULL,
    account_name VARCHAR(255) NOT NULL DEFAULT '',
    platform VARCHAR(50) NOT NULL,
    account_type VARCHAR(30) NOT NULL,
    event_type VARCHAR(30) NOT NULL
        CHECK (event_type IN ('terminal_loss', 'refund', 'reversal')),
    reason VARCHAR(80) NOT NULL,
    status_code INTEGER,
    upstream_code VARCHAR(120),
    message TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    currency VARCHAR(3) NOT NULL CHECK (currency IN ('USD', 'CNY')),
    amount NUMERIC(20,8) NOT NULL,
    accrued_cost NUMERIC(20,8) NOT NULL DEFAULT 0,
    recognized_cost NUMERIC(20,8) NOT NULL DEFAULT 0,
    billing_period_start TIMESTAMPTZ,
    billing_period_end TIMESTAMPTZ,
    cost_profile JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_event_id BIGINT REFERENCES account_cost_loss_events(id),
    idempotency_key VARCHAR(255) NOT NULL UNIQUE,
    algorithm_version VARCHAR(30) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT account_cost_loss_event_amount_direction CHECK (
        (event_type = 'terminal_loss' AND amount >= 0 AND source_event_id IS NULL)
        OR
        (event_type IN ('refund', 'reversal') AND amount <= 0 AND source_event_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_account_cost_loss_events_account
    ON account_cost_loss_events (account_id_snapshot, occurred_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_account_cost_loss_events_source
    ON account_cost_loss_events (source_event_id)
    WHERE source_event_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_account_cost_loss_events_terminal
    ON account_cost_loss_events (occurred_at DESC, id DESC)
    WHERE event_type = 'terminal_loss';
