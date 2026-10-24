package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A real locally built package can reproduce the release-installation failure
// without installing into the user's application or touching its database.
func TestPluginSharePackageSignatureBaseline(t *testing.T) {
	path := os.Getenv("SUB2API_SHARE_PACKAGE")
	if path == "" {
		t.Skip("set SUB2API_SHARE_PACKAGE and SUB2API_SHARE_PUBLIC_KEY")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var signer PluginSignature
	for _, file := range archive.File {
		if file.Name == pluginSignatureFilename {
			raw, readErr := readPluginZipFile(file, 64*1024)
			require.NoError(t, readErr)
			require.NoError(t, json.Unmarshal(raw, &signer))
		}
	}
	require.NotEmpty(t, signer.KeyID)
	cfg := testPluginConfig(t.TempDir(), false)
	_, err = NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.5"}).Install(context.Background(), bytes.NewReader(data), nil)
	require.ErrorContains(t, err, "不受信任")
	t.Logf("Fresh production host: %v", err)
	public := strings.TrimSpace(os.Getenv("SUB2API_SHARE_PUBLIC_KEY"))
	require.NotEmpty(t, public)
	cfg.Plugins.TrustedPublishers[signer.KeyID] = public
	installed, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.5"}).Install(context.Background(), bytes.NewReader(data), nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installed.SignatureStatus)
	t.Log("The same compiled package passes signature and file verification with the correct publisher key")
}
