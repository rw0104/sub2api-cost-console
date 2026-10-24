package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPluginConfigRecoveryRequiresStepUp(t *testing.T) {
	router := gin.New()
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Plugin: adminhandler.NewPluginHandler(nil)}}
	called := false
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { called = true; c.AbortWithStatus(http.StatusForbidden) })
	registerPluginRoutes(router.Group("/admin"), h, stepUp)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/admin/plugins/1/config/recover", nil))
	require.True(t, called)
	require.Equal(t, http.StatusForbidden, response.Code)
}
