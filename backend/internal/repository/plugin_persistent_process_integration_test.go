//go:build integration

package repository

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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Runs against the shipped SDK example in CI and an unchanged signed shared
// package when SUB2API_TEST_SHARED_PLUGIN_PACKAGE is supplied locally.
func TestPluginPersistentKeyProcessRestartAndExplicitRecovery(t *testing.T) {
	t.Setenv("SUB2API_DESKTOP", "1")
	t.Setenv("SUB2API_DESKTOP_VERSION", "0.3.4")
	ctx := context.Background()
	root := t.TempDir()
	raw := persistentPluginPackage(t, root)
	before := sha256.Sum256(raw)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	var sig service.PluginSignature
	var manifest service.PluginManifest
	for _, entry := range zr.File {
		if entry.Name != "signature.json" && entry.Name != "manifest.json" {
			continue
		}
		file, err := entry.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(file)
		require.NoError(t, file.Close())
		require.NoError(t, err)
		if entry.Name == "signature.json" {
			require.NoError(t, json.Unmarshal(data, &sig))
		} else {
			require.NoError(t, json.Unmarshal(data, &manifest))
		}
	}
	require.NotEmpty(t, sig.PublicKey)
	cfg := &config.Config{Plugins: config.PluginConfig{
		DataDir: filepath.Join(root, "plugins"), MaxUploadBytes: 64 * 1024 * 1024, MaxUncompressedBytes: 128 * 1024 * 1024,
		StartTimeoutSeconds: 30, TrustedPublishers: map[string]string{sig.KeyID: sig.PublicKey},
	}}
	bootstrap := func() service.SecretEncryptor {
		// Simulate freshly loading config without TOTP_ENCRYPTION_KEY.
		cfg.Totp = config.TotpConfig{}
		require.NoError(t, ensureBootstrapSecrets(ctx, integrationEntClient, cfg))
		encryptor, err := NewAESEncryptor(cfg)
		require.NoError(t, err)
		return encryptor
	}
	repo := &pluginRepository{db: integrationDB}
	first := service.NewPluginManager(repo, bootstrap(), cfg, service.PluginHostInfo{Version: "0.2.7", ExtensionVersion: "1.3.3"})
	require.NoError(t, first.Start(ctx))
	t.Cleanup(first.Stop)
	installed, err := first.Install(ctx, bytes.NewReader(raw), nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM sub2api_plugin_installations WHERE id=$1", installed.ID)
	})
	payload := json.RawMessage("{\"max_output_tokens\":25}")
	replacement := json.RawMessage("{\"max_output_tokens\":12}")
	if os.Getenv("SUB2API_TEST_SHARED_PLUGIN_PACKAGE") != "" {
		payload = json.RawMessage("{\"enabled\":true,\"inject_state\":true,\"harvest_on_demand\":true,\"proxy_url\":\"http://127.0.0.1:10808\"}")
		replacement = json.RawMessage("{\"enabled\":true,\"inject_state\":true,\"harvest_on_demand\":true,\"proxy_url\":\"http://127.0.0.1:10809\"}")
	}
	saved, err := first.SaveConfig(ctx, installed.ID, payload)
	require.NoError(t, err)
	enabled, err := first.Enable(ctx, installed.ID, true, 100)
	require.NoError(t, err)
	require.True(t, enabled.RuntimeHealthy)
	first.Stop()
	t.Setenv("SUB2API_DESKTOP_VERSION", "0.3.5")
	second := service.NewPluginManager(repo, bootstrap(), cfg, service.PluginHostInfo{Version: "0.2.7", ExtensionVersion: "1.3.3"})
	require.NoError(t, second.Start(ctx))
	t.Cleanup(second.Stop)
	retained, err := second.GetConfig(ctx, installed.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(saved), string(retained))
	row, err := second.Get(ctx, installed.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateDisabled, row.State)
	enabled, err = second.Enable(ctx, installed.ID, true, 100)
	require.NoError(t, err)
	require.True(t, enabled.RuntimeHealthy)
	tested, err := second.Test(ctx, installed.ID)
	require.NoError(t, err)
	require.True(t, tested.Success)
	_, err = second.Disable(ctx, installed.ID)
	require.NoError(t, err)
	const lostCipher = "ciphertext-from-a-lost-key"
	require.NoError(t, repo.UpdateConfig(ctx, installed.ID, lostCipher, installed.BinarySHA256))
	_, err = second.GetConfig(ctx, installed.ID)
	require.Error(t, err)
	stillStored, err := repo.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	require.Equal(t, lostCipher, stillStored.ConfigEncrypted)
	digest := sha256.Sum256([]byte(lostCipher))
	_, err = second.RecoverConfig(ctx, installed.ID, json.RawMessage("{\"unknown_invalid_field\":true}"), hex.EncodeToString(digest[:]), nil)
	require.Error(t, err)
	stillStored, err = repo.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	require.Equal(t, lostCipher, stillStored.ConfigEncrypted)
	recovered, err := second.RecoverConfig(ctx, installed.ID, replacement, hex.EncodeToString(digest[:]), nil)
	require.NoError(t, err)
	var backup string
	require.NoError(t, integrationDB.QueryRow("SELECT config_encrypted FROM sub2api_plugin_config_backups WHERE plugin_id=$1", installed.ID).Scan(&backup))
	require.Equal(t, lostCipher, backup)
	enabled, err = second.Enable(ctx, installed.ID, true, 100)
	require.NoError(t, err)
	require.True(t, enabled.RuntimeHealthy)
	second.Stop()
	third := service.NewPluginManager(repo, bootstrap(), cfg, service.PluginHostInfo{Version: "0.2.7", ExtensionVersion: "1.3.3"})
	require.NoError(t, third.Start(ctx))
	defer third.Stop()
	retained, err = third.GetConfig(ctx, installed.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(recovered), string(retained))
	row, err = third.Get(ctx, installed.ID)
	require.NoError(t, err)
	require.True(t, row.RuntimeHealthy)
	require.Equal(t, service.PluginStateEnabled, row.State)
	require.Equal(t, before, sha256.Sum256(raw), "the original shared archive is never rebuilt or modified")
}

func persistentPluginPackage(t *testing.T, root string) []byte {
	t.Helper()
	if path := os.Getenv("SUB2API_TEST_SHARED_PLUGIN_PACKAGE"); path != "" {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return data
	}
	binary := filepath.Join(root, "preprocess")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./pkg/pluginapi/examples/preprocess")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	contents, err := os.ReadFile(binary)
	require.NoError(t, err)
	source, err := os.ReadFile("../../pkg/pluginapi/examples/preprocess/manifest.source.json")
	require.NoError(t, err)
	var manifest service.PluginManifest
	require.NoError(t, json.Unmarshal(source, &manifest))
	manifest.Requires.Sub2API = ">=0.1.179 <1.0.0"
	manifest.Requires.TestedSub2APIVersions = []string{"0.2.7"}
	runtimePath := "bin/plugin"
	if runtime.GOOS == "windows" {
		runtimePath += ".exe"
	}
	manifest.Runtimes = map[string]service.PluginRuntime{runtime.GOOS + "-" + runtime.GOARCH: {Path: runtimePath}}
	files := map[string][]byte{runtimePath: contents, "ui/index.html": []byte("<html>fixture</html>")}
	manifest.Files = map[string]string{}
	for path, data := range files {
		hash := sha256.Sum256(data)
		manifest.Files[path] = hex.EncodeToString(hash[:])
	}
	mraw, err := json.Marshal(manifest)
	require.NoError(t, err)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sraw, err := json.Marshal(service.PluginSignature{Algorithm: "ed25519", KeyID: "persistent-example", PublicKey: base64.StdEncoding.EncodeToString(public), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, mraw))})
	require.NoError(t, err)
	files["manifest.json"] = mraw
	files["signature.json"] = sraw
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for path, body := range files {
		f, err := writer.Create(strings.ReplaceAll(path, "\\", "/"))
		require.NoError(t, err)
		_, err = f.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return data.Bytes()
}
