package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

type headerProbeProcessReport struct {
	Schema        int    `json:"schema"`
	Mode          string `json:"mode"`
	RequestsTotal uint64 `json:"requests_total"`
	Sessions      []struct {
		AccountID      int64  `json:"account_id"`
		Model          string `json:"model"`
		HeaderPresent  bool   `json:"header_present"`
		Parsed         bool   `json:"parsed"`
		ObservedLength int    `json:"observed_length"`
		ObservedBlocks int    `json:"observed_blocks"`
		Phase          string `json:"phase"`
		Requests       uint64 `json:"requests"`
	} `json:"sessions"`
}

// TestHeaderProbePackageProcessPreservesObservations uses an explicitly supplied
// package from the separate s2plugin repository's ccodex-header-probe plugin.
// No external plugin source is copied into this host repository. The test starts
// a fresh child with {} configuration, an in-memory repository and synthetic
// requests; it never loads user configuration or sends an upstream HTTP request.
func TestHeaderProbePackageProcessPreservesObservations(t *testing.T) {
	packageDir := os.Getenv("SUB2API_TEST_HEADER_PROBE_DIR")
	if packageDir == "" {
		t.Skip("set SUB2API_TEST_HEADER_PROBE_DIR to an extracted ccodex-header-probe package")
	}
	if testing.Short() {
		t.Skip("real header-probe plugin process")
	}
	packageDir, err := filepath.Abs(packageDir)
	require.NoError(t, err)
	manifestRaw, err := os.ReadFile(filepath.Join(packageDir, "manifest.source.json"))
	require.NoError(t, err)
	var manifest PluginManifest
	require.NoError(t, json.Unmarshal(manifestRaw, &manifest))
	require.Equal(t, "local.sub2api.ccodex-header-probe", manifest.ID)
	require.Equal(t, 2, manifest.SchemaVersion)
	require.Len(t, manifest.Capabilities, 1)
	capability := manifest.Capabilities[0]
	require.Equal(t, pluginv2.CapabilityRequestHeaderProbe, capability.ID)

	binaryName := "plugin"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(packageDir, "runtimes", runtime.GOOS+"-"+runtime.GOARCH, binaryName)
	binaryRaw, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	digest := sha256.Sum256(binaryRaw)
	// The supplied package is read-only input. Run a private executable copy so
	// any plugin-relative output cannot touch the installed user's plugin files.
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	require.NoError(t, os.MkdirAll(runtimeDir, 0o700))
	binaryPath = filepath.Join(runtimeDir, binaryName)
	require.NoError(t, os.WriteFile(binaryPath, binaryRaw, 0o755))
	installation := &PluginInstallation{
		ID: 1, PluginKey: manifest.ID, Version: manifest.Version, Manifest: manifest,
		State: PluginStateEnabled, InstallPath: runtimeDir, BinaryPath: binaryPath,
		BinarySHA256: hex.EncodeToString(digest[:]),
		Bindings: []PluginBinding{{
			Capability: capability.ID, Platform: capability.Platform, AccountType: capability.AccountType,
			Enabled: true, RolloutPercent: 100,
		}},
	}
	versionRaw, err := os.ReadFile(filepath.Join("..", "..", "cmd", "server", "VERSION"))
	require.NoError(t, err)
	host := PluginHostInfo{Version: strings.TrimSpace(string(versionRaw))}
	compatibility := EvaluatePluginCompatibility(manifest, host)
	require.True(t, compatibility.Compatible, compatibility.Message)
	socketDir := filepath.Join(root, "sockets")
	require.NoError(t, os.MkdirAll(socketDir, 0o700))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process, err := startPluginRuntime(ctx, installation, 10*time.Second, socketDir)
	require.NoError(t, err)
	defer process.kill()
	require.NoError(t, process.validateAndApplyConfig(ctx, []byte(`{}`)))
	require.NoError(t, process.checkHealth(ctx))
	manager := NewPluginManager(
		&extensionMemoryRepository{row: cloneExtensionInstallation(installation)},
		pluginTokenEncryptor{}, testPluginConfig(filepath.Join(root, "plugins"), true), host,
	)
	manager.desktopUpgrade = nil
	manager.publishRuntimeLocked(installation, process)
	instanceID := process.instanceID

	readStatus := func() headerProbeProcessReport {
		t.Helper()
		// Query the real Health RPC directly so UI status-cache TTL cannot mask
		// either the newly observed request or an accidental process replacement.
		health, readErr := process.fetchStatus(ctx)
		require.NoError(t, readErr)
		require.True(t, health.Healthy)
		var report headerProbeProcessReport
		require.NoError(t, json.Unmarshal([]byte(health.StatusJson), &report))
		require.Equal(t, 1, report.Schema)
		require.Equal(t, "observe", report.Mode)
		return report
	}
	require.Zero(t, readStatus().RequestsTotal)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	requestContext := WithOpenAIForwardModel(ctx, "header-probe-fixture-model", false)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, "https://fixture.invalid/v1/responses", nil)
	require.NoError(t, err)
	raw := make([]byte, 57+16*10)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Unix()))
	header := base64.RawURLEncoding.EncodeToString(raw)
	request.Header.Set("X-Codex-Turn-State", header)
	manager.ObserveOpenAIHeaderProbe(requestContext, request, account)
	report := readStatus()
	require.EqualValues(t, 1, report.RequestsTotal)
	require.Len(t, report.Sessions, 1)
	session := report.Sessions[0]
	require.Equal(t, account.ID, session.AccountID)
	require.Equal(t, "header-probe-fixture-model", session.Model)
	require.True(t, session.HeaderPresent)
	require.True(t, session.Parsed)
	require.Equal(t, len(header), session.ObservedLength)
	require.Equal(t, 10, session.ObservedBlocks)
	require.Equal(t, "observed", session.Phase)
	require.EqualValues(t, 1, session.Requests)

	request.Header.Del("X-Codex-Turn-State")
	manager.ObserveOpenAIHeaderProbe(requestContext, request, account)
	report = readStatus()
	require.EqualValues(t, 2, report.RequestsTotal)
	require.Len(t, report.Sessions, 1)
	session = report.Sessions[0]
	require.EqualValues(t, 2, session.Requests, "a missing header is still an observed request")
	require.False(t, session.HeaderPresent)
	require.False(t, session.Parsed)
	require.Zero(t, session.ObservedLength)
	require.Zero(t, session.ObservedBlocks)
	require.Equal(t, "unobserved", session.Phase)

	for round := 0; round < pluginReadinessFailureThreshold+1; round++ {
		expireReadinessRegressionInterval(t, process)
		require.NoError(t, manager.reconcileOnce(ctx))
		waitReadinessRegressionProbe(t, process)
		require.NoError(t, manager.reconcileOnce(ctx))
		require.Same(t, process, manager.runtimes[installation.ID])
		require.Equal(t, instanceID, manager.runtimes[installation.ID].instanceID)
		require.False(t, process.client.Exited())
		process.readinessMu.Lock()
		failures, readinessErr := process.readinessFailures, process.readinessErr
		process.readinessMu.Unlock()
		require.Zero(t, failures)
		require.NoError(t, readinessErr)

		request.Header.Set("X-Codex-Turn-State", header)
		manager.ObserveOpenAIHeaderProbe(requestContext, request, account)
		report = readStatus()
		require.EqualValues(t, 3+round, report.RequestsTotal, "health sampling must preserve cumulative observations")
		require.Len(t, report.Sessions, 1)
		require.EqualValues(t, 3+round, report.Sessions[0].Requests)
		require.True(t, report.Sessions[0].HeaderPresent)
		require.Equal(t, len(header), report.Sessions[0].ObservedLength)
		require.Equal(t, 10, report.Sessions[0].ObservedBlocks)
	}
}
