package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIUltraCompatibilityBeforeGroupPolicy(t *testing.T) {
	for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
		t.Run(path, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6.1-sol","reasoning":{"effort":"ultra"},"client_metadata":{"reasoning_effort":"ultra"}}`)
			if path == "reasoning_effort" {
				body = []byte(`{"model":"gpt-6.1-sol","reasoning_effort":"ultra","client_metadata":{"reasoning_effort":"ultra"}}`)
			}
			original := string(body)
			got, changed, err := ApplyOpenAIReasoningEffortPolicy(body, "", nil, "")
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "xhigh", gjson.GetBytes(got, path).String())
			require.Equal(t, "ultra", gjson.GetBytes(got, "client_metadata.reasoning_effort").String())
			require.Equal(t, original, string(body))

			got, changed, err = ApplyOpenAIReasoningEffortPolicy(body, "high", nil, ReasoningEffortOverLimitDowngrade)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "high", gjson.GetBytes(got, path).String())
			_, _, err = ApplyOpenAIReasoningEffortPolicy(body, "high", nil, ReasoningEffortOverLimitDeny)
			require.True(t, IsReasoningEffortPolicyDenied(err))

			got, _, err = ApplyOpenAIReasoningEffortPolicy(body, "", []ReasoningEffortMapping{{From: "xhigh", To: "low"}}, "")
			require.NoError(t, err)
			require.Equal(t, "low", gjson.GetBytes(got, path).String())
			_, _, err = ApplyOpenAIReasoningEffortPolicy(body, "", []ReasoningEffortMapping{{From: "xhigh", To: "deny"}}, "")
			require.True(t, IsReasoningEffortPolicyDenied(err))
		})
	}
}

func TestOpenAIUltraCompatibilityWithoutBoundPolicy(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"ultra"}}`)
	for _, ctx := range []context.Context{nil, context.Background(), WithOpenAIReasoningEffortPolicy(context.Background(), "high", nil, "")} {
		got, changed, err := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
		require.NoError(t, err)
		require.True(t, changed)
		expected := "xhigh"
		if ctx != nil && ctx.Value(openAIReasoningEffortPolicyContextKey{}) != nil {
			expected = "high"
		}
		require.Equal(t, expected, gjson.GetBytes(got, "reasoning.effort").String())
	}
}

func TestOpenAIUltraCompatibilityWebSocketTurns(t *testing.T) {
	for _, hooks := range []*OpenAIWSIngressHooks{nil, {}, {MaxReasoningEffort: "high"}} {
		for _, previous := range []string{"", "resp_previous_turn"} {
			body := []byte(`{"type":"response.create","model":"gpt-6.1-sol","previous_response_id":"` + previous + `","reasoning":{"effort":"ultra"}}`)
			got, err := applyOpenAIWSReasoningEffortPolicy(body, hooks)
			require.NoError(t, err)
			expected := "xhigh"
			if hooks != nil && hooks.MaxReasoningEffort != "" {
				expected = "high"
			}
			require.Equal(t, expected, gjson.GetBytes(got, "reasoning.effort").String())
			require.Equal(t, previous, gjson.GetBytes(got, "previous_response_id").String())
		}
	}
}

func TestOpenAIUltraCompatibilityPreservesOtherRequests(t *testing.T) {
	for _, raw := range []string{
		`{"model":"gpt-6.1-sol"}`,
		`{"model":"gpt-6.1-sol","reasoning":{"effort":"max"}}`,
		`{"model":"gpt-6.1-sol","reasoning":{"effort":"future-value"}}`,
	} {
		got, changed, err := ApplyOpenAIReasoningEffortPolicy([]byte(raw), "", nil, "")
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, raw, string(got))
	}
	native := []byte(`{"model":"claude-opus-4","output_config":{"effort":"ultra"}}`)
	got, changed, err := ApplyReasoningEffortPolicy(native, "", nil, "")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, native, got)
}

func TestOpenAIUltraCompatibilityFollowsModelDescriptor(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"gpt-6-astra", "xhigh"},
		{"gpt-6.1-sol", "xhigh"},
		{"gpt-6.1-terra", "xhigh"},
		{"gpt-5.6-sol", "max"},
		{"gpt-5.6-terra", "max"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got, changed, err := ApplyOpenAIReasoningEffortPolicy([]byte(`{"model":"`+tc.model+`","reasoning":{"effort":"ultra"}}`), "", nil, "")
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, tc.want, gjson.GetBytes(got, "reasoning.effort").String())
		})
	}
}
