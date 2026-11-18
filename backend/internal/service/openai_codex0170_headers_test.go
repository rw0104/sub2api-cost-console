package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Official Responses HTTP and WebSocket clients use the thread ID as the
// client request ID. Clients sending independent IDs can share these routes.
func TestCodex0170SubagentHeadersAndThreadCorrelation(t *testing.T) {
	for _, transport := range []string{"http", "http-passthrough", "ws"} {
		for _, requestID := range []string{"thread-1", "separate-request"} {
			t.Run(transport+"/"+requestID, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set("User-Agent", "codex_exec/0.170.0")
				c.Request.Header.Set("thread-id", "thread-1")
				c.Request.Header.Set("x-client-request-id", requestID)
				c.Request.Header.Set("x-openai-subagent", "collab_spawn")
				account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "account-1"}}
				svc := &OpenAIGatewayService{}
				body := []byte(`{"model":"gpt-6.2-sol","instructions":"test","input":"hello","stream":true}`)
				var headers http.Header
				if transport == "ws" {
					var err error
					headers, _, err = svc.buildOpenAIWSHeaders(context.Background(), c, account, "test-token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "", "")
					require.NoError(t, err)
				} else {
					var req *http.Request
					var err error
					if transport == "http-passthrough" {
						req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
					} else {
						req, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "test-token", true, "", true)
					}
					require.NoError(t, err)
					headers = req.Header
				}
				require.Equal(t, "collab_spawn", headers.Get("x-openai-subagent"))
				require.NotEmpty(t, headers.Get("thread-id"))
				require.NotEqual(t, "thread-1", headers.Get("thread-id"))
				require.NotEmpty(t, headers.Get("x-client-request-id"))
				require.Equal(t, requestID == "thread-1", headers.Get("x-client-request-id") == headers.Get("thread-id"))
				require.Equal(t, requestID, c.Request.Header.Get("x-client-request-id"))
			})
		}
	}
}

func TestCodex0170WSSubagentHandshakeReuse(t *testing.T) {
	parent := http.Header{}
	child := http.Header{}
	child.Set("x-openai-subagent", "collab_spawn")
	rootKey := normalizeOpenAIWSHandshakeCompatibility(nil, parent)
	childKey := normalizeOpenAIWSHandshakeCompatibility(nil, child)
	require.NotEqual(t, rootKey, childKey)
	require.Equal(t, childKey, normalizeOpenAIWSHandshakeCompatibility(nil, child.Clone()))
	child.Set("x-openai-subagent", "review")
	require.NotEqual(t, childKey, normalizeOpenAIWSHandshakeCompatibility(nil, child))
}
func TestCodex0170HTTPPreservesPromptCacheTTL(t *testing.T) {
	for _, ttl := range []string{"30m", "12h"} {
		t.Run(ttl, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6.2-sol","input":"hello","instructions":"test","prompt_cache_options":{"ttl":"` + ttl + `"},"stream":false}`)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse)}}
			c := newOpenAIRejectedFieldTestContext(body)
			c.Request.Header.Set("User-Agent", "codex_exec/0.170.0")
			_, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 1)
			require.Equal(t, ttl, gjson.GetBytes(upstream.bodies[0], "prompt_cache_options.ttl").String())
		})
	}
}
