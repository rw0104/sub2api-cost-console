package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingPluginConfigRepository struct {
	*extensionMemoryRepository
	updateErr error
}

func (r *failingPluginConfigRepository) UpdateConfig(context.Context, int64, string, string) error {
	return r.updateErr
}

func TestInvalidPluginConfigIsResetBeforeRuntimeRecovery(t *testing.T) {
	repo := &extensionMemoryRepository{row: &PluginInstallation{
		ID: 1, PluginKey: "local.example.plugin", BinarySHA256: "hash",
		ConfigEncrypted: "ENC:from-old-key", LastError: "",
	}}
	m := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}}
	installation, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)

	config, err := m.recoverInvalidPluginConfig(context.Background(), installation)
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(config))
	require.Empty(t, repo.row.ConfigEncrypted)
	require.Equal(t, invalidPluginConfigNotice, installation.LastError)
}

func TestInvalidPluginConfigRecoveryKeepsBlockingErrorWhenStorageFails(t *testing.T) {
	repo := &failingPluginConfigRepository{
		extensionMemoryRepository: &extensionMemoryRepository{row: &PluginInstallation{
			ID: 1, ConfigEncrypted: "ENC:from-old-key", BinarySHA256: "hash",
		}},
		updateErr: errors.New("database unavailable"),
	}
	m := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}}
	installation, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)

	_, err = m.recoverInvalidPluginConfig(context.Background(), installation)
	require.ErrorContains(t, err, invalidPluginConfigNotice)
	require.ErrorContains(t, err, "database unavailable")
	require.Equal(t, "ENC:from-old-key", repo.row.ConfigEncrypted)
}
