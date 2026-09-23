package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/upstream/proxyroute"
	"local.sub2api/ccodex-sleep-state/internal/upstream/settings"
)

func TestRequestFixCollectionRoundBudgetLeavesFormalRequestUsable(t *testing.T) {
	var probes, formal atomic.Int32
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("Reply with OK.")) {
			probes.Add(1)
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
			return
		}
		formal.Add(1)
		fmt.Fprint(w, "formal response")
	}))
	e.config.ProbeRoundSeconds = 1
	e.config.ProbeSeconds = 3
	e.config.PoolEnabled = true
	e.config.StateFallback = "passthrough"
	w := httptest.NewRecorder()
	started := time.Now()
	e.ServeHTTP(w, request(generation, "bounded-probe-credential"))
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("round exceeded budget: %v", elapsed)
	}
	if w.Code != 200 || probes.Load() != 1 || formal.Load() != 1 {
		t.Fatalf("status=%d probes=%d formal=%d body=%s", w.Code, probes.Load(), formal.Load(), w.Body.String())
	}
	if e.pool.Get("test-route").State == "failed" {
		t.Fatal("round cancellation poisoned proxy")
	}
}

func TestRequestFixNoStateRoundRobinUsesEveryRoute(t *testing.T) {
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	e.SetInjection(false)
	e.config.RoundRobin = true
	var calls [3]atomic.Int32
	e.routes = nil
	for i := range 3 {
		route := i
		e.routes = append(e.routes, proxyroute.Route{ID: fmt.Sprintf("route-%d", i), Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { calls[route].Add(1); return nil, nil }}})
	}
	for range 6 {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, request(generation, "round-robin-credential"))
		if w.Code != 200 {
			t.Fatalf("status=%d", w.Code)
		}
	}
	for i := range calls {
		if calls[i].Load() != 2 {
			t.Fatalf("route %d calls=%d", i, calls[i].Load())
		}
	}
}

func TestRequestFix403CooldownIsCrossModelButFinite(t *testing.T) {
	var calls atomic.Int32
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	e.SetInjection(false)
	e.config.PoolEnabled = true
	credential := "finite-403-credential"
	for _, model := range []string{settings.Model, "gpt-5.6-sol"} {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, request(strings.ReplaceAll(generation, settings.Model, model), credential))
		if w.Code != 403 {
			t.Fatalf("cross-model pause status=%d", w.Code)
		}
	}
	if calls.Load() != 1 || e.pool.Get("test-route").State == "failed" {
		t.Fatal("403 retried immediately or poisoned the transport route")
	}
	s, err := e.borrow(request(generation, credential).Header)
	if err != nil {
		t.Fatal(err)
	}
	defer release(s)
	s.limit.mu.Lock()
	if s.limit.blocked {
		t.Error("unclassified 403 permanently invalidated credential")
	}
	s.limit.retryUntil = time.Now().Add(-time.Second)
	s.limit.mu.Unlock()
	w := httptest.NewRecorder()
	e.ServeHTTP(w, request(generation, credential))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("cooled-down node not reusable status=%d calls=%d", w.Code, calls.Load())
	}
}

func TestRequestFix401RemainsPermanentCredentialGuard(t *testing.T) {
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	s, err := e.borrow(request(generation, "guard-401-credential").Header)
	if err != nil {
		t.Fatal(err)
	}
	defer release(s)
	e.reject(s, 401, 0, 0)
	s.limit.mu.Lock()
	s.limit.retryUntil = time.Now().Add(-time.Hour)
	s.limit.mu.Unlock()
	if status, _ := s.rejection(); status != 401 {
		t.Fatalf("401 expired incorrectly: %d", status)
	}
}

func TestRequestFix403HonorsLongUpstreamRetryAfter(t *testing.T) {
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	s, err := e.borrow(request(generation, "guard-403-long-retry").Header)
	if err != nil {
		t.Fatal(err)
	}
	defer release(s)
	before := time.Now()
	e.reject(s, 403, 2*time.Hour, 0)
	s.limit.mu.Lock()
	if s.limit.blocked || s.limit.retryUntil.Before(before.Add(2*time.Hour)) {
		t.Errorf("403 pause must be finite and honor server Retry-After: blocked=%v retry=%v", s.limit.blocked, s.limit.retryUntil)
	}
	s.limit.mu.Unlock()
	if status, seconds := s.rejection(); status != 403 || seconds < 7199 {
		t.Fatalf("403 retry delay shortened: status=%d seconds=%d", status, seconds)
	}
}
