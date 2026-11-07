//go:build unit

package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type admissionConcurrencyCache struct {
	fakeConcurrencyCache
	attempts  int
	releases  int
	waiting   int
	onAcquire func(int, int64) bool
}

func (c *admissionConcurrencyCache) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	c.attempts++
	if c.onAcquire != nil {
		return c.onAcquire(c.attempts, id), nil
	}
	return true, nil
}
func (c *admissionConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.releases++
	return nil
}
func (c *admissionConcurrencyCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	c.waiting++
	return true, nil
}
func (c *admissionConcurrencyCache) DecrementAccountWaitCount(context.Context, int64) error {
	c.waiting--
	return nil
}

func TestOpenAIAccountAdmission_QueuedAccountChangedReleasesAndReselects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := profitSlotTestAccount(1, 1)
	b := profitSlotTestAccount(2, 1)
	cache := &admissionConcurrencyCache{}
	cache.onAcquire = func(attempt int, id int64) bool {
		if attempt == 1 {
			return false
		}
		if id == a.ID {
			until := time.Now().Add(time.Hour)
			a.RateLimitResetAt = &until
		}
		return true
	}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0)}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	started := false
	selection := func(account *service.Account) *service.AccountSelectionResult {
		return &service.AccountSelectionResult{Account: account,
			WaitPlan: &service.AccountWaitPlan{AccountID: account.ID, MaxConcurrency: 1, Timeout: time.Second, MaxWaiting: 2}}
	}
	release, result := h.acquireResponsesAccountSlot(c, nil, "", selection(a), false, &started, zap.NewNop())
	require.Equal(t, openAISlotAcquireAccountChanged, result, "cooldown rejection must not be reported as a profit veto")
	require.True(t, result.shouldReselect())
	require.Nil(t, release)
	require.Equal(t, 1, cache.releases)
	require.Zero(t, cache.waiting)
	require.Zero(t, w.Body.Len(), "reselection must not commit a downstream error")
	deadline, ok := openAIAccountAdmissionDeadline(c)
	require.True(t, ok)
	failed, attempts := map[int64]struct{}{}, 0
	require.True(t, recordOpenAIAdmissionVeto(failed, a.ID, &attempts))
	require.Contains(t, failed, a.ID)
	release, result = h.acquireResponsesAccountSlot(c, nil, "", selection(b), false, &started, zap.NewNop())
	require.Equal(t, openAISlotAcquireOK, result)
	release()
	release()
	require.Equal(t, 2, cache.releases, "each acquired slot is released exactly once")
	after, _ := openAIAccountAdmissionDeadline(c)
	require.Equal(t, deadline, after, "switching accounts cannot restart the wait budget")
}

func TestOpenAIAccountAdmission_AbsoluteWaitDeadline(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	start := time.Now()
	require.Equal(t, time.Second, remainingOpenAIAccountAdmissionWait(c, time.Second, start))
	require.Equal(t, 250*time.Millisecond, remainingOpenAIAccountAdmissionWait(c, 30*time.Second, start.Add(750*time.Millisecond)))
	require.Equal(t, -time.Millisecond, remainingOpenAIAccountAdmissionWait(c, 30*time.Second, start.Add(1001*time.Millisecond)))
	_, hasModelDeadline := c.Request.Context().Deadline()
	require.False(t, hasModelDeadline, "admission budget must not become a timeout for model execution")

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(300*time.Millisecond))
	defer cancel()
	c2.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	require.Equal(t, 300*time.Millisecond, remainingOpenAIAccountAdmissionWait(c2, time.Second, start))
}

func TestOpenAIAccountAdmission_ExpiredBudgetReleasesAlreadyAcquiredSlot(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
		if cancelled {
			cancel()
		} else {
			c.Set(openAIAccountAdmissionDeadlineKey, time.Now().Add(-time.Second))
		}
		releases := 0
		h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
		started := false
		release, result := h.acquireResponsesAccountSlot(c, nil, "", &service.AccountSelectionResult{
			Account: profitSlotTestAccount(1, 1), Acquired: true, ReleaseFunc: func() { releases++ },
		}, false, &started, zap.NewNop())
		require.Equal(t, openAISlotAcquireFailed, result)
		require.Nil(t, release)
		require.Equal(t, 1, releases)
		if cancelled {
			require.Equal(t, 499, w.Code)
		} else {
			require.Equal(t, 429, w.Code)
		}
	}
}

func TestOpenAIAccountAdmission_ReselectionHasBoundedAttempts(t *testing.T) {
	failed, attempts := map[int64]struct{}{}, 0
	for i := 1; i <= maxProfitVetoAttempts; i++ {
		require.Equal(t, i < maxProfitVetoAttempts, recordOpenAIAdmissionVeto(failed, int64(i), &attempts))
	}
	require.Len(t, failed, maxProfitVetoAttempts)
}

func TestOpenAIAccountAdmission_HandlerPassesActualModelAfterWaiting(t *testing.T) {
	a := profitSlotTestAccount(1, 1)
	a.Credentials = map[string]any{"model_mapping": map[string]any{"public-codex": "gpt-6.1-terra"}}
	cache := &admissionConcurrencyCache{onAcquire: func(attempt int, _ int64) bool {
		if attempt == 1 {
			return false
		}
		a.Extra["model_rate_limits"] = map[string]any{
			"gpt-6.1-terra": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		}
		return true
	}}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0)}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	started := false
	release, result := h.acquireResponsesAccountSlot(c, nil, "", &service.AccountSelectionResult{Account: a,
		WaitPlan: &service.AccountWaitPlan{AccountID: a.ID, MaxConcurrency: 1, Timeout: time.Second, MaxWaiting: 2}},
		false, &started, zap.NewNop(), "public-codex")
	require.Equal(t, openAISlotAcquireAccountChanged, result)
	require.Nil(t, release)
	require.Equal(t, 1, cache.releases)
	require.Zero(t, cache.waiting)
	require.Zero(t, w.Body.Len())
}
