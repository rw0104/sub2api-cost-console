package repository

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// An advisory transaction lock also works after a hard-deleted account's
// immutable ledger survives. All terminal/refund/recovery writes share it.
func lockAccountCostLifecycle(ctx context.Context, tx *sql.Tx, accountID int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("sub2api:account-cost-loss:%d", accountID))
	return err
}

func markCostAccountTerminal(ctx context.Context, tx *sql.Tx, accountID int64, message string) error {
	result, err := tx.ExecContext(ctx, `UPDATE accounts
		SET status = $1, error_message = $2, schedulable = FALSE, updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL`, service.StatusError, message, accountID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return service.ErrAccountNotFound
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil)
}

func matchesCostLossAdjustment(event *service.AccountCostLossEvent, adjustment service.AccountCostLossAdjustment) bool {
	return event.AccountIDSnapshot == adjustment.AccountID && event.EventType == adjustment.EventType &&
		event.SourceEventID != nil && *event.SourceEventID == adjustment.SourceEventID &&
		(adjustment.EventType != service.AccountCostLossEventRefund || math.Abs(event.Amount+adjustment.Amount) <= 1e-8)
}

func reverseCostLifecycle(ctx context.Context, tx *sql.Tx, active []*service.AccountCostLossEvent, adjustment service.AccountCostLossAdjustment) (*service.AccountCostLossEvent, bool, error) {
	var requested *service.AccountCostLossEvent
	for _, source := range active {
		var prior float64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount), 0) FROM account_cost_loss_events
			WHERE source_event_id = $1 AND event_type IN ('refund', 'reversal')`, source.ID).Scan(&prior); err != nil {
			return nil, false, err
		}
		item := adjustment
		item.SourceEventID = source.ID
		if source.ID != adjustment.SourceEventID {
			item.Idempotency = fmt.Sprintf("reversal:%d:%d", adjustment.AccountID, source.ID)
		}
		event, _, err := insertCostAdjustment(ctx, tx, source, item, math.Max(0, source.Amount+prior))
		if err != nil {
			return nil, false, err
		}
		if source.ID == adjustment.SourceEventID {
			requested = event
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return requested, true, nil
}

func commitExistingCostLoss(tx *sql.Tx, event *service.AccountCostLossEvent) (*service.AccountCostLossEvent, bool, error) {
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return event, false, nil
}

func loadActiveCostTerminals(ctx context.Context, tx *sql.Tx, accountID int64) ([]*service.AccountCostLossEvent, error) {
	rows, err := tx.QueryContext(ctx, accountCostLossEventSelect+` terminal
		WHERE terminal.account_id_snapshot = $1 AND terminal.event_type = 'terminal_loss'
		AND NOT EXISTS (SELECT 1 FROM account_cost_loss_events recovery
			WHERE recovery.source_event_id = terminal.id AND recovery.event_type = 'reversal')
		ORDER BY terminal.id ASC`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []*service.AccountCostLossEvent
	for rows.Next() {
		event, err := scanAccountCostLossEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
