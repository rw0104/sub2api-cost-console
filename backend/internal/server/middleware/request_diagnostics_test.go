package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestdiag"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLoggerRequestDiagnosticsAreNumericAccessFieldsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sink := initMiddlewareTestLoggerWithLevel(t, "info")
	router := gin.New()
	router.Use(Logger())
	body := []byte(`{"input":"PRIVATE_RAW_TEXT"}`)
	router.POST("/v1/responses", func(c *gin.Context) {
		requestdiag.RecordRequest(c.Request.Context(), body)
		requestdiag.BeginUpstream(c.Request.Context())
		requestdiag.RecordDispatch(c.Request.Context(), 10)
		requestdiag.RecordDispatch(c.Request.Context(), 0)
		_ = c.Error(errors.New("synthetic failure"))
		c.Status(http.StatusBadGateway)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))))
	var sawAccess, sawWarning bool
	for _, event := range sink.list() {
		raw, err := json.Marshal(event.Fields)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "PRIVATE_RAW_TEXT")
		switch event.Message {
		case "http request completed":
			sawAccess = true
			require.Equal(t, "info", event.Level)
			require.EqualValues(t, len(body), event.Fields["request_body_bytes"])
			require.EqualValues(t, len(`"PRIVATE_RAW_TEXT"`), event.Fields["input_bytes"])
			require.EqualValues(t, 0, event.Fields["tools_bytes"])
			require.EqualValues(t, 0, event.Fields["instructions_bytes"])
			require.EqualValues(t, 0, event.Fields["upstream_body_bytes"])
			require.EqualValues(t, 2, event.Fields["upstream_attempts"])
		case "http request contains gin errors":
			sawWarning = true
			require.NotContains(t, event.Fields, "request_body_bytes")
			require.NotContains(t, event.Fields, "upstream_attempts")
		}
	}
	require.True(t, sawAccess)
	require.True(t, sawWarning)
}

func TestLoggerOmitsUnknownPluginCountsAndWebSocketSummary(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		t.Run(map[bool]string{false: "plugin", true: "websocket"}[websocket], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			sink := initMiddlewareTestLogger(t)
			router := gin.New()
			router.Use(Logger())
			router.GET("/v1/responses", func(c *gin.Context) {
				ctx := c.Request.Context()
				requestdiag.RecordRequest(ctx, []byte(`{}`))
				requestdiag.RecordDispatch(ctx, 10)
				requestdiag.OmitUpstream(ctx)
				requestdiag.RecordDispatch(ctx, 20)
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			if websocket {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			router.ServeHTTP(httptest.NewRecorder(), req)
			for _, event := range sink.list() {
				if event.Message != "http request completed" {
					continue
				}
				require.NotContains(t, event.Fields, "upstream_attempts")
				require.NotContains(t, event.Fields, "upstream_body_bytes")
				if websocket {
					require.NotContains(t, event.Fields, "request_body_bytes")
				} else {
					require.EqualValues(t, 2, event.Fields["request_body_bytes"])
				}
			}
		})
	}
}
