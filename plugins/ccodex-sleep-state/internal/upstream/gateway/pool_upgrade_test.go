package gateway

import (
	"bytes"
	"context"
	"io"
	"local.sub2api/ccodex-sleep-state/internal/upstream/proxyroute"
	"local.sub2api/ccodex-sleep-state/internal/upstream/turnstate"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfiguredLargeRequestAccepted(t *testing.T) {
	called := 0
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{}`))
	}))
	e.SetInjection(false)
	body := `{"model":"gpt-6-astra","input":"` + strings.Repeat("x", 17<<20) + `"}`
	for _, encoding := range []string{"gzip", "zstd"} {
		r := request(string(encodeRequest(t, encoding, []byte(body))), "fixture-large-key")
		r.Header.Set("Content-Encoding", encoding)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if called != 2 {
		t.Fatal(called)
	}
}
func TestShapeFailurePromotesStandbyWithoutReplay(t *testing.T) {
	calls := 0
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set(turnstate.Header, fakeToken(11, 3))
		w.Write([]byte("blocked-body"))
	}))
	r := request(generation, "fixture-promote-key")
	s, err := e.borrow(r.Header)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := turnstate.Parse(fakeToken(10, 1))
	b, _ := turnstate.Parse(fakeToken(10, 2))
	s.state.Offer(a, 0, time.Now())
	s.state.Offer(b, 0, time.Now())
	release(s)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 503 || bytes.Contains(w.Body.Bytes(), []byte("blocked-body")) || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
	active, ok := s.state.Acquire(time.Now())
	if !ok || active.Token.Value != b.Value {
		t.Fatal("standby not ready for next request")
	}
}
func TestRandomEgressExcludesHarvestAndParked(t *testing.T) {
	e, _ := testEngine(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	e.routes = append(e.routes, proxyroute.Route{ID: "b", Transport: &http.Transport{}}, proxyroute.Route{ID: "c", Transport: &http.Transport{}})
	e.config.EgressMode = "random"
	e.config.PoolEnabled = true
	e.pool.Change([]string{"b"}, "used", "test", false)
	for i := 0; i < 10; i++ {
		route, err := e.selectEgress(0, true)
		if err != nil || route != 2 {
			t.Fatal(route, err)
		}
	}
	e.pool.Change([]string{"c"}, "failed", "test", false)
	if _, err := e.selectEgress(0, true); err == nil {
		t.Fatal("exhausted pool reused")
	}
	e.config.EgressMode = "fixed"
	e.config.EgressRoute = "b"
	if route, err := e.selectEgress(0, true); err != nil || route != 1 {
		t.Fatal(route, err)
	}
}
func TestSingleNodeProbeKeepsExistingActive(t *testing.T) {
	calls := 0
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; complete(w, fakeToken(10, 2)) }))
	r := request(generation, "fixture-single-key")
	s, _ := e.borrow(r.Header)
	a, _ := turnstate.Parse(fakeToken(10, 1))
	s.state.Offer(a, 0, time.Now())
	s.activated = true
	release(s)
	if err := e.RetryState(context.Background(), s.id, "test-route"); err != nil {
		t.Fatal(err)
	}
	active, ok := s.state.Acquire(time.Now())
	if !ok || active.Token.Value != a.Value || !s.state.Status(time.Now()).Ready || calls != 1 {
		t.Fatal("active lost or standby missing")
	}
	if err := e.RetryState(context.Background(), s.id, "test-route"); err == nil {
		t.Fatal("cooldown bypassed")
	}
	if calls != 1 {
		t.Fatal("retried early")
	}
}

func TestDisabledNodeNeverDispatchesInLegacyMode(t *testing.T) {
	calls := 0
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{}`)) }))
	e.SetInjection(false)
	if err := e.pool.Change([]string{e.routes[0].ID}, "disabled", "manual", false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/backend-api/codex/responses", "/backend-api/codex/responses/compact", "/backend-api/codex/models"} {
		r := request(compactFixtureRequest, "fixture-disabled-key")
		r.URL.Path = path
		if strings.HasSuffix(path, "/models") {
			r.Method = http.MethodGet
			r.Body = nil
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "pool_node_disabled") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
