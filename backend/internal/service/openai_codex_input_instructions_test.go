package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Codex 0.162+ no longer sends a top-level instructions field: the base prompt
// is the leading developer message of input (openai/codex#51156). These tests
// pin that the gateway does not add its own default prompt on top of it.

const codexInputBasePrompt = "You are Codex, a coding agent based on GPT-6.1 Terra. Client base prompt."

func codexInputInstructionsBody(model string, withInstructions bool, firstRole string) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"` + model + `","stream":true,"store":false,`)
	if withInstructions {
		b.WriteString(`"instructions":"legacy client instructions",`)
	}
	b.WriteString(`"input":[`)
	if firstRole == "developer" {
		b.WriteString(`{"type":"message","role":"developer","id":"msg_base","content":[{"type":"input_text","text":"` + codexInputBasePrompt + `"}]},`)
	}
	b.WriteString(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	return []byte(b.String())
}

func codexInputInstructionsContext(userAgent, originator string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
	if userAgent != "" {
		c.Request.Header.Set("User-Agent", userAgent)
	}
	if originator != "" {
		c.Request.Header.Set("originator", originator)
	}
	return c
}

func codexInputInstructionsUpstream() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_input_instructions"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"", "data: [DONE]", "",
		}, "\n"))),
	}}
}

func codexInputInstructionsAccount(passthrough bool) *Account {
	extra := map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff}
	if passthrough {
		extra["openai_passthrough"] = true
	}
	return &Account{
		ID: 7001, Name: "codex-input-instructions", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
		Extra:       extra,
		Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1),
	}
}

func TestCodexClientCarriesInputBaseInstructions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	official := codexInputInstructionsContext("codex_cli_rs/0.165.0 (Windows 10.0.26100; x86_64) WindowsTerminal", "codex_cli_rs")
	cases := []struct {
		name string
		c    *gin.Context
		body string
		want bool
	}{
		{"codex_0165_shape", official, string(codexInputInstructionsBody("gpt-6.1-terra", false, "developer")), true},
		{"developer_without_type", official, `{"input":[{"role":"developer","content":"base"},{"role":"user","content":"hi"}]}`, true},
		{"instructions_present", official, string(codexInputInstructionsBody("gpt-6.1-terra", true, "developer")), false},
		{"empty_instructions_present", official, `{"instructions":"","input":[{"type":"message","role":"developer","content":"base"}]}`, false},
		{"user_first", official, string(codexInputInstructionsBody("gpt-6.1-terra", false, "user")), false},
		{"non_message_first", official, `{"input":[{"type":"function_call_output","role":"developer","call_id":"c1","output":"x"}]}`, false},
		{"string_input", official, `{"input":"hi"}`, false},
		{"no_input", official, `{"model":"gpt-6.1-terra"}`, false},
		{"third_party_client", codexInputInstructionsContext("curl/8.7.1", ""), string(codexInputInstructionsBody("gpt-6.1-terra", false, "developer")), false},
		{"nil_context", nil, string(codexInputInstructionsBody("gpt-6.1-terra", false, "developer")), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, codexClientCarriesInputBaseInstructions(tc.c, []byte(tc.body)))
		})
	}
}

func TestOpenAIGatewayService_Forward_Codex0165InputBaseInstructionsAreNotDuplicated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := codexInputInstructionsContext("codex_cli_rs/0.165.0 (Windows 10.0.26100; x86_64) WindowsTerminal", "codex_cli_rs")
	upstream := codexInputInstructionsUpstream()
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, codexInputInstructionsAccount(false), codexInputInstructionsBody("gpt-6.1-terra", false, "developer"))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists(), "official client base prompt already travels in input")
	require.Equal(t, "developer", gjson.GetBytes(upstream.lastBody, "input.0.role").String())
	require.Equal(t, codexInputBasePrompt, gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
	require.Equal(t, 1, strings.Count(string(upstream.lastBody), codexInputBasePrompt))
	require.NotContains(t, string(upstream.lastBody), strings.TrimSpace(defaultCodexSynthInstructions("gpt-6.1-terra"))[:64])
}

func TestOpenAIGatewayService_Forward_MissingInstructionsStillDefaultedOutsideCodex0165Shape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		userAgent  string
		originator string
		firstRole  string
	}{
		{name: "third_party_client_with_developer_prefix", userAgent: "curl/8.7.1", firstRole: "developer"},
		{name: "official_client_without_developer_prefix", userAgent: "codex_cli_rs/0.165.0 (Linux; x86_64) xterm", originator: "codex_cli_rs", firstRole: "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codexInputInstructionsContext(tc.userAgent, tc.originator)
			upstream := codexInputInstructionsUpstream()
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

			result, err := svc.Forward(context.Background(), c, codexInputInstructionsAccount(false), codexInputInstructionsBody("gpt-6.1-terra", false, tc.firstRole))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t,
				strings.TrimSpace(defaultCodexSynthInstructions("gpt-6.1-terra")),
				strings.TrimSpace(gjson.GetBytes(upstream.lastBody, "instructions").String()))
		})
	}
}

func TestOpenAIGatewayService_Forward_OfficialClientInstructionsArePreserved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := codexInputInstructionsContext("codex_cli_rs/0.161.0 (Linux; x86_64) xterm", "codex_cli_rs")
	upstream := codexInputInstructionsUpstream()
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, codexInputInstructionsAccount(false), codexInputInstructionsBody("gpt-6.1-terra", true, "developer"))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "legacy client instructions", gjson.GetBytes(upstream.lastBody, "instructions").String())
}

func TestOpenAIGatewayService_OAuthPassthrough_Codex0165InputBaseInstructionsAreNotDuplicated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name             string
		firstRole        string
		wantInstructions bool
	}{
		{name: "developer_prefix", firstRole: "developer", wantInstructions: false},
		{name: "no_developer_prefix", firstRole: "user", wantInstructions: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codexInputInstructionsContext("codex_cli_rs/0.165.0 (Linux; x86_64) xterm", "codex_cli_rs")
			upstream := codexInputInstructionsUpstream()
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

			result, err := svc.Forward(context.Background(), c, codexInputInstructionsAccount(true), codexInputInstructionsBody("codex-auto-review", false, tc.firstRole))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			if !tc.wantInstructions {
				require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
				require.Equal(t, 1, strings.Count(string(upstream.lastBody), codexInputBasePrompt))
				return
			}
			require.Equal(t,
				strings.TrimSpace(defaultCodexSynthInstructions("codex-auto-review")),
				strings.TrimSpace(gjson.GetBytes(upstream.lastBody, "instructions").String()))
		})
	}
}
