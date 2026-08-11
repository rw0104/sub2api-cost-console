package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetEconomicsSnapshot(c *gin.Context) {
	if h == nil || h.accountEconomics == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account economics service unavailable")
		return
	}
	cnyPerUSD := 0.0
	if raw := strings.TrimSpace(c.Query("cny_per_usd")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 {
			response.BadRequest(c, "Invalid cny_per_usd")
			return
		}
		cnyPerUSD = value
	}
	windowHours := 1.0
	if raw := strings.TrimSpace(c.Query("window_hours")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 || value > 24*30 {
			response.BadRequest(c, "Invalid window_hours")
			return
		}
		windowHours = value
	}
	accountIDs, err := parseEconomicsAccountIDs(c.Query("account_ids"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	snapshot, err := h.accountEconomics.GetSnapshot(
		c.Request.Context(),
		service.AccountEconomicsQuery{
			Scope: c.Query("scope"), Platform: c.Query("platform"), AccountIDs: accountIDs,
			CNYPerUSD: cnyPerUSD, ExchangeRateSource: c.Query("exchange_rate_source"),
			Window: time.Duration(windowHours * float64(time.Hour)), Now: time.Now().UTC(),
		},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, snapshot)
}

func parseEconomicsAccountIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 5000 {
		return nil, strconv.ErrRange
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, strconv.ErrSyntax
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}
