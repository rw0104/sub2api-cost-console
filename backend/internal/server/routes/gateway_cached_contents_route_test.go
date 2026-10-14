package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Gin panics on a conflicting route only while registering, so a compile-time
// check cannot catch it. Register the gateway and the host plugin management
// routes on one engine to prove the upstream explicit-cache endpoints coexist
// with the local plugin surface.
func TestGatewayRoutesGeminiCachedContentsAreRegisteredAlongsidePluginRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var router *gin.Engine
	require.NotPanics(t, func() {
		router = newGatewayRoutesTestRouter("gemini")
		registerPluginRoutes(
			router.Group("/api/v1/admin"),
			&handler.Handlers{Admin: &handler.AdminHandlers{Plugin: adminhandler.NewPluginHandler(nil)}},
			middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }),
		)
	}, "gateway and plugin routes must not collide")

	registered := make(map[string]struct{}, len(router.Routes()))
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	for _, expected := range []string{
		"POST /v1beta/cachedContents",
		"GET /v1beta/cachedContents",
		"GET /v1beta/cachedContents/:cacheID",
		"PATCH /v1beta/cachedContents/:cacheID",
		"DELETE /v1beta/cachedContents/:cacheID",
	} {
		_, ok := registered[expected]
		require.Truef(t, ok, "missing Gemini explicit cache route %s", expected)
	}
}
