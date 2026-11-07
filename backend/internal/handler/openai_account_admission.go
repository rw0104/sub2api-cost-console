package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const openAISlotAcquireAccountChanged openAISlotAcquireResult = 3
const openAIAccountAdmissionDeadlineKey = "openai_account_admission_deadline"

func (r openAISlotAcquireResult) shouldReselect() bool {
	return r == openAISlotAcquireProfitVetoed || r == openAISlotAcquireAccountChanged
}

func recordOpenAIAdmissionVeto(failed map[int64]struct{}, accountID int64, attempts *int) bool {
	failed[accountID] = struct{}{}
	*attempts++
	return *attempts < maxProfitVetoAttempts
}

func (h *OpenAIGatewayHandler) handleOpenAIAdmissionVetoExhausted(c *gin.Context, streamStarted bool, log *zap.Logger, attempts int) {
	log.Warn("openai.admission_attempts_exhausted", zap.Int("admission_veto_count", attempts))
	markOpsRoutingCapacityLimited(c)
	h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", profitVetoExhaustedMessage, streamStarted)
}

// The budget belongs to the request's admission phase, not to an account. It
// survives reselection without imposing that deadline on model execution.
func remainingOpenAIAccountAdmissionWait(c *gin.Context, timeout time.Duration, now time.Time) time.Duration {
	deadline, found := openAIAccountAdmissionDeadline(c)
	if !found {
		deadline = now.Add(timeout)
		if parent, ok := c.Request.Context().Deadline(); ok && parent.Before(deadline) {
			deadline = parent
		}
		c.Set(openAIAccountAdmissionDeadlineKey, deadline)
	}
	return deadline.Sub(now)
}

func openAIAccountAdmissionDeadline(c *gin.Context) (time.Time, bool) {
	value, ok := c.Get(openAIAccountAdmissionDeadlineKey)
	deadline, typed := value.(time.Time)
	return deadline, ok && typed
}

func openAIAccountAdmissionTimedOut(c *gin.Context) bool {
	deadline, ok := openAIAccountAdmissionDeadline(c)
	return ok && !time.Now().Before(deadline)
}
