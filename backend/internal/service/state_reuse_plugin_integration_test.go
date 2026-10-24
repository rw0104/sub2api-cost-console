package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
)

// This is an opt-in release check in an isolated temporary directory. It does
// not access a user database, credentials, or real upstream inference endpoint.
func TestStateReuseSignedPackageProcess(t *testing.T) {
	packagePath := os.Getenv("SUB2API_STATE_REUSE_PACKAGE")
	if packagePath == "" {
		t.Skip("set SUB2API_STATE_REUSE_PACKAGE and SUB2API_STATE_REUSE_PUBLIC_KEY")
	}
	data, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	root := t.TempDir()
	cfg := testPluginConfig(filepath.Join(root, "plugins"), false)
	cfg.Plugins.MaxUncompressedBytes = 256 * 1024 * 1024
	public := strings.TrimSpace(os.Getenv("SUB2API_STATE_REUSE_PUBLIC_KEY"))
	require.NotEmpty(t, public)
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.7"})
	_, err = installer.Install(context.Background(), bytes.NewReader(data), nil)
	require.Error(t, err, "a new publisher must not be silently trusted")
	cfg.Plugins.TrustedPublishers["local-state-reuse-share-v1"] = public
	installation, err := installer.Install(context.Background(), bytes.NewReader(data), nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
	require.True(t, installation.Compatibility.Compatible)
	require.Equal(t, "1.0.14", installation.Version)
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		require.FileExists(t, filepath.Join(installation.InstallPath, "ui", name))
	}
	runtime, err := startPluginRuntime(context.Background(), installation, 15*time.Second, filepath.Join(root, "sockets"))
	require.NoError(t, err)
	defer runtime.kill()
	require.NotNil(t, runtime.transport)
	storeDir := filepath.Join(root, "state")
	raw, err := json.Marshal(map[string]any{"proxy_url": "http://127.0.0.1:1", "accounts": []int64{17}, "suspended": []int64{}, "data_dir": storeDir})
	require.NoError(t, err)
	require.NoError(t, runtime.validateAndApplyConfig(context.Background(), raw))
	require.DirExists(t, filepath.Join(storeDir, "incoming"))
	require.NoError(t, runtime.checkHealth(context.Background()))
	result, err := runtime.testConfig(context.Background(), raw)
	require.NoError(t, err)
	require.True(t, result.Success)
	invalid := []byte(`{"proxy_url":"ftp://invalid.example","accounts":[17]}`)
	require.Error(t, runtime.validateAndApplyConfig(context.Background(), invalid))
	result, err = runtime.testConfig(context.Background(), raw)
	require.NoError(t, err)
	require.True(t, result.Success)
	// Forward an invalid destination to verify the actual v2 transport service
	// rejects it locally, with no upstream request sent.
	stream, err := runtime.transport.Forward(context.Background())
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{RequestId: "state-reuse-host-check", Method: "POST", Url: "http://invalid.example/responses", AccountId: 17, Platform: "openai", AccountType: "oauth"}}}))
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}))
	require.NoError(t, stream.CloseSend())
	response, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, response.GetError())
	require.False(t, response.GetError().RequestSent)
	require.Equal(t, "invalid_upstream", response.GetError().Code)
}
