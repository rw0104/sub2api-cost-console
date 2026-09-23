package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCCodexSigned503CoolingThroughHost(t *testing.T) {
	path := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PACKAGE")
	if path == "" || runtime.GOOS != "linux" {
		t.Skip("requires signed package and Linux child CA launcher")
	}
	archive, err := os.ReadFile(path)
	require.NoError(t, err)
	t.Logf("signed 503 host package SHA256=%x", sha256.Sum256(archive))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var calls atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(503)
		io.WriteString(w, "synthetic-original-503")
	}))
	defer origin.Close()
	root := t.TempDir()
	cfg := testPluginConfig(filepath.Join(root, "plugins"), false)
	cfg.Plugins.MaxUncompressedBytes = 256 << 20
	key := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_KEY_ID")
	cfg.Plugins.TrustedPublishers[key] = strings.TrimSpace(os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY"))
	installation, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.7"}).Install(ctx, bytes.NewReader(archive), nil)
	require.NoError(t, err)
	installation.ID = 93
	installation.State = PluginStateEnabled
	installation.ConfigEncrypted = "ENC:{}"
	installation.Bindings = []PluginBinding{{Capability: v2.CapabilityProtectionTransport, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100, AccountIDs: []int64{41, 42}, MaxConcurrency: 8, TimeoutMS: 30000}}
	process := ccodexStartWithTestCA(t, ctx, installation, root, origin.Certificate().Raw)
	process.installation = installation
	repo := &protectionMemoryRepository{extensionMemoryRepository: &extensionMemoryRepository{row: cloneExtensionInstallation(installation)}}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.2.7"})
	manager.runtimes[93] = process
	manager.publishExtensionRoutesLocked(installation, process, "")
	saved, err := manager.SaveConfig(ctx, 93, []byte(`{"enabled":true,"inject_state":false,"harvest_on_demand":false,"fail_closed":false,"direct":true}`))
	require.NoError(t, err)
	request := func(accountID int64) (*http.Response, error) {
		t.Helper()
		account := &Account{ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		body := []byte(`{"model":"gpt-6-astra","input":"synthetic","stream":true}`)
		requestCtx := withPluginProtectionOriginal(ctx, account, body)
		r, err := http.NewRequestWithContext(requestCtx, "POST", origin.URL+"/responses", bytes.NewReader(body))
		require.NoError(t, err)
		r.Header.Set("Authorization", "Bearer synthetic-quarantine-host")
		r.Header.Set("ChatGPT-Account-Id", "fixture-workspace")
		r.Header.Set("Content-Type", "application/json")
		response, handled, err := manager.RoundTripOpenAIOAuth(requestCtx, r, "", account)
		require.True(t, handled)
		return response, err
	}
	response, err := request(41)
	require.NoError(t, err)
	require.Equal(t, 503, response.StatusCode)
	require.Equal(t, "120", response.Header.Get("Retry-After"))
	require.Equal(t, "upstream", response.Header.Get("X-Sleep-State-Error-Source"))
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "synthetic-original-503", string(body))
	var document map[string]any
	require.NoError(t, json.Unmarshal(saved, &document))
	document["verification_interval_seconds"] = 10
	document["route_503_soft_cooldown_seconds"] = 0
	next, _ := json.Marshal(document)
	saved, err = manager.SaveConfig(ctx, 93, next)
	require.NoError(t, err)
	_, err = request(41)
	var cooling *PluginPreprocessError
	require.ErrorAs(t, err, &cooling)
	require.Equal(t, "all_routes_cooling", cooling.DiagnosticCode)
	require.Positive(t, cooling.RetryAfterSeconds)
	require.LessOrEqual(t, cooling.RetryAfterSeconds, 120)
	recorder := httptest.NewRecorder()
	httpContext, _ := gin.CreateTestContext(recorder)
	var gateway *OpenAIGatewayService
	terminal := gateway.handleOpenAIUpstreamTransportError(ctx, httpContext, nil, err, true)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(terminal, &failover))
	require.Equal(t, 503, recorder.Code)
	require.Equal(t, "local", recorder.Header().Get("X-Sleep-State-Error-Source"))
	require.NotEmpty(t, recorder.Header().Get("Retry-After"))
	require.Contains(t, recorder.Body.String(), "all_routes_cooling")
	require.EqualValues(t, 1, calls.Load())
	response, err = request(42)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 503, response.StatusCode)
	require.EqualValues(t, 2, calls.Load(), "one scope must not pause another")
	process.kill()
	restored := ccodexStartWithTestCA(t, ctx, installation, t.TempDir(), origin.Certificate().Raw)
	restored.installation = installation
	require.NoError(t, restored.validateAndApplyConfig(ctx, saved))
	manager.runtimes[93] = restored
	manager.publishExtensionRoutesLocked(installation, restored, "")
	_, err = request(41)
	require.ErrorAs(t, err, &cooling)
	require.Equal(t, "all_routes_cooling", cooling.DiagnosticCode)
	require.EqualValues(t, 2, calls.Load(), "restart must not dispatch or replay during cooldown")
	t.Log("upstream 503/body/Retry-After preserved; compatible save and restart preserve cooling; terminal local 503 does not fail over; account scopes isolated; no replay")
}
