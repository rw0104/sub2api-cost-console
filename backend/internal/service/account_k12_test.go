package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountK12ModelAllowlist(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{"plan_type": "k-12"},
		Extra:       map[string]any{"k12_allowed_models": []any{"gpt-5.1*", "gpt-5.2-codex"}},
	}

	require.True(t, account.IsK12Account())
	require.True(t, account.IsModelSupported("gpt-5.1-codex"))
	require.True(t, account.IsModelSupported("gpt-5.2-codex"))
	require.False(t, account.IsModelSupported("gpt-5.3-codex"))
}

func TestAccountK12WithoutAllowlistPreservesExistingScheduling(t *testing.T) {
	account := &Account{Extra: map[string]any{"plan_type": "chatgpt_for_teachers"}}
	require.True(t, account.IsK12Account())
	require.True(t, account.IsModelSupported("gpt-5.3-codex"))
}

func TestOpenAIModelCapacityForcesNextAccount(t *testing.T) {
	body := []byte(`{"error":{"message":"Selected model is at capacity. Please try a different model."}}`)
	err := newOpenAIUpstreamFailoverError(http.StatusBadRequest, http.Header{}, body, "", false)

	require.Equal(t, openAIModelCapacityReason, err.Reason)
	require.Equal(t, GatewayFailureScopeAccount, err.Scope)
	require.Equal(t, NextAccountRetry, err.NextAccountAction)
	require.False(t, err.RetryableOnSameAccount)
	require.True(t, err.ShouldRetryNextAccount())
}
