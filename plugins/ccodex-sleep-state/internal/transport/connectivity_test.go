package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectivityCountsUsableRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	registry := &Registry{routes: []Route{
		{ID: "available", Transport: http.DefaultTransport.(*http.Transport).Clone()},
		{ID: "missing", Transport: &http.Transport{Proxy: http.ProxyURL(mustURL(t, "http://127.0.0.1:1"))}},
	}}
	defer registry.Close()
	report, err := testConnectivity(context.Background(), registry, server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || report.Available != 1 {
		t.Fatalf("report = %+v, want 1/2", report)
	}
}

func TestConnectivityShowsRestrictedHTTPWithoutMisreportingNetworkFailure(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		registry := &Registry{routes: []Route{{ID: "test-route", Name: "Test node", Protocol: "http", Transport: http.DefaultTransport.(*http.Transport).Clone()}}}
		report, err := testConnectivity(context.Background(), registry, server.URL, time.Second)
		registry.Close()
		server.Close()
		if err != nil || report.Available != 0 || report.Reachable != 1 || len(report.Rows) != 1 || report.Rows[0].HTTPStatus != status || report.Rows[0].Status != "restricted" {
			t.Fatalf("HTTP %d report=%+v err=%v", status, report, err)
		}
	}
}

func TestConnectivitySelectedNodeAndCancelledRows(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	registry := &Registry{routes: []Route{
		{ID: "route-one", Name: "First", Protocol: "http", Transport: http.DefaultTransport.(*http.Transport).Clone()},
		{ID: "route-two", Name: "Second", Protocol: "http", Transport: http.DefaultTransport.(*http.Transport).Clone()},
	}}
	defer registry.Close()
	report, err := checkRoutes(context.Background(), registry, "route-two", server.URL, time.Second)
	if err != nil || report.Total != 1 || len(report.Rows) != 1 || report.Rows[0].ID != "route-two" || calls.Load() != 1 {
		t.Fatalf("selected route report=%+v err=%v calls=%d", report, err, calls.Load())
	}
	if _, err := checkRoutes(context.Background(), registry, "missing", server.URL, time.Second); err == nil {
		t.Fatal("missing route was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = checkRoutes(ctx, registry, "", server.URL, time.Second)
	if err == nil || len(report.Rows) != 2 {
		t.Fatalf("cancelled report=%+v err=%v", report, err)
	}
	for _, row := range report.Rows {
		if row.Status != "cancelled" || row.ErrorCode != "cancelled" {
			t.Fatalf("cancelled row=%+v", row)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("cancelled checks made new requests")
	}
}

func TestConnectivityDiagnosticsDoNotExposeProxyCredentials(t *testing.T) {
	registry, err := NewRegistry([]string{"http://diagnostic-user:diagnostic-secret@127.0.0.1:1"}, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	report, _ := testConnectivity(context.Background(), registry, "https://example.test", time.Second)
	data, _ := json.Marshal(report)
	for _, secret := range []string{"diagnostic-user", "diagnostic-secret", "127.0.0.1", "example.test"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("connection diagnostic contains private connection data")
		}
	}
	if len(report.Rows) != 1 || report.Rows[0].ErrorCode != "connect" {
		t.Fatalf("report=%+v", report)
	}
}
