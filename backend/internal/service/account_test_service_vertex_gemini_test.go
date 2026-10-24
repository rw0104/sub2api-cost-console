//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// vertexTestServiceAccountJSON 是 parseVertexServiceAccountKey 能接受的最小凭据，
// 字段集与 TestParseVertexServiceAccountKey 保持一致。
const vertexTestServiceAccountJSON = `{
    "type": "service_account",
    "project_id": "sa-project",
    "private_key_id": "kid",
    "private_key": "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n",
    "client_email": "svc@sa-project.iam.gserviceaccount.com"
}`

func vertexTestServiceAccount() *Account {
	return &Account{
		Platform: PlatformGemini,
		Type:     AccountTypeServiceAccount,
		Credentials: map[string]any{
			"project_id":           "sa-project",
			"location":             "global",
			"service_account_json": vertexTestServiceAccountJSON,
		},
	}
}

// TestBuildGeminiServiceAccountRequest_VertexAuthAndEndpoint 覆盖账号连通性测试里
// Vertex Gemini 请求的装配（源计划 R9）。
//
// v0.2.16 把这里从「硬要求 tokenProvider + 恒用 Bearer + 恒走项目级 URL」改为委托
// buildVertexGeminiAccountURL 与 setVertexGeminiAuth，两者都带账号形态分支，因此这
// 是行为扩展而非单纯提取函数：Vertex API Key 账号现在走 Express 全局端点并改用
// x-goog-api-key 认证。两个 helper 各自有单元测试，但改写后的这个调用方此前没有任何
// 测试，Bearer 分支也没有断言 —— 接错 action、漏调认证或两种形态互相串台都不会被发现。
func TestBuildGeminiServiceAccountRequest_VertexAuthAndEndpoint(t *testing.T) {
	ctx := context.Background()
	payload := []byte(`{"contents":[]}`)
	const model = "gemini-3-flash-preview"

	t.Run("service_account_json_uses_project_endpoint_and_bearer", func(t *testing.T) {
		svc := &AccountTestService{geminiTokenProvider: &GeminiTokenProvider{
			tokenCache: &fakeGeminiTokenCache{token: "svc-token"},
		}}

		req, err := svc.buildGeminiServiceAccountRequest(ctx, vertexTestServiceAccount(), model, payload)
		require.NoError(t, err)
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t,
			"https://aiplatform.googleapis.com/v1/projects/sa-project/locations/global/publishers/google/models/gemini-3-flash-preview:streamGenerateContent?alt=sse",
			req.URL.String(), "服务账号须走项目级端点")
		require.Equal(t, "Bearer svc-token", req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("x-goog-api-key"), "服务账号不得退化成 API Key 认证")
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body, "请求体必须是传入的 payload")
	})

	t.Run("vertex_api_key_without_project_uses_express_endpoint", func(t *testing.T) {
		// Express 模式：auth_mode=apikey 且未配 project_id。
		account := &Account{
			Platform:    PlatformGemini,
			Type:        AccountTypeServiceAccount,
			Credentials: map[string]any{"auth_mode": "apikey", "api_key": " vertex-key "},
		}
		// 该形态不触碰 token provider，故这里刻意不配。
		svc := &AccountTestService{}

		req, err := svc.buildGeminiServiceAccountRequest(ctx, account, model, payload)
		require.NoError(t, err)
		require.Equal(t,
			"https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-3-flash-preview:streamGenerateContent?alt=sse",
			req.URL.String(), "未配 project_id 的 API Key 账号须走 Express 全局端点")
		require.Equal(t, "vertex-key", req.Header.Get("x-goog-api-key"))
		require.Empty(t, req.Header.Get("Authorization"), "Express 模式不得发送 Bearer")
	})

	t.Run("service_account_without_token_provider_fails_closed", func(t *testing.T) {
		svc := &AccountTestService{}

		_, err := svc.buildGeminiServiceAccountRequest(ctx, vertexTestServiceAccount(), model, payload)
		require.ErrorContains(t, err, "failed to authenticate vertex request")
		require.ErrorContains(t, err, "gemini token provider not configured")
	})

	t.Run("vertex_api_key_blank_key_fails_closed", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformGemini,
			Type:        AccountTypeServiceAccount,
			Credentials: map[string]any{"auth_mode": "apikey", "api_key": "   "},
		}
		svc := &AccountTestService{}

		_, err := svc.buildGeminiServiceAccountRequest(ctx, account, model, payload)
		require.ErrorContains(t, err, "vertex api_key not configured")
	})

	t.Run("unsupported_action_is_rejected_before_any_auth", func(t *testing.T) {
		// 本调用方固定传 streamGenerateContent；这里直接校验底层拒绝非法 action，
		// 保证将来改动 action 常量时不会静默拼出无效端点。
		_, err := buildVertexGeminiAccountURL(vertexTestServiceAccount(), model, "rawPredict", true)
		require.ErrorContains(t, err, "unsupported vertex gemini action")
	})
}
