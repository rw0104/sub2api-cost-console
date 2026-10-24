package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type extensionListRepository struct{ service.PluginRepository }

func (*extensionListRepository) List(context.Context) ([]*service.PluginInstallation, error) {
	return []*service.PluginInstallation{{
		ID: 1, PluginKey: "example.request-policy", Name: "Policy", Version: "0.1.0", State: service.PluginStateDisabled,
		ConfigEncrypted: "private-config", ArtifactData: []byte("private-artifact"),
		Manifest: service.PluginManifest{SchemaVersion: 2, Requires: service.PluginRequirements{Sub2API: ">=0.1.179", PluginProtocol: 2, ExtensionAPI: 1, UIBridge: 1},
			Capabilities: []service.PluginCapability{{
				ID: pluginv2.CapabilityRequestPreprocess, Kind: pluginv2.CapabilityKindHook, Platform: "openai", AccountType: "oauth",
				Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionRequestBody},
				TimeoutMS:   200, FailureMode: pluginv2.FailureModeClosed, Synchronous: true,
			}}},
	}}, nil
}
func TestPluginHandlerListsExtensionMetadataWithoutSecrets(t *testing.T) {
	manager := service.NewPluginManager(&extensionListRepository{}, nil, &config.Config{}, service.PluginHostInfo{Version: "0.1.179"})
	handler := NewPluginHandler(manager)
	router := gin.New()
	router.GET("/plugins", handler.List)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/plugins", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	for _, field := range []string{`"extension_api":1`, `"schema_version":2`, `"timeout_ms":200`, `"failure_mode":"fail_closed"`,
		`"request.body.read"`, `"compatible":true`} {
		require.Contains(t, body, field)
	}
	require.NotContains(t, body, "private-config")
	require.NotContains(t, body, "private-artifact")
}
