package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPricingCatalogEndpointsExposeStableUnavailableReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &ChannelHandler{}
	router.GET("/channels/pricing/status", h.GetPricingStatus)
	router.POST("/channels/pricing/refresh", h.RefreshPricing)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/channels/pricing/status", nil),
		httptest.NewRequest(http.MethodPost, "/channels/pricing/refresh", nil),
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusInternalServerError, response.Code)

		var body struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, http.StatusInternalServerError, body.Code)
		require.Equal(t, "PRICING_SERVICE_UNAVAILABLE", body.Reason)
	}
}

