package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This test always builds and starts a real plugin process; no external package,
// credentials, database, or remote upstream is needed.
func TestPluginExtensionProcessIntegration(t *testing.T) {
	runPluginExtensionProcessIntegration(t, false)
}

func TestPluginExtensionContainerProcessIntegration(t *testing.T) {
	if os.Getenv("SUB2API_TEST_SANDBOX_CONTAINER") != "1" {
		t.Skip("set SUB2API_TEST_SANDBOX_CONTAINER=1 after building the sandbox image")
	}
	runPluginExtensionProcessIntegration(t, true)
}

func runPluginExtensionProcessIntegration(t *testing.T, isolated bool) {
	if testing.Short() {
		t.Skip("real plugin process build")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "preprocess")
	if runtime.GOOS == "windows" && !isolated {
		binary += ".exe"
	}
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./pkg/pluginapi/examples/preprocess")
	build.Dir = filepath.Join("..", "..")
	if isolated {
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	}
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	runtimePath := "bin/preprocess"
	if runtime.GOOS == "windows" && !isolated {
		runtimePath += ".exe"
	}
	files := map[string][]byte{runtimePath: data, "ui/index.html": []byte("<html>request policy</html>")}
	manifest := testPluginManifest(nil)
	manifest.SchemaVersion = 2
	manifest.ID = "example.request-policy"
	manifest.Requires.PluginProtocol = 2
	manifest.Requires.TransportAPI = 0
	manifest.Requires.ExtensionAPI = 1
	cap := testPreprocessCapability()
	cap.TimeoutMS = 200
	manifest.Capabilities = []PluginCapability{cap}
	runtimeKey := manifest.RuntimeKey()
	if isolated {
		runtimeKey = "linux-" + runtime.GOARCH
	}
	manifest.Runtimes = map[string]PluginRuntime{runtimeKey: {Path: runtimePath}}
	manifest.Files = map[string]string{}
	for name, data := range files {
		digest := sha256.Sum256(data)
		manifest.Files[name] = hex.EncodeToString(digest[:])
	}
	manifestRaw, err := json.Marshal(manifest)
	require.NoError(t, err)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signature, err := json.Marshal(PluginSignature{Algorithm: "ed25519", KeyID: "integration", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifestRaw))})
	require.NoError(t, err)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	writeZipEntry(t, writer, "manifest.json", manifestRaw)
	writeZipEntry(t, writer, "signature.json", signature)
	for name, data := range files {
		writeZipEntry(t, writer, name, data)
	}
	require.NoError(t, writer.Close())
	cfg := testPluginConfig(filepath.Join(root, "installed"), false)
	if isolated {
		cfg.Plugins.V2Sandbox.Mode = "container"
		cfg.Plugins.StartTimeoutSeconds = 30
	}
	cfg.Plugins.TrustedPublishers["integration"] = base64.StdEncoding.EncodeToString(public)
	installation, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"}).Install(context.Background(), bytes.NewReader(archive.Bytes()), nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
	socketDir := filepath.Join(root, "sockets")
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	proc, err := startPluginRuntimeWithSandbox(context.Background(), installation, 30*time.Second, socketDir, cfg.Plugins.V2Sandbox)
	require.NoError(t, err)
	if isolated {
		require.Equal(t, "container", proc.isolation)
	}
	defer proc.kill()
	require.NoError(t, proc.validateAndApplyConfig(context.Background(), []byte(`{"max_output_tokens":25}`)))
	require.NoError(t, proc.checkHealth(context.Background()))
	tested, err := proc.testConfig(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	require.True(t, tested.Success)
	installation.ID = 1
	installation.Bindings = []PluginBinding{{Capability: cap.ID, Platform: cap.Platform, AccountType: cap.AccountType, Enabled: true, RolloutPercent: 100}}
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	manager.publishRuntimeLocked(installation, proc)
	// Publish a v1 route as well; v2 activation must leave it intact.
	manager.route.Store(&pluginRoute{pluginID: 2, rolloutPercent: 100, unavailable: "v1"})
	upstreamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		body, readErr := io.ReadAll(r.Body)
		require.NoError(t, readErr)
		require.JSONEq(t, `{"model":"gpt-test","stream":true,"max_output_tokens":25}`, string(body))
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	request := testPreprocessRequest(t)
	request.URL.Scheme = "http"
	request.URL.Host = server.Listener.Addr().String()
	prepared, err := manager.PreprocessOpenAI(context.Background(), request, account)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(prepared)
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, 1, upstreamCalls)
	require.EqualValues(t, 2, manager.route.Load().pluginID)
	require.NoError(t, proc.validateAndApplyConfig(context.Background(), []byte(`{"deny_model":"gpt-test"}`)))
	_, err = manager.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
	require.Error(t, err)
	require.IsType(t, &PluginPreprocessError{}, err)
	require.NoError(t, proc.validateAndApplyConfig(context.Background(), []byte(`{"delay_ms":1000}`)))
	started := time.Now()
	_, err = manager.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
	require.Error(t, err)
	require.Less(t, time.Since(started), 800*time.Millisecond)
	require.Equal(t, 1, upstreamCalls, "deny and timeout must not send upstream requests")
	proc.kill()
	_, err = manager.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
	require.Error(t, err, "crashed/exited process fails closed")
	require.Zero(t, proc.inFlight.Load())

	// Exercise the actual public lifecycle with two local managers and separate
	// installation roots. The second restores the signed package from storage.
	installation.State = PluginStateDisabled
	installation.Bindings[0].Enabled = false
	repo := &extensionMemoryRepository{row: cloneExtensionInstallation(installation)}
	first := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.1.179"})
	defer first.Stop()
	enabled, err := first.Enable(context.Background(), 1, true, 100)
	require.NoError(t, err)
	require.True(t, enabled.RuntimeHealthy)
	require.True(t, first.ShouldPreprocess(account))
	secondCfg := testPluginConfig(filepath.Join(root, "second-instance"), false)
	secondCfg.Plugins.V2Sandbox = cfg.Plugins.V2Sandbox
	secondCfg.Plugins.StartTimeoutSeconds = cfg.Plugins.StartTimeoutSeconds
	secondCfg.Plugins.TrustedPublishers["integration"] = cfg.Plugins.TrustedPublishers["integration"]
	second := NewPluginManager(repo, pluginTokenEncryptor{}, secondCfg, PluginHostInfo{Version: "0.1.179"})
	defer second.Stop()
	require.NoError(t, second.reconcileOnce(context.Background()))
	require.True(t, second.ShouldPreprocess(account))
	require.NotEqual(t, first.runtimes[1].installation.BinaryPath, second.runtimes[1].installation.BinaryPath)
	_, err = first.SaveConfig(context.Background(), 1, []byte(`{"max_output_tokens":12}`))
	require.NoError(t, err)
	require.NoError(t, second.reconcileOnce(context.Background()))
	changed, err := second.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
	require.NoError(t, err)
	changedBody, err := io.ReadAll(changed.Body)
	require.NoError(t, err)
	_ = changed.Body.Close()
	require.JSONEq(t, `{"model":"gpt-test","stream":true,"max_output_tokens":12}`, string(changedBody))
	// An exited child is replaced using authoritative package and config state.
	first.runtimes[1].kill()
	require.NoError(t, first.reconcileOnce(context.Background()))
	require.NoError(t, first.runtimes[1].checkHealth(context.Background()))

	t.Run("hot upgrade and rollback", func(t *testing.T) {
		upgradeBinary := filepath.Join(root, "preprocess-upgrade.exe")
		// Give this compilation its own deadline; the initial build's budget
		// also elapsed during process, routing and reconciliation checks.
		upgradeBuildCtx, cancelUpgradeBuild := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancelUpgradeBuild()
		command := exec.CommandContext(upgradeBuildCtx, "go", "build", "-ldflags", "-X main.pluginVersion=0.1.1", "-o", upgradeBinary, "./pkg/pluginapi/examples/preprocess")
		command.Dir = filepath.Join("..", "..")
		if isolated {
			command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
		}
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		newBinary, err := os.ReadFile(upgradeBinary)
		require.NoError(t, err)
		newManifest := manifest
		newManifest.Version = "0.1.1"
		newManifest.Files = map[string]string{}
		newFiles := map[string][]byte{runtimePath: newBinary, "ui/index.html": files["ui/index.html"]}
		for name, data := range newFiles {
			digest := sha256.Sum256(data)
			newManifest.Files[name] = hex.EncodeToString(digest[:])
		}
		newRaw, err := json.Marshal(newManifest)
		require.NoError(t, err)
		sig, err := json.Marshal(PluginSignature{Algorithm: "ed25519", KeyID: "integration", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, newRaw))})
		require.NoError(t, err)
		var packageData bytes.Buffer
		zipWriter := zip.NewWriter(&packageData)
		writeZipEntry(t, zipWriter, "manifest.json", newRaw)
		writeZipEntry(t, zipWriter, "signature.json", sig)
		for name, data := range newFiles {
			writeZipEntry(t, zipWriter, name, data)
		}
		require.NoError(t, zipWriter.Close())
		previous := first.runtimes[1]
		// An in-flight old call holds the retired process alive while new calls
		// already see the candidate route.
		require.True(t, previous.beginRequest())
		upgraded, err := first.Upgrade(context.Background(), 1, bytes.NewReader(packageData.Bytes()), nil, true)
		require.NoError(t, err)
		require.Equal(t, "0.1.1", upgraded.Version)
		require.True(t, upgraded.RuntimeHealthy)
		require.NotSame(t, previous, first.runtimes[1])
		require.False(t, previous.client.Exited())
		previous.finishRequest()
		require.Eventually(t, previous.client.Exited, 3*time.Second, 20*time.Millisecond)
		oldReplica := second.runtimes[1]
		oldReplicaConfig := oldReplica.installation.ConfigEncrypted
		_, err = second.SaveConfig(context.Background(), 1, []byte(`{"max_output_tokens":14}`))
		require.NoError(t, err)
		require.Equal(t, oldReplicaConfig, oldReplica.installation.ConfigEncrypted, "lagging replica must validate against the target version without reconfiguring the old process")
		require.NoError(t, second.reconcileOnce(context.Background()))
		require.Equal(t, "0.1.1", second.runtimes[1].installation.Version)
		versions, err := first.ListVersions(context.Background(), 1)
		require.NoError(t, err)
		require.Len(t, versions, 1)
		require.Equal(t, "0.1.0", versions[0].Version)
		_, err = first.SaveConfig(context.Background(), 1, []byte(`{"max_output_tokens":9}`))
		require.NoError(t, err)
		rolledBack, err := first.Rollback(context.Background(), 1, versions[0].ID, true)
		require.NoError(t, err)
		require.Equal(t, "0.1.0", rolledBack.Version)
		require.NoError(t, second.reconcileOnce(context.Background()))
		result, err := second.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
		require.NoError(t, err)
		body, err := io.ReadAll(result.Body)
		require.NoError(t, err)
		_ = result.Body.Close()
		require.JSONEq(t, `{"model":"gpt-test","stream":true,"max_output_tokens":12}`, string(body), "rollback restores old config, not the new version's 9 token limit")
		// Tampering fails before any database or route replacement.
		active := first.runtimes[1]
		_, err = first.Upgrade(context.Background(), 1, bytes.NewReader([]byte("not a plugin package")), nil, true)
		require.Error(t, err)
		require.Same(t, active, first.runtimes[1])
		require.NoError(t, active.checkHealth(context.Background()))

		// A validly signed package with mismatching runtime identity fails during
		// candidate startup, while the old process and persisted version survive.
		newManifest.Version = "0.1.2"
		badRaw, err := json.Marshal(newManifest)
		require.NoError(t, err)
		badSignature, err := json.Marshal(PluginSignature{Algorithm: "ed25519", KeyID: "integration", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, badRaw))})
		require.NoError(t, err)
		var badPackage bytes.Buffer
		badZip := zip.NewWriter(&badPackage)
		writeZipEntry(t, badZip, "manifest.json", badRaw)
		writeZipEntry(t, badZip, "signature.json", badSignature)
		for name, data := range newFiles {
			writeZipEntry(t, badZip, name, data)
		}
		require.NoError(t, badZip.Close())
		_, err = first.Upgrade(context.Background(), 1, bytes.NewReader(badPackage.Bytes()), nil, true)
		require.ErrorContains(t, err, "目标版本验证失败")
		require.Same(t, active, first.runtimes[1])
		stored, err := repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.Equal(t, "0.1.0", stored.Version)

		// A replica that cannot start a newly selected package keeps the healthy
		// prior runtime with the same grants, until a valid target is selected.
		target := *stored
		target.BinarySHA256 = manifest.Files[runtimePath]
		if target.BinarySHA256 == active.installation.BinarySHA256 {
			target.BinarySHA256 = newManifest.Files[runtimePath]
		}
		require.True(t, first.canKeepRuntimeDuringReplacement(context.Background(), active, &target))
		target.Bindings = append([]PluginBinding(nil), stored.Bindings...)
		target.Bindings[0].Enabled = false
		require.False(t, first.canKeepRuntimeDuringReplacement(context.Background(), active, &target), "changed grants cannot retain an obsolete route")
	})

	routingRow, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	policy := PluginRoutingPolicy{Capability: cap.ID, Priority: 50, AccountIDs: []int64{account.ID}, UserIDs: []int64{7}, GroupIDs: []int64{9},
		RolloutPercent: 100, MaxConcurrency: 2, TimeoutMS: 100}
	_, err = first.SaveRouting(context.Background(), 1, []PluginRoutingPolicy{policy}, routingRow.UpdatedAt)
	require.NoError(t, err)
	require.NoError(t, second.reconcileOnce(context.Background()))
	principalCtx := WithPluginPrincipal(context.Background(), 7, 9)
	require.True(t, second.ShouldPreprocessForRequest(principalCtx, account))
	require.False(t, second.ShouldPreprocessForRequest(WithPluginPrincipal(context.Background(), 8, 9), account))
	require.False(t, second.ShouldPreprocess(account), "missing authenticated principal cannot match a user-scoped route")
	require.Equal(t, 2, second.extensionStatus(1)[0].ConcurrencyLimit)

	_, err = first.Disable(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, first.ShouldPreprocess(account))
	require.NoError(t, second.reconcileOnce(context.Background()))
	require.False(t, second.ShouldPreprocess(account))
	require.Empty(t, second.runtimes)
}
