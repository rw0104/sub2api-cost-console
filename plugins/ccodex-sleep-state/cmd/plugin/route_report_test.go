package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/transport"
)

func TestValidateConfigDiscoversSubscriptionAndDoesNotReplayAction(t *testing.T) {
	var downloads atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write([]byte("http://user:secret@127.0.0.1:19801\nsocks5://127.0.0.1:19802\n"))
	}))
	defer source.Close()
	p := newPlugin()
	raw, _ := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": source.URL}}, "route_request": config.RouteRequest{Operation: "discover"}})
	normalized, err := p.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	next, err := config.Parse(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if next.RouteRequest != nil || strings.Contains(string(normalized), "route_request") {
		t.Fatal("one-shot request leaked into persisted config")
	}
	if next.RouteReport == nil || next.RouteReport.ErrorCode != "" || len(next.RouteReport.Nodes) != 2 {
		t.Fatalf("missing discovery report: %+v", next.RouteReport)
	}
	for _, node := range next.RouteReport.Nodes {
		if node.ID == "" || node.Status != "untested" || strings.Contains(node.Name, "secret") || strings.Contains(node.Name, "127.0.0.1") {
			t.Fatalf("invalid node metadata: %+v", node)
		}
	}
	if err := p.ApplyConfig(context.Background(), normalized); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ValidateConfig(context.Background(), normalized); err != nil {
		t.Fatal(err)
	}
	// Host restarts use the same persisted config. Neither operation fetches.
	if err := newPlugin().ApplyConfig(context.Background(), normalized); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatalf("restore replayed subscription download %d times", downloads.Load())
	}
	if p.config.Load().RouteReport == nil {
		t.Fatal("report did not survive restore")
	}
}

func TestValidateConfigTestsAuthenticatedProxyAndReturnsSafeFailure(t *testing.T) {
	var requests atomic.Int32
	var sawAuth atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			sawAuth.Store(true)
		}
		w.Header().Set("Proxy-Authenticate", `Basic realm="fixture"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	proxyURL := strings.Replace(proxy.URL, "http://", "http://user:secret@", 1)
	raw, _ := json.Marshal(map[string]any{"proxy_urls": []string{proxyURL}, "route_request": config.RouteRequest{Operation: "test"}})
	normalized, err := newPlugin().ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := config.Parse(normalized)
	if next.RouteReport == nil || len(next.RouteReport.Nodes) != 1 {
		t.Fatalf("missing test rows: %+v", next.RouteReport)
	}
	row := next.RouteReport.Nodes[0]
	if row.Status != "unavailable" || row.ErrorCode != "proxy_auth" {
		t.Fatalf("proxy authentication failure was not classified: %+v", row)
	}
	if requests.Load() == 0 || !sawAuth.Load() {
		t.Fatal("test did not send configured proxy authentication")
	}
	if next.RouteReport.ErrorCode != "NO_AVAILABLE_ROUTES" {
		t.Fatalf("missing overall failure: %+v", next.RouteReport)
	}
	if reportJSON, _ := json.Marshal(next.RouteReport); strings.Contains(string(reportJSON), "secret") {
		t.Fatal("diagnostic report leaked credentials")
	}
}

func TestValidateConfigSubscriptionFailureIsPersistableReport(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"http_error", "credential-in-upstream-error", "SUBSCRIPTION_HTTP_ERROR", 403},
		{"invalid_nodes", "not a proxy subscription", "SUBSCRIPTION_INVALID", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer source.Close()
			raw, _ := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": source.URL + "?token=private"}}, "route_request": config.RouteRequest{Operation: "discover"}})
			normalized, err := newPlugin().ValidateConfig(context.Background(), raw)
			if err != nil {
				t.Fatalf("valid source config must survive a transient discovery failure: %v", err)
			}
			next, _ := config.Parse(normalized)
			if next.RouteReport == nil || next.RouteReport.ErrorCode != tc.want {
				t.Fatalf("report = %+v; want %s", next.RouteReport, tc.want)
			}
			if next.RouteRequest != nil || len(next.RouteReport.Nodes) != 0 {
				t.Fatal("failed action persisted a replay or fake nodes")
			}
			if reportJSON, _ := json.Marshal(next.RouteReport); strings.Contains(string(reportJSON), "private") || strings.Contains(string(reportJSON), tc.body) {
				t.Fatal("raw source error leaked to report")
			}
		})
	}
}

func TestDiagnosticsOnlyApplyPreservesWorkingRegistry(t *testing.T) {
	p := newPlugin()
	if err := p.ApplyConfig(context.Background(), []byte(`{"proxy_urls":["http://127.0.0.1:19804"]}`)); err != nil {
		t.Fatal(err)
	}
	registry, err := p.registryFor(context.Background(), p.config.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	next := *p.config.Load()
	next.Revision++
	next.RouteReport = &config.RouteReport{Schema: 1, SourceSignature: next.SourceSignature(), GeneratedAt: time.Now().UTC().Format(time.RFC3339), Nodes: []config.RouteNode{}}
	raw, _ := json.Marshal(next)
	if err := p.ApplyConfig(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	current, err := p.registryFor(context.Background(), p.config.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	if registry != current || registry.Len() != 1 {
		t.Fatal("diagnostic report save tore down the live route registry")
	}
}

func TestRouteReportRecordsIndividualResultsAndPreservesOtherChecks(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer target.Close()
	transportA := target.Client().Transport.(*http.Transport).Clone()
	transportA.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local fixture has a different target hostname
	transportA.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	transportB := transportA.Clone()
	registry, err := transport.NewRegistryFromRoutes([]transport.Route{{ID: "route-aaaa", Name: "Local A", Protocol: "http", Transport: transportA}, {ID: "route-bbbb", Name: "Local B", Protocol: "socks5", Transport: transportB}}, false, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	c, _ := config.Parse([]byte(`{"proxy_urls":["http://127.0.0.1:19805"]}`))
	c.RouteReport = &config.RouteReport{Schema: 1, SourceSignature: c.SourceSignature(), GeneratedAt: time.Now().UTC().Format(time.RFC3339), Nodes: []config.RouteNode{{ID: "route-bbbb", Name: "Local B", Protocol: "socks5", Status: "unavailable", ErrorCode: "connect"}}}
	report := &config.RouteReport{Schema: 1, SourceSignature: c.SourceSignature(), GeneratedAt: time.Now().UTC().Format(time.RFC3339), Nodes: []config.RouteNode{}}
	got := newPlugin().inspectRegistry(context.Background(), c, config.RouteRequest{Operation: "test", RouteID: "route-aaaa"}, registry, report)
	if len(got.Nodes) != 2 || got.Nodes[0].Status != "available" || got.Nodes[0].HTTPStatus != 401 {
		t.Fatalf("individual check result missing: %+v", got)
	}
	if got.Nodes[1].Status != "unavailable" || got.Nodes[1].ErrorCode != "connect" {
		t.Fatalf("single test overwrote unrelated result: %+v", got.Nodes[1])
	}
}

func TestValidateConfigTimeoutReturnsWithinCallerBudget(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer source.Close()
	raw, _ := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": source.URL}}, "route_request": config.RouteRequest{Operation: "discover"}})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	normalized, err := newPlugin().ValidateConfig(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := config.Parse(normalized)
	if time.Since(started) > time.Second || next.RouteReport == nil || next.RouteReport.ErrorCode != "SOURCE_TIMEOUT" {
		t.Fatalf("timeout budget/report mismatch: %+v", next.RouteReport)
	}
}

func TestDiscoveredFixedSelectionAndReportPersistWithoutRefetch(t *testing.T) {
	var downloads atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write([]byte("http://127.0.0.1:19821\nsocks5://127.0.0.1:19822\n"))
	}))
	defer source.Close()
	p := newPlugin()
	raw, _ := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": source.URL}}, "route_request": config.RouteRequest{Operation: "discover"}})
	normalized, err := p.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyConfig(context.Background(), normalized); err != nil {
		t.Fatal(err)
	}
	selected, _ := config.Parse(normalized)
	selected.RouteMode, selected.FixedRouteID = "fixed", selected.RouteReport.Nodes[1].ID
	raw, _ = json.Marshal(selected)
	selectedJSON, err := p.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("selecting an existing node unexpectedly refetched the subscription")
	}
	restored := newPlugin()
	if err := restored.ApplyConfig(context.Background(), selectedJSON); err != nil {
		t.Fatal(err)
	}
	loaded := restored.config.Load()
	if loaded.RouteRequest != nil || loaded.RouteMode != "fixed" || loaded.FixedRouteID != selected.FixedRouteID || loaded.RouteReport == nil || len(loaded.RouteReport.Nodes) != 2 {
		t.Fatalf("selection/report lost on fresh process Apply: %+v", loaded)
	}
	registry, err := restored.registryFor(context.Background(), loaded, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	_, route, exists := registry.ByID(loaded.FixedRouteID)
	if !exists || route.ID != selected.FixedRouteID {
		t.Fatal("persisted selection does not resolve in live registry")
	}
}

func TestDiagnosticsMissingFixedRouteIsActionableWithCurrentNodes(t *testing.T) {
	p := newPlugin()
	normalized, err := p.ValidateConfig(context.Background(), []byte(`{"proxy_urls":["http://127.0.0.1:19831"],"route_mode":"fixed","fixed_route_id":"route-deadbeef","route_request":{"operation":"test","route_id":"route-deadbeef"}}`))
	if err != nil {
		t.Fatal(err)
	}
	next, _ := config.Parse(normalized)
	if next.RouteRequest != nil || next.RouteReport == nil || next.RouteReport.ErrorCode != "ROUTE_UNAVAILABLE" || len(next.RouteReport.Nodes) != 1 {
		t.Fatalf("missing fixed node did not return repairable selection data: %+v", next.RouteReport)
	}
}

func TestDiscoveredReportSurvivesUIEmptySourceListsAndFixedSelection(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("http://127.0.0.1:19841\nsocks5://127.0.0.1:19842\n"))
	}))
	defer source.Close()
	p := newPlugin()
	raw, _ := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": source.URL}}, "route_request": config.RouteRequest{Operation: "discover"}})
	discovered, err := p.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyConfig(context.Background(), discovered); err != nil {
		t.Fatal(err)
	}
	var uiConfig map[string]any
	if err := json.Unmarshal(discovered, &uiConfig); err != nil {
		t.Fatal(err)
	}
	selectedID := p.config.Load().RouteReport.Nodes[1].ID
	// The browser writes [] for empty textareas; omitempty persisted none.
	uiConfig["proxy_envs"] = []string{}
	uiConfig["proxy_urls"] = []string{}
	uiConfig["route_mode"], uiConfig["fixed_route_id"] = "fixed", selectedID
	uiJSON, _ := json.Marshal(uiConfig)
	selected, err := p.ValidateConfig(context.Background(), uiJSON)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		restored, parseErr := config.Parse(selected)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if restored.RouteReport == nil || len(restored.RouteReport.Nodes) != 2 || restored.FixedRouteID != selectedID {
			t.Fatalf("round %d: report or selected node disappeared after saving UI empty lists", round)
		}
		selected, _ = json.Marshal(restored)
	}
	if err := newPlugin().ApplyConfig(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
}
