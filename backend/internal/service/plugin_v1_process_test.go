package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPluginV1ProcessCompatibility(t *testing.T) {
	if testing.Short() {
		t.Skip("real original-protocol process")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "transport.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./testdata/v1transport")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	raw, err := os.ReadFile(binary)
	require.NoError(t, err)
	hash := sha256.Sum256(raw)
	manifest := testPluginManifest(nil)
	installation := &PluginInstallation{ID: 1, PluginKey: manifest.ID, Version: manifest.Version, Manifest: manifest, BinaryPath: binary, BinarySHA256: hex.EncodeToString(hash[:])}
	rt, err := startPluginRuntime(ctx, installation, 10*time.Second, filepath.Join(root, "runtime"))
	require.NoError(t, err)
	defer rt.kill()
	require.NoError(t, rt.validateAndApplyConfig(ctx, []byte(`{}`)))
	require.NoError(t, rt.checkHealth(ctx))
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
	request, err := http.NewRequest("POST", "http://127.0.0.1/echo", strings.NewReader("chunked-v1-body"))
	require.NoError(t, err)
	require.True(t, rt.beginRequest())
	response, err := rt.roundTrip(ctx, request, "", account)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "chunked-v1-body", string(body))
	request, err = http.NewRequest("POST", "http://127.0.0.1/sent", strings.NewReader("payload"))
	require.NoError(t, err)
	_, err = rt.roundTrip(ctx, request, "", account)
	var sent *PluginTransportError
	require.ErrorAs(t, err, &sent)
	require.True(t, sent.RequestSent)
	callCtx, cancelCall := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelCall()
	request, err = http.NewRequestWithContext(callCtx, "POST", "http://127.0.0.1/cancel", nil)
	require.NoError(t, err)
	_, err = rt.roundTrip(callCtx, request, "", account)
	require.Error(t, err)
	require.NoError(t, rt.checkHealth(ctx))
}
