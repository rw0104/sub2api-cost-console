package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT61TerraMappedResponsesPreserveReasoningIntent(t *testing.T) {
	for _, effort := range []string{"none", "medium", "max"} {
		t.Run(effort, func(t *testing.T) {
			body := []byte(`{"model":"public","reasoning":{"mode":"pro","effort":"` + effort + `"},"temperature":0.5,"top_p":0.9,"include":["message.output_text.logprobs","reasoning.encrypted_content"]}`)
			out, changed, err := normalizeOpenAIResponsesReasoningMode(body, "gpt-6.1-terra")
			require.NoError(t, err)
			require.Equal(t, "public", gjson.GetBytes(out, "model").String())
			require.Equal(t, effort, gjson.GetBytes(out, "reasoning.effort").String())
			require.Equal(t, "pro", gjson.GetBytes(out, "reasoning.mode").String())
			require.Equal(t, effort != "none", changed)
			require.Equal(t, effort == "none", gjson.GetBytes(out, "temperature").Exists())
			require.Equal(t, effort == "none", gjson.GetBytes(out, "top_p").Exists())
			require.NoError(t, validateGPT61SolCompatRequest(body, "gpt-6.1-terra"))
		})
	}
}

func TestGPT61TerraChatOnlyToolCallsRejectedBeforeUpstream(t *testing.T) {
	for _, protocol := range []string{"chat", "messages", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			body := []byte(`{"model":"public","reasoning_effort":"none","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
			if protocol == "messages" {
				body = []byte(`{"model":"public","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`)
			}
			if protocol == "responses" {
				body = []byte(`{"model":"public","input":"hi","reasoning":{"effort":"none"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"public": "gpt-6.1-terra"}}}
			var err error
			switch protocol {
			case "chat":
				_, err = svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
			case "messages":
				_, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, body, "")
			case "responses":
				_, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, body, time.Now())
			}
			require.ErrorContains(t, err, "requires Responses")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "gpt-6.1-terra")
			require.Nil(t, upstream.lastReq)
		})
	}
}

func TestGPT61TerraPlainChatKeepsNoneEffort(t *testing.T) {
	body := []byte(`{"model":"public","reasoning_effort":"none","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://api.openai.com", "model_mapping": map[string]any{"public": "gpt-6.1-terra"}}}
	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-6.1-terra", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
}
