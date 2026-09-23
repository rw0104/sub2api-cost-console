package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCCodexSignedPackageProxyProtocol is the release gate for a working proxy,
// not just a successful process handshake or TCP probe. It installs the actual
// signed package, runs its unchanged Linux executable, routes through the real
// host manager and a localhost CONNECT proxy, and observes local HTTPS/SSE.
//
// The TLS root is scoped to a test-only exec launcher because the production
// host deliberately uses SkipHostEnv. No system roots, production TLS settings,
// user credentials, remote subscriptions, or paid inference endpoints change.
func TestCCodexSignedPackageProxyProtocol(t *testing.T) {
	packagePath := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PACKAGE")
	publicKey := strings.TrimSpace(os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY"))
	keyID := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_KEY_ID")
	if packagePath == "" || publicKey == "" || keyID == "" {
		t.Skip("set the three SUB2API_CCODEX_SLEEP_STATE_PACKAGE/PUBLIC_KEY/KEY_ID release inputs")
	}
	if runtime.GOOS != "linux" {
		t.Skip("run the Linux test binary natively or in WSL; test CA injection is Linux-only")
	}
	archive, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	archiveHash := sha256.Sum256(archive)
	t.Logf("signed artifact SHA256=%x", archiveHash)

	for _, scenario := range []struct {
		name        string
		blocks      int
		strict      bool
		model       string
		formalCalls int32
	}{
		{name: "valid_state_injection_and_cache", blocks: 10, strict: true, model: "gpt-6-astra", formalCalls: 2},
		{name: "wrong_shape_compatible_fallback_preserves_proxy", blocks: 11, model: "gpt-6-astra", formalCalls: 2},
		{name: "wrong_shape_strict_is_terminal_host_policy", blocks: 11, strict: true, model: "gpt-6-astra"},
		{name: "configured_new_model_reaches_upstream", blocks: 10, strict: true, model: "new-codex-model", formalCalls: 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			token := ccodexFixtureState(scenario.blocks)
			const privatePrompt = "signed-fixture-private-user-prompt"
			payload := fmt.Sprintf(`{"model":%q,"input":[{"role":"user","content":%q}],"stream":true}`, scenario.model, privatePrompt)
			const sse = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture-response\",\"status\":\"completed\"}}\n\n"
			var probes, formal atomic.Int32
			var proxyPeers sync.Map
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_, cameThroughProxy := proxyPeers.Load(req.RemoteAddr)
				assert.True(t, cameThroughProxy, "every probe and formal request must arrive on a CONNECT-owned connection")
				body, readErr := io.ReadAll(io.LimitReader(req.Body, 1<<20))
				if !assert.NoError(t, readErr) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.Equal(t, http.MethodPost, req.Method)
				assert.Equal(t, "/backend-api/codex/responses", req.URL.Path)
				for name, value := range map[string]string{
					"Authorization":      "Bearer synthetic-signed-package-fixture",
					"ChatGPT-Account-Id": "fixture-workspace",
					"User-Agent":         "codex_fixture/1",
					"Version":            "0.154.0",
					"Originator":         "codex_cli_rs",
					"OpenAI-Beta":        "responses=experimental",
				} {
					assert.Equal(t, value, req.Header.Get(name), "critical header %s", name)
				}
				if bytes.Contains(body, []byte("Reply with OK.")) {
					probes.Add(1)
					assert.NotContains(t, string(body), privatePrompt, "probing must never replay a private user prompt")
					for _, name := range []string{"Session_id", "Conversation_id", "X-Codex-Turn-State", "X-Codex-Feature"} {
						assert.Empty(t, req.Header.Get(name), "probe copied conversation header %s", name)
					}
					var probe map[string]any
					assert.NoError(t, json.Unmarshal(body, &probe))
					assert.Equal(t, scenario.model, probe["model"])
					assert.Equal(t, true, probe["parallel_tool_calls"])
					assert.Contains(t, string(body), "reasoning.encrypted_content")
				} else {
					formal.Add(1)
					assert.Equal(t, payload, string(body), "formal body must be forwarded exactly once per request")
					assert.Equal(t, "trace=a%2Fb", req.URL.RawQuery)
					assert.Equal(t, "fixture-private-session", req.Header.Get("Session_id"))
					assert.Equal(t, "fixture-private-conversation", req.Header.Get("Conversation_id"))
					assert.Equal(t, "preserve-host-header", req.Header.Get("X-Codex-Feature"))
					if scenario.blocks == 10 {
						assert.Equal(t, token, req.Header.Get("X-Codex-Turn-State"), "qualified state must be injected")
					} else {
						assert.NotEqual(t, token, req.Header.Get("X-Codex-Turn-State"), "wrong-shape state must never be injected")
					}
				}
				w.Header().Set("X-Codex-Turn-State", token)
				w.Header().Set("Set-Cookie", "not-forwarded=1")
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, sse)
				w.(http.Flusher).Flush()
			}))
			t.Cleanup(origin.Close)
			proxyURL, connects := ccodexConnectFixture(t, origin.Listener.Addr().String(), &proxyPeers)
			root := t.TempDir()
			cfg := testPluginConfig(filepath.Join(root, "plugins"), false)
			cfg.Plugins.MaxUncompressedBytes = 256 * 1024 * 1024
			cfg.Plugins.TrustedPublishers[keyID] = publicKey
			installation, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.7"}).Install(ctx, bytes.NewReader(archive), nil)
			require.NoError(t, err)
			require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
			require.True(t, installation.Compatibility.Compatible)
			installedBinary, err := os.ReadFile(installation.BinaryPath)
			require.NoError(t, err)
			binaryHash := sha256.Sum256(installedBinary)
			require.Equal(t, installation.BinarySHA256, hex.EncodeToString(binaryHash[:]))
			t.Logf("installed version=%s runtime=linux-%s SHA256=%x", installation.Version, runtime.GOARCH, binaryHash)
			proc := ccodexStartWithTestCA(t, ctx, installation, root, origin.Certificate().Raw)
			host := newPluginHostServices(installation, func(context.Context, string, string) (pluginv2.SecretValue, error) {
				return pluginv2.SecretValue{}, fmt.Errorf("fixture has no real secrets")
			})
			proc.host = host
			t.Cleanup(host.Close)
			connector, ok := proc.extension.(pluginv2.HostConnector)
			require.True(t, ok)
			require.NoError(t, connector.AttachHost(ctx, host))
			rawConfig, err := json.Marshal(map[string]any{
				"enabled": true, "inject_state": true, "harvest_on_demand": true,
				"fail_closed": scenario.strict, "pool_enabled": true, "account_mode": "personal",
				"max_probes_per_round": 1, "state_refresh_mode": "on_demand",
				"probe_timeout_seconds": 3, "proxy_urls": []string{proxyURL},
				"models": []string{scenario.model},
			})
			require.NoError(t, err)
			require.NoError(t, proc.validateAndApplyConfig(ctx, rawConfig))
			installation.ID = 73
			installation.Bindings = []PluginBinding{{Capability: pluginv2.CapabilityProtectionTransport,
				Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true,
				RolloutPercent: 100, AccountIDs: []int64{41}, MaxConcurrency: 1, TimeoutMS: 15000}}
			manager := &PluginManager{}
			manager.publishExtensionRoutesLocked(installation, proc, "")
			account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
			require.True(t, manager.hasProtectionTransport(account))
			for i := 0; i < 2; i++ {
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin.URL+"/backend-api/codex/responses?trace=a%2Fb", strings.NewReader(payload))
				require.NoError(t, err)
				for name, value := range map[string]string{
					"Authorization": "Bearer synthetic-signed-package-fixture", "ChatGPT-Account-Id": "fixture-workspace",
					"Content-Type": "application/json", "User-Agent": "codex_fixture/1", "Version": "0.154.0",
					"Originator": "codex_cli_rs", "OpenAI-Beta": "responses=experimental",
					"Session_id": "fixture-private-session", "Conversation_id": "fixture-private-conversation",
					"X-Codex-Feature": "preserve-host-header",
				} {
					request.Header.Set(name, value)
				}
				response, handled, forwardErr := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
				require.True(t, handled, "the protection binding must actually handle the request")
				if scenario.formalCalls == 0 {
					if response != nil {
						_ = response.Body.Close()
					}
					var policyErr *PluginPreprocessError
					require.ErrorAs(t, forwardErr, &policyErr, "local state policy must be a terminal host error, never an HTTP 503 response")
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, forwardErr, &failover)
					require.Nil(t, response)
				} else {
					require.NoError(t, forwardErr)
					require.NotNil(t, response)
					responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
					closeErr := response.Body.Close()
					require.NoError(t, readErr)
					require.NoError(t, closeErr)
					require.Equal(t, http.StatusOK, response.StatusCode, "response body: %s", responseBody)
					require.Equal(t, sse, string(responseBody), "formal SSE must survive the process/RPC boundary")
					require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
					require.Equal(t, token, response.Header.Get("X-Codex-Turn-State"))
					require.Empty(t, response.Header.Get("Set-Cookie"))
				}
			}
			require.EqualValues(t, 1, probes.Load(), "one bounded harvest; cache/cooldown prevents repeated probes")
			require.Equal(t, scenario.formalCalls, formal.Load())
			require.Positive(t, connects.Load(), "requests must traverse the actual localhost HTTP CONNECT proxy")
			t.Logf("verified CONNECT=%d probe=%d formal=%d strict=%v blocks=%d", connects.Load(), probes.Load(), formal.Load(), scenario.strict, scenario.blocks)
		})
	}
}

func ccodexFixtureState(blocks int) string {
	raw := make([]byte, 57+16*blocks)
	raw[0] = 0x80
	raw[9] = 7
	binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Unix()))
	return base64.URLEncoding.EncodeToString(raw)
}

func ccodexStartWithTestCA(t *testing.T, ctx context.Context, installation *PluginInstallation, root string, certificate []byte) *pluginRuntime {
	t.Helper()
	caPath := filepath.Join(root, "fixture-ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600))
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	launcher := []byte("#!/bin/sh\nexport SSL_CERT_FILE=" + quote(caPath) + "\nexec " + quote(installation.BinaryPath) + "\n")
	launcherPath := filepath.Join(root, "test-ca-launcher")
	require.NoError(t, os.WriteFile(launcherPath, launcher, 0700))
	launcherHash := sha256.Sum256(launcher)
	// Only the transient launch record points to this verified fixture wrapper.
	// Installer signature verification and the signed binary hash were checked
	// above. The final exec preserves the real runtime's installed layout.
	launchRecord := *installation
	launchRecord.BinaryPath, launchRecord.BinarySHA256 = launcherPath, hex.EncodeToString(launcherHash[:])
	// Unix socket paths have a small OS limit; subtest names make t.TempDir long.
	socketDir, err := os.MkdirTemp("", "ccodex-rpc-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	proc, err := startPluginRuntime(ctx, &launchRecord, 15*time.Second, socketDir)
	require.NoError(t, err)
	t.Cleanup(proc.kill)
	require.NotNil(t, proc.transport)
	return proc
}

func ccodexConnectFixture(t *testing.T, target string, proxyPeers *sync.Map) (string, *atomic.Int32) {
	t.Helper()
	var connects atomic.Int32
	var mu sync.Mutex
	connections := map[net.Conn]struct{}{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect || req.Host != target {
			t.Errorf("proxy refused unexpected request method=%s target=%s", req.Method, req.Host)
			http.Error(w, "fixture only allows its own upstream", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", target, 3*time.Second)
		if err != nil {
			http.Error(w, "fixture upstream unavailable", http.StatusBadGateway)
			return
		}
		proxyPeers.Store(upstream.LocalAddr().String(), struct{}{})
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		mu.Lock()
		connections[client], connections[upstream] = struct{}{}, struct{}{}
		mu.Unlock()
		connects.Add(1)
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		mu.Lock()
		delete(connections, client)
		delete(connections, upstream)
		mu.Unlock()
	}))
	t.Cleanup(func() {
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		proxy.Close()
	})
	return proxy.URL, &connects
}
