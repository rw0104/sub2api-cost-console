package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterAccountRoutesIncludesEconomicsAndCostLossContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Account: &adminhandler.AccountHandler{},
	}}

	registerAccountRoutes(router.Group("/api/v1/admin"), handlers, nil)

	type route struct {
		method string
		path   string
	}
	expected := []route{
		{http.MethodGet, "/api/v1/admin/accounts/economics/snapshot"},
		{http.MethodGet, "/api/v1/admin/accounts/cost-loss-states"},
		{http.MethodPost, "/api/v1/admin/accounts/:id/cost-loss/confirm"},
		{http.MethodPost, "/api/v1/admin/accounts/cost-loss-events/:event_id/refund"},
		{http.MethodPost, "/api/v1/admin/accounts/:id/cost-loss/reverse"},
	}

	routes := router.Routes()
	positions := make(map[route]int, len(routes))
	for index, registered := range routes {
		positions[route{method: registered.Method, path: registered.Path}] = index
	}
	for _, want := range expected {
		_, ok := positions[want]
		require.Truef(t, ok, "missing route %s %s", want.method, want.path)
	}

	accountByID := route{method: http.MethodGet, path: "/api/v1/admin/accounts/:id"}
	accountByIDPosition, ok := positions[accountByID]
	require.True(t, ok, "generic account lookup route is missing")
	for _, want := range expected[:2] {
		require.Lessf(t, positions[want], accountByIDPosition,
			"%s %s must be registered before %s %s", want.method, want.path, accountByID.method, accountByID.path)
	}
}
