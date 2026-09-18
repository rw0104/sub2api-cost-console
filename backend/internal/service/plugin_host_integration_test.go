package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestPluginHostAPIProcessIntegration(t *testing.T) { runPluginHostIntegration(t, false) }
func TestPluginHostAPIContainerIntegration(t *testing.T) {
	if os.Getenv("SUB2API_TEST_SANDBOX_CONTAINER") != "1" {
		t.Skip("requires the sandbox image")
	}
	runPluginHostIntegration(t, true)
}
func runPluginHostIntegration(t *testing.T, isolated bool) {
	if testing.Short() {
		t.Skip("real process test")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "host-aware")
	if runtime.GOOS == "windows" && !isolated {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./pkg/pluginapi/examples/host-aware")
	build.Dir = filepath.Join("..", "..")
	if isolated {
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	}
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	hash := sha256.Sum256(data)
	manifest := testExtensionManifest()
	manifest.ID = "example.host-services"
	manifest.Capabilities = []PluginCapability{hostCapability()}
	i := &PluginInstallation{ID: 1, PluginKey: manifest.ID, Version: "0.1.0", Manifest: manifest, BinaryPath: binary, BinarySHA256: hex.EncodeToString(hash[:])}
	secretDigest := sha256.Sum256([]byte("temporary-test-secret"))
	raw, _ := json.Marshal(map[string]string{"alias": "policy_key", "secret_digest": hex.EncodeToString(secretDigest[:])})
	i.ConfigEncrypted = "ENC:" + string(raw)
	repo := &extensionMemoryRepository{row: i}
	cfg := testPluginConfig(filepath.Join(root, "host"), true)
	cfg.Plugins.StartTimeoutSeconds = 30
	if isolated {
		cfg.Plugins.V2Sandbox.Mode = "container"
	}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.1.179"})
	require.NoError(t, manager.PutSecretGrant(ctx, 1, pluginv2.CapabilityRequestPreprocess, "policy_key", "temporary-test-secret", 60))
	process, err := manager.prepareRuntime(ctx, i, true)
	require.NoError(t, err)
	defer process.kill()
	require.NotNil(t, process.host)
	request := pluginv2.PreprocessRequest{Capability: pluginv2.CapabilityRequestPreprocess, Context: pluginv2.RequestContext{
		RequestID: "host-api-integration", Platform: "openai", AccountType: "oauth", Method: "POST", Path: "/v1/responses", Deadline: time.Now().Add(5 * time.Second)}}
	result, err := process.extension.Preprocess(ctx, request)
	require.NoError(t, err)
	require.Equal(t, pluginv2.DecisionPass, result.Decision)
	require.Empty(t, result.Reason, "the sample never returns the secret value")
	require.Eventually(t, func() bool { return process.host.Snapshot().EventsAccepted == 1 }, time.Second, 5*time.Millisecond)
	stats := process.host.Snapshot()
	require.EqualValues(t, 1, stats.Logs)
	require.EqualValues(t, 1, stats.Metrics["requests"])
	metadata, err := manager.ListSecretGrants(ctx, 1)
	require.NoError(t, err)
	encoded, _ := json.Marshal(metadata)
	require.NotContains(t, string(encoded), "temporary-test-secret")
	require.NotContains(t, string(encoded), "EncryptedValue")
	_, err = manager.readPluginSecret(ctx, 2, pluginv2.CapabilityRequestPreprocess, "policy_key")
	require.Error(t, err, "same alias cannot cross installation identity")
	require.NoError(t, manager.DeleteSecretGrant(ctx, 1, pluginv2.CapabilityRequestPreprocess, "policy_key"))
	request.Context.Deadline = time.Now().Add(5 * time.Second)
	result, err = process.extension.Preprocess(ctx, request)
	require.NoError(t, err)
	require.Equal(t, pluginv2.DecisionDeny, result.Decision, "revocation takes effect on the next read")
}
