package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const AccountCostLossAlgorithmVersion = "1.5.0"

const (
	AccountCostLossEventTerminal = "terminal_loss"
	AccountCostLossEventRefund   = "refund"
	AccountCostLossEventReversal = "reversal"
)

const (
	TerminalFailureTokenRevoked          = "token_revoked"
	TerminalFailureUnauthorizedPermanent = "unauthorized_permanent"
	TerminalFailureRefreshUnavailable    = "refresh_unavailable"
	TerminalFailureWorkspaceDeactivated  = "workspace_deactivated"
	TerminalFailureAdminConfirmed        = "admin_confirmed"
)

var (
	ErrAccountCostLossIneligible = errors.New("account is not eligible for terminal cost loss recognition")
	ErrInvalidCostProfile        = errors.New("invalid account cost profile")
	ErrInvalidCostLossAdjustment = errors.New("invalid account cost loss adjustment")
)

type TerminalFailure struct {
	Reason       string
	StatusCode   int
	UpstreamCode string
	Message      string
	OccurredAt   time.Time
	Idempotency  string
}

type AccountCostProfileSnapshot struct {
	Amount           float64        `json:"amount"`
	Currency         string         `json:"currency"`
	BillingCycle     string         `json:"billing_cycle"`
	StartedAt        time.Time      `json:"started_at"`
	Source           string         `json:"source"`
	AlgorithmVersion string         `json:"algorithm_version"`
	Raw              map[string]any `json:"raw,omitempty"`
}

type AccountCostLossDraft struct {
	AccountID       int64
	AccountName     string
	Platform        string
	AccountType     string
	Failure         TerminalFailure
	CostProfile     AccountCostProfileSnapshot
	Currency        string
	BillingPeriodAt time.Time
	BillingPeriodTo time.Time
	AccruedCost     float64
	LossAmount      float64
	RecognizedCost  float64
	Algorithm       string
	IdempotencyKey  string
}

type AccountCostLossEvent struct {
	ID                int64                      `json:"id"`
	AccountID         *int64                     `json:"account_id,omitempty"`
	AccountIDSnapshot int64                      `json:"account_id_snapshot"`
	AccountName       string                     `json:"account_name"`
	Platform          string                     `json:"platform"`
	AccountType       string                     `json:"account_type"`
	EventType         string                     `json:"event_type"`
	Reason            string                     `json:"reason"`
	StatusCode        int                        `json:"status_code,omitempty"`
	UpstreamCode      string                     `json:"upstream_code,omitempty"`
	Message           string                     `json:"message,omitempty"`
	OccurredAt        time.Time                  `json:"occurred_at"`
	Currency          string                     `json:"currency"`
	Amount            float64                    `json:"amount"`
	AccruedCost       float64                    `json:"accrued_cost"`
	RecognizedCost    float64                    `json:"recognized_cost"`
	BillingPeriodAt   *time.Time                 `json:"billing_period_start,omitempty"`
	BillingPeriodTo   *time.Time                 `json:"billing_period_end,omitempty"`
	CostProfile       AccountCostProfileSnapshot `json:"cost_profile"`
	SourceEventID     *int64                     `json:"source_event_id,omitempty"`
	IdempotencyKey    string                     `json:"idempotency_key"`
	AlgorithmVersion  string                     `json:"algorithm_version"`
	CreatedAt         time.Time                  `json:"created_at"`
}

type AccountCostLossState struct {
	AccountIDSnapshot int64     `json:"account_id"`
	AccountName       string    `json:"account_name"`
	Platform          string    `json:"platform"`
	AccountType       string    `json:"account_type"`
	TerminalEventID   int64     `json:"terminal_event_id"`
	OccurredAt        time.Time `json:"occurred_at"`
	Currency          string    `json:"currency"`
	AccruedCost       float64   `json:"accrued_cost"`
	GrossLoss         float64   `json:"gross_loss"`
	RefundAmount      float64   `json:"refund_amount"`
	ReversalAmount    float64   `json:"reversal_amount"`
	NetLoss           float64   `json:"net_loss"`
	RecognizedCost    float64   `json:"recognized_cost"`
	Active            bool      `json:"active"`
	AccountDeleted    bool      `json:"account_deleted"`
}

type AccountCostLossAdjustment struct {
	SourceEventID int64
	AccountID     int64
	EventType     string
	Amount        float64
	OccurredAt    time.Time
	Idempotency   string
	Message       string
}

type AccountCostLossRepository interface {
	RecordTerminalFailure(ctx context.Context, draft AccountCostLossDraft, errorMessage string) (*AccountCostLossEvent, bool, error)
	ListStates(ctx context.Context) ([]AccountCostLossState, error)
	RecordAdjustment(ctx context.Context, adjustment AccountCostLossAdjustment) (*AccountCostLossEvent, bool, error)
}

type AccountCostLossService struct {
	repository AccountCostLossRepository
}

func NewAccountCostLossService(repository AccountCostLossRepository) *AccountCostLossService {
	return &AccountCostLossService{repository: repository}
}

func (s *AccountCostLossService) ConfirmTerminalFailure(
	ctx context.Context,
	account *Account,
	failure TerminalFailure,
	errorMessage string,
) (*AccountCostLossEvent, bool, error) {
	if s == nil || s.repository == nil {
		return nil, false, errors.New("account cost loss repository is unavailable")
	}
	draft, err := BuildTerminalCostLoss(account, failure)
	if err != nil {
		return nil, false, err
	}
	return s.repository.RecordTerminalFailure(ctx, draft, errorMessage)
}

func (s *AccountCostLossService) ListStates(ctx context.Context) ([]AccountCostLossState, error) {
	if s == nil || s.repository == nil {
		return nil, errors.New("account cost loss repository is unavailable")
	}
	return s.repository.ListStates(ctx)
}

func (s *AccountCostLossService) RecordRefund(
	ctx context.Context,
	sourceEventID int64,
	accountID int64,
	amount float64,
	occurredAt time.Time,
	idempotency string,
	message string,
) (*AccountCostLossEvent, bool, error) {
	if s == nil || s.repository == nil {
		return nil, false, errors.New("account cost loss repository is unavailable")
	}
	if sourceEventID <= 0 || accountID <= 0 || amount <= 0 || occurredAt.IsZero() {
		return nil, false, ErrInvalidCostLossAdjustment
	}
	if strings.TrimSpace(idempotency) == "" {
		return nil, false, ErrInvalidCostLossAdjustment
	}
	return s.repository.RecordAdjustment(ctx, AccountCostLossAdjustment{
		SourceEventID: sourceEventID,
		AccountID:     accountID,
		EventType:     AccountCostLossEventRefund,
		Amount:        amount,
		OccurredAt:    occurredAt.UTC(),
		Idempotency:   idempotency,
		Message:       message,
	})
}

func (s *AccountCostLossService) ReverseActiveLossesForAccount(
	ctx context.Context,
	accountID int64,
	occurredAt time.Time,
	message string,
) (int, error) {
	if s == nil || s.repository == nil {
		return 0, errors.New("account cost loss repository is unavailable")
	}
	if accountID <= 0 || occurredAt.IsZero() {
		return 0, ErrInvalidCostLossAdjustment
	}
	states, err := s.repository.ListStates(ctx)
	if err != nil {
		return 0, err
	}
	reversed := 0
	for _, state := range states {
		if state.AccountIDSnapshot != accountID || !state.Active {
			continue
		}
		_, created, err := s.repository.RecordAdjustment(ctx, AccountCostLossAdjustment{
			SourceEventID: state.TerminalEventID,
			AccountID:     accountID,
			EventType:     AccountCostLossEventReversal,
			Amount:        state.NetLoss,
			OccurredAt:    occurredAt.UTC(),
			Idempotency:   fmt.Sprintf("reversal:%d:%d", accountID, state.TerminalEventID),
			Message:       message,
		})
		if err != nil {
			return reversed, err
		}
		if created {
			reversed++
		}
	}
	return reversed, nil
}

var costCycleHours = map[string]float64{
	"hourly":  1,
	"daily":   24,
	"weekly":  168,
	"monthly": 730,
}

func BuildTerminalCostLoss(account *Account, failure TerminalFailure) (AccountCostLossDraft, error) {
	if account == nil || failure.OccurredAt.IsZero() {
		return AccountCostLossDraft{}, ErrInvalidCostProfile
	}
	if !isConfirmedTerminalReason(failure.Reason) {
		return AccountCostLossDraft{}, ErrAccountCostLossIneligible
	}
	if !isProcurementAccountForCostLoss(account) {
		return AccountCostLossDraft{}, ErrAccountCostLossIneligible
	}
	profile, err := resolveAccountCostProfileSnapshot(account)
	if err != nil {
		return AccountCostLossDraft{}, err
	}
	if failure.OccurredAt.Before(profile.StartedAt) {
		return AccountCostLossDraft{}, ErrInvalidCostProfile
	}

	draft := AccountCostLossDraft{
		AccountID:      account.ID,
		AccountName:    account.Name,
		Platform:       account.Platform,
		AccountType:    account.Type,
		Failure:        failure,
		CostProfile:    profile,
		Currency:       profile.Currency,
		Algorithm:      AccountCostLossAlgorithmVersion,
		IdempotencyKey: failure.Idempotency,
	}
	if draft.IdempotencyKey == "" {
		draft.IdempotencyKey = fmt.Sprintf("terminal:%d:%d:%s", account.ID, account.UpdatedAt.UnixNano(), failure.Reason)
	}

	if profile.BillingCycle == "one_time" {
		draft.BillingPeriodAt = profile.StartedAt
		draft.BillingPeriodTo = profile.StartedAt
		draft.AccruedCost = profile.Amount
		draft.RecognizedCost = profile.Amount
		return draft, nil
	}

	cycleHours, ok := costCycleHours[profile.BillingCycle]
	if !ok || cycleHours <= 0 {
		return AccountCostLossDraft{}, ErrInvalidCostProfile
	}
	elapsedHours := failure.OccurredAt.Sub(profile.StartedAt).Hours()
	completedCycles := math.Floor(elapsedHours / cycleHours)
	cyclePositionHours := elapsedHours - completedCycles*cycleHours
	if math.Abs(cyclePositionHours) < 1e-9 && elapsedHours > 0 {
		cyclePositionHours = 0
	}
	draft.BillingPeriodAt = profile.StartedAt.Add(time.Duration(completedCycles * cycleHours * float64(time.Hour)))
	draft.BillingPeriodTo = draft.BillingPeriodAt.Add(time.Duration(cycleHours * float64(time.Hour)))
	draft.AccruedCost = profile.Amount * elapsedHours / cycleHours
	remainingCurrentCycle := 0.0
	if cyclePositionHours > 0 || elapsedHours == 0 {
		remainingCurrentCycle = profile.Amount * (1 - cyclePositionHours/cycleHours)
	}
	draft.LossAmount = math.Max(0, remainingCurrentCycle)
	draft.RecognizedCost = draft.AccruedCost + draft.LossAmount
	return draft, nil
}

func isConfirmedTerminalReason(reason string) bool {
	switch reason {
	case TerminalFailureTokenRevoked,
		TerminalFailureUnauthorizedPermanent,
		TerminalFailureRefreshUnavailable,
		TerminalFailureWorkspaceDeactivated,
		TerminalFailureAdminConfirmed:
		return true
	default:
		return false
	}
}

func isProcurementAccountForCostLoss(account *Account) bool {
	if account == nil || (account.Type != AccountTypeOAuth && account.Type != AccountTypeSetupToken) {
		return false
	}
	if account.ParentAccountID != nil || account.IsCustomBaseURLEnabled() {
		return false
	}
	if strings.TrimSpace(account.GetExtraString("crs_account_id")) != "" {
		return false
	}
	if enabled, ok := account.Extra["custom_base_url_enabled"].(bool); ok && enabled && strings.TrimSpace(account.GetExtraString("custom_base_url")) != "" {
		return false
	}
	if enabled, ok := account.Credentials["pool_mode"].(bool); ok && enabled {
		return false
	}
	return true
}

func resolveAccountCostProfileSnapshot(account *Account) (AccountCostProfileSnapshot, error) {
	raw, ok := account.Extra["cost_profile"].(map[string]any)
	if !ok {
		plan := inferCostPlan(account)
		prices := map[string]float64{
			"free": 0, "plus": 20, "pro": 100, "team": 25, "business": 25, "k12": 0, "unknown": 0,
		}
		return AccountCostProfileSnapshot{
			Amount:           prices[plan],
			Currency:         "USD",
			BillingCycle:     "monthly",
			StartedAt:        account.CreatedAt,
			Source:           "default",
			AlgorithmVersion: AccountCostLossAlgorithmVersion,
		}, nil
	}
	amount, ok := numericCostValue(raw["amount"])
	if !ok || amount < 0 {
		return AccountCostProfileSnapshot{}, ErrInvalidCostProfile
	}
	currency := strings.ToUpper(strings.TrimSpace(stringCostValue(raw["currency"])))
	if currency != "USD" && currency != "CNY" {
		return AccountCostProfileSnapshot{}, ErrInvalidCostProfile
	}
	cycle := strings.ToLower(strings.TrimSpace(stringCostValue(raw["billing_cycle"])))
	if cycle != "one_time" {
		if _, ok := costCycleHours[cycle]; !ok {
			return AccountCostProfileSnapshot{}, ErrInvalidCostProfile
		}
	}
	startedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(stringCostValue(raw["started_at"])))
	if err != nil {
		return AccountCostProfileSnapshot{}, ErrInvalidCostProfile
	}
	if startedAt.Before(account.CreatedAt) {
		startedAt = account.CreatedAt
	}
	algorithm := strings.TrimSpace(stringCostValue(raw["algorithm_version"]))
	if algorithm == "" {
		algorithm = "legacy-unversioned"
	}
	return AccountCostProfileSnapshot{
		Amount:           amount,
		Currency:         currency,
		BillingCycle:     cycle,
		StartedAt:        startedAt,
		Source:           "custom",
		AlgorithmVersion: algorithm,
		Raw:              raw,
	}, nil
}

func inferCostPlan(account *Account) string {
	if account == nil {
		return "unknown"
	}
	candidates := []any{
		account.Extra["plan_type"], account.Extra["subscription_tier"],
		account.Credentials["plan_type"], account.Credentials["subscription_tier"],
		account.Credentials["plan"], account.Credentials["subscription_plan"], account.Credentials["tier"],
	}
	for _, candidate := range candidates {
		plan := normalizeCostPlan(stringCostValue(candidate))
		if plan != "unknown" {
			return plan
		}
	}
	return "unknown"
}

func normalizeCostPlan(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(parts) == 0 {
		return "unknown"
	}
	for _, part := range parts {
		switch part {
		case "free", "plus", "pro", "team", "business":
			return part
		case "k12", "education", "edu", "teachers", "teacher":
			return "k12"
		}
	}
	return "unknown"
}

func numericCostValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func stringCostValue(value any) string {
	text, _ := value.(string)
	return text
}
