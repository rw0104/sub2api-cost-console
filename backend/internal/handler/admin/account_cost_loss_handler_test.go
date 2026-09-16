package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type costRefundAliasRepository struct {
	adjustment service.AccountCostLossAdjustment
}

func (*costRefundAliasRepository) RecordTerminalFailure(context.Context, service.AccountCostLossDraft, string) (*service.AccountCostLossEvent, bool, error) {
	return nil, false, nil
}
func (*costRefundAliasRepository) ListStates(context.Context) ([]service.AccountCostLossState, error) {
	return []service.AccountCostLossState{{AccountIDSnapshot: 42, TerminalEventID: 7, TerminalEventIDs: []int64{7, 8}, Active: true}}, nil
}
func (r *costRefundAliasRepository) RecordAdjustment(_ context.Context, adjustment service.AccountCostLossAdjustment) (*service.AccountCostLossEvent, bool, error) {
	r.adjustment = adjustment
	return &service.AccountCostLossEvent{ID: 9}, true, nil
}

func TestCostLossRefundAcceptsLegacyTerminalAlias(t *testing.T) {
	repo := &costRefundAliasRepository{}
	h := &AccountHandler{accountCostLoss: service.NewAccountCostLossService(repo)}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "event_id", Value: "8"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"amount":1,"idempotency_key":"refund-alias"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.RecordCostLossRefund(ctx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, int64(42), repo.adjustment.AccountID)
	require.Equal(t, int64(8), repo.adjustment.SourceEventID)
}
