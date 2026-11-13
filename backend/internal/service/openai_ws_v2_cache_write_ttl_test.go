package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise a real downstream WebSocket, the passthrough adapter and RecordUsage.
// The persisted log and debit must use reported lifetimes, without counting the
// aggregate as an additional write or carrying a prior turn's 12h evidence.
func TestOpenAIWSPassthroughCacheWriteTTLRecordUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controlCtx, cancelControl := context.WithCancelCause(context.Background())
	defer cancelControl(context.Canceled)
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	proxy := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	type completedTurn struct {
		result *OpenAIForwardResult
		err    error
	}
	turns := make(chan completedTurn, 8)
	hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
		turns <- completedTurn{result: result, err: err}
	}}
	server, _ := startPassthroughHookRecordingServer(t, controlCtx, proxy, account, hooks)
	defer server.Close()
	request := `{"type":"response.create","model":"gpt-6-luna","input":"synthetic TTL check"}`
	client := dialPassthroughLifecycleClientWithPayload(t, server, request)
	defer func() { cancelControl(context.Canceled); _ = client.CloseNow() }()

	const groupID int64 = 777
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	users := &openAIRecordUsageUserRepoStub{}
	billing := newOpenAIRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{}, nil)
	channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, ApplyPricingToAccountStats: true,
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-6-luna"}, BillingMode: BillingModeToken,
			InputPrice: float64Ptr(1e-6), OutputPrice: float64Ptr(2e-6),
			CacheWritePrice: float64Ptr(3e-6), CacheWrite12hPrice: float64Ptr(6e-6),
		}},
	}
	billing.channelService = &ChannelService{}
	billing.channelService.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{groupID: PlatformOpenAI}))
	billing.resolver = NewModelPricingResolver(billing.channelService, billing.billingService)

	for index, tc := range []struct {
		name, details string
		short, long   int
		cacheCost     float64
	}{
		{"mixed", `,"cache_write_tokens_details":{"ttl_30m_tokens":3,"ttl_12h_tokens":7}`, 3, 7, 51e-6},
		{"short", `,"cache_write_tokens_details":{"ttl_30m_tokens":10,"ttl_12h_tokens":0}`, 10, 0, 30e-6},
		{"missing", ``, 0, 0, 30e-6},
		{"invalid", `,"cache_write_tokens_details":{"ttl_30m_tokens":0,"ttl_12h_tokens":99}`, 0, 0, 30e-6},
	} {
		if index > 0 {
			writeCtx, cancelWrite := context.WithTimeout(controlCtx, 3*time.Second)
			err := client.Write(writeCtx, coderws.MessageText, []byte(request))
			cancelWrite()
			require.NoError(t, err)
		}
		requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
		upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_ttl_%s","model":"gpt-6-luna","usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":10%s}}}}`, tc.name, tc.details))
		_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
		require.NoError(t, err)
		var turn completedTurn
		select {
		case turn = <-turns:
		case <-time.After(3 * time.Second):
			t.Fatal("WS turn was not reported")
		}
		require.NoError(t, turn.err)
		require.NotNil(t, turn.result)
		require.Equal(t, tc.short, turn.result.Usage.CacheCreation30mInputTokens, tc.name)
		require.Equal(t, tc.long, turn.result.Usage.CacheCreation12hInputTokens, tc.name)
		require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: turn.result,
			APIKey: &APIKey{ID: 1, GroupID: i64p(groupID), Group: &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 0.5}},
			User:   &User{ID: 1}, Account: account,
			ChannelUsageFields: ChannelUsageFields{OriginalModel: "gpt-6-luna", BillingModelSource: BillingModelSourceRequested},
		}))
		require.NotNil(t, logs.lastLog)
		require.Equal(t, 3, logs.lastLog.InputTokens)
		require.Equal(t, 10, logs.lastLog.CacheCreationTokens)
		require.Equal(t, tc.short, logs.lastLog.CacheCreation30mTokens)
		require.Equal(t, tc.long, logs.lastLog.CacheCreation12hTokens)
		require.InDelta(t, tc.cacheCost, logs.lastLog.CacheCreationCost, 1e-12, tc.name)
		wantTotal := 3e-6 + 5*2e-6 + tc.cacheCost
		require.InDelta(t, wantTotal, logs.lastLog.TotalCost, 1e-12)
		require.InDelta(t, wantTotal*0.5, users.lastAmount, 1e-12)
		require.NotNil(t, logs.lastLog.AccountStatsCost)
		require.InDelta(t, wantTotal, *logs.lastLog.AccountStatsCost, 1e-12)
		require.Equal(t, index+1, logs.calls)
		require.Equal(t, index+1, users.deductCalls)
	}
}
