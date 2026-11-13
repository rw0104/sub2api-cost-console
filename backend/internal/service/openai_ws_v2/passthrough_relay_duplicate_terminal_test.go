package openai_ws_v2

import (
	"context"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelayDuplicateTerminalAllowsSameIDAfterNewRequest(t *testing.T) {
	t.Parallel()
	client := newPassthroughTestFrameConn(nil, false)
	upstream := newPassthroughTestFrameConn(nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	turns := make(chan RelayTurnResult, 4)
	type outcome struct {
		result RelayResult
		exit   *RelayExit
	}
	done := make(chan outcome, 1)
	request := []byte(`{"type":"response.create","model":"gpt-6-luna","input":[]}`)
	go func() {
		result, exit := Relay(ctx, client, upstream, request, RelayOptions{
			OnTurnComplete: func(turn RelayTurnResult) { turns <- turn },
		})
		done <- outcome{result: result, exit: exit}
	}()
	first := []byte(`{"type":"response.completed","response":{"id":"resp_reused","usage":{"input_tokens":13,"output_tokens":5,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":3,"ttl_12h_tokens":7}}}}}`)
	upstream.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: first}
	select {
	case turn := <-turns:
		require.Equal(t, 7, turn.Usage.CacheCreation12hInputTokens)
	case <-ctx.Done():
		t.Fatal("first turn was not settled")
	}
	upstream.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: first}
	upstream.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"rate_limits.updated"}`)}
	require.Eventually(t, func() bool { return len(client.Writes()) == 3 }, time.Second, time.Millisecond,
		"the duplicate and following barrier must remain visible to the client")
	require.Empty(t, turns, "a duplicate terminal must not emit a second settlement")

	// A new client request is a new turn even if a compatible provider reuses
	// its response ID. Do not implement a connection-wide response-ID blacklist.
	client.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: request}
	require.Eventually(t, func() bool { return len(upstream.Writes()) == 2 }, time.Second, time.Millisecond)
	upstream.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.created","response":{"id":"resp_reused"}}`)}
	upstream.readCh <- passthroughTestFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp_reused","usage":{"input_tokens":4,"output_tokens":2}}}`)}
	select {
	case turn := <-turns:
		require.Equal(t, "resp_reused", turn.RequestID)
		require.Equal(t, 4, turn.Usage.InputTokens)
		require.Zero(t, turn.Usage.CacheCreation12hInputTokens)
	case <-ctx.Done():
		t.Fatal("new request with a reused response ID was incorrectly suppressed")
	}
	_ = upstream.Close()
	select {
	case result := <-done:
		require.Nil(t, result.exit)
		require.Equal(t, 17, result.result.Usage.InputTokens)
		require.Equal(t, 7, result.result.Usage.OutputTokens)
		require.Equal(t, 10, result.result.Usage.CacheCreationInputTokens)
		require.Equal(t, 7, result.result.Usage.CacheCreation12hInputTokens)
	case <-ctx.Done():
		t.Fatal("relay did not finish")
	}
}

func TestRelayDuplicateTerminalDoesNotResurrectOrCorruptTurn(t *testing.T) {
	t.Parallel()
	state := &relayState{}
	now := time.Unix(10, 0)
	observe := func(payload string) observedUpstreamEvent {
		return observeUpstreamMessage(state, []byte(payload), now, func() time.Time { return now }, nil)
	}
	state.setPendingTurnStartedAt(now)
	first := `{"type":"response.completed","response":{"id":"resp_old","usage":{"input_tokens":13,"output_tokens":5}}}`
	require.True(t, observe(first).terminal)
	require.False(t, observe(`{"type":"response.in_progress","response":{"id":"resp_old","usage":{"input_tokens":99}}}`).terminal)
	require.Nil(t, state.activeTurn, "late metadata must not resurrect the completed turn")
	require.Equal(t, Usage{}, state.turnUsage)

	state.setPendingTurnStartedAt(now.Add(time.Second))
	observe(`{"type":"response.created","response":{"id":"resp_new"}}`)
	require.Equal(t, "resp_new", openAIWSRelayActiveTurnID(state))
	require.False(t, observe(first).terminal, "the preceding turn's duplicate cannot settle the active turn")
	require.Equal(t, "resp_new", openAIWSRelayActiveTurnID(state))
	require.Equal(t, Usage{}, state.turnUsage)
	require.True(t, observe(`{"type":"response.completed","response":{"id":"resp_new","usage":{"input_tokens":4,"output_tokens":2}}}`).terminal)
	require.Equal(t, Usage{InputTokens: 17, OutputTokens: 7}, state.usage)
}
