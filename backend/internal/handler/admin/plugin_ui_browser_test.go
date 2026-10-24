package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pluginUIRepository struct {
	service.PluginRepository
	installation *service.PluginInstallation
}

func (r *pluginUIRepository) GetByID(context.Context, int64) (*service.PluginInstallation, error) {
	return r.installation, nil
}

type pluginUIFixtureEncryptor struct{}

func (pluginUIFixtureEncryptor) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (pluginUIFixtureEncryptor) Decrypt(s string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	return string(b), e
}

// Uses the actual signed package and production asset handler, never a running
// user database. Only opt-in browser tests keep the temporary listener alive.
func TestPluginUIBrowserFixture(t *testing.T) {
	fixturePath := os.Getenv("SUB2API_PLUGIN_UI_BROWSER_FIXTURE")
	if fixturePath == "" {
		t.Skip("set SUB2API_PLUGIN_UI_BROWSER_FIXTURE and SUB2API_ACCOUNT_PROTECTION_PACKAGE")
	}
	packagePath := os.Getenv("SUB2API_ACCOUNT_PROTECTION_PACKAGE")
	data, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	public, err := os.ReadFile(filepath.Join(filepath.Dir(packagePath), "publisher-public-key.txt"))
	require.NoError(t, err)
	cfg := &config.Config{Plugins: config.PluginConfig{DataDir: t.TempDir(), MaxUploadBytes: 128 << 20, MaxUncompressedBytes: 256 << 20,
		TrustedPublishers: map[string]string{"local-account-protection-v1": string(bytes.TrimSpace(public))}}}
	installed, err := service.NewPluginPackageInstaller(cfg, service.PluginHostInfo{Version: "0.2.5"}).Install(context.Background(), bytes.NewReader(data), nil)
	require.NoError(t, err)
	installed.ID = 1
	manager := service.NewPluginManager(&pluginUIRepository{installation: installed}, pluginUIFixtureEncryptor{}, cfg, service.PluginHostInfo{Version: "0.2.5"})
	h := NewPluginHandler(manager)
	router := gin.New()
	router.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
	router.POST("/api/v1/admin/plugins/:id/ui-session", h.CreateUISession)
	router.GET("/api/v1/plugin-ui/:token/*path", h.ServeUIAsset)
	router.GET("/fixture/plugin", func(c *gin.Context) { c.JSON(200, installed) })
	router.GET("/fixture/parent", func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; frame-src 'self'")
		c.Data(http.StatusOK, "text/html", []byte(`<html><body><iframe sandbox="allow-scripts" style="width:100%;height:850px"></iframe><script>window.ready=false;addEventListener('message',e=>{if(e.data?.type==='sub2api.plugin.ready')window.ready=true;});</script></body></html>`))
	})
	server := httptest.NewServer(router)
	defer server.Close()
	raw, _ := json.Marshal(map[string]string{"backend_url": server.URL, "done_file": fixturePath + ".done"})
	require.NoError(t, os.WriteFile(fixturePath, raw, 0600))
	t.Log("Browser fixture ready")
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(8 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(fixturePath + ".done"); err == nil {
				return
			}
		case <-timeout.C:
			t.Fatal("browser fixture timed out")
		}
	}
}
