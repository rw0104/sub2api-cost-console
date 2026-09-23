package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
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

	v2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The gate uses the signed, installed process and production SaveConfig/CAS,
// not a mocked plugin or a UI assertion standing in for real B qualification.
func TestCCodexSignedAccountRouteReuse(t *testing.T) {
	packagePath := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PACKAGE")
	if packagePath == "" || runtime.GOOS != "linux" {
		t.Skip("requires signed package and Linux test CA launcher")
	}
	archive, err := os.ReadFile(packagePath)
	require.NoError(t, err)
	t.Logf("account route reuse signed package SHA256=%x", sha256.Sum256(archive))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var peers sync.Map
	var bMismatch atomic.Bool
	var expectBInjection atomic.Bool
	bMismatch.Store(true)
	var countsMu sync.Mutex
	counts := map[string]int{}
	token := func(blocks int, marker byte) string {
		raw := make([]byte, 57+16*blocks)
		raw[0] = 0x80
		raw[9] = marker
		binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Unix()))
		return base64.URLEncoding.EncodeToString(raw)
	}
	aState, bState := token(10, 1), token(12, 2)
	streamStarted, releaseStream := make(chan struct{}), make(chan struct{})
	solStarted := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseStream) }) }
	defer release()
	const sse = "data: {\"type\":\"response.completed\"}\n\n"
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, proxied := peers.Load(r.RemoteAddr)
		assert.True(t, proxied, "both accounts must traverse CONNECT")
		body, readErr := io.ReadAll(r.Body)
		if !assert.NoError(t, readErr) {
			return
		}
		var input struct {
			Model string `json:"model"`
		}
		assert.NoError(t, json.Unmarshal(body, &input))
		account := "A"
		state := aState
		if r.Header.Get("Authorization") == "Bearer fixture-account-B-credential" {
			account = "B"
			state = bState
			if bMismatch.Load() {
				state = token(13, 3)
			}
		} else {
			assert.Equal(t, "Bearer fixture-account-A-credential", r.Header.Get("Authorization"))
		}
		probe := bytes.Contains(body, []byte("Reply with OK."))
		kind := "formal"
		if probe {
			kind = "probe"
			assert.Empty(t, r.Header.Get("X-Codex-Turn-State"))
			assert.NotContains(t, string(body), "private-user-message")
		}
		countsMu.Lock()
		counts[account+"/"+input.Model+"/"+kind]++
		countsMu.Unlock()
		if !probe {
			if account == "A" {
				assert.Equal(t, aState, r.Header.Get("X-Codex-Turn-State"))
			} else {
				assert.NotEqual(t, aState, r.Header.Get("X-Codex-Turn-State"))
				if expectBInjection.Load() && input.Model == "gpt-6-astra" {
					assert.Equal(t, bState, r.Header.Get("X-Codex-Turn-State"), "B must inject its own freshly harvested state")
				}
				if input.Model == "gpt-5.6-sol" {
					assert.Equal(t, bState, r.Header.Get("X-Codex-Turn-State"))
				}
			}
		}
		w.Header().Set("X-Codex-Turn-State", state)
		w.Header().Set("Content-Type", "text/event-stream")
		if !probe && bytes.Contains(body, []byte("hold-sol")) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(solStarted)
			select {
			case <-releaseStream:
			case <-r.Context().Done():
				t.Error("B's other-model stream interrupted")
				return
			}
		}
		if !probe && bytes.Contains(body, []byte("hold-stream")) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(streamStarted)
			select {
			case <-releaseStream:
			case <-r.Context().Done():
				t.Error("A stream interrupted by B configuration")
				return
			}
		}
		_, _ = io.WriteString(w, sse)
	}))
	defer func() { release(); origin.Close() }()
	proxy, connects := ccodexConnectFixture(t, origin.Listener.Addr().String(), &peers)
	root := t.TempDir()
	hostCfg := testPluginConfig(filepath.Join(root, "plugins"), false)
	hostCfg.Plugins.MaxUncompressedBytes = 256 << 20
	key := os.Getenv("SUB2API_CCODEX_SLEEP_STATE_KEY_ID")
	if key == "" {
		key = "local-account-protection-v1"
	}
	hostCfg.Plugins.TrustedPublishers[key] = strings.TrimSpace(os.Getenv("SUB2API_CCODEX_SLEEP_STATE_PUBLIC_KEY"))
	installation, err := NewPluginPackageInstaller(hostCfg, PluginHostInfo{Version: "0.2.7"}).Install(ctx, bytes.NewReader(archive), nil)
	require.NoError(t, err)
	require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
	installation.ID = 86
	installation.State = PluginStateEnabled
	installation.Bindings = []PluginBinding{{Capability: v2.CapabilityProtectionTransport, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100, AccountIDs: []int64{41, 42}, MaxConcurrency: 8, TimeoutMS: 30000}}
	installation.ConfigEncrypted = "ENC:{}"
	proc := ccodexStartWithTestCA(t, ctx, installation, root, origin.Certificate().Raw)
	// Launch wrapper is test-only. After handshake identify the already verified
	// real installation so SaveConfig keeps this live process and its sessions.
	proc.installation = installation
	repo := &protectionMemoryRepository{extensionMemoryRepository: &extensionMemoryRepository{row: cloneExtensionInstallation(installation)}}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, hostCfg, PluginHostInfo{Version: "0.2.7"})
	manager.runtimes[86] = proc
	manager.publishExtensionRoutesLocked(installation, proc, "")
	var saved map[string]any
	save := func(config map[string]any) (map[string]any, error) {
		raw, _ := json.Marshal(config)
		out, err := manager.SaveConfig(ctx, 86, raw)
		if err != nil {
			return nil, err
		}
		var next map[string]any
		err = json.Unmarshal(out, &next)
		if err == nil {
			saved = next
		}
		return next, err
	}
	load := func() map[string]any {
		raw, err := manager.GetConfig(ctx, 86)
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, json.Unmarshal(raw, &data))
		return data
	}
	command := func(op map[string]any) (map[string]any, error) {
		data := load()
		data["core_request"] = op
		return save(data)
	}
	getReport := func() map[string]any {
		result, err := manager.Status(ctx, 86)
		require.NoError(t, err)
		var envelope map[string]any
		require.NoError(t, json.Unmarshal([]byte(result.StatusJson), &envelope))
		return envelope["core_report"].(map[string]any)
	}
	_, err = save(map[string]any{"enabled": true, "inject_state": true, "harvest_on_demand": true, "fail_closed": false, "pool_enabled": true, "account_mode": "personal", "state_refresh_mode": "on_demand", "cooldown_seconds": 1, "probe_timeout_seconds": 2, "probe_round_seconds": 3, "max_probes_per_round": 1, "models": []string{"gpt-6-astra", "gpt-5.6-sol"}, "proxy_urls": []string{proxy}, "accounts": map[string]any{"42": map[string]any{"inject_state": false, "harvest_on_demand": false, "account_mode": "team"}}, "route_request": map[string]any{"operation": "discover"}})
	require.NoError(t, err)
	route := saved["route_report"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["id"].(string)
	request := func(account int64, model, message string) *http.Response {
		payload, _ := json.Marshal(map[string]any{"model": model, "input": []map[string]string{{"role": "user", "content": message}}, "stream": true})
		r, err := http.NewRequestWithContext(ctx, "POST", origin.URL+"/backend-api/codex/responses", bytes.NewReader(payload))
		require.NoError(t, err)
		label := "A"
		if account == 42 {
			label = "B"
		}
		r.Header.Set("Authorization", "Bearer fixture-account-"+label+"-credential")
		r.Header.Set("ChatGPT-Account-Id", "workspace-"+label)
		r.Header.Set("Content-Type", "application/json")
		plan := "plus"
		if account == 42 {
			plan = "team"
		}
		response, handled, err := manager.RoundTripOpenAIOAuth(ctx, r, "", &Account{ID: account, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8, Credentials: map[string]any{"plan_type": plan, "chatgpt_account_id": r.Header.Get("ChatGPT-Account-Id")}})
		require.True(t, handled)
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
		return response
	}
	finish := func(r *http.Response) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, r.Body.Close())
		require.Equal(t, sse, string(data))
	}
	if os.Getenv("SUB2API_CCODEX_REUSE_BROWSER_SCRIPT") == "" {
		initial := load()
		initial["accounts"].(map[string]any)["42"].(map[string]any)["model_route_bindings"] = map[string]any{"gpt-5.6-sol": map[string]any{"route_id": route, "enabled": true, "inject_state": true, "harvest_on_demand": true}}
		_, err = save(initial)
		require.NoError(t, err)
		bMismatch.Store(false)
		finish(request(42, "gpt-5.6-sol", "private-user-message"))
		bMismatch.Store(true)
	}
	finish(request(41, "gpt-6-astra", "private-user-message"))
	finish(request(42, "gpt-6-astra", "private-user-message"))
	report := getReport()
	var aSession, bSession string
	for _, raw := range report["sessions"].([]any) {
		s := raw.(map[string]any)
		if s["account_id"] == float64(41) {
			aSession = s["id"].(string)
		} else if s["model"] == "gpt-6-astra" {
			bSession = s["id"].(string)
		}
	}
	require.NotEmpty(t, aSession)
	require.NotEmpty(t, bSession)

	// A failed write of an incompatible policy must never apply it to the live
	// process. Read-only reports do not increment the persisted revision.
	beforeFailedSave := load()
	failedConfig := load()
	failedConfig["account_mode"] = "team"
	repo.failSave = true
	_, err = save(failedConfig)
	require.ErrorIs(t, err, ErrPluginStateChanged)
	repo.failSave = false
	afterFailedSave := getReport()
	foundA := false
	for _, raw := range afterFailedSave["sessions"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == aSession {
			foundA = true
			require.Equal(t, true, row["usable"])
			require.EqualValues(t, 292, row["expected_length"])
		}
	}
	require.True(t, foundA, "failed persistence discarded A session")
	require.Equal(t, beforeFailedSave["revision"], load()["revision"], "passive status or failed save changed revision")
	// Repeated compatible saves preserve both headers and the same state expiry.
	for range 2 {
		next := load()
		next["verification_interval_seconds"] = 0
		_, err = save(next)
		require.NoError(t, err)
	}
	_, err = command(map[string]any{"operation": "verify_nodes", "session_id": aSession, "route_ids": []string{route}})
	require.NoError(t, err)
	wait := func(predicate func(map[string]any) bool) map[string]any {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			r := getReport()
			if predicate(r) {
				return r
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("runtime operation did not reach required state")
		return nil
	}
	var sourceResult string
	wait(func(r map[string]any) bool {
		for _, raw := range r["node_verifications"].([]any) {
			v := raw.(map[string]any)
			if v["session_id"] == aSession && v["state"] == "completed" {
				row := v["rows"].([]any)[0].(map[string]any)
				require.Equal(t, true, row["qualified"])
				sourceResult = row["result_id"].(string)
				return true
			}
		}
		return false
	})
	if script := os.Getenv("SUB2API_CCODEX_REUSE_BROWSER_SCRIPT"); script != "" {
		ccodexAccountReuseBrowser(t, manager, installation, script, aSession, bSession, &bMismatch, repo, func() { expectBInjection.Store(true); finish(request(42, "gpt-6-astra", "private-user-message")) })
		return
	}
	operationJobs := map[string]string{}
	start := func(operation string) string {
		before := map[string]bool{}
		if report, ok := saved["core_report"].(map[string]any); ok {
			if rows, ok := report["account_route_jobs"].([]any); ok {
				for _, raw := range rows {
					before[raw.(map[string]any)["id"].(string)] = true
				}
			}
		}
		out, err := command(map[string]any{"operation": "verify_route_for_account", "operation_id": operation, "source_result_id": sourceResult, "session_id": bSession, "route_id": route})
		require.NoError(t, err)
		r := out["core_report"].(map[string]any)
		require.Empty(t, r["error_code"])
		jobs := r["account_route_jobs"].([]any)
		if id := operationJobs[operation]; id != "" {
			require.Len(t, jobs, len(before), "duplicate operation created another job")
			found := false
			for _, raw := range jobs {
				current := raw.(map[string]any)["id"].(string)
				require.True(t, before[current])
				if current == id {
					found = true
				}
			}
			require.True(t, found)
			return id
		}
		for _, raw := range jobs {
			id := raw.(map[string]any)["id"].(string)
			if !before[id] {
				require.Len(t, jobs, len(before)+1)
				operationJobs[operation] = id
				return id
			}
		}
		t.Fatal("new operation did not return a new job")
		return ""
	}
	jobRow := func(r map[string]any, id string) map[string]any {
		for _, raw := range r["account_route_jobs"].([]any) {
			j := raw.(map[string]any)
			if j["id"] == id {
				return j
			}
		}
		return nil
	}
	bad := start("B-mismatch-check")
	badReport := wait(func(r map[string]any) bool { j := jobRow(r, bad); return j != nil && j["state"] == "failed" })
	badRow := jobRow(badReport, bad)
	require.Equal(t, false, badRow["can_apply"])
	require.Equal(t, float64(332), badRow["target"].(map[string]any)["expected_length"])
	require.Equal(t, float64(356), badRow["target"].(map[string]any)["observed_length"])
	out, err := command(map[string]any{"operation": "apply_account_route", "job_id": bad, "expected_binding_revision": ""})
	require.NoError(t, err)
	require.Equal(t, "TARGET_NOT_QUALIFIED", out["core_report"].(map[string]any)["error_code"])
	bMismatch.Store(false)
	good := start("B-qualified-check")
	duplicate := start("B-qualified-check")
	require.Equal(t, good, duplicate)
	wait(func(r map[string]any) bool { j := jobRow(r, good); return j != nil && j["can_apply"] == true })
	// An in-flight A response remains intact across B's config apply and rollback.
	aResponse := request(41, "gpt-6-astra", "hold-stream")
	<-streamStarted
	solResponse := request(42, "gpt-5.6-sol", "hold-sol")
	<-solStarted
	repo.failSave = true
	_, err = command(map[string]any{"operation": "apply_account_route", "job_id": good, "expected_binding_revision": "", "enable_protection": true})
	require.ErrorIs(t, err, ErrPluginStateChanged)
	repo.failSave = false
	original := load()
	require.NotContains(t, original["accounts"].(map[string]any)["42"].(map[string]any)["model_route_bindings"].(map[string]any), "gpt-6-astra")
	out, err = command(map[string]any{"operation": "apply_account_route", "job_id": good, "expected_binding_revision": "", "enable_protection": true})
	require.NoError(t, err)
	require.Empty(t, out["core_report"].(map[string]any)["error_code"])
	bindings := out["accounts"].(map[string]any)["42"].(map[string]any)["model_route_bindings"].(map[string]any)
	require.Len(t, bindings, 2)
	require.Equal(t, original["accounts"].(map[string]any)["42"].(map[string]any)["model_route_bindings"].(map[string]any)["gpt-5.6-sol"], bindings["gpt-5.6-sol"])
	require.Equal(t, route, bindings["gpt-6-astra"].(map[string]any)["route_id"])
	release()
	finish(aResponse)
	finish(solResponse)
	finish(request(41, "gpt-6-astra", "private-user-message"))
	finish(request(42, "gpt-5.6-sol", "private-user-message"))
	expectBInjection.Store(true)
	bResponse := request(42, "gpt-6-astra", "private-user-message")
	require.Equal(t, bState, bResponse.Header.Get("X-Codex-Turn-State"))
	finish(bResponse)
	countsMu.Lock()
	require.Equal(t, 2, counts["A/gpt-6-astra/probe"], "A's own harvest and explicit verification only; B apply must not restart A")
	require.Equal(t, 2, counts["B/gpt-6-astra/probe"], "rejected and accepted verification only; saving the same stable binding must preserve B state")
	require.Equal(t, 1, counts["B/gpt-5.6-sol/probe"], "B's other model must retain its independent active state")
	countsMu.Unlock()
	report = getReport()
	var revision string
	for _, raw := range report["account_bindings"].([]any) {
		v := raw.(map[string]any)
		if v["account_id"] == float64(42) && v["model"] == "gpt-6-astra" {
			revision = v["revision"].(string)
		}
	}
	require.NotEmpty(t, revision)
	out, err = command(map[string]any{"operation": "remove_account_route_binding", "account_id": 42, "model": "gpt-6-astra", "expected_binding_revision": ""})
	require.NoError(t, err)
	require.Equal(t, "CONFIG_CONFLICT", out["core_report"].(map[string]any)["error_code"])
	out, err = command(map[string]any{"operation": "remove_account_route_binding", "account_id": 42, "model": "gpt-6-astra", "expected_binding_revision": revision})
	require.NoError(t, err)
	require.Empty(t, out["core_report"].(map[string]any)["error_code"])
	views := out["core_report"].(map[string]any)["account_bindings"].([]any)
	var removedRevision string
	for _, raw := range views {
		view := raw.(map[string]any)
		if view["account_id"] == float64(42) && view["model"] == "gpt-6-astra" {
			removedRevision = view["revision"].(string)
		}
	}
	out, err = command(map[string]any{"operation": "restore_account_route_binding", "account_id": 42, "model": "gpt-6-astra", "expected_binding_revision": removedRevision})
	require.NoError(t, err)
	require.Empty(t, out["core_report"].(map[string]any)["error_code"])
	// A fresh process keeps the binding but cannot use old process result IDs.
	proc.kill()
	nextProc := ccodexStartWithTestCA(t, ctx, installation, t.TempDir(), origin.Certificate().Raw)
	nextProc.installation = installation
	manager.runtimes[86] = nextProc
	manager.publishExtensionRoutesLocked(installation, nextProc, "")
	stored, err := manager.GetConfig(ctx, 86)
	require.NoError(t, err)
	require.NoError(t, nextProc.validateAndApplyConfig(ctx, stored))
	out, err = command(map[string]any{"operation": "apply_account_route", "job_id": good, "expected_binding_revision": ""})
	require.NoError(t, err)
	require.Equal(t, "VERIFICATION_EXPIRED", out["core_report"].(map[string]any)["error_code"])
	finish(request(42, "gpt-6-astra", "private-user-message"))
	require.Positive(t, connects.Load())
	t.Log("verified: A/B independent credentials and Team policy, B mismatch rejected, idempotent validation, CAS rollback/retry, A and B-other-model in-flight SSE/state preserved, B-only binding/own-state harvest, stale-save refusal, restore and process restart")
}
