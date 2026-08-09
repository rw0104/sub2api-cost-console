package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) ListCostLossStates(c *gin.Context) {
	if h.accountCostLoss == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account cost loss service unavailable")
		return
	}
	states, err := h.accountCostLoss.ListStates(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"algorithm_version": service.AccountCostLossAlgorithmVersion,
		"states":            states,
	})
}

type confirmCostLossRequest struct {
	Message        string     `json:"message"`
	StatusCode     int        `json:"status_code"`
	UpstreamCode   string     `json:"upstream_code"`
	OccurredAt     *time.Time `json:"occurred_at"`
	IdempotencyKey string     `json:"idempotency_key"`
}

func (h *AccountHandler) ConfirmCostLoss(c *gin.Context) {
	accountID, ok := parseCostLossID(c, "id", "Invalid account ID")
	if !ok {
		return
	}
	if h.accountCostLoss == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account cost loss service unavailable")
		return
	}
	var req confirmCostLossRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	occurredAt := time.Now().UTC()
	if req.OccurredAt != nil {
		occurredAt = req.OccurredAt.UTC()
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "terminal account failure confirmed by administrator"
	}
	event, created, err := h.accountCostLoss.ConfirmTerminalFailure(c.Request.Context(), account, service.TerminalFailure{
		Reason: service.TerminalFailureAdminConfirmed, StatusCode: req.StatusCode,
		UpstreamCode: strings.TrimSpace(req.UpstreamCode), Message: message,
		OccurredAt:  occurredAt,
		Idempotency: firstNonEmptyCostLossKey(req.IdempotencyKey, c.GetHeader("Idempotency-Key")),
	}, message)
	if err != nil {
		writeCostLossError(c, err)
		return
	}
	response.Success(c, gin.H{"event": event, "created": created})
}

type costLossRefundRequest struct {
	Amount         float64    `json:"amount" binding:"required,gt=0"`
	Message        string     `json:"message"`
	OccurredAt     *time.Time `json:"occurred_at"`
	IdempotencyKey string     `json:"idempotency_key"`
}

func (h *AccountHandler) RecordCostLossRefund(c *gin.Context) {
	eventID, ok := parseCostLossID(c, "event_id", "Invalid cost loss event ID")
	if !ok {
		return
	}
	if h.accountCostLoss == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account cost loss service unavailable")
		return
	}
	var req costLossRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	states, err := h.accountCostLoss.ListStates(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var accountID int64
	for _, state := range states {
		if state.TerminalEventID == eventID {
			accountID = state.AccountIDSnapshot
			break
		}
	}
	if accountID == 0 {
		response.BadRequest(c, "Terminal cost loss event not found")
		return
	}
	occurredAt := time.Now().UTC()
	if req.OccurredAt != nil {
		occurredAt = req.OccurredAt.UTC()
	}
	event, created, err := h.accountCostLoss.RecordRefund(
		c.Request.Context(), eventID, accountID, req.Amount, occurredAt,
		firstNonEmptyCostLossKey(req.IdempotencyKey, c.GetHeader("Idempotency-Key")), strings.TrimSpace(req.Message),
	)
	if err != nil {
		writeCostLossError(c, err)
		return
	}
	response.Success(c, gin.H{"event": event, "created": created})
}

func (h *AccountHandler) ReverseCostLoss(c *gin.Context) {
	accountID, ok := parseCostLossID(c, "id", "Invalid account ID")
	if !ok {
		return
	}
	if h.accountCostLoss == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account cost loss service unavailable")
		return
	}
	reversed, err := h.accountCostLoss.ReverseActiveLossesForAccount(c.Request.Context(), accountID, time.Now().UTC(), "cost loss reversed by administrator")
	if err != nil {
		writeCostLossError(c, err)
		return
	}
	response.Success(c, gin.H{"reversed": reversed})
}

func parseCostLossID(c *gin.Context, param, message string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, message)
		return 0, false
	}
	return id, true
}

func firstNonEmptyCostLossKey(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func writeCostLossError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrInvalidCostLossAdjustment) || errors.Is(err, service.ErrInvalidCostProfile) || errors.Is(err, service.ErrAccountCostLossIneligible) {
		response.BadRequest(c, err.Error())
		return
	}
	response.ErrorFrom(c, err)
}
