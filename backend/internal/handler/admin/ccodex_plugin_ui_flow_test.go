package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The fixture persists the exact normalized document and implements production's
// config CAS contract. Every save below starts a real temporary plugin process.
type ccodexUIFlowRepository struct {
	service.PluginRepository
	mu           sync.Mutex
	installation *service.PluginInstallation
}

func (r *ccodexUIFlowRepository) GetByID(context.Context, int64) (*service.PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *r.installation
	return &copy, nil
}

func (r *ccodexUIFlowRepository) UpdateConfigCAS(_ context.Context, _ int64, next, binary, previous string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.installation.BinarySHA256 != binary || r.installation.ConfigEncrypted != previous {
		return errors.New("fixture config compare-and-swap conflict")
	}
	r.installation.ConfigEncrypted = next
	return nil
}

// TestCCodexPluginHostUIFlow deliberately uses the installed signed runtime and
// production handlers, not the old browser harness's fabricated status success.
// It never connects to a user database or uses a real subscription credential.
func TestCCodexPluginHostUIFlow(t *testing.T) {
	packagePath := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PACKAGE")
	if packagePath == "" {
		t.Skip("set SUB2API_CCODEX_SLEEP_STATE_PACKAGE to the signed candidate package")
	}
	archive, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	t.Logf("signed package SHA-256: %x", sha256.Sum256(archive))
	publicKey := strings.TrimSpace(os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY"))
	if publicKey == "" {
		public, err := os.ReadFile(filepath.Join(filepath.Dir(packagePath), "publisher-public-key.txt"))
		require.NoError(t, err)
		publicKey = string(bytes.TrimSpace(public))
	}
	keyID := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_KEY_ID")
	if keyID == "" {
		keyID = "local-account-protection-v1"
	}
	cfg := &config.Config{Plugins: config.PluginConfig{
		DataDir: t.TempDir(), MaxUploadBytes: 128 << 20, MaxUncompressedBytes: 256 << 20,
		StartTimeoutSeconds: 15, TrustedPublishers: map[string]string{keyID: publicKey},
	}}
	installed, err := service.NewPluginPackageInstaller(cfg, service.PluginHostInfo{Version: "0.2.7"}).Install(context.Background(), bytes.NewReader(archive), nil)
	require.NoError(t, err)
	require.Equal(t, service.PluginSignatureTrusted, installed.SignatureStatus)
	installed.ID = 1
	installed.State = service.PluginStateDisabled
	repo := &ccodexUIFlowRepository{installation: installed}
	manager := service.NewPluginManager(repo, pluginUIFixtureEncryptor{}, cfg, service.PluginHostInfo{Version: "0.2.7"})
	handler := NewPluginHandler(manager)

	// Real local proxies produce deterministic failures without reaching the
	// Internet. Target HTTP 403 classification is covered by transport tests;
	// this flow verifies that per-node errors survive the actual host bridge.
	authProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer authProxy.Close()
	failedProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failedProxy.Close()
	subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/invalid" {
			_, _ = io.WriteString(w, "this is not a proxy subscription")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "%s\n%s\n", authProxy.URL, failedProxy.URL)
	}))
	defer subscription.Close()

	router := gin.New()
	router.POST("/api/v1/admin/plugins/:id/ui-session", handler.CreateUISession)
	router.GET("/api/v1/plugin-ui/:token/*path", handler.ServeUIAsset)
	router.GET("/api/v1/admin/plugins/:id/config", handler.GetConfig)
	router.PUT("/api/v1/admin/plugins/:id/config", handler.SaveConfig)
	router.POST("/api/v1/admin/plugins/:id/test", handler.Test)
	router.GET("/api/v1/admin/plugins/:id/status", handler.Status)
	router.GET("/fixture/parent", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(ccodexUIFlowParent))
	})
	router.GET("/fixture/sources", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"subscription_url": subscription.URL, "invalid_subscription_url": subscription.URL + "/invalid", "proxy_url": authProxy.URL})
	})
	server := httptest.NewServer(router)
	defer server.Close()

	request := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, err = json.Marshal(body)
			require.NoError(t, err)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(encoded))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 40 * time.Second}
		response, err := client.Do(req)
		require.NoError(t, err)
		defer response.Body.Close()
		var result map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
		return response.StatusCode, result
	}
	configPath := "/api/v1/admin/plugins/1/config"
	code, initial := request(http.MethodPut, configPath, map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, code, initial)
	code, tested := request(http.MethodPost, "/api/v1/admin/plugins/1/test", nil)
	require.Equal(t, http.StatusOK, code, tested)
	require.Equal(t, true, tested["data"].(map[string]any)["success"])
	initial["subscriptions"] = []any{map[string]any{"url": subscription.URL}}
	initial["route_request"] = map[string]any{"operation": "discover"}
	code, discovered := request(http.MethodPut, configPath, initial)
	require.Equal(t, http.StatusOK, code, discovered)
	require.NotContains(t, discovered, "route_request", "commands must not be replayed from stored configuration")
	report, ok := discovered["route_report"].(map[string]any)
	require.True(t, ok, "normalized config must expose the route report")
	nodes, ok := report["nodes"].([]any)
	require.True(t, ok)
	require.Len(t, nodes, 2)
	selectedID := nodes[0].(map[string]any)["id"].(string)
	for _, entry := range nodes {
		node := entry.(map[string]any)
		require.Equal(t, "untested", node["status"])
		require.NotEmpty(t, node["id"])
		require.NotEmpty(t, node["protocol"])
	}
	discovered["route_request"] = map[string]any{"operation": "test"}
	code, checked := request(http.MethodPut, configPath, discovered)
	require.Equal(t, http.StatusOK, code, checked)
	checkedNodes := checked["route_report"].(map[string]any)["nodes"].([]any)
	require.Len(t, checkedNodes, 2)
	for _, entry := range checkedNodes {
		node := entry.(map[string]any)
		require.Equal(t, "unavailable", node["status"])
		require.NotEmpty(t, node["error_code"], "the host must preserve safe node diagnostic codes")
	}
	checked["route_mode"] = "fixed"
	checked["fixed_route_id"] = selectedID
	// Browser forms submit empty arrays, while JSON omitempty removes them
	// from persisted configs. Semantically equal sources must retain reports.
	checked["proxy_urls"] = []any{}
	checked["proxy_envs"] = []any{}
	code, selected := request(http.MethodPut, configPath, checked)
	require.Equal(t, http.StatusOK, code, selected)
	selectedReport, ok := selected["route_report"].(map[string]any)
	require.True(t, ok, "saving a UI-shaped selection must retain its node report")
	require.Len(t, selectedReport["nodes"], 2)
	code, loaded := request(http.MethodGet, configPath, nil)
	require.Equal(t, http.StatusOK, code, loaded)
	require.Equal(t, "fixed", loaded["route_mode"])
	require.Equal(t, selectedID, loaded["fixed_route_id"])

	// The core controls travel through the same normalized config bridge on a
	// stock host. A disabled temporary process must not manufacture a live
	// OAuth session merely because policy switches are enabled.
	loaded["enabled"], loaded["inject_state"], loaded["harvest_on_demand"] = true, true, true
	loaded["fail_closed"], loaded["pool_enabled"] = true, true
	loaded["account_mode"], loaded["state_refresh_mode"] = "team", "standby"
	loaded["max_probes_per_round"] = 8
	loaded["egress_mode"], loaded["egress_route"] = "fixed", selectedID
	loaded["max_body_bytes"], loaded["zstd_window_mib"], loaded["compact_limit_mib"] = 128<<20, 80, 96
	loaded["core_request"] = map[string]any{"operation": "status"}
	code, coreChecked := request(http.MethodPut, configPath, loaded)
	require.Equal(t, http.StatusOK, code, coreChecked)
	require.NotContains(t, coreChecked, "core_request", "core commands must not survive normalization")
	coreReport, ok := coreChecked["core_report"].(map[string]any)
	require.True(t, ok, "core status must be available through config.save on the existing host")
	require.EqualValues(t, 1, coreReport["schema"])
	require.EqualValues(t, 0, coreReport["engines"])
	require.EqualValues(t, 0, coreReport["requests_total"])
	require.Empty(t, coreReport["sessions"], "a temporary process has no OAuth credentials or live sessions")
	require.NotEmpty(t, coreReport["message"], "the empty state needs an actionable explanation")
	for key, expected := range map[string]any{
		"enabled": true, "inject_state": true, "harvest_on_demand": true, "fail_closed": true,
		"pool_enabled": true, "account_mode": "team", "state_refresh_mode": "standby",
		"egress_mode": "fixed", "egress_route": selectedID,
	} {
		require.Equal(t, expected, coreChecked[key], "core field %s did not persist", key)
	}
	for key, expected := range map[string]int{"max_probes_per_round": 8, "max_body_bytes": 128 << 20, "zstd_window_mib": 80, "compact_limit_mib": 96} {
		require.EqualValues(t, expected, coreChecked[key], "core numeric field %s did not persist", key)
	}
	assertPoolState := func(config map[string]any, want string) {
		t.Helper()
		report, ok := config["core_report"].(map[string]any)
		require.True(t, ok)
		pool, ok := report["pool"].([]any)
		require.True(t, ok)
		require.Len(t, pool, 2, "discovered nodes must remain visible in the pool snapshot")
		found := false
		for _, entry := range pool {
			node := entry.(map[string]any)
			if node["id"] == selectedID {
				found = true
				require.Equal(t, want, node["state"])
			}
		}
		require.True(t, found)
		require.Empty(t, report["sessions"])
		routes := config["route_report"].(map[string]any)["nodes"].([]any)
		require.Len(t, routes, 2, "core commands must preserve connectivity diagnostics")
		for _, entry := range routes {
			require.Equal(t, "unavailable", entry.(map[string]any)["status"])
		}
	}
	assertPoolState(coreChecked, "available")
	for _, state := range []string{"disabled", "available"} {
		coreChecked["core_request"] = map[string]any{"operation": "pool", "route_ids": []string{selectedID}, "action": state}
		code, coreChecked = request(http.MethodPut, configPath, coreChecked)
		require.Equal(t, http.StatusOK, code, coreChecked)
		require.NotContains(t, coreChecked, "core_request")
		assertPoolState(coreChecked, state)
		code, coreChecked = request(http.MethodGet, configPath, nil)
		require.Equal(t, http.StatusOK, code, coreChecked)
		coreChecked["core_request"] = map[string]any{"operation": "status"}
		code, coreChecked = request(http.MethodPut, configPath, coreChecked)
		require.Equal(t, http.StatusOK, code, coreChecked)
		assertPoolState(coreChecked, state)
	}
	coreChecked["core_request"] = map[string]any{"operation": "retry", "session_id": "no-live-session"}
	code, rejectedRetry := request(http.MethodPut, configPath, coreChecked)
	require.Equal(t, http.StatusOK, code, rejectedRetry)
	rejectedReport := rejectedRetry["core_report"].(map[string]any)
	require.Equal(t, "CORE_RETRY_REJECTED", rejectedReport["error_code"])
	require.Empty(t, rejectedReport["sessions"], "retry without credentials cannot create a ready state")
	require.NotContains(t, rejectedRetry, "core_request")
	loaded = rejectedRetry
	loaded["route_mode"] = "round_robin"
	loaded["fixed_route_id"] = ""
	loaded["subscriptions"] = []any{map[string]any{"url": subscription.URL + "/invalid"}}
	loaded["route_request"] = map[string]any{"operation": "discover"}
	code, invalid := request(http.MethodPut, configPath, loaded)
	require.Equal(t, http.StatusOK, code, invalid)
	invalidReport := invalid["route_report"].(map[string]any)
	require.NotEmpty(t, invalidReport["error_code"], "source failures must be displayed instead of becoming an opaque host RPC error")
	if liveProxy := strings.TrimSpace(os.Getenv("CCODEX_TEST_PROXY_URL")); liveProxy != "" {
		code, proxyChecked := request(http.MethodPut, configPath, map[string]any{
			"enabled": false, "proxy_urls": []string{liveProxy},
			"route_request": map[string]any{"operation": "test"},
		})
		require.Equal(t, http.StatusOK, code, "live proxy diagnostic must complete through the host")
		proxyReport, ok := proxyChecked["route_report"].(map[string]any)
		require.True(t, ok, "live proxy diagnostic returned no report")
		proxyNodes, ok := proxyReport["nodes"].([]any)
		require.True(t, ok, "live proxy diagnostic returned no node list")
		require.Equal(t, 1, len(proxyNodes), "live proxy diagnostic must contain one node")
		proxyNode := proxyNodes[0].(map[string]any)
		proxyStatus, _ := proxyNode["status"].(string)
		summary, err := json.Marshal(map[string]any{"protocol": proxyNode["protocol"], "status": proxyStatus, "http_status": proxyNode["http_status"], "error_code": proxyNode["error_code"]})
		require.NoError(t, err)
		t.Logf("live host local proxy check: %s", summary)
		require.True(t, proxyStatus == "available" || proxyStatus == "restricted", "the real local proxy did not reach the connectivity target")
	}

	// Optional live verification uses the SAME host path. Never attach a config,
	// node name, URL, or raw response to failure messages: this input can carry
	// a subscription credential, and reports can contain private node labels.
	if liveURL := strings.TrimSpace(os.Getenv("CCODEX_TEST_SUBSCRIPTION_URL")); liveURL != "" {
		liveConfig := map[string]any{
			"enabled": false, "subscriptions": []any{map[string]any{"url": liveURL}},
			"route_request": map[string]any{"operation": "discover"},
		}
		code, liveDiscovered := request(http.MethodPut, configPath, liveConfig)
		require.Equal(t, http.StatusOK, code, "live discovery must complete through the host save endpoint")
		liveReport, ok := liveDiscovered["route_report"].(map[string]any)
		require.True(t, ok, "live discovery returned no report")
		liveCode, _ := liveReport["error_code"].(string)
		require.Empty(t, liveCode, "live discovery failed with a safe report code")
		liveNodes, ok := liveReport["nodes"].([]any)
		require.True(t, ok, "live discovery returned no node list")
		require.Positive(t, len(liveNodes), "live subscription returned no nodes")
		_, commandPresent := liveDiscovered["route_request"]
		require.False(t, commandPresent, "live discovery command must not persist")
		liveDiscovered["route_request"] = map[string]any{"operation": "test"}
		code, liveChecked := request(http.MethodPut, configPath, liveDiscovered)
		require.Equal(t, http.StatusOK, code, "live node testing must return a report through the host save endpoint")
		liveTestReport, ok := liveChecked["route_report"].(map[string]any)
		require.True(t, ok, "live testing returned no report")
		liveTestNodes, ok := liveTestReport["nodes"].([]any)
		require.True(t, ok, "live testing returned no node list")
		require.Equal(t, len(liveNodes), len(liveTestNodes), "live testing must report every discovered node")
		counts := map[string]int{}
		codes := map[string]int{}
		for _, entry := range liveTestNodes {
			node, ok := entry.(map[string]any)
			require.True(t, ok, "live node report is invalid")
			status, _ := node["status"].(string)
			switch status {
			case "available", "restricted", "unavailable", "cancelled":
				counts[status]++
			default:
				t.Fatal("live testing returned a node without an explicit test outcome")
			}
			if nodeCode, _ := node["error_code"].(string); nodeCode != "" {
				codes[nodeCode]++
			}
		}
		liveTestCode, _ := liveTestReport["error_code"].(string)
		summary, err := json.Marshal(map[string]any{"nodes": len(liveTestNodes), "statuses": counts, "error_codes": codes, "report_error_code": liveTestCode})
		require.NoError(t, err)
		t.Logf("live host route check: %s", summary)
		require.Positive(t, counts["available"]+counts["restricted"], "live host route check could not reach the connectivity target")
	}

	// A browser can run the same production bridge endpoints against the signed
	// assets. The process is disabled throughout, exposing hidden assumptions
	// about background jobs surviving between config calls.
	if python := os.Getenv("SUB2API_CCODEX_UI_PYTHON"); python != "" {
		code, _ = request(http.MethodPut, configPath, map[string]any{"enabled": false})
		require.Equal(t, http.StatusOK, code)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		scriptPath := os.Getenv("SUB2API_CCODEX_UI_SCRIPT")
		if scriptPath == "" {
			scriptPath = filepath.Join("..", "..", "..", "..", "plugins", "ccodex-sleep-state", "scripts", "test-host-ui.py")
		}
		script, err := filepath.Abs(scriptPath)
		require.NoError(t, err)
		command := exec.CommandContext(ctx, python, script, "--url", server.URL)
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		t.Log(string(output))
	}
}

const ccodexUIFlowParent = `<!doctype html><meta charset="utf-8"><title>Real host plugin UI flow</title>
<style>body{margin:0}iframe{border:0;width:100%;height:900px}</style><iframe id="plugin" sandbox="allow-scripts"></iframe>
<script>
window.bridgeCalls=[]; window.ready=false;
(async()=>{
 const envelope=await fetch('/api/v1/admin/plugins/1/ui-session',{method:'POST'}).then(r=>r.json());
 const session=envelope.data, frame=document.getElementById('plugin');
 addEventListener('message',async event=>{
  const message=event.data;
  if(event.source!==frame.contentWindow||event.origin!=='null'||message?.source!=='sub2api-plugin-ui'||message.bridge_token!==session.bridge_token)return;
  if(message.type==='sub2api.plugin.ready'){window.ready=true;return;}
  if(message.type==='ui.resize'){return;}
  if(!message.request_id)return;
  window.bridgeCalls.push(message.type);
  const reply={source:'sub2api-plugin-host',bridge_token:session.bridge_token,request_id:message.request_id,ok:true};
  try{
   let path='/api/v1/admin/plugins/1/', options={};
   if(message.type==='config.load')path+='config';
   else if(message.type==='config.save'){path+='config';options={method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(message.config)};}
   else if(message.type==='config.test'){path+='test';options={method:'POST'};}
   else if(message.type==='plugin.status')path+='status';
   else throw Error('Unsupported bridge command');
   const response=await fetch(path,options), data=await response.json();
   if(!response.ok)throw Error(data.message||'Host request failed');
   if(message.type==='config.load'||message.type==='config.save')reply.config=data;
   else {reply.result=data.data;reply.ok=message.type!=='config.test'||reply.result.success;}
  }catch(error){reply.ok=false;reply.error=error.message;}
  frame.contentWindow.postMessage(reply,'*');
 });
 frame.src=session.url;
})();
</script>`
