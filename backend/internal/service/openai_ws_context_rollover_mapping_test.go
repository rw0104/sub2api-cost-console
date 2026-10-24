package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSContextWindowRolloverPreservesPerTurnMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, storeEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("store=%t", storeEnabled), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

			upstream := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_window_a","model":"gpt-5.1","usage":{"input_tokens":3,"output_tokens":1}}}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_window_b","model":"gpt-5.2","usage":{"input_tokens":7,"output_tokens":2}}}`),
			}}
			dialer := &openAIWSCaptureDialer{conn: upstream}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			defer pool.Close()
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     &httpUpstreamRecorder{},
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			account := &Account{
				ID: 915, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"api_key": "sk-test",
					"model_mapping": map[string]any{
						"channel-a": "gpt-5.1",
						"channel-b": "gpt-5.2",
					},
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
				},
			}

			type mappedTurn struct {
				turn           int
				requestedModel string
			}
			type completedTurn struct {
				turn   int
				result *OpenAIForwardResult
				err    error
			}
			var mapped []mappedTurn
			var completed []completedTurn
			hooks := &OpenAIWSIngressHooks{
				MapRequestModel: func(turn int, originalModel string) (string, error) {
					mapped = append(mapped, mappedTurn{turn: turn, requestedModel: originalModel})
					switch originalModel {
					case "client-a":
						return "channel-a", nil
					case "client-b":
						return "channel-b", nil
					default:
						return "", fmt.Errorf("unexpected requested model %q", originalModel)
					}
				},
				AfterTurn: func(turn int, result *OpenAIForwardResult, err error) {
					completed = append(completed, completedTurn{turn: turn, result: result, err: err})
				},
			}
			controlCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server, serverErr := startPassthroughHookRecordingServer(t, controlCtx, svc, account, hooks)
			defer server.Close()
			dialCtx, cancelDial := context.WithTimeout(controlCtx, 3*time.Second)
			client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			cancelDial()
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()

			payloads := []string{
				fmt.Sprintf(`{"type":"response.create","model":"client-a","stream":false,"store":%t,"client_metadata":{"x-codex-window-id":"window-a"},"input":[{"type":"input_text","text":"old window input"}]}`, storeEnabled),
				fmt.Sprintf(`{"type":"response.create","model":"client-b","stream":false,"store":%t,"previous_response_id":"resp_window_a","client_metadata":{"x-codex-window-id":"window-b"},"input":[{"type":"input_text","text":"new window input"}]}`, storeEnabled),
			}
			for turn, payload := range payloads {
				writeCtx, cancelWrite := context.WithTimeout(controlCtx, 3*time.Second)
				err = client.Write(writeCtx, coderws.MessageText, []byte(payload))
				cancelWrite()
				require.NoError(t, err)
				event, readErr := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, readErr)
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
				require.Equal(t, []string{"client-a", "client-b"}[turn], gjson.GetBytes(event, "response.model").String(), "each client response retains its requested model")
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err = <-serverErr:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("context-window rollover session did not finish")
			}

			require.Equal(t, []mappedTurn{{turn: 1, requestedModel: "client-a"}, {turn: 2, requestedModel: "client-b"}}, mapped)
			require.Len(t, completed, 2)
			for turn, want := range []struct {
				requestedModel string
				upstreamModel  string
				inputTokens    int
			}{{"client-a", "gpt-5.1", 3}, {"client-b", "gpt-5.2", 7}} {
				got := completed[turn]
				require.Equal(t, turn+1, got.turn)
				require.NoError(t, got.err)
				require.NotNil(t, got.result)
				require.Equal(t, want.requestedModel, got.result.Model, "service Model supplies the original requested model to usage recording")
				require.Equal(t, want.upstreamModel, got.result.UpstreamModel)
				require.Equal(t, want.upstreamModel, got.result.UpstreamResponseModel)
				require.Equal(t, want.inputTokens, got.result.Usage.InputTokens)
			}
			require.Equal(t, 1, dialer.DialCount(), "rollover must work within the existing ctx_pool session")
			require.Len(t, upstream.writes, 2)
			firstWrite := requestToJSONString(upstream.writes[0])
			secondWrite := requestToJSONString(upstream.writes[1])
			require.Equal(t, "gpt-5.1", gjson.Get(firstWrite, "model").String())
			require.Equal(t, "gpt-5.2", gjson.Get(secondWrite, "model").String())
			require.Equal(t, "window-a", gjson.Get(firstWrite, "client_metadata.x-codex-window-id").String())
			require.Equal(t, "window-b", gjson.Get(secondWrite, "client_metadata.x-codex-window-id").String())
			require.False(t, gjson.Get(secondWrite, "previous_response_id").Exists(), "the new window must not retain the old response anchor")
			require.JSONEq(t, `[{"type":"input_text","text":"new window input"}]`, gjson.Get(secondWrite, "input").Raw, "rollover must not replay input from the old window")
		})
	}
}
