package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseEconomicsAccountIDsAcceptsStableProviderScope(t *testing.T) {
	ids, err := parseEconomicsAccountIDs("42, 7,42")
	require.NoError(t, err)
	require.Equal(t, []int64{42, 7}, ids)
}

func TestParseEconomicsWindowRetainsExplicitOffsets(t *testing.T) {
	start, end, err := parseEconomicsWindow("2026-03-08T00:00:00-05:00", "2026-03-08T12:00:00-04:00")
	require.NoError(t, err)
	require.Equal(t, 11*time.Hour, end.Sub(start))
	_, _, err = parseEconomicsWindow("2026-09-16T00:00:00Z", "")
	require.Error(t, err)
}

func TestEconomicsHandlerRejectsInvalidWindowAndNonFiniteInputs(t *testing.T) {
	h := &AccountHandler{accountEconomics: service.NewAccountEconomicsService(nil, nil, nil)}
	for _, query := range []url.Values{
		{"window_hours": {"NaN"}}, {"window_hours": {"+Inf"}}, {"window_hours": {"0"}},
		{"cny_per_usd": {"NaN"}}, {"cny_per_usd": {"+Inf"}},
		{"start_time": {"2026-09-16T00:00:00Z"}},
		{"start_time": {"2026-09-16T01:00:00Z"}, "end_time": {"2026-09-16T00:00:00Z"}},
		{"timezone": {"invalid/zone"}},
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
		h.GetEconomicsSnapshot(ctx)
		require.Equal(t, http.StatusBadRequest, recorder.Code, query.Encode())
	}
}

func TestParseEconomicsAccountIDsRejectsInvalidValues(t *testing.T) {
	_, err := parseEconomicsAccountIDs("42,not-an-id")
	require.Error(t, err)
}
