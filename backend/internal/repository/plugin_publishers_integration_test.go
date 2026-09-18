//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func publisherFixture(keyID, seed string) *service.PluginPublisher {
	key := sha256.Sum256([]byte(seed))
	fingerprint := sha256.Sum256(key[:])
	return &service.PluginPublisher{KeyID: keyID, PublicKey: base64.StdEncoding.EncodeToString(key[:]), Fingerprint: "sha256:" + hex.EncodeToString(fingerprint[:])}
}

func publisherInstallation(key string) *service.PluginInstallation {
	return &service.PluginInstallation{PluginKey: key, Name: "Publisher test", Version: "1.0.0", Manifest: service.PluginManifest{SchemaVersion: 1, ID: key, Name: "Publisher test", Version: "1.0.0"}, ArtifactData: []byte("synthetic"), ArtifactPath: "/tmp/plugin.s2plugin", InstallPath: "/tmp/plugin", BinaryPath: "/tmp/plugin/runtime", BinarySHA256: strings.Repeat("a", 64), SignatureStatus: service.PluginSignatureTrusted, State: service.PluginStateDisabled}
}

func TestPluginPublisherTrustIsAtomicWithInstallation(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	prefix := "publisher-" + time.Now().Format("150405.000000000")
	publisher := publisherFixture(prefix, "first")
	plugin := publisherInstallation("example." + prefix)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE plugin_key=$1", plugin.PluginKey)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_publishers WHERE key_id=$1", prefix)
	})
	bad := *plugin
	bad.State = "invalid-state"
	_, err := repo.InstallWithPublisher(ctx, &bad, nil, publisher)
	require.Error(t, err)
	stored, err := repo.GetTrustedPublisher(ctx, prefix)
	require.NoError(t, err)
	require.Nil(t, stored, "a failed install must not leave a trust grant")
	installed, err := repo.InstallWithPublisher(ctx, plugin, nil, publisher)
	require.NoError(t, err)
	stored, err = repo.GetTrustedPublisher(ctx, prefix)
	require.NoError(t, err)
	require.Equal(t, publisher, stored)
	other := *plugin
	other.PluginKey += ".other"
	other.Manifest.ID = other.PluginKey
	_, err = repo.InstallWithPublisher(ctx, &other, nil, publisherFixture(prefix, "different"))
	require.ErrorIs(t, err, service.ErrPluginPublisherKeyChanged)
	_, err = repo.GetByKey(ctx, other.PluginKey)
	require.ErrorIs(t, err, sql.ErrNoRows)
	current, err := repo.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", current.Version)
	// Atomicity also covers a failed version switch with a new publisher ID.
	newPublisher := publisherFixture(prefix+"-upgrade", "upgrade")
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_publishers WHERE key_id=$1", newPublisher.KeyID)
	})
	stale := *current
	stale.BinarySHA256 = strings.Repeat("f", 64)
	replacement := *current
	replacement.Version = "1.0.1"
	replacement.Manifest.Version = "1.0.1"
	_, err = repo.SwapVersionWithPublisher(ctx, &stale, &replacement, newPublisher)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	stored, err = repo.GetTrustedPublisher(ctx, newPublisher.KeyID)
	require.NoError(t, err)
	require.Nil(t, stored)
}

func TestPluginPublisherConcurrentDifferentKeysCannotOverwrite(t *testing.T) {
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	prefix := "publisher-race-" + time.Now().Format("150405.000000000")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, suffix := range []string{"a", "b"} {
		plugin := publisherInstallation("example." + prefix + "." + suffix)
		t.Cleanup(func() {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_installations WHERE plugin_key=$1", plugin.PluginKey)
		})
		wg.Go(func() {
			<-start
			_, err := repo.InstallWithPublisher(ctx, plugin, nil, publisherFixture(prefix, suffix))
			results <- err
		})
	}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM sub2api_plugin_publishers WHERE key_id=$1", prefix)
	})
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, service.ErrPluginPublisherKeyChanged)
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
}
