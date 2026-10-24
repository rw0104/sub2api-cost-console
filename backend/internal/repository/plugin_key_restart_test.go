package repository

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/securitysecret"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPluginEncryptionSurvivesNewBootstrap(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	// Config.Load produces a different provisional key each time the old host
	// starts. Exercise the real bootstrap and AES implementation, not a mock.
	first := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("11", 32)}}
	require.NoError(t, ensureBootstrapSecrets(context.Background(), client, first))
	writer, err := NewAESEncryptor(first)
	require.NoError(t, err)
	ciphertext, err := writer.Encrypt("{\"proxy_url\":\"http://127.0.0.1:10808\",\"enabled\":true}")
	require.NoError(t, err)
	second := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("22", 32)}}
	require.NoError(t, ensureBootstrapSecrets(context.Background(), client, second))
	reader, err := NewAESEncryptor(second)
	require.NoError(t, err)
	plaintext, err := reader.Decrypt(ciphertext)
	require.NoError(t, err, "a new process must decrypt the previously stored plugin config")
	require.Contains(t, plaintext, "10808")
	require.True(t, second.Totp.EncryptionKeyConfigured, "a persisted key is durable")
}

func TestPluginEncryptionPreservesExplicitKeyAndDetectsMismatch(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	ctx := context.Background()
	original := strings.Repeat("ab", 32)
	first := &config.Config{Totp: config.TotpConfig{EncryptionKey: original, EncryptionKeyConfigured: true}}
	require.NoError(t, ensureBootstrapSecrets(ctx, client, first))
	writer, err := NewAESEncryptor(first)
	require.NoError(t, err)
	ciphertext, err := writer.Encrypt("previously-configured-plugin")
	require.NoError(t, err)
	// An absent environment variable must reuse the original configured key.
	next := &config.Config{}
	require.NoError(t, ensureBootstrapSecrets(ctx, client, next))
	reader, err := NewAESEncryptor(next)
	require.NoError(t, err)
	plain, err := reader.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, "previously-configured-plugin", plain)
	conflict := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("cd", 32), EncryptionKeyConfigured: true}}
	err = ensureBootstrapSecrets(ctx, client, conflict)
	require.ErrorContains(t, err, "conflicts")
	require.NotContains(t, err.Error(), original)
	require.NotContains(t, err.Error(), conflict.Totp.EncryptionKey)
	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, original, stored.Value)
}

func TestPluginEncryptionConcurrentBootstrapUsesOneDurableKey(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	const count = 6
	cfgs := make([]*config.Config, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfgs[i] = &config.Config{}
			errs[i] = ensureBootstrapSecrets(context.Background(), client, cfgs[i])
		}(i)
	}
	wg.Wait()
	for i := range count {
		require.NoError(t, errs[i])
		require.Equal(t, cfgs[0].Totp.EncryptionKey, cfgs[i].Totp.EncryptionKey)
		require.True(t, cfgs[i].Totp.EncryptionKeyConfigured)
	}
	total, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, total)
}

func TestPluginEncryptionRejectsCorruptStoredKeyWithoutReplacingIt(t *testing.T) {
	for _, value := range []string{"short", strings.Repeat("z", 64), strings.Repeat("a", 32)} {
		t.Run(value[:5], func(t *testing.T) {
			client := newSecuritySecretTestClient(t)
			ctx := context.Background()
			_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyTOTP).SetValue(value).Save(ctx)
			require.NoError(t, err)
			cfg := &config.Config{}
			require.Error(t, ensureBootstrapSecrets(ctx, client, cfg))
			require.False(t, cfg.Totp.EncryptionKeyConfigured)
			got, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Only(ctx)
			require.NoError(t, err)
			require.Equal(t, value, got.Value)
		})
	}
}
