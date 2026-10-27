package admin

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// VerifyOfficialModels fetches the account-visible official catalog and checks a
// mapping draft. It never updates account credentials, metadata, or mapping rules.
// POST /api/v1/admin/accounts/:id/models/verify-official
func (h *AccountHandler) VerifyOfficialModels(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var request struct {
		ModelMapping map[string]string `json:"model_mapping"`
	}
	if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Invalid model mapping request")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.NotFound(c, "Account not found")
		return
	}
	if h.accountTestService == nil {
		response.InternalError(c, "Account test service is not configured")
		return
	}
	result, err := h.accountTestService.VerifyOfficialOpenAIModels(c.Request.Context(), account, request.ModelMapping)
	if err != nil {
		var syncErr *service.UpstreamModelSyncError
		if errors.As(err, &syncErr) {
			switch syncErr.Kind {
			case service.UpstreamModelSyncErrorConfiguration, service.UpstreamModelSyncErrorUnsupported:
				response.BadRequest(c, syncErr.SafeMessage())
			case service.UpstreamModelSyncErrorInternal:
				response.InternalError(c, syncErr.SafeMessage())
			default:
				response.Error(c, http.StatusBadGateway, syncErr.SafeMessage())
			}
			return
		}
		response.Error(c, http.StatusBadGateway, "Failed to verify the official model list")
		return
	}
	response.Success(c, result)
}
