package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountCostLossRepository struct {
	db *sql.DB
}

func NewAccountCostLossRepository(db *sql.DB) service.AccountCostLossRepository {
	return &accountCostLossRepository{db: db}
}

func (r *accountCostLossRepository) RecordTerminalFailure(
	ctx context.Context,
	draft service.AccountCostLossDraft,
	errorMessage string,
) (*service.AccountCostLossEvent, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("account cost loss repository is unavailable")
	}
	profileJSON, err := json.Marshal(draft.CostProfile)
	if err != nil {
		return nil, false, fmt.Errorf("marshal account cost profile: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var eventID int64
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO account_cost_loss_events (
			account_id, account_id_snapshot, account_name, platform, account_type,
			event_type, reason, status_code, upstream_code, message, occurred_at,
			currency, amount, accrued_cost, recognized_cost,
			billing_period_start, billing_period_end, cost_profile,
			idempotency_key, algorithm_version
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
			$12,$13,$14,$15,$16,$17,$18::jsonb,$19,$20
		)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, created_at
	`,
		draft.AccountID, draft.AccountID, draft.AccountName, draft.Platform, draft.AccountType,
		service.AccountCostLossEventTerminal, draft.Failure.Reason, nullableInt(draft.Failure.StatusCode), nullableString(draft.Failure.UpstreamCode), draft.Failure.Message, draft.Failure.OccurredAt.UTC(),
		draft.Currency, draft.LossAmount, draft.AccruedCost, draft.RecognizedCost,
		nullableTime(draft.BillingPeriodAt), nullableTime(draft.BillingPeriodTo), profileJSON,
		draft.IdempotencyKey, draft.Algorithm,
	).Scan(&eventID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		event, loadErr := loadAccountCostLossEventByKey(ctx, tx, draft.IdempotencyKey)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, false, commitErr
		}
		return event, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE accounts
		SET status = $1,
			error_message = $2,
			schedulable = FALSE,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL
	`, service.StatusError, errorMessage, draft.AccountID)
	if err != nil {
		return nil, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if updated != 1 {
		return nil, false, service.ErrAccountNotFound
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &draft.AccountID, nil, nil); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}

	accountID := draft.AccountID
	periodStart, periodEnd := draft.BillingPeriodAt, draft.BillingPeriodTo
	return &service.AccountCostLossEvent{
		ID: eventID, AccountID: &accountID, AccountIDSnapshot: draft.AccountID,
		AccountName: draft.AccountName, Platform: draft.Platform, AccountType: draft.AccountType,
		EventType: service.AccountCostLossEventTerminal, Reason: draft.Failure.Reason,
		StatusCode: draft.Failure.StatusCode, UpstreamCode: draft.Failure.UpstreamCode,
		Message: draft.Failure.Message, OccurredAt: draft.Failure.OccurredAt.UTC(),
		Currency: draft.Currency, Amount: draft.LossAmount, AccruedCost: draft.AccruedCost,
		RecognizedCost: draft.RecognizedCost, BillingPeriodAt: &periodStart, BillingPeriodTo: &periodEnd,
		CostProfile: draft.CostProfile, IdempotencyKey: draft.IdempotencyKey,
		AlgorithmVersion: draft.Algorithm, CreatedAt: createdAt,
	}, true, nil
}

func (r *accountCostLossRepository) ListStates(ctx context.Context) ([]service.AccountCostLossState, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("account cost loss repository is unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			t.account_id_snapshot,
			t.account_name,
			t.platform,
			t.account_type,
			t.id AS terminal_event_id,
			t.occurred_at,
			t.currency,
			t.accrued_cost,
			t.amount AS gross_loss,
			COALESCE(-SUM(adjustment.amount) FILTER (WHERE adjustment.event_type = 'refund'), 0) AS refund_amount,
			COALESCE(-SUM(adjustment.amount) FILTER (WHERE adjustment.event_type = 'reversal'), 0) AS reversal_amount,
			GREATEST(0, t.amount + COALESCE(SUM(adjustment.amount), 0)) AS net_loss,
			t.accrued_cost + GREATEST(0, t.amount + COALESCE(SUM(adjustment.amount), 0)) AS recognized_cost,
			t.cost_profile,
			(COUNT(adjustment.id) FILTER (WHERE adjustment.event_type = 'reversal') = 0) AS active,
			(t.account_id IS NULL OR a.id IS NULL OR a.deleted_at IS NOT NULL) AS account_deleted
		FROM account_cost_loss_events t
		LEFT JOIN account_cost_loss_events adjustment ON adjustment.source_event_id = t.id
		LEFT JOIN accounts a ON a.id = t.account_id
		WHERE t.event_type = 'terminal_loss'
		GROUP BY t.id, a.id, a.deleted_at
		ORDER BY t.occurred_at DESC, t.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make([]service.AccountCostLossState, 0)
	for rows.Next() {
		var state service.AccountCostLossState
		var profileJSON []byte
		if err := rows.Scan(
			&state.AccountIDSnapshot, &state.AccountName, &state.Platform, &state.AccountType,
			&state.TerminalEventID, &state.OccurredAt, &state.Currency, &state.AccruedCost,
			&state.GrossLoss, &state.RefundAmount, &state.ReversalAmount, &state.NetLoss,
			&state.RecognizedCost, &profileJSON, &state.Active, &state.AccountDeleted,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(profileJSON, &state.CostProfile); err != nil {
			return nil, fmt.Errorf("unmarshal account cost profile state: %w", err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func (r *accountCostLossRepository) RecordAdjustment(
	ctx context.Context,
	adjustment service.AccountCostLossAdjustment,
) (*service.AccountCostLossEvent, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("account cost loss repository is unavailable")
	}
	if adjustment.SourceEventID <= 0 || adjustment.AccountID <= 0 || adjustment.Amount < 0 ||
		adjustment.OccurredAt.IsZero() || adjustment.Idempotency == "" ||
		(adjustment.EventType != service.AccountCostLossEventRefund && adjustment.EventType != service.AccountCostLossEventReversal) {
		return nil, false, service.ErrInvalidCostLossAdjustment
	}
	if adjustment.EventType == service.AccountCostLossEventRefund && adjustment.Amount <= 0 {
		return nil, false, service.ErrInvalidCostLossAdjustment
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := loadAccountCostLossEventByKey(ctx, tx, adjustment.Idempotency)
	if err == nil {
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, false, commitErr
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	source, err := scanAccountCostLossEvent(tx.QueryRowContext(ctx,
		accountCostLossEventSelect+" WHERE id = $1 AND event_type = 'terminal_loss' FOR UPDATE",
		adjustment.SourceEventID,
	))
	if err != nil {
		return nil, false, err
	}
	if source.AccountIDSnapshot != adjustment.AccountID {
		return nil, false, service.ErrInvalidCostLossAdjustment
	}

	var priorAdjustments float64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM account_cost_loss_events
		WHERE source_event_id = $1
		  AND event_type IN ('refund', 'reversal')
	`, adjustment.SourceEventID).Scan(&priorAdjustments); err != nil {
		return nil, false, err
	}
	outstanding := math.Max(0, source.Amount+priorAdjustments)
	consume := adjustment.Amount
	if adjustment.EventType == service.AccountCostLossEventReversal {
		consume = outstanding
	}
	if (adjustment.EventType == service.AccountCostLossEventRefund && consume <= 0) || consume-outstanding > 1e-8 {
		return nil, false, service.ErrInvalidCostLossAdjustment
	}

	profileJSON, err := json.Marshal(source.CostProfile)
	if err != nil {
		return nil, false, fmt.Errorf("marshal account cost profile: %w", err)
	}
	reason := "provider_refund"
	if adjustment.EventType == service.AccountCostLossEventReversal {
		reason = "account_recovered"
	}
	var eventID int64
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO account_cost_loss_events (
			account_id, account_id_snapshot, account_name, platform, account_type,
			event_type, reason, message, occurred_at, currency,
			amount, accrued_cost, recognized_cost,
			billing_period_start, billing_period_end, cost_profile,
			source_event_id, idempotency_key, algorithm_version
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
			$11,0,$12,$13,$14,$15::jsonb,$16,$17,$18
		)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, created_at
	`, nullableInt64Ptr(source.AccountID), source.AccountIDSnapshot, source.AccountName, source.Platform, source.AccountType,
		adjustment.EventType, reason, adjustment.Message, adjustment.OccurredAt.UTC(), source.Currency,
		-consume, -consume, nullableTimePtr(source.BillingPeriodAt), nullableTimePtr(source.BillingPeriodTo), profileJSON,
		source.ID, adjustment.Idempotency, service.AccountCostLossAlgorithmVersion,
	).Scan(&eventID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		event, loadErr := loadAccountCostLossEventByKey(ctx, tx, adjustment.Idempotency)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, false, commitErr
		}
		return event, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}

	event := *source
	event.ID = eventID
	event.EventType = adjustment.EventType
	event.Reason = reason
	event.StatusCode = 0
	event.UpstreamCode = ""
	event.Message = adjustment.Message
	event.OccurredAt = adjustment.OccurredAt.UTC()
	event.Amount = -consume
	event.AccruedCost = 0
	event.RecognizedCost = -consume
	event.SourceEventID = &source.ID
	event.IdempotencyKey = adjustment.Idempotency
	event.AlgorithmVersion = service.AccountCostLossAlgorithmVersion
	event.CreatedAt = createdAt
	return &event, true, nil
}

const accountCostLossEventSelect = `
	SELECT id, account_id, account_id_snapshot, account_name, platform, account_type,
		event_type, reason, status_code, upstream_code, message, occurred_at,
		currency, amount, accrued_cost, recognized_cost,
		billing_period_start, billing_period_end, cost_profile,
		source_event_id, idempotency_key, algorithm_version, created_at
	FROM account_cost_loss_events
`

func loadAccountCostLossEventByKey(ctx context.Context, tx *sql.Tx, key string) (*service.AccountCostLossEvent, error) {
	return scanAccountCostLossEvent(tx.QueryRowContext(ctx, accountCostLossEventSelect+" WHERE idempotency_key = $1", key))
}

type sqlRowScanner interface {
	Scan(dest ...any) error
}

func scanAccountCostLossEvent(row sqlRowScanner) (*service.AccountCostLossEvent, error) {
	var event service.AccountCostLossEvent
	var accountID, sourceEventID sql.NullInt64
	var statusCode sql.NullInt64
	var upstreamCode sql.NullString
	var periodStart, periodEnd sql.NullTime
	var profileJSON []byte
	if err := row.Scan(
		&event.ID, &accountID, &event.AccountIDSnapshot, &event.AccountName, &event.Platform, &event.AccountType,
		&event.EventType, &event.Reason, &statusCode, &upstreamCode, &event.Message, &event.OccurredAt,
		&event.Currency, &event.Amount, &event.AccruedCost, &event.RecognizedCost,
		&periodStart, &periodEnd, &profileJSON, &sourceEventID, &event.IdempotencyKey,
		&event.AlgorithmVersion, &event.CreatedAt,
	); err != nil {
		return nil, err
	}
	if accountID.Valid {
		event.AccountID = &accountID.Int64
	}
	if sourceEventID.Valid {
		event.SourceEventID = &sourceEventID.Int64
	}
	if statusCode.Valid {
		event.StatusCode = int(statusCode.Int64)
	}
	if upstreamCode.Valid {
		event.UpstreamCode = upstreamCode.String
	}
	if periodStart.Valid {
		event.BillingPeriodAt = &periodStart.Time
	}
	if periodEnd.Valid {
		event.BillingPeriodTo = &periodEnd.Time
	}
	if err := json.Unmarshal(profileJSON, &event.CostProfile); err != nil {
		return nil, fmt.Errorf("decode account cost profile: %w", err)
	}
	return &event, nil
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableInt64Ptr(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC()
}
