package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/config"
)

func TestNewRegistryFromSubscriptionFile(t *testing.T) {
	file := t.TempDir() + string(os.PathSeparator) + "nodes.txt"
	if err := os.WriteFile(file, []byte("http://127.0.0.1:18080\nsocks5h://127.0.0.1:18081\nhttp://127.0.0.1:18080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"file": file}}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistryFromConfig(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if registry.Len() != 2 {
		t.Fatalf("subscription route count = %d, want 2", registry.Len())
	}
}

func TestLiveSubscriptionBuildsAndFindsAvailableRoute(t *testing.T) {
	subscriptionURL := os.Getenv("CCODEX_TEST_SUBSCRIPTION_URL")
	if subscriptionURL == "" {
		t.Skip("set CCODEX_TEST_SUBSCRIPTION_URL for live subscription integration")
	}
	raw, err := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": subscriptionURL}}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	registry, err := NewRegistryFromConfig(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if registry.Len() == 0 {
		t.Fatal("subscription produced no routes")
	}
	report, err := TestConnectivity(ctx, registry)
	if err != nil {
		t.Fatalf("connectivity report=%+v: %v", report, err)
	}
	t.Logf("subscription routes: %d/%d available", report.Available, report.Total)
}

func TestLiveAuthenticatedProxyIsAvailable(t *testing.T) {
	proxyURL := os.Getenv("CCODEX_TEST_PROXY_URL")
	if proxyURL == "" {
		t.Skip("set CCODEX_TEST_PROXY_URL for authenticated proxy integration")
	}
	raw, err := json.Marshal(map[string]any{"proxy_urls": []string{proxyURL}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	registry, err := NewRegistryFromConfig(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	report, err := TestConnectivity(ctx, registry)
	if err != nil {
		t.Fatalf("authenticated proxy report=%+v: %v", report, err)
	}
	t.Logf("authenticated proxy routes: %d/%d available", report.Available, report.Total)
}

func TestNewRegistryFromLoopbackSubscriptionURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxies:\n  - name: local\n    type: http\n    server: 127.0.0.1\n    port: 18082\n"))
	}))
	defer server.Close()
	c, err := config.Parse([]byte(`{"subscriptions":[{"url":"` + server.URL + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistryFromConfig(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if registry.Len() != 1 {
		t.Fatalf("URL subscription route count = %d, want 1", registry.Len())
	}
}

func TestProxyEnvironmentSource(t *testing.T) {
	t.Setenv("CCODEX_TEST_PROXY", "http://127.0.0.1:18083")
	c, err := config.Parse([]byte(`{"proxy_envs":["CCODEX_TEST_PROXY"]}`))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistryFromConfig(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if registry.Len() != 1 {
		t.Fatalf("environment route count = %d, want 1", registry.Len())
	}
}

func TestAuthenticatedHTTPProxySourceBuilds(t *testing.T) {
	c, err := config.Parse([]byte(`{"proxy_urls":["http://user:pass@127.0.0.1:18084"]}`))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistryFromConfig(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if registry.Len() != 1 {
		t.Fatalf("authenticated HTTP route count = %d, want 1", registry.Len())
	}
}

func TestConfiguredRoutesUseTimeoutAndHTTPDefaultPort(t *testing.T) {
	c, err := config.Parse([]byte(`{"proxy_urls":["http://127.0.0.1","http://127.0.0.1:80","https://127.0.0.1","https://127.0.0.1:443"],"response_header_timeout_seconds":9}`))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistryFromConfig(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if registry.Len() != 2 {
		t.Fatalf("default ports did not deduplicate: %d routes", registry.Len())
	}
	for index := 0; index < registry.Len(); index++ {
		route, _ := registry.At(index)
		if route.Transport.ResponseHeaderTimeout != 9*time.Second || !route.Transport.DisableCompression {
			t.Fatalf("route ignores configured transport policy: timeout=%s compression_disabled=%v", route.Transport.ResponseHeaderTimeout, route.Transport.DisableCompression)
		}
	}
}
