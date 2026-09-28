package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

func corsPluginHeaderContains(value, wanted string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), wanted) {
			return true
		}
	}
	return false
}

func TestCORSPluginRevisionPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	origins := []string{"http://tauri.localhost", "https://tauri.localhost", "tauri://localhost"}
	for _, origin := range origins {
		for _, revisionHeader := range []string{"If-Match", "X-Plugin-Revision"} {
			t.Run(origin+"/"+revisionHeader, func(t *testing.T) {
				router := gin.New()
				router.Use(CORS(config.CORSConfig{AllowedOrigins: origins, AllowCredentials: true}))
				request := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/plugins/4/config", nil)
				request.Header.Set("Origin", origin)
				request.Header.Set("Access-Control-Request-Method", http.MethodPut)
				request.Header.Set("Access-Control-Request-Headers", "authorization, content-type, x-admin-ui-request, "+strings.ToLower(revisionHeader))
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent {
					t.Fatalf("OPTIONS status = %d", response.Code)
				}
				if response.Header().Get("Access-Control-Allow-Origin") != origin || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
					t.Fatal("Origin or credentials policy changed")
				}
				for _, header := range []string{"Authorization", "Content-Type", "X-Admin-UI-Request", revisionHeader} {
					if !corsPluginHeaderContains(response.Header().Get("Access-Control-Allow-Headers"), header) {
						t.Errorf("OPTIONS returned 204 but missing permitted header %s; browser must block PUT", header)
					}
				}
			})
		}
	}
}

func TestCORSPluginRevisionResponseMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS(config.CORSConfig{AllowedOrigins: []string{"http://tauri.localhost"}, AllowCredentials: true}))
	router.PUT("/api/v1/admin/plugins/4/config", func(c *gin.Context) {
		c.Header("ETag", `"plugin-4-12"`)
		c.Header("X-Plugin-Revision", "12")
		c.Header("X-Plugin-Operation-ID", "synthetic-operation")
		c.JSON(http.StatusOK, gin.H{"enabled": true})
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/4/config", strings.NewReader(`{"enabled":true}`))
	request.Header.Set("Origin", "http://tauri.localhost")
	request.Header.Set("If-Match", "11")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", response.Code)
	}
	for _, header := range []string{"ETag", "X-Plugin-Revision", "X-Plugin-Operation-ID", "Server-Timing"} {
		if !corsPluginHeaderContains(response.Header().Get("Access-Control-Expose-Headers"), header) {
			t.Errorf("response metadata %s is not readable by cross-origin clients", header)
		}
	}
}

func TestCORSPluginUntrustedOriginRemainsBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, origin := range []string{"https://attacker.example", "null", ""} {
		t.Run(origin, func(t *testing.T) {
			router := gin.New()
			router.Use(CORS(config.CORSConfig{AllowedOrigins: []string{"http://tauri.localhost"}, AllowCredentials: true}))
			request := httptest.NewRequest(http.MethodOptions, "/api/v1/admin/plugins/4/config", nil)
			request.Header.Set("Origin", origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodPut)
			request.Header.Set("Access-Control-Request-Headers", "if-match")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("untrusted OPTIONS status = %d", response.Code)
			}
			for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Headers", "Access-Control-Allow-Credentials", "Access-Control-Expose-Headers"} {
				if response.Header().Get(header) != "" {
					t.Errorf("untrusted origin received %s", header)
				}
			}
		})
	}
}

func TestCORSPluginSameOriginDoesNotDropRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS(config.CORSConfig{AllowedOrigins: []string{"http://tauri.localhost"}, AllowCredentials: true}))
	router.PUT("/api/v1/admin/plugins/4/config", func(c *gin.Context) {
		if c.GetHeader("If-Match") != "11" {
			t.Error("same-origin request lost revision protection")
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:18765/api/v1/admin/plugins/4/config", nil)
	request.Header.Set("If-Match", "11")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("same-origin PUT status = %d", response.Code)
	}
}
