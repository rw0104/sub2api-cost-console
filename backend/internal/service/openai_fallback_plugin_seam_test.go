//go:build unit

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

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestResponsesChatFallback_UpstreamKeepsPluginSeam 钉住 R5 真正要防的不变量：
// /v1/responses 降级到 raw chat completions 之后，出站仍必须经过 doOpenAIUpstream，
// 插件接缝（PreprocessOpenAI / ObserveOpenAIHeaderProbe / RoundTripOpenAIOAuth）不被绕过。
// 上游本轮改过 forward、cc_pipeline、chat_completions 与本回退文件，其中任何一处
// 把出站换成直连 httpUpstream，插件都会静默失效且编译期无法发现。
//
// 关于源计划 R5 的验收场景：它写的是「启用保护传输的 OAuth 账号走本回退时命中
// RoundTripOpenAIOAuth」，该场景不可达 —— resolveCCFallbackTarget 要求
// account.GetOpenAIProtocolAPIKey() 非空（注释亦写明回退路径仅面向 APIKey 账号），
// OAuth 账号在取凭证处即报 missing api_key；而保护传输与 Header Probe 两个能力
// 都只绑定 accountType=oauth。因此这里按回退路径真实的账号类型验证接缝仍在链路上。
func TestResponsesChatFallback_UpstreamKeepsPluginSeam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	capability := testPreprocessCapability()
	capability.AccountType = AccountTypeAPIKey

	calls := 0
	capabilityMatched := false
	manager, _ := testPreprocessManager(t, capability, &testPreprocessor{
		call: func(_ context.Context, request pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
			calls++
			capabilityMatched = request.Capability == pluginv2.CapabilityRequestPreprocess
			return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionPass}, nil
		},
	})

	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	responseBody := `{"id":"chatcmpl_seam","choices":[{"message":{"role":"assistant","content":"ok"},
"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	svc.SetPluginManager(manager)

	result, err := svc.forwardResponsesViaRawChatCompletions(
		context.Background(), c, forceChatResponsesFallbackAccount(), body, time.Now(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, 1, calls, "回退出站必须经过插件接缝，而不是直连 httpUpstream")
	require.True(t, capabilityMatched, "接缝上报的能力必须是 request preprocess")

	// 插件放行后请求照常抵达上游，且协议已转换为 chat completions。
	require.Len(t, upstream.requests, 1)
	require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/chat/completions"),
		"回退后的出站端点应为 chat completions，实际为 "+upstream.lastReq.URL.Path)
}
