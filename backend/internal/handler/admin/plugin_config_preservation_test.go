package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type unreadableConfigRepository struct {
	service.PluginRepository
}

func (*unreadableConfigRepository) GetByID(context.Context, int64) (*service.PluginInstallation, error) {
	return &service.PluginInstallation{ID: 1, State: service.PluginStateDisabled, ConfigEncrypted: "preserved-private-cipher"}, nil
}

type wrongConfigKey struct{}

func (wrongConfigKey) Decrypt(string) (string, error) {
	return "", errors.New("synthetic-private-error")
}
func (wrongConfigKey) Encrypt(string) (string, error) { return "", errors.New("must not encrypt") }

func TestUnreadablePluginConfigAPIProvidesRecoveryWithoutWritingOrLeaking(t *testing.T) {
	m := service.NewPluginManager(&unreadableConfigRepository{}, wrongConfigKey{}, &config.Config{}, service.PluginHostInfo{})
	h := NewPluginHandler(m)
	r := gin.New()
	r.GET("/plugins/:id/config", h.GetConfig)
	r.PUT("/plugins/:id/config", h.SaveConfig)
	for _, method := range []string{"GET", "PUT"} {
		recorder := httptest.NewRecorder()
		r.ServeHTTP(recorder, httptest.NewRequest(method, "/plugins/1/config", strings.NewReader("{}")))
		require.Equal(t, http.StatusConflict, recorder.Code)
		body := recorder.Body.String()
		require.Contains(t, body, "PLUGIN_CONFIG_UNREADABLE")
		require.Contains(t, body, "config_digest")
		require.NotContains(t, body, "preserved-private-cipher")
		require.NotContains(t, body, "synthetic-private-error")
	}
}
