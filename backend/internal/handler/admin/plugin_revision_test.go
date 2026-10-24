package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type revisionConfigRepository struct{ service.PluginRepository }

func (*revisionConfigRepository) GetByID(context.Context, int64) (*service.PluginInstallation, error) {
	return &service.PluginInstallation{ID: 4, Revision: 11, ETag: service.PluginInstallationETag(4, 11), ConfigEncrypted: `{"enabled":true}`}, nil
}

type revisionConfigEncryptor struct{}

func (revisionConfigEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (revisionConfigEncryptor) Decrypt(value string) (string, error) { return value, nil }

func TestPluginExpectedRevisionAcceptsRevisionAndETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, header := range map[string][2]string{
		"explicit revision": {"X-Plugin-Revision", "9"},
		"etag":              {"If-Match", `"plugin-4-11"`},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("PUT", "/", nil)
			c.Request.Header.Set(header[0], header[1])
			revision, err := pluginExpectedRevision(c)
			require.NoError(t, err)
			if name == "etag" {
				require.EqualValues(t, 11, revision)
			} else {
				require.EqualValues(t, 9, revision)
			}
		})
	}
}

func TestPluginExpectedRevisionRejectsMalformedHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("PUT", "/", nil)
	c.Request.Header.Set("If-Match", `"plugin-4-nope"`)
	_, err := pluginExpectedRevision(c)
	require.Error(t, err)
}

func TestPluginGetConfigReturnsRevisionValidators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := service.NewPluginManager(&revisionConfigRepository{}, revisionConfigEncryptor{}, &config.Config{}, service.PluginHostInfo{})
	h := NewPluginHandler(m)
	r := gin.New()
	r.GET("/plugins/:id/config", h.GetConfig)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plugins/4/config", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, `"plugin-4-11"`, response.Header().Get("ETag"))
	require.Equal(t, "11", response.Header().Get("X-Plugin-Revision"))
}
