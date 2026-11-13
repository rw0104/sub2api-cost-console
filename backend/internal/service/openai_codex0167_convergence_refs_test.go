package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodex0167ConvergedTurnReferencesPreserveRelationships(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"session", "full"} {
		for _, source := range []string{"both", "header-only", "body-only"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				project := func(accountID, session, turn, rootTurn, parentTurn string, raw bool) map[string]any {
					t.Helper()
					account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
						Credentials: map[string]any{"chatgpt_account_id": accountID},
						Extra:       map[string]any{codexFingerprintModeExtraKey: mode, codexFingerprintSeedExtraKey: "11111111-1111-4111-8111-111111111111"},
					}
					metadata := codex0167ReferenceMetadata(turn, rootTurn, parentTurn)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Set("api_key", &APIKey{ID: 77})
					c.Request.Header.Set("session-id", session)
					c.Request.Header.Set("thread-id", "client-thread")
					if source != "body-only" {
						c.Request.Header.Set("x-codex-turn-metadata", codex0167JSON(t, metadata))
					}
					cm := map[string]any{}
					if source != "header-only" {
						for k, v := range metadata {
							cm[k] = v
						}
						cm["x-codex-turn-metadata"] = codex0167JSON(t, metadata)
					}
					body := map[string]any{"model": "gpt-6.1-sol", "client_metadata": cm}
					applyCodexAccountIdentityClientMetadataMap(body, account, 77)
					ids := resolveCodexFingerprintIDsFromContext(c, account)
					require.NotNil(t, ids)
					if raw {
						data, changed, err := applyCodexFingerprintClientMetadataRaw([]byte(codex0167JSON(t, body)), ids)
						require.NoError(t, err)
						require.True(t, changed)
						require.NoError(t, json.Unmarshal(data, &body))
					} else {
						require.True(t, applyCodexFingerprintClientMetadata(body, ids))
					}
					headers := c.Request.Header.Clone()
					applyCodexAccountIdentityHeaders(headers, account, 77)
					applyCodexFingerprintHeaders(headers, ids)
					flat := body["client_metadata"].(map[string]any)
					var canonical map[string]any
					if source != "body-only" {
						require.NoError(t, json.Unmarshal([]byte(headers.Get("x-codex-turn-metadata")), &canonical))
						require.Equal(t, flat["turn_id"], canonical["turn_id"])
					}
					if source != "header-only" {
						var embedded map[string]any
						require.NoError(t, json.Unmarshal([]byte(flat["x-codex-turn-metadata"].(string)), &embedded))
						for _, key := range []string{"turn_id", "root_turn_id", "parent_turn_id", "context_window_id"} {
							require.Equal(t, flat[key], embedded[key], key)
							if canonical != nil {
								require.Equal(t, flat[key], canonical[key], key)
							}
						}
						canonical = embedded
					}
					require.Equal(t, ids.turnID, canonical["turn_id"])
					require.Equal(t, "read-only", canonical["sandbox"])
					require.NotEqual(t, "context-window", canonical["context_window_id"])
					require.NotEqual(t, canonical["context_window_id"], canonical["window_id"])
					require.NotEqual(t, canonical["context_window_id"], canonical["thread_id"])
					return canonical
				}
				parent := project("upstream-a", "client-session", "parent-turn", "parent-turn", "older-turn", false)
				retry := project("upstream-a", "client-session", "parent-turn", "parent-turn", "older-turn", true)
				child := project("upstream-a", "client-session", "child-turn", "parent-turn", "parent-turn", true)
				otherAccount := project("upstream-b", "client-session", "parent-turn", "parent-turn", "older-turn", false)
				otherSession := project("upstream-a", "other-session", "parent-turn", "parent-turn", "older-turn", false)
				for _, key := range []string{"turn_id", "root_turn_id", "parent_turn_id", "context_window_id"} {
					require.Equal(t, parent[key], retry[key], key)
					require.NotEqual(t, parent[key], otherAccount[key], key)
					require.NotEqual(t, parent[key], otherSession[key], key)
				}
				require.Equal(t, parent["turn_id"], parent["root_turn_id"])
				require.Equal(t, parent["turn_id"], child["root_turn_id"])
				require.Equal(t, parent["turn_id"], child["parent_turn_id"])
				require.NotEqual(t, child["turn_id"], child["parent_turn_id"])
			})
		}
	}
}

func TestCodex0167ConvergenceWithoutClientTurnRetainsGeneratedFallback(t *testing.T) {
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintModeExtraKey: "session", codexFingerprintSeedExtraKey: "11111111-1111-4111-8111-111111111111"}}
	first := resolveCodexFingerprintIDsFromRequest(account, http.Header{})
	second := resolveCodexFingerprintIDsFromRequest(account, http.Header{})
	for _, ids := range []*codexFingerprintIDs{first, second} {
		body := map[string]any{}
		initial := ids.turnID
		require.True(t, applyCodexFingerprintClientMetadata(body, ids))
		require.Equal(t, initial, body["client_metadata"].(map[string]any)["turn_id"])
	}
	require.NotEmpty(t, first.turnID)
	require.NotEqual(t, first.turnID, second.turnID)
}
