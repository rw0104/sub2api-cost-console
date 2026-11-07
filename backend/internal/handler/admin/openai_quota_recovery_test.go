//go:build unit

package admin

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type observingQuotaRefreshStub struct {
	openAIQuotaWorkflowStub
	recoveryCalls        int
	cacheCallsAtRecovery int
}

func (s *observingQuotaRefreshStub) RecoverQuotaState(_ context.Context, _ int64, usage *service.OpenAIQuotaUsage) (bool, error) {
	s.recoveryCalls++
	s.cacheCallsAtRecovery = s.cacheCalls + s.creditsCacheCalls
	return usage != nil, nil
}

func TestOpenAIRefreshQuota_RecoversBeforeDisplayCacheWrites(t *testing.T) {
	quota := &observingQuotaRefreshStub{openAIQuotaWorkflowStub: *successfulOpenAIQuotaWorkflowStub()}
	handler := &OpenAIOAuthHandler{quotaService: quota}
	status, envelope := performOpenAIQuotaRefreshRequest(t, handler)
	require.Equal(t, 200, status)
	require.Equal(t, 1, quota.recoveryCalls)
	require.Zero(t, quota.cacheCallsAtRecovery, "display writes change the row version and must follow observed-state CAS")
	require.True(t, envelope.Data.CachePersisted)
	require.True(t, envelope.Data.CreditsCachePersisted)
}
