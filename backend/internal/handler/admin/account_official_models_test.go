package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func officialModelsHandlerAccount() service.Account {
	return service.Account{
		ID: 91, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "official-handler-secret",
			"model_mapping": map[string]any{"saved-alias": "gpt-6.1-terra"},
		},
	}
}

// officialModelsAdminService reports unknown IDs as missing; the shared stub
// fabricates a placeholder account for every ID.
type officialModelsAdminService struct{ *availableModelsAdminService }

func (s officialModelsAdminService) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	if s.account.ID != id {
		return nil, errors.New("account not found")
	}
	acc := s.account
	return &acc, nil
}

func setupOfficialModelsHandlerRouter(account service.Account, status int, body string, configured bool) (*gin.Engine, *availableModelsAdminService) {
	gin.SetMode(gin.TestMode)
	adminSvc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: account}
	upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
	var testSvc *service.AccountTestService
	if configured {
		testSvc = service.NewAccountTestService(nil, nil, nil, nil, nil, upstream,
			&config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}, nil)
	}
	handler := &AccountHandler{adminService: officialModelsAdminService{adminSvc}, accountTestService: testSvc}
	router := gin.New()
	router.POST("/api/v1/admin/accounts/:id/models/verify-official", handler.VerifyOfficialModels)
	return router, adminSvc
}

func TestVerifyOfficialModelsHandlerReturnsLiveCatalogAndPreservesSavedMapping(t *testing.T) {
	router, adminSvc := setupOfficialModelsHandlerRouter(officialModelsHandlerAccount(), http.StatusOK,
		`{"models":[{"slug":"gpt-6.1-terra","display_name":"GPT-6.1 Terra"},{"slug":"gpt-6.1-sol","display_name":"GPT-6.1 Sol"}]}`, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/91/models/verify-official",
		strings.NewReader(`{"model_mapping":{"alias":"gpt-6.1-terra","existing-remap":"not-listed"}}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var envelope struct {
		Data service.OfficialOpenAIModelVerification `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, []string{"gpt-6.1-sol", "gpt-6.1-terra"}, envelope.Data.Models)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/models", envelope.Data.Source)
	require.NotEmpty(t, envelope.Data.FetchedAt)
	require.Equal(t, []service.OfficialOpenAIModelMappingCheck{
		{From: "alias", To: "gpt-6.1-terra", Available: true},
		{From: "existing-remap", To: "not-listed", Available: false},
	}, envelope.Data.Checks)
	require.Equal(t, map[string]string{
		"alias": "gpt-6.1-terra", "existing-remap": "not-listed",
		"gpt-6.1-sol": "gpt-6.1-sol", "gpt-6.1-terra": "gpt-6.1-terra",
	}, envelope.Data.SuggestedModelMapping)
	require.Equal(t, map[string]any{"saved-alias": "gpt-6.1-terra"}, adminSvc.account.Credentials["model_mapping"])
	require.Nil(t, adminSvc.account.Extra)
	require.NotContains(t, recorder.Body.String(), "official-handler-secret")
}

func TestVerifyOfficialModelsHandlerOptionalBodyUsesSavedMapping(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		wantSaved bool
	}{
		{name: "no body", body: "", wantSaved: true},
		{name: "no mapping field", body: "{}", wantSaved: true},
		{name: "empty draft", body: `{"model_mapping":{}}`, wantSaved: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := setupOfficialModelsHandlerRouter(officialModelsHandlerAccount(), http.StatusOK,
				`{"models":[{"slug":"gpt-6.1-terra"}]}`, true)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/91/models/verify-official", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var envelope struct {
				Data service.OfficialOpenAIModelVerification `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
			if tc.wantSaved {
				require.Equal(t, []service.OfficialOpenAIModelMappingCheck{{From: "saved-alias", To: "gpt-6.1-terra", Available: true}}, envelope.Data.Checks)
			} else {
				require.Empty(t, envelope.Data.Checks)
			}
		})
	}
}

func TestVerifyOfficialModelsHandlerReportsUnverifiedFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       string
		body       string
		upstream   int
		configured bool
		wantStatus int
	}{
		{name: "invalid ID", path: "bad", body: "{}", upstream: http.StatusOK, configured: true, wantStatus: http.StatusBadRequest},
		{name: "zero ID", path: "0", body: "{}", upstream: http.StatusOK, configured: true, wantStatus: http.StatusBadRequest},
		{name: "missing account", path: "900", body: "{}", upstream: http.StatusOK, configured: true, wantStatus: http.StatusNotFound},
		{name: "invalid draft", path: "91", body: `{"model_mapping":"bad"}`, upstream: http.StatusOK, configured: true, wantStatus: http.StatusBadRequest},
		{name: "unconfigured service", path: "91", body: "{}", upstream: http.StatusOK, configured: false, wantStatus: http.StatusInternalServerError},
		{name: "no local fallback for 404", path: "91", body: "{}", upstream: http.StatusNotFound, configured: true, wantStatus: http.StatusBadGateway},
		{name: "no local fallback for 405", path: "91", body: "{}", upstream: http.StatusMethodNotAllowed, configured: true, wantStatus: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := setupOfficialModelsHandlerRouter(officialModelsHandlerAccount(), tc.upstream, `{"error":"secret-upstream-body"}`, tc.configured)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/"+tc.path+"/models/verify-official", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			require.NotContains(t, recorder.Body.String(), "secret-upstream-body")
			require.NotContains(t, recorder.Body.String(), "official-handler-secret")
			require.NotContains(t, recorder.Body.String(), "suggested_model_mapping")
		})
	}
}

func TestVerifyOfficialModelsHandlerRejectsThirdPartyAsOfficial(t *testing.T) {
	account := officialModelsHandlerAccount()
	account.Type = service.AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "relay-secret", "base_url": "https://relay.example.com/v1"}
	router, _ := setupOfficialModelsHandlerRouter(account, http.StatusOK, `{"data":[{"id":"gpt-6.1-terra"}]}`, true)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/91/models/verify-official", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "relay-secret")
}
