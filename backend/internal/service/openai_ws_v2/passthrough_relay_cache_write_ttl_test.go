package openai_ws_v2

import (
	"context"
	"fmt"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelayCacheWriteTTLPerTurnAndAggregate(t *testing.T) {
	t.Parallel()
	first := `{"type":"response.completed","response":{"id":"resp_ttl_1","usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":3,"ttl_12h_tokens":7}}}}}`
	frames := []string{
		`{"type":"response.in_progress","response":{"id":"resp_ttl_1","usage":{"input_tokens":13,"output_tokens":1,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":3,"ttl_12h_tokens":7}}}}}`,
		first, first,
		`{"type":"response.done","response":{"id":"resp_ttl_2","usage":{"prompt_tokens":13,"completion_tokens":5,"prompt_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":10,"ttl_12h_tokens":0}}}}}`,
		`{"type":"response.completed","response":{"id":"resp_ttl_3","usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cache_write_tokens":10}}}}`,
		`{"type":"response.completed","response":{"id":"resp_ttl_4","usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":1,"ttl_12h_tokens":99}}}}}`,
	}
	upstreamFrames := make([]passthroughTestFrame, 0, len(frames))
	for _, frame := range frames {
		upstreamFrames = append(upstreamFrames, passthroughTestFrame{msgType: coderws.MessageText, payload: []byte(frame)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var turns []RelayTurnResult
	result, exit := Relay(ctx, newPassthroughTestFrameConn(nil, false), newPassthroughTestFrameConn(upstreamFrames, true),
		[]byte(`{"type":"response.create","model":"gpt-6-luna","input":[]}`),
		RelayOptions{OnTurnComplete: func(turn RelayTurnResult) { turns = append(turns, turn) }})
	require.Nil(t, exit)
	require.Len(t, turns, 4, "duplicate terminal events must not debit a turn twice")
	for index, expected := range [][2]int{{3, 7}, {10, 0}, {0, 0}, {0, 0}} {
		require.Equal(t, 10, turns[index].Usage.CacheCreationInputTokens)
		require.Equal(t, expected[0], turns[index].Usage.CacheCreation30mInputTokens)
		require.Equal(t, expected[1], turns[index].Usage.CacheCreation12hInputTokens)
	}
	require.Equal(t, 40, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 13, result.Usage.CacheCreation30mInputTokens)
	require.Equal(t, 7, result.Usage.CacheCreation12hInputTokens)
	require.Equal(t, 52, result.Usage.InputTokens)
	require.Equal(t, 20, result.Usage.OutputTokens)
}

func TestRelayCacheWriteTTLFallbackAndAggregateReplacement(t *testing.T) {
	t.Parallel()
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			state := &relayState{}
			parseUsageAndAccumulate(state, []byte(`{"usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":3,"ttl_12h_tokens":7}}}}`), "response.in_progress", nil)
			if replace {
				// A later aggregate without TTL must erase the earlier breakdown.
				parseUsageAndAccumulate(state, []byte(`{"usage":{"input_tokens_details":{"cache_write_tokens":20}}}`), "response.in_progress", nil)
			}
			parseUsageAndAccumulate(state, []byte(`{"response":{"id":"resp_fallback"}}`), "response.completed", nil)
			usage := finalizeRelayTurnUsage(state)
			if replace {
				require.Equal(t, 20, usage.CacheCreationInputTokens)
				require.Zero(t, usage.CacheCreation30mInputTokens)
				require.Zero(t, usage.CacheCreation12hInputTokens)
			} else {
				require.Equal(t, 10, usage.CacheCreationInputTokens)
				require.Equal(t, 3, usage.CacheCreation30mInputTokens)
				require.Equal(t, 7, usage.CacheCreation12hInputTokens)
			}
			require.Equal(t, usage, state.usage)
			require.Equal(t, Usage{}, state.turnUsage)
		})
	}
}

func TestRelayCacheWriteTTLInvalidDetailsKeepAggregate(t *testing.T) {
	t.Parallel()
	for _, details := range []string{
		`null`, `{}`, `{"ttl_12h_tokens":10}`, `{"ttl_30m_tokens":0,"ttl_12h_tokens":9}`,
		`{"ttl_30m_tokens":9,"ttl_12h_tokens":9}`, `{"ttl_30m_tokens":-1,"ttl_12h_tokens":11}`,
		`{"ttl_30m_tokens":0.5,"ttl_12h_tokens":9.5}`, `{"ttl_30m_tokens":"0","ttl_12h_tokens":"10"}`,
	} {
		t.Run(details, func(t *testing.T) {
			state := &relayState{}
			usage := parseUsageAndAccumulate(state, []byte(`{"usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":`+details+`}}}`), "response.completed", nil)
			require.Equal(t, 10, usage.CacheCreationInputTokens)
			require.Zero(t, usage.CacheCreation30mInputTokens)
			require.Zero(t, usage.CacheCreation12hInputTokens)
		})
	}
}
