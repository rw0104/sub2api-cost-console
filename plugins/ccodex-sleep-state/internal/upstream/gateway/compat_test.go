package gateway

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"local.sub2api/ccodex-sleep-state/internal/upstream/proxyroute"
	"local.sub2api/ccodex-sleep-state/internal/upstream/settings"
	"local.sub2api/ccodex-sleep-state/internal/upstream/turnstate"
)

func relayEngine(t *testing.T, h http.HandlerFunc) *Engine {
	t.Helper()
	upstream := httptest.NewServer(h)
	t.Cleanup(upstream.Close)
	c := settings.Default()
	c.UpstreamKind = "relay"
	c.Upstream = upstream.URL + "/custom/v1/"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	e := New(c, []proxyroute.Route{{ID: "relay-egress", Transport: &http.Transport{}}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(e.Close)
	return e
}

func TestRelayMapsPathsAndPreservesSSEWithoutProbes(t *testing.T) {
	var calls atomic.Int32
	e := relayEngine(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/custom/v1/responses" || r.URL.RawQuery != "mode=test" {
			t.Errorf("URL %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer relay-key-private" {
			t.Error("API key lost")
		}
		for _, key := range []string{turnstate.Header, "ChatGPT-Account-Id", "ChatGPT-Plan", "Cookie", "Proxy-Authorization", "Originator"} {
			if r.Header.Get(key) != "" {
				t.Errorf("leaked %s", key)
			}
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != generation {
			t.Error("generation mutated or probe sent")
		}
		complete(w, fakeToken(11, 1))
	})
	e.SetInjection(true)
	r := request(generation, "relay-key-private")
	r.URL.RawQuery = "mode=test"
	for _, key := range []string{turnstate.Header, "ChatGPT-Account-Id", "ChatGPT-Plan", "Cookie", "Proxy-Authorization", "Originator"} {
		r.Header.Set(key, "must-not-leak")
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "response.completed") || calls.Load() != 1 {
		t.Fatalf("%d %s calls %d", w.Code, w.Body.String(), calls.Load())
	}
	if w.Header().Get(turnstate.Header) != "" {
		t.Error("relay state escaped")
	}
	if e.Status()["injection_enabled"] != false {
		t.Error("relay injection enabled")
	}
}

func TestRelayRejectsOfficialOAuthBeforeTransport(t *testing.T) {
	e := relayEngine(t, func(http.ResponseWriter, *http.Request) { t.Error("official credential leaked") })
	jwt := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"private"}}`)) + ".sig"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, request(generation, jwt))
	if w.Code != 401 || !strings.Contains(w.Body.String(), "official_credentials_on_relay") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRelayQuotaStopSurvivesInjectionToggle(t *testing.T) {
	var calls atomic.Int32
	e := relayEngine(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		e.SetInjection(i == 1)
		e.ServeHTTP(w, request(generation, "relay-api-key"))
		if w.Code != 429 {
			t.Fatal(w.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("quota rejection retried")
	}
}

func TestUpstream503DistinguishedWithoutReplay(t *testing.T) {
	var calls atomic.Int32
	e := relayEngine(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		io.WriteString(w, "upstream busy")
	})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, request(generation, "relay-api-key"))
	if w.Code != 503 || w.Header().Get("X-Sleep-State-Error-Source") != "upstream" || w.Body.String() != "upstream busy" || calls.Load() != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestDisableDuringProbeDoesNotPublishStateOrProbeAgain(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-finish
		complete(w, fakeToken(10, 1))
	}))
	s, err := e.borrow(request(generation, "official-test-token").Header)
	if err != nil {
		t.Fatal(err)
	}
	defer release(s)
	done := make(chan struct{})
	go func() { e.refresh(context.Background(), s, true); close(done) }()
	<-entered
	e.SetInjection(false)
	close(finish)
	<-done
	e.refresh(context.Background(), s, true)
	if e.Status()["injection_enabled"] != false || calls.Load() != 1 {
		t.Fatal("disabled engine probed")
	}
	state, _ := s.state.Acquire(testTokenTime)
	if state.Token.Value != "" {
		t.Fatal("disabled probe published state")
	}
}

func TestUpstreamKindValidation(t *testing.T) {
	for _, tc := range []struct {
		kind, url string
		ok        bool
	}{{"relay", "https://example.com/v1", true}, {"relay", "http://127.0.0.1:1234/v1", true}, {"relay", "http://example.com/v1", false}, {"official", "https://chatgpt.com/v1", false}, {"relay", "https://example.com/v1/../hidden", false}, {"unknown", "https://example.com/backend-api/codex", false}} {
		c := settings.Default()
		c.UpstreamKind = tc.kind
		c.Upstream = tc.url
		if err := c.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
