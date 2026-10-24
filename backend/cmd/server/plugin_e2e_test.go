//go:build plugin_e2e

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"golang.org/x/crypto/bcrypt"
)

// This opt-in suite uses the production dependency graph, router, authentication,
// database, billing workers and real plugin executables. Only the upstream is a
// deterministic localhost fixture. No existing database/Redis service is used.
func TestPluginProductionE2E(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("plugin_e2e"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("isolated-test-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(ctx)) })
	rd, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rd.Terminate(ctx)) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	const email, password = "plugin-e2e@example.test", "Isolated-plugin-e2e-Only!42"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	var adminID int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,role,balance,concurrency) VALUES($1,$2,'admin',100,16) RETURNING id`, email, string(hash)).Scan(&adminID))
	_, err = db.Exec(`INSERT INTO settings(key,value) VALUES('totp_enabled','true'),('step_up_enabled','true'),('plugin_management_enabled','true'),('openai_codex_version_auto_sync_enabled','false') ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
	require.NoError(t, err)

	root := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyFile := filepath.Join(root, "publisher.key")
	require.NoError(t, os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(private)), 0600))
	packages := map[string]string{}
	browserPackages := map[string]string{}
	_, browserPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	browserKeyFile := filepath.Join(root, "browser-publisher.key")
	require.NoError(t, os.WriteFile(browserKeyFile, []byte(base64.StdEncoding.EncodeToString(browserPrivate)), 0600))
	for _, version := range []string{"0.1.0", "0.1.1"} {
		binary := filepath.Join(root, "preprocess-"+version)
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		runPluginE2EGo(t, "build", "-ldflags", "-X main.accountType=apikey -X main.pluginVersion="+version, "-o", binary, "./pkg/pluginapi/examples/preprocess")
		packages[version] = filepath.Join(root, "request-policy-"+version+".s2plugin")
		runPluginE2EGo(t, "run", "./pkg/pluginapi/examples/preprocess/pack", "-binary", binary, "-out", packages[version], "-version", version, "-account-type", "apikey", "-signing-key", keyFile, "-key-id", "e2e")
		browserPackages[version] = filepath.Join(root, "request-policy-browser-"+version+".s2plugin")
		runPluginE2EGo(t, "run", "./pkg/pluginapi/examples/preprocess/pack", "-binary", binary, "-out", browserPackages[version], "-version", version, "-account-type", "apikey", "-signing-key", browserKeyFile, "-key-id", "e2e-browser")
	}

	var upstreamMu sync.Mutex
	var forwarded []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/pricing" {
			_, _ = io.WriteString(w, `{"gpt-4o-mini":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"litellm_provider":"openai","mode":"chat"}}`)
			return
		}
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer isolated-upstream-key" {
			t.Logf("Unexpected fixture request: %s %s (auth matched=%t)", r.Method, r.URL.Path, r.Header.Get("Authorization") == "Bearer isolated-upstream-key")
			http.Error(w, "unexpected upstream request", http.StatusBadRequest)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		// Account creation performs an independent tool-capability probe. It is
		// not a user gateway call and must not enter the forwarding/billing count.
		if body["tool_choice"] == "required" {
			_, _ = io.WriteString(w, `{"id":"resp_probe","object":"response","status":"completed","output":[{"type":"function_call","name":"probe_ping","call_id":"probe","arguments":"{\"ok\":true}"}]}`)
			return
		}
		upstreamMu.Lock()
		forwarded = append(forwarded, body)
		seq := len(forwarded)
		upstreamMu.Unlock()
		w.Header().Set("X-Request-ID", fmt.Sprintf("plugin-e2e-%d", seq))
		_, _ = fmt.Fprintf(w, `{"id":"resp_e2e_%d","object":"response","created_at":1789640000,"status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"msg_e2e","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105}}`, seq)
	}))
	t.Cleanup(upstream.Close)
	pgURL, err := url.Parse(dsn)
	require.NoError(t, err)
	pgPort, err := strconv.Atoi(pgURL.Port())
	require.NoError(t, err)
	redisHost, err := rd.Host(ctx)
	require.NoError(t, err)
	redisPort, err := rd.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	cfg := map[string]any{
		"server":   map[string]any{"host": "127.0.0.1", "mode": "release"},
		"database": map[string]any{"host": pgURL.Hostname(), "port": pgPort, "user": "postgres", "password": "isolated-test-only", "dbname": "plugin_e2e", "sslmode": "disable"},
		"redis":    map[string]any{"host": redisHost, "port": redisPort.Int(), "password": "", "db": 0},
		"jwt":      map[string]any{"secret": strings.Repeat("plugin-e2e-only-", 4)},
		"totp":     map[string]any{"encryption_key": strings.Repeat("a", 64)},
		"pricing":  map[string]any{"remote_url": upstream.URL + "/pricing", "hash_url": "", "data_dir": filepath.Join(root, "pricing"), "fallback_file": ""},
		"plugins":  map[string]any{"data_dir": filepath.Join(root, "plugins"), "trusted_publishers": map[string]string{}},
		// The test provider is plain HTTP on loopback. Production allowlist mode
		// requires HTTPS; this isolated config does not change deployment defaults.
		"security": map[string]any{"url_allowlist": map[string]any{"enabled": false, "allow_private_hosts": true, "allow_insecure_http": true}},
	}
	cfgRaw, err := json.Marshal(cfg)
	require.NoError(t, err)
	cfgPath := filepath.Join(root, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgRaw, 0600))
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("DATA_DIR", root)
	t.Setenv("PLUGINS_DATA_DIR", filepath.Join(root, "plugins"))
	t.Setenv("PRICING_REMOTE_URL", upstream.URL+"/pricing")
	t.Setenv("PRICING_DATA_DIR", filepath.Join(root, "pricing"))
	// Explicitly bind isolated endpoints even if the developer shell has config
	// overrides. Never accidentally run this test against a developer database.
	for key, value := range map[string]string{"DATABASE_HOST": pgURL.Hostname(), "DATABASE_PORT": pgURL.Port(), "DATABASE_USER": "postgres", "DATABASE_PASSWORD": "isolated-test-only", "DATABASE_DBNAME": "plugin_e2e", "DATABASE_SSLMODE": "disable", "REDIS_HOST": redisHost, "REDIS_PORT": redisPort.Port(), "REDIS_PASSWORD": "", "REDIS_DB": "0", "RUN_MODE": "standard"} {
		t.Setenv(key, value)
	}
	app, err := initializeApplication(handler.BuildInfo{Version: strings.TrimSpace(embeddedVersion), BuildType: "plugin-e2e"})
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	require.NoError(t, app.PluginManager.Start(ctx))
	server := httptest.NewServer(app.Server.Handler)
	t.Cleanup(server.Close)
	client := &pluginE2EClient{t: t, base: server.URL, client: &http.Client{Timeout: 30 * time.Second}}
	login := client.json("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, 200)
	client.token = login["access_token"].(string)
	compliance := client.json("GET", "/api/v1/admin/compliance", nil, 200)
	client.json("POST", "/api/v1/admin/compliance/accept", map[string]any{"phrase": compliance["ack_phrase_en"], "language": "en"}, 200)
	setup := client.json("POST", "/api/v1/user/totp/setup", map[string]any{"password": password}, 200)
	secret := setup["secret"].(string)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	client.json("POST", "/api/v1/user/totp/enable", map[string]any{"totp_code": code, "setup_token": setup["setup_token"]}, 200)
	client.json("POST", "/api/v1/admin/plugins/1/enable", map[string]any{"rollout_percent": 100, "accept_untested": true}, 403)
	client.json("POST", "/api/v1/admin/plugins/authorize-upload", map[string]any{}, 403)
	client.json("POST", "/api/v1/user/totp/step-up", map[string]any{"code": code}, 200)
	client.json("POST", "/api/v1/admin/plugins/authorize-upload", map[string]any{}, 200)
	group := client.json("POST", "/api/v1/admin/groups", map[string]any{"name": "plugin-e2e", "platform": "openai", "rate_multiplier": 1, "subscription_type": "standard"}, 200)
	account := client.json("POST", "/api/v1/admin/accounts", map[string]any{"name": "plugin-e2e", "platform": "openai", "type": "apikey", "credentials": map[string]any{"api_key": "isolated-upstream-key", "base_url": upstream.URL + "/v1"}, "concurrency": 16, "priority": 1, "rate_multiplier": 1, "group_ids": []any{group["id"]}, "upstream_billing_probe_enabled": false}, 200)
	key := client.json("POST", "/api/v1/keys", map[string]any{"name": "plugin-e2e", "group_id": group["id"]}, 200)
	apiKey := key["key"].(string)
	inspection := client.upload("/api/v1/admin/plugins/inspect", packages["0.1.0"], 200)
	require.Equal(t, "untrusted", inspection["signature_status"])
	publisher := inspection["publisher"].(map[string]any)
	client.upload("/api/v1/admin/plugins/upload", packages["0.1.0"], 400)
	var trustCount int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_plugin_publishers`).Scan(&trustCount))
	require.Zero(t, trustCount)
	installed := client.uploadWithFields("/api/v1/admin/plugins/upload", packages["0.1.0"], 201, map[string]string{
		"trust_publisher": "true", "package_sha256": inspection["package_sha256"].(string), "publisher_fingerprint": publisher["fingerprint"].(string),
	})
	var savedPublic string
	require.NoError(t, db.QueryRow(`SELECT public_key FROM sub2api_plugin_publishers WHERE key_id='e2e'`).Scan(&savedPublic))
	require.Equal(t, base64.StdEncoding.EncodeToString(public), savedPublic)
	knownInspection := client.upload("/api/v1/admin/plugins/inspect", packages["0.1.1"], 200)
	require.Equal(t, "trusted", knownInspection["signature_status"], "later packages from this publisher do not need repeated trust")
	pluginPath := fmt.Sprintf("/api/v1/admin/plugins/%.0f", installed["id"].(float64))
	client.json("PUT", pluginPath+"/config", map[string]any{"max_output_tokens": 12}, 200)
	client.json("POST", pluginPath+"/enable", map[string]any{"rollout_percent": 100, "accept_untested": true}, 200)
	call := func(t *testing.T, expected int) {
		t.Helper()
		previousTest := client.t
		client.t = t
		defer func() { client.t = previousTest }()
		old := client.token
		client.token = apiKey
		defer func() { client.token = old }()
		client.json("POST", "/v1/responses", map[string]any{"model": "gpt-4o-mini", "input": "E2E synthetic request", "stream": false, "max_output_tokens": 100}, expected)
	}
	count := func() int { upstreamMu.Lock(); defer upstreamMu.Unlock(); return len(forwarded) }
	assertBilling := func(t *testing.T, n int) {
		t.Helper()
		require.Eventually(t, func() bool {
			var rows int
			var cost, balance float64
			if db.QueryRow(`SELECT count(*), COALESCE(sum(actual_cost),0) FROM usage_logs WHERE user_id=$1`, adminID).Scan(&rows, &cost) != nil {
				return false
			}
			if db.QueryRow(`SELECT balance FROM users WHERE id=$1`, adminID).Scan(&balance) != nil {
				return false
			}
			return rows == n && absPluginE2E(cost-float64(n)*0.00011) < 0.00000001 && absPluginE2E(balance-(100-cost)) < 0.00000001
		}, 10*time.Second, 50*time.Millisecond, "usage count/cost and user balance must agree exactly")
	}
	call(t, 200)
	require.Equal(t, 1, count())
	upstreamMu.Lock()
	sent := forwarded[0]
	upstreamMu.Unlock()
	require.Equal(t, float64(12), sent["max_output_tokens"])
	require.Equal(t, "gpt-4o-mini", sent["model"])
	assertBilling(t, 1)
	t.Run("deny and deadline never bill or reach upstream", func(t *testing.T) {
		client.json("PUT", pluginPath+"/config", map[string]any{"max_output_tokens": 12, "deny_model": "gpt-4o-mini"}, 200)
		call(t, 403)
		require.Equal(t, 1, count())
		assertBilling(t, 1)
		client.json("PUT", pluginPath+"/config", map[string]any{"max_output_tokens": 12, "delay_ms": 500}, 200)
		call(t, 503)
		require.Equal(t, 1, count())
		assertBilling(t, 1)
		var status string
		var schedulable bool
		require.NoError(t, db.QueryRow(`SELECT status,schedulable FROM accounts WHERE id=$1`, account["id"]).Scan(&status, &schedulable))
		require.Equal(t, "active", status)
		require.True(t, schedulable)
	})
	client.json("PUT", pluginPath+"/config", map[string]any{"max_output_tokens": 12}, 200)
	t.Run("upgrade rollback and disabled fallback", func(t *testing.T) {
		upgraded := client.upload(pluginPath+"/upgrade", packages["0.1.1"], 200)
		require.Equal(t, "0.1.1", upgraded["runtime_version"])
		call(t, 200)
		assertBilling(t, 2)
		var versionID int64
		require.NoError(t, db.QueryRow(`SELECT id FROM sub2api_plugin_versions WHERE plugin_id=$1 ORDER BY id DESC LIMIT 1`, installed["id"]).Scan(&versionID))
		rolled := client.json("POST", pluginPath+"/rollback", map[string]any{"version_id": versionID, "accept_untested": true}, 200)
		require.Equal(t, "0.1.0", rolled["runtime_version"])
		call(t, 200)
		assertBilling(t, 3)
		client.json("POST", pluginPath+"/disable", map[string]any{}, 200)
		call(t, 200)
		assertBilling(t, 4)
		upstreamMu.Lock()
		last := forwarded[len(forwarded)-1]
		upstreamMu.Unlock()
		require.Equal(t, float64(100), last["max_output_tokens"])
	})
	t.Run("bounded concurrent traffic has exact once billing", func(t *testing.T) {
		client.json("POST", pluginPath+"/enable", map[string]any{"rollout_percent": 100, "accept_untested": true}, 200)
		const total, concurrency = 24, 8
		type result struct {
			elapsed time.Duration
			err     error
		}
		results := make(chan result, total)
		slots := make(chan struct{}, concurrency)
		for n := 0; n < total; n++ {
			slots <- struct{}{}
			go func() {
				defer func() { <-slots }()
				start := time.Now()
				req, err := http.NewRequest("POST", server.URL+"/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"synthetic concurrency probe","stream":false,"max_output_tokens":100}`))
				if err == nil {
					req.Header.Set("Authorization", "Bearer "+apiKey)
					req.Header.Set("Content-Type", "application/json")
					var res *http.Response
					res, err = client.client.Do(req)
					if err == nil {
						raw, readErr := io.ReadAll(res.Body)
						_ = res.Body.Close()
						err = readErr
						if res.StatusCode != 200 {
							err = fmt.Errorf("gateway returned %d: %s", res.StatusCode, raw)
						}
					}
				}
				results <- result{time.Since(start), err}
			}()
		}
		latencies := make([]time.Duration, 0, total)
		for n := 0; n < total; n++ {
			result := <-results
			require.NoError(t, result.err)
			latencies = append(latencies, result.elapsed)
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		t.Logf("requests=%d concurrency=%d p50=%s p95=%s (local synthetic upstream; not a production capacity claim)", total, concurrency, latencies[total/2], latencies[(total*95/100)])
		require.Equal(t, 4+total, count())
		assertBilling(t, 4+total)
		client.json("POST", pluginPath+"/disable", map[string]any{}, 200)
	})
	// Optional local browser harness. The private fixture contains only synthetic
	// credentials for these temporary containers. A bounded wait always cleans up.
	if fixture := os.Getenv("SUB2API_PLUGIN_E2E_FIXTURE"); fixture != "" {
		data, _ := json.Marshal(map[string]any{"backend_url": server.URL, "email": email, "password": password, "totp_secret": secret, "packages": browserPackages, "plugin_id": installed["id"], "admin_id": adminID, "group_id": group["id"], "account_id": account["id"], "api_key": apiKey, "done_file": filepath.Join(root, "browser.done")})
		require.NoError(t, os.WriteFile(fixture, data, 0600))
		t.Cleanup(func() { _ = os.Remove(fixture) })
		t.Logf("Browser fixture ready: %s", fixture)
		require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(root, "browser.done")); return err == nil }, 15*time.Minute, 250*time.Millisecond, "browser harness did not finish")
		result, err := os.ReadFile(filepath.Join(root, "browser.done"))
		require.NoError(t, err)
		require.JSONEq(t, `{"passed":true}`, string(result))
	}
	client.json("DELETE", pluginPath, nil, 200)
}

type pluginE2EClient struct {
	t           *testing.T
	base, token string
	client      *http.Client
}

func (c *pluginE2EClient) request(method, path, contentType string, body io.Reader, status int) map[string]any {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", contentType)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.client.Do(req)
	require.NoError(c.t, err)
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(c.t, err)
	require.Equal(c.t, status, res.StatusCode, "%s %s: %s", method, path, raw)
	var envelope map[string]any
	require.NoError(c.t, json.Unmarshal(raw, &envelope), string(raw))
	if data, ok := envelope["data"].(map[string]any); ok {
		return data
	}
	return envelope
}
func (c *pluginE2EClient) json(method, path string, body any, status int) map[string]any {
	c.t.Helper()
	data, err := json.Marshal(body)
	require.NoError(c.t, err)
	return c.request(method, path, "application/json", bytes.NewReader(data), status)
}
func (c *pluginE2EClient) upload(path, file string, status int) map[string]any {
	return c.uploadWithFields(path, file, status, nil)
}

func (c *pluginE2EClient) uploadWithFields(path, file string, status int, fields map[string]string) map[string]any {
	c.t.Helper()
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	for key, value := range fields {
		require.NoError(c.t, w.WriteField(key, value))
	}
	part, err := w.CreateFormFile("plugin", filepath.Base(file))
	require.NoError(c.t, err)
	raw, err := os.ReadFile(file)
	require.NoError(c.t, err)
	_, err = part.Write(raw)
	require.NoError(c.t, err)
	require.NoError(c.t, w.WriteField("accept_untested", "true"))
	require.NoError(c.t, w.Close())
	return c.request("POST", path, w.FormDataContentType(), &data, status)
}
func runPluginE2EGo(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = filepath.Join("..", "..")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}
func absPluginE2E(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
