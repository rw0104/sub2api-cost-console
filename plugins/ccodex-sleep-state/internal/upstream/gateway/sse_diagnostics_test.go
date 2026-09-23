package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"local.sub2api/ccodex-sleep-state/internal/upstream/settings"
	"local.sub2api/ccodex-sleep-state/internal/upstream/turnstate"
)

func TestProbeStreamClassifiesCodesNotPrivateMessages(t *testing.T) {
	cases := []struct {
		name, body, kind string
		status           int
	}{
		{"capacity", `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"private organization SECRET"}}}`, "model_capacity", 0},
		{"slow down", `{"type":"response.failed","response":{"error":{"code":"slow_down"}}}`, "model_capacity", 0},
		{"rate limit", `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded"}}}`, "upstream_rate_limited", 429},
		{"quota", `{"type":"response.failed","response":{"error":{"code":"insufficient_quota"}}}`, "upstream_rate_limited", 429},
		{"top level error", `{"type":"error","code":"rate_limit_exceeded","message":"SECRET"}`, "upstream_rate_limited", 429},
		{"nested error", `{"type":"error","error":{"code":"server_is_overloaded","message":"SECRET"}}`, "model_capacity", 0},
		{"unknown code", `{"type":"response.failed","response":{"error":{"code":"SECRET-new-code","message":"Selected model is at capacity. Try again in 1 seconds."}}}`, "response_failed", 0},
		{"message not evidence", `{"type":"response.failed","response":{"error":{"message":"401 403 429 serverOverloaded insufficient_quota"}}}`, "response_failed", 0},
		{"unverified auth code", `{"type":"error","code":"authentication_error"}`, "response_failed", 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			done, err := probeStreamOutcome([]byte("data: " + tt.body + "\n\n"))
			failure, ok := err.(*probeStreamError)
			if done || !ok || failure.kind != tt.kind || failure.status != tt.status {
				t.Fatalf("done=%v error=%#v", done, err)
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatal("private upstream value leaked into error")
			}
		})
	}
}

func TestFailedStreamCannotBeOverriddenByCompleted(t *testing.T) {
	for _, stream := range []string{
		"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\ndata: {\"type\":\"response.completed\"}\n\n",
		"data: {\"type\":\"response.completed\"}\n\ndata: {\"type\":\"response.failed\"}\n\n",
	} {
		if completed([]byte(stream)) {
			t.Fatal("failed stream became a usable state")
		}
	}
	// SSE data may span lines; event names can supply the envelope type.
	done, err := probeStreamOutcome([]byte("event: response.failed\r\ndata: {\r\ndata: \"response\": {\"error\": {\"code\": \"slow_down\"}}}\r\n\r\n"))
	if done || probeReason(err) != "model_capacity" {
		t.Fatalf("multiline SSE: done=%v error=%v", done, err)
	}
}

func TestCapacityEndsProbeRoundWithoutExitRotationOrStateHeader(t *testing.T) {
	var calls atomic.Int32
	e, logs := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"SECRET organization details\"}}}\n\n")
	}))
	e.routes = append(e.routes, e.routes[0], e.routes[0])
	w := httptest.NewRecorder()
	e.ServeHTTP(w, request(generation, "capacity-account"))
	if w.Code != 503 || calls.Load() != 1 {
		t.Fatalf("capacity rotated exits: status=%d calls=%d", w.Code, calls.Load())
	}
	status, _ := json.Marshal(e.Status())
	if !strings.Contains(string(status), `"diagnostic":"model_capacity"`) {
		t.Fatalf("wrong diagnostic %s", status)
	}
	if strings.Contains(string(status)+logs.String()+w.Body.String(), "SECRET") {
		t.Fatal("SSE message leaked")
	}
}

func TestStreamRateLimitBlocksAllModelsAndRoutes(t *testing.T) {
	for _, code := range []string{"rate_limit_exceeded", "insufficient_quota"} {
		t.Run(code, func(t *testing.T) {
			var calls atomic.Int32
			e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set(turnstate.Header, fakeToken(10, 81))
				fmt.Fprintf(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":%q}}}\n\n", code)
			}))
			e.routes = append(e.routes, e.routes[0], e.routes[0])
			e.config.StateFallback = "passthrough"
			for _, model := range settings.SupportedModels() {
				w := httptest.NewRecorder()
				e.ServeHTTP(w, request(strings.ReplaceAll(generation, settings.Model, model), "stream-rate-limited"))
				if w.Code != 429 {
					t.Fatalf("SSE account restriction lost for %s: %d", model, w.Code)
				}
			}
			if calls.Load() != 1 || !e.Restricted() {
				t.Fatalf("bypassed SSE rate limit calls=%d", calls.Load())
			}
		})
	}
}

func TestUnknownFailedEventStopsProbeRound(t *testing.T) {
	var calls atomic.Int32
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"future-code\"}}}\n\n")
	}))
	e.routes = append(e.routes, e.routes[0], e.routes[0])
	w := httptest.NewRecorder()
	e.ServeHTTP(w, request(generation, "failed-round-account"))
	if w.Code != 503 || calls.Load() != 1 {
		t.Fatalf("failed event rotated exits: %d %d", w.Code, calls.Load())
	}
	status, _ := json.Marshal(e.Status())
	if !strings.Contains(string(status), `"diagnostic":"response_failed"`) {
		t.Fatal("unknown failure not classified safely")
	}
}
