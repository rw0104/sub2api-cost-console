package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPluginManagementRoutesCoverFrontendOperations(t *testing.T) {
	router := gin.New()
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Plugin: adminhandler.NewPluginHandler(nil)}}
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	registerPluginRoutes(router.Group("/admin"), h, stepUp)

	routes := make(map[string]struct{}, len(router.Routes()))
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"PUT /admin/plugins/:id/routing",
		"GET /admin/plugins/:id/versions",
		"POST /admin/plugins/:id/upgrade",
		"POST /admin/plugins/:id/rollback",
	} {
		_, ok := routes[expected]
		require.Truef(t, ok, "missing plugin management route %s", expected)
	}
}
