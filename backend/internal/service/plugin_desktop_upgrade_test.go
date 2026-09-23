package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func desktopUpgradeFixture(t *testing.T) (*PluginManager, *extensionMemoryRepository) {
	t.Helper()
	t.Setenv("SUB2API_DESKTOP", "1")
	t.Setenv("SUB2API_DESKTOP_VERSION", "0.3.2")
	manifest := testExtensionManifest()
	manifest.Requires.Sub2API = ">=0.2.5 <0.2.7"
	manifest.Capabilities = []PluginCapability{protectionTestCapability()}
	cap := manifest.Capabilities[0]
	repo := &extensionMemoryRepository{row: &PluginInstallation{
		ID: 1, PluginKey: manifest.ID, Manifest: manifest, State: PluginStateEnabled,
		BinarySHA256: strings.Repeat("a", 64), ConfigEncrypted: "ENC:retained-configuration",
		Bindings: []PluginBinding{{Capability: cap.ID, Platform: cap.Platform, AccountType: cap.AccountType,
			Enabled: true, RolloutPercent: 100, AccountIDs: []int64{7}, Priority: 5}},
		UpdatedAt: time.Now(),
	}}
	m := NewPluginManager(repo, pluginTokenEncryptor{}, testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.2.7"})
	return m, repo
}

func TestDesktopUpgradeDisablesPersistedPluginBindingsBeforeRecovery(t *testing.T) {
	m, repo := desktopUpgradeFixture(t)
	// Simulate the next desktop process after the installer stopped the old core.
	err := m.reconcileOnce(context.Background())
	require.NoError(t, err)
	row, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, PluginStateDisabled, row.State)
	require.False(t, row.Bindings[0].Enabled)
	require.Equal(t, "ENC:retained-configuration", row.ConfigEncrypted)
	require.Equal(t, []int64{7}, row.Bindings[0].AccountIDs)
	require.Equal(t, 5, row.Bindings[0].Priority)
	request, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	require.NoError(t, err)
	_, handled, routeErr := m.RoundTripOpenAIOAuth(context.Background(), request, "", &Account{ID: 7, Platform: "openai", Type: "oauth"})
	require.NoError(t, routeErr)
	require.False(t, handled, "disabled plugin must not retain a blocking transport route")
}

func TestV2FailureModeRequiresV2ManifestReader(t *testing.T) {
	// Upstream's v1 capability reader rejects the screenshot's field. The current
	// fork must retain it, since dropping it would change failure behavior.
	var legacy struct {
		ID          string `json:"id"`
		Platform    string `json:"platform"`
		AccountType string `json:"account_type"`
	}
	decoder := json.NewDecoder(strings.NewReader(`{"failure_mode":"fail_closed","id":"openai.oauth.protection_transport.v1","platform":"openai","account_type":"oauth"}`))
	decoder.DisallowUnknownFields()
	require.EqualError(t, decoder.Decode(&legacy), `json: unknown field "failure_mode"`)
	manifest := testExtensionManifest()
	manifest.Requires.Sub2API = ">=0.2.5 <0.3.0"
	manifest.Capabilities = []PluginCapability{protectionTestCapability()}
	installer := NewPluginPackageInstaller(testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.2.7"})
	installed, err := installer.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, manifest, nil, "", nil)), nil)
	require.NoError(t, err)
	require.Equal(t, manifest.Capabilities[0].FailureMode, installed.Manifest.Capabilities[0].FailureMode)
	require.True(t, installed.Compatibility.Compatible)
}

func TestDesktopUpgradeDoesNotRepeatOnOrdinaryRestart(t *testing.T) {
	m, repo := desktopUpgradeFixture(t)
	require.NoError(t, m.reconcileOnce(context.Background()))
	// Model the administrator re-enabling this plugin after reviewing the new host.
	repo.row.State = PluginStateEnabled
	repo.row.Bindings[0].Enabled = true
	restarted := NewPluginManager(repo, pluginTokenEncryptor{}, m.cfg, m.hostInfo)
	rows, err := repo.List(context.Background())
	require.NoError(t, err)
	require.NoError(t, restarted.prepareDesktopPlugins(context.Background(), rows))
	require.True(t, repo.row.Bindings[0].Enabled)
	// A same-version runtime/compatibility failure must still enforce fail_closed.
	require.Error(t, restarted.reconcileOnce(context.Background()))
	require.True(t, repo.row.Bindings[0].Enabled)
	require.True(t, restarted.ShouldRouteOpenAIOAuth(&Account{ID: 7, Platform: "openai", Type: "oauth"}))
}

func TestDesktopUpgradeHandlesDesktopCoreExtensionAndRollbackTransitions(t *testing.T) {
	for _, transition := range []string{"desktop", "core", "extension", "rollback", "legacy-shell"} {
		t.Run(transition, func(t *testing.T) {
			m, repo := desktopUpgradeFixture(t)
			require.NoError(t, m.reconcileOnce(context.Background()))
			repo.row.State = PluginStateError
			repo.row.Bindings[0].Enabled = true
			host := m.hostInfo
			switch transition {
			case "desktop":
				t.Setenv("SUB2API_DESKTOP_VERSION", "0.3.3")
			case "core":
				host.Version = "0.2.8"
			case "extension":
				host.ExtensionVersion = "1.3.2"
			case "rollback":
				t.Setenv("SUB2API_DESKTOP_VERSION", "0.3.1")
			case "legacy-shell":
				t.Setenv("SUB2API_DESKTOP_VERSION", "")
			}
			next := NewPluginManager(repo, pluginTokenEncryptor{}, m.cfg, host)
			require.NoError(t, next.reconcileOnce(context.Background()))
			require.Equal(t, PluginStateDisabled, repo.row.State)
			require.False(t, repo.row.Bindings[0].Enabled)
			require.FileExists(t, next.desktopUpgrade.path)
		})
	}
}

type failingDesktopUpgradeRepository struct {
	*extensionMemoryRepository
	fail bool
}

func (r *failingDesktopUpgradeRepository) UpdateBindingsAndState(ctx context.Context, id int64, bindings []PluginBinding, state, message string, enabled *time.Time, expected, hash string) error {
	if r.fail {
		return errors.New("database unavailable")
	}
	return r.extensionMemoryRepository.UpdateBindingsAndState(ctx, id, bindings, state, message, enabled, expected, hash)
}

func TestDesktopUpgradeRetriesPersistenceFailureWithoutBypassingPolicy(t *testing.T) {
	m, repo := desktopUpgradeFixture(t)
	failing := &failingDesktopUpgradeRepository{extensionMemoryRepository: repo, fail: true}
	m.repo = failing
	require.ErrorContains(t, m.reconcileOnce(context.Background()), "database unavailable")
	require.NoFileExists(t, m.desktopUpgrade.path)
	require.True(t, repo.row.Bindings[0].Enabled)
	require.True(t, m.ShouldRouteOpenAIOAuth(&Account{ID: 7, Platform: "openai", Type: "oauth"}))
	failing.fail = false
	require.NoError(t, m.reconcileOnce(context.Background()))
	require.False(t, repo.row.Bindings[0].Enabled)
	require.False(t, m.ShouldRouteOpenAIOAuth(&Account{ID: 7, Platform: "openai", Type: "oauth"}))
}

func TestDesktopUpgradeLeavesServerBindingsAndUnreadableMarkersAlone(t *testing.T) {
	m, repo := desktopUpgradeFixture(t)
	t.Setenv("SUB2API_DESKTOP", "")
	server := NewPluginManager(repo, pluginTokenEncryptor{}, m.cfg, m.hostInfo)
	require.Nil(t, server.desktopUpgrade)
	require.Error(t, server.reconcileOnce(context.Background()))
	require.True(t, repo.row.Bindings[0].Enabled)
	// A directory at the marker path is an I/O failure, not an upgrade event.
	require.NoError(t, os.MkdirAll(m.desktopUpgrade.path, 0o700))
	require.Error(t, m.reconcileOnce(context.Background()))
	require.True(t, repo.row.Bindings[0].Enabled)
}
