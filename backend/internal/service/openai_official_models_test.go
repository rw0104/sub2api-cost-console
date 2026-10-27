package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newOfficialModelsTestService(status int, body string) (*AccountTestService, *httpUpstreamRecorder) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
	return &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}, upstream
}

func officialModelsOAuthTestAccount() *Account {
	return &Account{
		ID:       91,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "official-verification-secret",
			"chatgpt_account_id": "official-verification-account",
		},
	}
}

func TestOfficialOpenAIModelsVerificationPreservesDraftAndUsesLiveSlugs(t *testing.T) {
	svc, upstream := newOfficialModelsTestService(http.StatusOK, `{"models":[
		{"slug":"gpt-6.1-terra","id":"display-only-id","display_name":"GPT-6.1 Terra","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"none"},{"effort":"medium"},{"effort":"max"}],"context_window":1050000,"max_output_tokens":128000,"input_modalities":["text","image"],"supports_search_tool":true},
		{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol"},
		{"slug":"gpt-5.6-luna"},
		{"slug":"future-official-slug","display_name":"An unrelated display name"},
		{"display_name":"GPT-6 Sol","name":"gpt-6-sol"}
	]}`)
	account := officialModelsOAuthTestAccount()
	account.Extra = map[string]any{"unrelated": "keep"}
	repo := &upstreamModelMetadataRepoStub{}
	svc.accountRepo = repo
	draft := map[string]string{
		"my-alias":      "gpt-6.1-terra",
		"gpt-6.1-sol":   "gpt-5.6-luna",
		"gpt-5.6-*":     "gpt-5.6-luna",
		"retired-alias": "retired-official-model",
	}
	wantOriginal := map[string]string{
		"my-alias":      "gpt-6.1-terra",
		"gpt-6.1-sol":   "gpt-5.6-luna",
		"gpt-5.6-*":     "gpt-5.6-luna",
		"retired-alias": "retired-official-model",
	}

	result, err := svc.VerifyOfficialOpenAIModels(context.Background(), account, draft)
	require.NoError(t, err)
	require.Equal(t, []string{"future-official-slug", "gpt-5.6-luna", "gpt-6.1-sol", "gpt-6.1-terra"}, result.Models)
	require.Equal(t, officialCodexModelsURL, result.Source)
	fetchedAt, err := time.Parse(time.RFC3339, result.FetchedAt)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), fetchedAt, 2*time.Second)
	require.Equal(t, []string{"gpt-6-sol", "gpt-6-luna", "gpt-5.6-terra", "gpt-6-astra", "gpt-5.6-sol"}, result.MissingModels)
	require.Equal(t, []OfficialOpenAIModelMappingCheck{
		{From: "gpt-5.6-*", To: "gpt-5.6-luna", Available: true},
		{From: "gpt-6.1-sol", To: "gpt-5.6-luna", Available: true},
		{From: "my-alias", To: "gpt-6.1-terra", Available: true},
		{From: "retired-alias", To: "retired-official-model", Available: false},
	}, result.Checks)
	require.Equal(t, map[string]string{
		"my-alias":      "gpt-6.1-terra",
		"gpt-6.1-sol":   "gpt-5.6-luna",
		"gpt-5.6-*":     "gpt-5.6-luna",
		"retired-alias": "retired-official-model",
		"gpt-6.1-terra": "gpt-6.1-terra",
	}, result.SuggestedModelMapping)
	require.Equal(t, wantOriginal, draft, "suggestions must not mutate the mapping draft")
	require.Equal(t, map[string]any{"unrelated": "keep"}, account.Extra)
	require.Nil(t, repo.updates, "official verification must not persist a metadata snapshot")
	require.Len(t, upstream.requests, 1, "verification must not fetch a third-party registry")
	require.Equal(t, officialCodexModelsURL, upstream.lastReq.URL.Scheme+"://"+upstream.lastReq.URL.Host+upstream.lastReq.URL.Path)
	require.Equal(t, "Bearer official-verification-secret", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "official-verification-account", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.NotEmpty(t, upstream.lastReq.URL.Query().Get("client_version"))
	deadline, hasDeadline := upstream.lastReq.Context().Deadline()
	require.True(t, hasDeadline)
	require.LessOrEqual(t, time.Until(deadline), codexModelsManifestRequestTimeout)
	terra := result.Metadata["gpt-6.1-terra"]
	require.Equal(t, "GPT-6.1 Terra", terra.DisplayName)
	require.Equal(t, int64(1050000), terra.ContextWindow)
	require.Equal(t, int64(128000), terra.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, terra.InputModalities)
	require.Equal(t, []string{"none", "medium", "max"}, terra.SupportedReasoningLevels)
	require.JSONEq(t, "true", string(terra.CodexToolCapabilities["supports_search_tool"]))
}

func TestOfficialOpenAIModelsVerificationRespectsBroadWildcard(t *testing.T) {
	svc, _ := newOfficialModelsTestService(http.StatusOK, `{"models":[{"slug":"gpt-6.1-terra"},{"slug":"gpt-6.1-sol"}]}`)
	draft := map[string]string{"gpt-*": "gpt-6.1-sol", "custom": "gpt-6.1-terra"}
	result, err := svc.VerifyOfficialOpenAIModels(context.Background(), officialModelsOAuthTestAccount(), draft)
	require.NoError(t, err)
	require.Equal(t, draft, result.SuggestedModelMapping, "supplementing must not override wildcard routing")
}

func TestOfficialOpenAIModelsVerificationSavedVersusEmptyDraft(t *testing.T) {
	for _, tc := range []struct {
		name    string
		draft   map[string]string
		wantLen int
	}{
		{name: "saved mapping", draft: nil, wantLen: 1},
		{name: "explicit empty draft", draft: map[string]string{}, wantLen: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newOfficialModelsTestService(http.StatusOK, `{"models":[{"slug":"gpt-6.1-terra"}]}`)
			account := officialModelsOAuthTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"saved-alias": "gpt-6.1-terra"}
			result, err := svc.VerifyOfficialOpenAIModels(context.Background(), account, tc.draft)
			require.NoError(t, err)
			require.Len(t, result.Checks, tc.wantLen)
			require.Equal(t, "gpt-6.1-terra", result.SuggestedModelMapping["gpt-6.1-terra"])
		})
	}
}

func TestOfficialOpenAIModelsVerificationUsesOfficialAPIKeyEndpoint(t *testing.T) {
	for _, base := range []string{"", "https://api.openai.com", "https://api.openai.com/", "https://api.openai.com/v1", "https://api.openai.com/v1/"} {
		t.Run(base, func(t *testing.T) {
			svc, upstream := newOfficialModelsTestService(http.StatusOK, `{"data":[{"id":"gpt-6.1-terra","name":"Different display text"},{"name":"gpt-6-sol"}]}`)
			result, err := svc.VerifyOfficialOpenAIModels(context.Background(), &Account{
				ID: 92, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": base, "api_key": "platform-secret"},
			}, map[string]string{})
			require.NoError(t, err)
			require.Equal(t, officialOpenAIModelsURL, result.Source)
			require.Equal(t, officialOpenAIModelsURL, upstream.lastReq.URL.String())
			require.Equal(t, "Bearer platform-secret", upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, []string{"gpt-6.1-terra"}, result.Models)
		})
	}
}

func TestOfficialOpenAIModelsVerificationRejectsNonOfficialConfigurationBeforeRequest(t *testing.T) {
	for _, base := range []string{
		"https://relay.example.com/v1", "http://api.openai.com/v1",
		"https://api.openai.com.evil.example/v1", "https://api.openai.com:8443/v1",
		"https://user:secret@api.openai.com/v1", "https://api.openai.com/other/v1",
		"https://api.openai.com/v1?token=secret", "https://api.openai.com/v1?",
		"https://api.openai.com/v1#secret", "https://api.openai.com/%76%31",
	} {
		t.Run(base, func(t *testing.T) {
			svc, upstream := newOfficialModelsTestService(http.StatusOK, `{"data":[{"id":"gpt-6.1-terra"}]}`)
			_, err := svc.VerifyOfficialOpenAIModels(context.Background(), &Account{
				Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": base, "api_key": "platform-secret"},
			}, nil)
			var syncErr *UpstreamModelSyncError
			require.ErrorAs(t, err, &syncErr)
			require.Equal(t, UpstreamModelSyncErrorConfiguration, syncErr.Kind)
			require.Empty(t, upstream.requests)
			require.NotContains(t, syncErr.SafeMessage(), "secret")
		})
	}
	svc, upstream := newOfficialModelsTestService(http.StatusOK, `{"data":[{"id":"gpt-6.1-terra"}]}`)
	_, err := svc.VerifyOfficialOpenAIModels(context.Background(), &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey}, nil)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, UpstreamModelSyncErrorUnsupported, syncErr.Kind)
	require.Empty(t, upstream.requests)
}

func TestOfficialOpenAIModelsVerificationRejectsFallbackAndUnsafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "unsupported endpoint", status: http.StatusNotFound, body: `{"error":"secret-upstream-token"}`},
		{name: "method unsupported", status: http.StatusMethodNotAllowed, body: `secret-upstream-token`},
		{name: "auth error", status: http.StatusUnauthorized, body: `secret-upstream-token`},
		{name: "invalid JSON", status: http.StatusOK, body: `secret-upstream-token`},
		{name: "empty models", status: http.StatusOK, body: `{"models":[]}`},
		{name: "display name only", status: http.StatusOK, body: `{"models":[{"name":"gpt-6.1-terra","display_name":"GPT-6.1 Terra"}]}`},
		{name: "wrong envelope", status: http.StatusOK, body: `{"data":[{"id":"gpt-6.1-terra"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, upstream := newOfficialModelsTestService(tc.status, tc.body)
			account := officialModelsOAuthTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"gpt-6.1-terra": "gpt-6.1-terra"}
			result, err := svc.VerifyOfficialOpenAIModels(context.Background(), account, nil)
			require.Nil(t, result)
			var syncErr *UpstreamModelSyncError
			require.ErrorAs(t, err, &syncErr)
			require.Equal(t, UpstreamModelSyncErrorUpstream, syncErr.Kind)
			require.NotContains(t, syncErr.SafeMessage(), "secret-upstream-token")
			require.NotContains(t, syncErr.SafeMessage(), "official-verification-secret")
			require.Len(t, upstream.requests, 1)
		})
	}
	svc, upstream := newOfficialModelsTestService(http.StatusOK, "")
	upstream.err = errors.New("network failed with secret-upstream-token")
	_, err := svc.VerifyOfficialOpenAIModels(context.Background(), officialModelsOAuthTestAccount(), nil)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.NotContains(t, syncErr.SafeMessage(), "secret-upstream-token")
}

func TestOfficialOpenAIModelsRequestRequiresExactSource(t *testing.T) {
	for _, endpoint := range []string{
		"https://relay.example.com/backend-api/codex/models?client_version=1.0.0",
		"https://chatgpt.com/other?client_version=1.0.0",
		"https://chatgpt.com/backend-api/codex/models?client_version=1.0.0&other=x",
		"https://chatgpt.com/backend-api/codex/models?client_version=1&client_version=2",
		"https://chatgpt.com/backend-api/codex/models",
	} {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		require.NoError(t, err)
		require.False(t, officialOpenAIModelsRequestMatchesSource(req.URL, officialCodexModelsURL))
	}
}

func TestOfficialOpenAIModelsVerificationDisablesRedirects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account *Account
		body    string
	}{
		{
			name:    "OAuth",
			account: officialModelsOAuthTestAccount(),
			body:    `{"models":[{"slug":"gpt-6.1-terra"}]}`,
		},
		{
			name: "API key",
			account: &Account{
				ID: 92, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "official-verification-secret"},
			},
			body: `{"data":[{"id":"gpt-6.1-terra"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []int{
				http.StatusOK, http.StatusMovedPermanently, http.StatusFound,
				http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
			} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					svc, upstream := newOfficialModelsTestService(status, tc.body)
					upstream.resp.Header.Set("Location", "https://relay.example.com/models")
					repo := &upstreamModelMetadataRepoStub{}
					svc.accountRepo = repo
					result, err := svc.VerifyOfficialOpenAIModels(context.Background(), tc.account, nil)
					require.NotNil(t, upstream.lastReq)
					require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()), "official verification must not follow redirects")
					require.Len(t, upstream.requests, 1)
					require.Nil(t, repo.updates)
					if status == http.StatusOK {
						require.NoError(t, err)
						require.Equal(t, []string{"gpt-6.1-terra"}, result.Models)
						return
					}
					require.Nil(t, result, "a redirect response must never be reported as an official catalog")
					var syncErr *UpstreamModelSyncError
					require.ErrorAs(t, err, &syncErr)
					require.Equal(t, UpstreamModelSyncErrorUpstream, syncErr.Kind)
					require.Equal(t, status, syncErr.StatusCode)
				})
			}
		})
	}
}
