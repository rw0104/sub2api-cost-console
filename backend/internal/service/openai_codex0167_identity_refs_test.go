package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func codex0167ReferenceMetadata(turn, rootTurn, parentTurn string) map[string]any {
	return map[string]any{
		"installation_id": "client-installation", "session_id": "client-session",
		"thread_id": "client-thread", "parent_thread_id": "parent-thread",
		"forked_from_thread_id": "parent-thread", "x-codex-parent-thread-id": "parent-thread",
		"turn_id": turn, "root_turn_id": rootTurn, "parent_turn_id": parentTurn,
		"window_id": "client-thread:0", "context_window_id": "context-window",
		"sandbox": "read-only", "history_initialization": "new",
	}
}

func codex0167JSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func TestCodex0167AccountReferencesStayRelatedAcrossTransports(t *testing.T) {
	for _, accountID := range []string{"upstream-a", "upstream-b"} {
		t.Run(accountID, func(t *testing.T) {
			account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": accountID}}
			parent := codex0167ReferenceMetadata("parent-turn", "parent-turn", "older-turn")
			parent["thread_id"] = "parent-thread"
			child := codex0167ReferenceMetadata("child-turn", "parent-turn", "parent-turn")
			project := func(metadata map[string]any, rawBody bool) map[string]any {
				t.Helper()
				headers := http.Header{}
				headers.Set("x-codex-turn-metadata", codex0167JSON(t, metadata))
				headers.Set("x-codex-parent-thread-id", "parent-thread")
				bodyMetadata := make(map[string]any, len(metadata)+1)
				for k, v := range metadata {
					bodyMetadata[k] = v
				}
				bodyMetadata["x-codex-turn-metadata"] = codex0167JSON(t, metadata)
				body := map[string]any{"model": "gpt-6.1-sol", "client_metadata": bodyMetadata}
				if rawBody {
					raw, changed, err := applyCodexAccountIdentityClientMetadataRaw([]byte(codex0167JSON(t, body)), account, 77)
					require.NoError(t, err)
					require.True(t, changed)
					require.NoError(t, json.Unmarshal(raw, &body))
				} else {
					require.True(t, applyCodexAccountIdentityClientMetadataMap(body, account, 77))
				}
				applyCodexAccountIdentityHeaders(headers, account, 77)
				var headerMetadata, embedded map[string]any
				require.NoError(t, json.Unmarshal([]byte(headers.Get("x-codex-turn-metadata")), &headerMetadata))
				flat := body["client_metadata"].(map[string]any)
				require.NoError(t, json.Unmarshal([]byte(flat["x-codex-turn-metadata"].(string)), &embedded))
				for _, key := range []string{"thread_id", "parent_thread_id", "forked_from_thread_id", "root_turn_id", "parent_turn_id", "turn_id", "context_window_id"} {
					require.NotEqual(t, metadata[key], flat[key], key)
					require.Equal(t, flat[key], embedded[key], key)
					require.Equal(t, flat[key], headerMetadata[key], key)
				}
				require.Equal(t, flat["parent_thread_id"], headers.Get("x-codex-parent-thread-id"))
				require.Equal(t, "read-only", flat["sandbox"])
				return flat
			}
			parentProjected := project(parent, false)
			childProjected := project(child, true)
			require.Equal(t, parentProjected["turn_id"], parentProjected["root_turn_id"])
			require.Equal(t, parentProjected["turn_id"], childProjected["parent_turn_id"])
			require.Equal(t, parentProjected["turn_id"], childProjected["root_turn_id"])
			require.Equal(t, parentProjected["thread_id"], childProjected["parent_thread_id"])
			require.Equal(t, parentProjected["thread_id"], childProjected["forked_from_thread_id"])
			require.NotEqual(t, childProjected["context_window_id"], childProjected["thread_id"])
			require.NotEqual(t, childProjected["context_window_id"], childProjected["window_id"])
		})
	}
}

func TestCodex0167HTTPForwardsObservedIdentityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			name := accountType
			if passthrough {
				name += "/passthrough"
			}
			t.Run(name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				for key, value := range map[string]string{"session-id": "session", "thread-id": "thread", "x-client-request-id": "request", "x-codex-parent-thread-id": "parent-thread"} {
					c.Request.Header.Set(key, value)
				}
				account := &Account{ID: 11, Platform: PlatformOpenAI, Type: accountType, Credentials: map[string]any{"chatgpt_account_id": "upstream-a"}}
				svc := &OpenAIGatewayService{}
				body := []byte(`{"model":"gpt-6.1-sol","stream":true,"input":"synthetic"}`)
				var req *http.Request
				var err error
				if passthrough {
					req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "synthetic-token")
				} else {
					req, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic-token", true, "", true)
				}
				require.NoError(t, err)
				for _, key := range []string{"session-id", "thread-id", "x-client-request-id", "x-codex-parent-thread-id"} {
					require.NotEmpty(t, req.Header.Get(key), key)
					if accountType == AccountTypeAPIKey {
						require.Equal(t, c.Request.Header.Get(key), req.Header.Get(key), key)
					} else {
						require.NotEqual(t, c.Request.Header.Get(key), req.Header.Get(key), key)
					}
				}
			})
		}
	}
}

func TestCodex0167WSForwardsParentReferenceWithMatchingMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			for key, value := range map[string]string{
				"session-id": "client-session", "thread-id": "client-thread",
				"x-client-request-id": "client-request", "x-codex-parent-thread-id": "parent-thread",
			} {
				c.Request.Header.Set(key, value)
			}
			metadata := codex0167JSON(t, codex0167ReferenceMetadata("child-turn", "parent-turn", "parent-turn"))
			account := &Account{ID: 11, Platform: PlatformOpenAI, Type: accountType, Credentials: map[string]any{"chatgpt_account_id": "upstream-a"}}
			svc := &OpenAIGatewayService{}
			headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, account, "synthetic-token",
				OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true,
				"", metadata, "client-session", "", "")
			require.NoError(t, err)
			for _, key := range []string{"session-id", "thread-id", "x-client-request-id", "x-codex-parent-thread-id"} {
				require.NotEmpty(t, headers.Get(key), key)
				if accountType == AccountTypeAPIKey {
					require.Equal(t, c.Request.Header.Get(key), headers.Get(key), key)
				} else {
					require.NotEqual(t, c.Request.Header.Get(key), headers.Get(key), key)
				}
			}
			var projected map[string]any
			require.NoError(t, json.Unmarshal([]byte(headers.Get("x-codex-turn-metadata")), &projected))
			require.Equal(t, projected["parent_thread_id"], headers.Get("x-codex-parent-thread-id"))
			require.NotEqual(t, projected["turn_id"], projected["parent_turn_id"])
			require.Equal(t, projected["root_turn_id"], projected["parent_turn_id"])
			require.Equal(t, "parent-thread", c.Request.Header.Get("x-codex-parent-thread-id"), "source request must not be rewritten")
		})
	}
}
