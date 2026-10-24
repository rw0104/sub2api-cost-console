package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnreadablePluginConfigurationIsNeverDeletedByAccess(t *testing.T) {
	for _, action := range []string{"load", "test", "save", "start"} {
		t.Run(action, func(t *testing.T) {
			const oldCipher = "cipher-from-a-lost-key"
			repo := &extensionMemoryRepository{row: &PluginInstallation{
				ID: 1, BinarySHA256: "hash", ConfigEncrypted: oldCipher, State: PluginStateDisabled,
			}}
			manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}}
			var err error
			switch action {
			case "load":
				_, err = manager.GetConfig(context.Background(), 1)
			case "test":
				_, err = manager.Test(context.Background(), 1)
			case "save":
				_, err = manager.SaveConfig(context.Background(), 1, json.RawMessage("[]"))
			case "start":
				_, err = manager.prepareRuntime(context.Background(), repo.row, true)
			}
			require.Equal(t, oldCipher, repo.row.ConfigEncrypted, "access must not erase data")
			require.Error(t, err, "unreadable config must not be reported as empty defaults")
		})
	}
}

func TestPluginConfigRecoveryRejectsUnconfirmedOrRunningSnapshots(t *testing.T) {
	for _, name := range []string{"running", "bound", "stale", "readable"} {
		t.Run(name, func(t *testing.T) {
			installation := &PluginInstallation{ID: 1, State: PluginStateDisabled, ConfigEncrypted: "lost-key"}
			digest := pluginConfigDigest(installation.ConfigEncrypted)
			switch name {
			case "running":
				installation.State = PluginStateEnabled
			case "bound":
				installation.Bindings = []PluginBinding{{Enabled: true}}
			case "stale":
				digest = pluginConfigDigest("different-snapshot")
			case "readable":
				installation.ConfigEncrypted = "ENC:{}"
				digest = pluginConfigDigest(installation.ConfigEncrypted)
			}
			repo := &extensionMemoryRepository{row: installation}
			m := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}}
			original := installation.ConfigEncrypted
			_, err := m.RecoverConfig(context.Background(), 1, json.RawMessage("{}"), digest, nil)
			require.Error(t, err)
			require.Equal(t, original, repo.row.ConfigEncrypted)
		})
	}
}
