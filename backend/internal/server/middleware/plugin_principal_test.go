//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type principalPluginRepository struct{ service.PluginRepository }

func (*principalPluginRepository) List(context.Context) ([]*service.PluginInstallation, error) {
	return []*service.PluginInstallation{{ID: 1, PluginKey: "example.scope", Version: "0.1.0", State: service.PluginStateEnabled,
		Manifest: service.PluginManifest{SchemaVersion: 2, Requires: service.PluginRequirements{Sub2API: ">=0.1.0", PluginProtocol: 2, ExtensionAPI: 1, UIBridge: 1},
			Capabilities: []service.PluginCapability{{ID: pluginv2.CapabilityRequestPreprocess, Kind: pluginv2.CapabilityKindHook, Platform: "openai", AccountType: "oauth",
				Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata}, TimeoutMS: 100, FailureMode: pluginv2.FailureModeClosed, Synchronous: true}}},
		Bindings: []service.PluginBinding{{Capability: pluginv2.CapabilityRequestPreprocess, Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 100,
			UserIDs: []int64{7}, GroupIDs: []int64{9}}}}}, nil
}
func (*principalPluginRepository) GetArtifact(context.Context, int64) ([]byte, error) {
	return nil, errors.New("test route intentionally has no runtime")
}
func TestAPIKeyAuthFreezesPluginPrincipalAgainstBodyHeadersAndFallbackGroup(t *testing.T) {
	groupID := int64(9)
	key := &service.APIKey{ID: 1, UserID: 7, Key: "sk-plugin-principal", Status: service.StatusActive, GroupID: &groupID,
		User: &service.User{ID: 7, Status: service.StatusActive}, Group: &service.Group{ID: 9, Status: service.StatusActive, Platform: service.PlatformOpenAI}}
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, value string) (*service.APIKey, error) {
		if value != key.Key {
			return nil, service.ErrAPIKeyNotFound
		}
		copy := *key
		return &copy, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple, Plugins: config.PluginConfig{DataDir: t.TempDir(), StartTimeoutSeconds: 1}}
	keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	manager := service.NewPluginManager(&principalPluginRepository{}, nil, cfg, service.PluginHostInfo{Version: "0.1.179"})
	require.NoError(t, manager.Start(context.Background()))
	defer manager.Stop()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(keys, nil, cfg)))
	router.POST("/v1/responses", func(c *gin.Context) {
		// Composite/fallback selection may replace the working group after auth.
		ctx := context.WithValue(c.Request.Context(), ctxkey.Group, &service.Group{ID: 99, Status: service.StatusActive})
		require.True(t, manager.ShouldPreprocessForRequest(ctx, &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}))
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"user":88,"group_id":99}`))
	request.Header.Set("Authorization", "Bearer "+key.Key)
	request.Header.Set("X-User-ID", "88")
	request.Header.Set("X-Group-ID", "99")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
}
