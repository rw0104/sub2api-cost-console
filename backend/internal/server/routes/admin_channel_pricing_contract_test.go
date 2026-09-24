package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterChannelRoutesIncludesPricingCatalogContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Channel: adminhandler.NewChannelHandler(nil, nil, service.NewPricingService(nil, nil)),
	}}

	registerChannelRoutes(router.Group("/api/v1/admin"), handlers)

	routes := make(map[string]bool)
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes[http.MethodGet+" /api/v1/admin/channels/pricing/status"])
	require.True(t, routes[http.MethodPost+" /api/v1/admin/channels/pricing/refresh"])
	// Keep the original model synchronization endpoint alongside the status
	// contract; the cost center must not accidentally reuse it for refresh.
	require.True(t, routes[http.MethodGet+" /api/v1/admin/channels/pricing/sync-models"])
}

