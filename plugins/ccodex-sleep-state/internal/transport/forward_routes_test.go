package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/turnstate"
)

func fixedRouteTransport(server *httptest.Server) *http.Transport {
	address := server.Listener.Addr().String()
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
		},
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // local test servers only
		ResponseHeaderTimeout: 5 * time.Second,
	}
}

func routeRegistryForServers(a, b *httptest.Server) *Registry {
	return &Registry{routes: []Route{
		{ID: "route-a", Transport: fixedRouteTransport(a)},
		{ID: "route-b", Transport: fixedRouteTransport(b)},
	}}
}

func routeRequestStream(accountID int64) *fakeStream {
	payload := []byte(`{"model":"gpt-6-astra","input":[]}`)
	return &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{
			Method: http.MethodPost, Url: "https://example.test/responses", Host: "example.test", AccountId: accountID,
			Platform: "openai", AccountType: "oauth", ContentLength: int64(len(payload)), HasBody: true,
		}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: payload}},
		{Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
}

func TestForwardKeepsProbeRouteForFormalRequest(t *testing.T) {
	stateA := validTurnState('a')
	var callsA, callsB atomic.Int32
	serverA := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsA.Add(1)
		w.Header().Set(turnstate.Header, stateA)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer serverA.Close()
	serverB := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsB.Add(1)
		w.Header().Set(turnstate.Header, validTurnState('b'))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer serverB.Close()

	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(stateA))))
	if err != nil {
		t.Fatal(err)
	}
	store := turnstate.New()
	registry := routeRegistryForServers(serverA, serverB)
	handler := &Handler{
		Config: func() *config.Config { return c },
		States: store,
		Registry: func(*config.Config, string) (*Registry, error) {
			return registry, nil
		},
	}
	stream := routeRequestStream(71)
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if callsA.Load() != 2 || callsB.Load() != 0 {
		t.Fatalf("route calls = A:%d B:%d, want A:2 B:0", callsA.Load(), callsB.Load())
	}
	policy, ok := policyForConfig(c)
	if !ok {
		t.Fatal("invalid policy")
	}
	snapshot, ok := store.Acquire(71, "gpt-6-astra", policy, time.Now())
	if !ok || snapshot.Route != 0 || snapshot.Token.Value != stateA {
		t.Fatalf("snapshot = %#v usable=%v, want route 0 and state A", snapshot, ok)
	}
}

func TestForwardFailsWhenSavedRouteDisappears(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	seed := validTurnState('x')
	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(seed))))
	if err != nil {
		t.Fatal(err)
	}
	store := turnstate.New()
	policy, ok := policyForConfig(c)
	if !ok || !store.Offer(72, "gpt-6-astra", seed, 9, policy, time.Now()) {
		t.Fatal("failed to seed saved route state")
	}
	registry := &Registry{routes: []Route{{ID: "route-only", Transport: fixedRouteTransport(server)}}}
	handler := &Handler{
		Config:   func() *config.Config { return c },
		States:   store,
		Registry: func(*config.Config, string) (*Registry, error) { return registry, nil },
	}
	stream := routeRequestStream(72)
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) != 1 || stream.responses[0].GetError() == nil || stream.responses[0].GetError().Code != "ROUTE_UNAVAILABLE" || stream.responses[0].GetError().RequestSent {
		t.Fatalf("unexpected missing route response: %+v", stream.responses)
	}
	if calls.Load() != 0 {
		t.Fatalf("missing route unexpectedly reached upstream: %d", calls.Load())
	}
}

func TestProbeUsesNextRouteAfterTransportFailure(t *testing.T) {
	stateB := validTurnState('b')
	var callsB atomic.Int32
	serverB := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsB.Add(1)
		w.Header().Set(turnstate.Header, stateB)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer serverB.Close()
	failedServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// The handler closes before the probe can complete below.
	}))
	failedServer.Close()

	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(stateB))))
	if err != nil {
		t.Fatal(err)
	}
	registry := routeRegistryForServers(failedServer, serverB)
	store := turnstate.New()
	handler := &Handler{Config: func() *config.Config { return c }, States: store, Registry: func(*config.Config, string) (*Registry, error) { return registry, nil }}
	stream := routeRequestStream(73)
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if callsB.Load() != 2 {
		t.Fatalf("second route calls = %d, want probe and formal request", callsB.Load())
	}
	if len(stream.responses) < 2 || stream.responses[0].GetStart() == nil {
		t.Fatalf("unexpected response frames: %+v", stream.responses)
	}
	if !strings.Contains(stream.responses[0].GetStart().Status, "200") {
		t.Fatalf("unexpected response status: %+v", stream.responses[0].GetStart())
	}
}

func TestFixedRouteModeUsesOnlyConfiguredRoute(t *testing.T) {
	stateB := validTurnState('b')
	var callsA, callsB atomic.Int32
	serverA := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsA.Add(1)
		w.Header().Set(turnstate.Header, validTurnState('a'))
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer serverA.Close()
	serverB := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsB.Add(1)
		w.Header().Set(turnstate.Header, stateB)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer serverB.Close()
	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"state_target_length":%d,"route_mode":"fixed","fixed_route_id":"route-b","models":["gpt-6-astra"]}`, len(stateB))))
	if err != nil {
		t.Fatal(err)
	}
	registry := routeRegistryForServers(serverA, serverB)
	handler := &Handler{Config: func() *config.Config { return c }, States: turnstate.New(), Registry: func(*config.Config, string) (*Registry, error) { return registry, nil }}
	if err := handler.Forward(routeRequestStream(74)); err != nil {
		t.Fatal(err)
	}
	if callsA.Load() != 0 || callsB.Load() != 2 {
		t.Fatalf("fixed route calls = A:%d B:%d, want A:0 B:2", callsA.Load(), callsB.Load())
	}
}

func TestFailedFormalRouteInvalidatesRegistry(t *testing.T) {
	state := validTurnState('i')
	closed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	registry := &Registry{routes: []Route{{ID: "route-failed", Transport: fixedRouteTransport(closed)}}}
	closed.Close()
	c, err := config.Parse([]byte(fmt.Sprintf(`{"enabled":true,"inject_state":true,"state_target_length":%d,"models":["gpt-6-astra"]}`, len(state))))
	if err != nil {
		t.Fatal(err)
	}
	store := turnstate.New()
	policy, ok := policyForConfig(c)
	if !ok || !store.Offer(75, "gpt-6-astra", state, 0, policy, time.Now()) {
		t.Fatal("failed to seed state")
	}
	var invalidations atomic.Int32
	handler := &Handler{
		Config:   func() *config.Config { return c },
		States:   store,
		Registry: func(*config.Config, string) (*Registry, error) { return registry, nil },
		InvalidateRoutes: func(*config.Config, string) {
			invalidations.Add(1)
		},
	}
	stream := routeRequestStream(75)
	if err := handler.Forward(stream); err != nil {
		t.Fatal(err)
	}
	if invalidations.Load() != 1 {
		t.Fatalf("registry invalidations = %d, want 1", invalidations.Load())
	}
	if len(stream.responses) != 1 || stream.responses[0].GetError() == nil || stream.responses[0].GetError().Code != "UPSTREAM_FAILED" {
		t.Fatalf("unexpected response: %+v", stream.responses)
	}
}
