package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/turnstate"
)

func TestApplyConfigReplacesRegistriesAndClearsChangedRouteState(t *testing.T) {
	p := newPlugin()
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_urls":["http://127.0.0.1:18080"],"models":["gpt-6-astra"]}`)); err != nil {
		t.Fatal(err)
	}
	first, err := p.registryFor(context.Background(), p.config.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.states.Acquire(1, "gpt-6-astra", turnstate.Policy{TTL: time.Hour}, time.Now())
	if p.states.Len() != 1 {
		t.Fatalf("state machines = %d, want 1", p.states.Len())
	}
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_urls":["socks5h://127.0.0.1:18081"],"models":["gpt-6-astra"]}`)); err != nil {
		t.Fatal(err)
	}
	if p.states.Len() != 0 {
		t.Fatalf("state machines after route replacement = %d, want 0", p.states.Len())
	}
	if first.Len() != 1 {
		t.Fatal("configuration swap interrupted the immutable route snapshot of an in-flight request")
	}
	defer first.Close()
	second, err := p.registryFor(context.Background(), p.config.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || second.Len() != 1 {
		t.Fatalf("registry was not replaced: first=%p second=%p len=%d", first, second, second.Len())
	}
}

func TestApplyConfigKeepsStateWhenOnlyPolicyChanges(t *testing.T) {
	p := newPlugin()
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_urls":["http://127.0.0.1:18080"],"models":["gpt-6-astra"]}`)); err != nil {
		t.Fatal(err)
	}
	_, _ = p.states.Acquire(2, "gpt-6-astra", turnstate.Policy{TTL: time.Hour}, time.Now())
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_urls":["http://127.0.0.1:18080"],"models":["gpt-6-astra"],"cooldown_seconds":300}`)); err != nil {
		t.Fatal(err)
	}
	if p.states.Len() != 1 {
		t.Fatalf("policy-only config change cleared state machines: %d", p.states.Len())
	}
	if _, err := p.registryFor(context.Background(), p.config.Load(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestTestConfigResolvesSources(t *testing.T) {
	p := newPlugin()
	t.Setenv("CCODEX_MISSING_PROXY", "")
	if _, err := p.TestConfig(context.Background(), []byte(`{"proxy_envs":["CCODEX_MISSING_PROXY"]}`)); err == nil {
		t.Fatal("missing proxy environment source must fail TestConfig")
	}
}

func TestDynamicRegistryRefreshesAfterInterval(t *testing.T) {
	t.Setenv("CCODEX_REFRESH_PROXY", "http://127.0.0.1:18085")
	p := newPlugin()
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_envs":["CCODEX_REFRESH_PROXY"],"subscription_refresh_seconds":60}`)); err != nil {
		t.Fatal(err)
	}
	c := p.config.Load()
	first, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	key := registryKey(c, "", c.RouteURLs())
	p.routeMu.Lock()
	cached := p.registries[key]
	cached.loadedAt = time.Now().Add(-2 * time.Minute)
	p.registries[key] = cached
	p.routeMu.Unlock()
	t.Setenv("CCODEX_REFRESH_PROXY", "http://127.0.0.1:18088")
	_, _ = p.states.Acquire(3, "gpt-6-astra", turnstate.Policy{TTL: time.Hour}, time.Now())
	second, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("expired dynamic registry was not reloaded")
	}
	if p.states.Len() != 0 {
		t.Fatalf("state was not cleared after route refresh: %d", p.states.Len())
	}
	second.Close()
}

func TestUnchangedDynamicRegistryRefreshPreservesState(t *testing.T) {
	t.Setenv("CCODEX_STABLE_PROXY", "http://127.0.0.1:18089")
	p := newPlugin()
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_envs":["CCODEX_STABLE_PROXY"],"subscription_refresh_seconds":60}`)); err != nil {
		t.Fatal(err)
	}
	c := p.config.Load()
	first, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	key := registryKey(c, "", c.RouteURLs())
	p.routeMu.Lock()
	cached := p.registries[key]
	cached.loadedAt = time.Now().Add(-2 * time.Minute)
	p.registries[key] = cached
	p.routeMu.Unlock()
	_, _ = p.states.Acquire(4, "gpt-6-astra", turnstate.Policy{TTL: time.Hour}, time.Now())
	second, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("unchanged route set replaced the working registry")
	}
	if p.states.Len() != 1 {
		t.Fatalf("unchanged route refresh cleared state: %d", p.states.Len())
	}
	first.Close()
}

func TestInvalidateRegistryForcesNextRequestReload(t *testing.T) {
	p := newPlugin()
	c, err := config.Parse([]byte(`{"proxy_urls":["http://127.0.0.1:18086"]}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	p.invalidateRegistry(c, "")
	second, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("invalidated registry was reused")
	}
	second.Close()
}

func TestFailedSubscriptionRefreshKeepsStaleRegistry(t *testing.T) {
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("http://127.0.0.1:18087\n"))
	}))
	defer server.Close()
	raw, err := json.Marshal(map[string]any{
		"subscriptions":                []map[string]string{{"url": server.URL}},
		"subscription_refresh_seconds": 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := newPlugin()
	first, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	key := registryKey(c, "", c.RouteURLs())
	p.routeMu.Lock()
	cached := p.registries[key]
	cached.loadedAt = time.Now().Add(-2 * time.Minute)
	p.registries[key] = cached
	p.routeMu.Unlock()
	fail.Store(true)
	second, err := p.registryFor(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("refresh failure discarded the stale registry")
	}
	first.Close()
}
