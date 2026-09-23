package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type orderedConfigClient struct {
	normalizingPluginClient
	writes int
	calls  int
	reject bool
}

func TestRouteCoolingRemainsTerminalWithRetryAfter(t *testing.T) {
	for _, code := range []string{"all_routes_cooling", "fixed_route_cooling"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		failure := &PluginPreprocessError{DiagnosticCode: code, RetryAfterSeconds: 120}
		var gateway *OpenAIGatewayService
		err := gateway.handleOpenAIUpstreamTransportError(context.Background(), ctx, nil, failure, true)
		require.Same(t, failure, err)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "120", recorder.Header().Get("Retry-After"))
		require.Equal(t, "local", recorder.Header().Get("X-Sleep-State-Error-Source"))
		require.Contains(t, recorder.Body.String(), code)
	}
}

func TestStatusReturnsStructuredEmptyReportBeforeRuntimeStarts(t *testing.T) {
	installation := &PluginInstallation{ID: 17, BinarySHA256: strings.Repeat("c", 64), ConfigEncrypted: `ENC:{"revision":7}`}
	repo := &pluginConfigRepository{installation: installation}
	manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}, runtimes: map[int64]*pluginRuntime{}}
	status, err := manager.Status(context.Background(), 17)
	require.NoError(t, err)
	require.False(t, status.Healthy)
	require.Contains(t, status.Message, "尚无运行会话")
	var envelope struct {
		CoreReport struct {
			Schema         int    `json:"schema"`
			ConfigRevision uint64 `json:"config_revision"`
			Sessions       []any  `json:"sessions"`
			Message        string `json:"message"`
		} `json:"core_report"`
	}
	require.NoError(t, json.Unmarshal([]byte(status.StatusJson), &envelope))
	require.Equal(t, 1, envelope.CoreReport.Schema)
	require.EqualValues(t, 7, envelope.CoreReport.ConfigRevision)
	require.Empty(t, envelope.CoreReport.Sessions)
	require.Contains(t, envelope.CoreReport.Message, "尚未启动")
}

func (c *orderedConfigClient) ApplyConfig(ctx context.Context, r *pluginv1.ApplyConfigRequest, opts ...grpc.CallOption) (*pluginv1.ApplyConfigResponse, error) {
	c.calls++
	if c.writes == 0 {
		return nil, errors.New("Apply occurred before persistence")
	}
	if c.reject {
		c.reject = false
		return &pluginv1.ApplyConfigResponse{Applied: false}, nil
	}
	return c.normalizingPluginClient.ApplyConfig(ctx, r, opts...)
}

type orderedConfigRepo struct {
	*pluginConfigRepository
	client *orderedConfigClient
	fail   bool
}

func (r *orderedConfigRepo) UpdateConfigCAS(_ context.Context, _ int64, next, hash, old string) error {
	if r.fail {
		return ErrPluginStateChanged
	}
	if r.installation.BinarySHA256 != hash || r.installation.ConfigEncrypted != old {
		return ErrPluginStateChanged
	}
	r.client.writes++
	r.installation.ConfigEncrypted = next
	return nil
}

func TestSaveConfigPersistenceFailureNeverAppliesProvisionalPolicy(t *testing.T) {
	installation := &PluginInstallation{ID: 9, BinarySHA256: strings.Repeat("a", 64), ConfigEncrypted: `ENC:{"revision":1}`, Manifest: PluginManifest{Capabilities: []PluginCapability{protectionTestCapability()}}}
	client := &orderedConfigClient{normalizingPluginClient: normalizingPluginClient{normalized: []byte(`{"revision":2}`), applied: []byte(`{"revision":1}`)}}
	repo := &orderedConfigRepo{pluginConfigRepository: &pluginConfigRepository{installation: installation}, client: client, fail: true}
	process := &pluginRuntime{installation: cloneExtensionInstallation(installation), api: client}
	manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}, runtimes: map[int64]*pluginRuntime{9: process}}
	_, err := manager.SaveConfig(context.Background(), 9, []byte(`{"revision":2}`))
	require.ErrorIs(t, err, ErrPluginStateChanged)
	require.Zero(t, client.calls, "failed persistence cannot destroy live state through Apply or rollback")
	require.JSONEq(t, `{"revision":1}`, string(client.applied))
	repo.fail = false
	saved, err := manager.SaveConfig(context.Background(), 9, []byte(`{"revision":2}`))
	require.NoError(t, err)
	require.Equal(t, 1, client.calls)
	require.JSONEq(t, string(saved), string(client.applied))
}

func TestSaveConfigApplyFailureRollsBackPersistedRevision(t *testing.T) {
	installation := &PluginInstallation{ID: 9, BinarySHA256: strings.Repeat("b", 64), ConfigEncrypted: `ENC:{"revision":1}`, Manifest: PluginManifest{Capabilities: []PluginCapability{protectionTestCapability()}}}
	client := &orderedConfigClient{normalizingPluginClient: normalizingPluginClient{normalized: []byte(`{"revision":2}`), applied: []byte(`{"revision":1}`)}, reject: true}
	repo := &orderedConfigRepo{pluginConfigRepository: &pluginConfigRepository{installation: installation}, client: client}
	process := &pluginRuntime{installation: cloneExtensionInstallation(installation), api: client}
	manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}, runtimes: map[int64]*pluginRuntime{9: process}}
	_, err := manager.SaveConfig(context.Background(), 9, []byte(`{"revision":2}`))
	require.Error(t, err)
	require.Equal(t, `ENC:{"revision":1}`, installation.ConfigEncrypted)
	require.JSONEq(t, `{"revision":1}`, string(client.applied))
	require.Equal(t, 2, client.writes)
}
