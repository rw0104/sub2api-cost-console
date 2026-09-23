package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const compactFixtureRequest = `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"keep original"}]}],"instructions":"keep instructions","parallel_tool_calls":true,"reasoning":{"effort":"low"},"service_tier":"auto","prompt_cache_key":"fixture-key","text":{"verbosity":"low"}}`

func completeCompact(w http.ResponseWriter, opaque string) {
	w.Header().Set("Content-Type", "text/event-stream")
	item := map[string]any{"type": "compaction", "id": "cmp-fixture", "encrypted_content": opaque, "future_field": "preserve"}
	for _, event := range []map[string]any{{"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{item}}}} {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
}

func TestOfficialLegacyCompactionBridgesOnceAndPreservesFields(t *testing.T) {
	var calls atomic.Int32
	const opaque = "opaque-compaction-never-log-this"
	e, logs := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/backend-api/codex/responses" || r.URL.RawQuery != "trace=a%2Fb" {
			t.Error("bridge used incorrect path/query")
		}
		if r.Header.Get("Authorization") != "Bearer bridge-account-token" || r.Header.Get("ChatGPT-Account-Id") != "fixture-account" {
			t.Error("authentication or account changed")
		}
		body, _ := io.ReadAll(r.Body)
		if !remoteCompactionV2(body) {
			t.Error("missing V2 trigger")
		}
		var original, converted map[string]json.RawMessage
		_ = json.Unmarshal([]byte(compactFixtureRequest), &original)
		_ = json.Unmarshal(body, &converted)
		for key, value := range original {
			if key != "input" && string(value) != string(converted[key]) {
				t.Errorf("field %s changed", key)
			}
		}
		var before, after []json.RawMessage
		_ = json.Unmarshal(original["input"], &before)
		_ = json.Unmarshal(converted["input"], &after)
		if len(after) != len(before)+1 || string(before[0]) != string(after[0]) {
			t.Error("original input changed")
		}
		if string(converted["stream"]) != "true" || string(converted["store"]) != "false" {
			t.Error("incorrect streaming envelope")
		}
		completeCompact(w, opaque)
	}))
	r := request(compactFixtureRequest, "bridge-account-token")
	r.URL.Path = "/backend-api/codex/responses/compact"
	r.URL.RawQuery = "trace=a%2Fb"
	r.Header.Set("ChatGPT-Account-Id", "fixture-account")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	var result struct {
		Object string `json:"object"`
		Output []struct {
			Type      string `json:"type"`
			Encrypted string `json:"encrypted_content"`
			Future    string `json:"future_field"`
		} `json:"output"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Object != "response.compaction" || len(result.Output) != 1 || result.Output[0].Encrypted != opaque || result.Output[0].Future != "preserve" {
		t.Fatalf("invalid bridge result %s", w.Body.String())
	}
	if w.Code != 200 || calls.Load() != 1 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("X-Sleep-State-Compaction") != "v1-to-v2" {
		t.Fatalf("status=%d calls=%d headers=%v", w.Code, calls.Load(), w.Header())
	}
	if strings.Contains(logs.String(), opaque) {
		t.Fatal("compaction ciphertext leaked into log")
	}
}

func TestRelayKeepsNativeLegacyCompaction(t *testing.T) {
	var calls atomic.Int32
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/backend-api/codex/responses/compact" || string(body) != compactFixtureRequest {
			t.Error("relay request was bridged")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"response.compaction","output":[]}`)
	}))
	e.config.UpstreamKind = "relay"
	r := request(compactFixtureRequest, "relay-api-key-token")
	r.URL.Path = "/backend-api/codex/responses/compact"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 200 || calls.Load() != 1 || w.Header().Get("X-Sleep-State-Compaction") != "" {
		t.Fatalf("status=%d calls=%d", w.Code, calls.Load())
	}
}

func TestLegacyBridgeNeverInventsSuccess(t *testing.T) {
	cases := []struct {
		name, stream string
		status       int
	}{
		{"no completion", `data: {"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"opaque"}}` + "\n\n", 502},
		{"no compaction", `data: {"type":"response.completed","response":{"output":[]}}` + "\n\n", 502},
		{"empty cipher", `data: {"type":"response.completed","response":{"output":[{"type":"compaction","encrypted_content":""}]}}` + "\n\n", 502},
		{"duplicate compaction", `data: {"type":"response.completed","response":{"output":[{"type":"compaction","encrypted_content":"a"},{"type":"compaction","encrypted_content":"b"}]}}` + "\n\n", 502},
		{"failed", `data: {"type":"response.failed","response":{"error":{"code":"future-error","message":"private-upstream-message"}}}` + "\n\n", 502},
		{"capacity", `data: {"type":"response.failed","response":{"error":{"code":"server_is_overloaded"}}}` + "\n\n", 503},
		{"rate limit", `data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded"}}}` + "\n\n", 429},
		{"oversize", strings.Repeat("x", maxCompactBytes+1), 502},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			e, logs := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tt.stream)
			}))
			r := request(compactFixtureRequest, "failed-bridge-token")
			r.URL.Path = "/backend-api/codex/responses/compact"
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			if w.Code != tt.status || calls.Load() != 1 {
				t.Fatalf("status=%d calls=%d", w.Code, calls.Load())
			}
			if strings.Contains(w.Body.String()+logs.String(), "private-upstream-message") {
				t.Fatal("private SSE error leaked")
			}
			if tt.status == 429 {
				w = httptest.NewRecorder()
				e.ServeHTTP(w, request(strings.ReplaceAll(generation, "gpt-6-astra", "gpt-5.6-sol"), "failed-bridge-token"))
				if w.Code != 429 || calls.Load() != 1 {
					t.Fatal("bridge rate limit did not protect another model")
				}
			}
		})
	}
}

func TestLegacyBridgeHonorsContextCancellation(t *testing.T) {
	entered := make(chan struct{})
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := request(compactFixtureRequest, "cancel-bridge-token").WithContext(ctx)
	r.URL.Path = "/backend-api/codex/responses/compact"
	done := make(chan struct{})
	go func() { defer close(done); e.ServeHTTP(httptest.NewRecorder(), r) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge ignored request cancellation")
	}
}

func TestCompactOutputCanUseStreamedItems(t *testing.T) {
	raw := []byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"opaque\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	output, err := compactOutput(raw)
	if err != nil || len(output) != 1 {
		t.Fatalf("output=%v err=%v", output, err)
	}
}

func TestCompressedLegacyCompactionUsesV2Bridge(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var calls atomic.Int32
			e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/backend-api/codex/responses" || !remoteCompactionV2(body) || r.Header.Get("Content-Encoding") != "" {
					t.Error("compressed V1 did not become a plain V2 request")
				}
				completeCompact(w, "opaque-compressed-fixture")
			}))
			wire := encodeRequest(t, encoding, []byte(compactFixtureRequest))
			r := request(string(wire), "compressed-v1-token")
			r.URL.Path = "/backend-api/codex/responses/compact"
			r.Header.Set("Content-Encoding", encoding)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			if w.Code != 200 || calls.Load() != 1 || !strings.Contains(w.Body.String(), "response.compaction") {
				t.Fatalf("status=%d calls=%d", w.Code, calls.Load())
			}
		})
	}
}
