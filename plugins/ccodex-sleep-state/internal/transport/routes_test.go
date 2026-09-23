package transport

import "testing"

func TestRegistryDeduplicatesAndRoundRobins(t *testing.T) {
	registry, err := NewRegistry([]string{
		"HTTP://127.0.0.1:18080/",
		"http://127.0.0.1:18080",
		"socks5h://127.0.0.1:18081",
	}, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if got := registry.Len(); got != 2 {
		t.Fatalf("registry length = %d, want 2", got)
	}
	firstIndex, first, ok := registry.Next()
	if !ok || firstIndex != 0 || first.ProxyURL != "http://127.0.0.1:18080" {
		t.Fatalf("first route = %d %#v %v", firstIndex, first, ok)
	}
	secondIndex, second, ok := registry.Next()
	if !ok || secondIndex != 1 || second.ProxyURL != "socks5h://127.0.0.1:18081" {
		t.Fatalf("second route = %d %#v %v", secondIndex, second, ok)
	}
	thirdIndex, third, ok := registry.Next()
	if !ok || thirdIndex != 0 || third.ID != first.ID {
		t.Fatalf("round-robin route = %d %#v %v", thirdIndex, third, ok)
	}
}

func TestRegistryAddsDirectOnlyWhenRequestedOrEmpty(t *testing.T) {
	withDirect, err := NewRegistry([]string{"http://127.0.0.1:18080"}, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if withDirect.Len() != 2 {
		t.Fatalf("direct registry length = %d, want 2", withDirect.Len())
	}
	withDirect.Close()

	empty, err := NewRegistry(nil, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if empty.Len() != 1 {
		t.Fatalf("empty registry length = %d, want 1", empty.Len())
	}
	if route, ok := empty.At(0); !ok || route.ID != "route-direct" || route.ProxyURL != "" {
		t.Fatalf("unexpected direct route: %#v %v", route, ok)
	}
}

func TestRegistryRejectsUnsupportedRoute(t *testing.T) {
	if _, err := NewRegistry([]string{"vmess://example.test"}, false, 10); err == nil {
		t.Fatal("unsupported route scheme must be rejected")
	}
}

func TestRegistryCloseMakesRoutesUnavailable(t *testing.T) {
	registry, err := NewRegistry([]string{"http://127.0.0.1:18080"}, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
	if registry.Len() != 0 {
		t.Fatalf("closed registry length = %d, want 0", registry.Len())
	}
	if _, _, ok := registry.Next(); ok {
		t.Fatal("closed registry must not select a route")
	}
}

func TestRegistryFindsStableRouteID(t *testing.T) {
	registry, err := NewRegistry([]string{"http://127.0.0.1:18080"}, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	index, route, ok := registry.ByID(routeID("http://127.0.0.1:18080"))
	if !ok || index != 0 || route.ID == "" {
		t.Fatalf("route lookup = %d %#v %v", index, route, ok)
	}
	if _, _, ok := registry.ByID("route-missing"); ok {
		t.Fatal("missing route ID must not resolve")
	}
}
