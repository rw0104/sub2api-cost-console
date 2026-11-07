package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSOutputReplayPreservesSourceAndUnrelatedMetadata(t *testing.T) {
	message := []byte(`{"type":"response.output_item.done","item":{"type":"message","id":"msg_1","role":"assistant","content":[],"internal_chat_message_metadata_passthrough":{"content_token_count":0,"source":"retained"}}}`)
	original := string(message)
	collector := &openAIWSToolCallReplayCollector{}
	collector.AddEvent("response.output_item.done", message)
	require.Equal(t, original, string(message))
	items := collector.AllItems()
	require.Len(t, items, 1)
	require.False(t, gjson.GetBytes(items[0], "internal_chat_message_metadata_passthrough.content_token_count").Exists())
	require.Equal(t, "retained", gjson.GetBytes(items[0], "internal_chat_message_metadata_passthrough.source").String())
	require.Equal(t, "msg_1", gjson.GetBytes(items[0], "id").String())
}

// Exercise the real WS ingress -> HTTP bridge -> replay path. The second turn
// must retain its own output budget and input metadata without inheriting the
// previous forced tool selection or echoing output-only token counts upstream.
func TestOpenAIWSBridgeReplayKeepsCurrentTurnControls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstEvent := `{"type":"response.completed","response":{"id":"resp_replay_1","model":"gpt-6.1-sol","status":"completed","output":[{"type":"function_call","id":"fc_replay","call_id":"call_replay","name":"run","arguments":"{}","internal_chat_message_metadata_passthrough":{"content_token_count":11}}],"usage":{"input_tokens":9,"output_tokens":1}}}`
	secondEvent := `{"type":"response.completed","response":{"id":"resp_replay_2","model":"gpt-6.1-sol","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":1}}}`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + firstEvent + "\n\n"))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + secondEvent + "\n\n"))},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
	account := &Account{ID: 9217, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, payload, err := conn.Read(ctx)
		if err != nil {
			errCh <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "sk-test", payload, nil)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","max_output_tokens":321,"tool_choice":{"type":"function","name":"run"},"tools":[{"type":"function","name":"run","parameters":{"type":"object","properties":{}}}],"input":"run once"}`)))
	_, delivered, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(11), gjson.GetBytes(delivered, "response.output.0.internal_chat_message_metadata_passthrough.content_token_count").Int())
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","max_output_tokens":127,"instructions":"","previous_response_id":"resp_replay_1","input":[{"type":"function_call_output","call_id":"call_replay","output":"done","internal_chat_message_metadata_passthrough":{"content_token_count":31,"caller":true}}]}`)))
	_, delivered, err = conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "resp_replay_2", gjson.GetBytes(delivered, "response.id").String())
	require.NoError(t, conn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("bridge did not finish after client closure")
	}
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, int64(321), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
	second := upstream.bodies[1]
	require.Equal(t, int64(127), gjson.GetBytes(second, "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(second, "tool_choice").Exists())
	require.Equal(t, `""`, gjson.GetBytes(second, "instructions").Raw)
	items := gjson.GetBytes(second, "input").Array()
	require.Len(t, items, 3)
	require.False(t, items[1].Get("internal_chat_message_metadata_passthrough").Exists())
	require.Equal(t, "call_replay", items[1].Get("call_id").String())
	require.Equal(t, int64(31), items[2].Get("internal_chat_message_metadata_passthrough.content_token_count").Int())
	require.True(t, items[2].Get("internal_chat_message_metadata_passthrough.caller").Bool())
}
