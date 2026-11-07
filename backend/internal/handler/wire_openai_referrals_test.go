package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProvideOpenAIOAuthHandler_NilQuotaKeepsReferralsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := ProvideOpenAIOAuthHandler(nil, nil, nil, nil)
	router := gin.New()
	router.POST("/:id/referrals/refresh", h.RefreshReferrals)
	router.POST("/:id/referrals/invite", h.SendReferralInvite)
	for _, endpoint := range []string{"refresh", "invite"} {
		t.Run(endpoint, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/42/referrals/"+endpoint,
				strings.NewReader(`{"email":"synthetic@example.test","program_id":"codex_referral_consumer","confirmed":true}`))
			request.Header.Set("Content-Type", "application/json")
			require.NotPanics(t, func() { router.ServeHTTP(recorder, request) }, "a typed nil must not become an enabled referral capability")
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "OpenAI referral service is not enabled")
		})
	}
}
