package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/klauspost/compress/zstd"
	"local.sub2api/ccodex-sleep-state/internal/config"
)

const coreGeneration = `{"model":"gpt-6-astra","input":[{"role":"user","content":"private original prompt"}],"stream":true}`

func coreToken(marker byte) string {
	raw := make([]byte, 57+16*10)
	raw[0] = 0x80
	raw[9] = marker
	binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Unix()))
	return base64.URLEncoding.EncodeToString(raw)
}

func coreFixture(t *testing.T, handler http.HandlerFunc) (*CoreRuntime, *config.Config, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	registry, err := NewRegistryFromRoutes([]Route{{ID: "route-fixture", Name: "Fixture", Protocol: "direct", Transport: server.Client().Transport.(*http.Transport).Clone()}}, false, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	runtime := NewCoreRuntime(func(context.Context, *config.Config, string) (*Registry, error) { return registry, nil }, nil, nil)
	t.Cleanup(runtime.Close)
	cfg, err := config.Parse([]byte(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"fail_closed":true,"max_probes_per_round":1,"state_refresh_mode":"on_demand","models":["gpt-6-astra","gpt-5.6-sol"]}`))
	if err != nil {
		t.Fatal(err)
	}
	return runtime, cfg, server.URL
}

func coreStream(url string, body []byte) *fakeStream {
	return &fakeStream{ctx: context.Background(), requests: []*v1.ForwardRequest{
		{Frame: &v1.ForwardRequest_Start{Start: &v1.ForwardRequestStart{Method: "POST", Url: url, AccountId: 41, Platform: "openai", AccountType: "oauth", HasBody: true, ContentLength: int64(len(body)), Headers: map[string]*v1.HeaderValues{
			"Authorization": {Values: []string{"Bearer fixture-credential-12345"}}, "ChatGPT-Account-Id": {Values: []string{"workspace-1"}}, "Content-Type": {Values: []string{"application/json"}},
			"User-Agent": {Values: []string{"codex_fixture/1"}}, "Version": {Values: []string{"0.154.0"}}, "Originator": {Values: []string{"codex_cli_rs"}}, "OpenAI-Beta": {Values: []string{"responses=experimental"}},
			"Session_id": {Values: []string{"private-session"}}, "Conversation_id": {Values: []string{"private-conversation"}}, "X-Codex-Turn-State": {Values: []string{"client-old-state"}}, "X-Codex-Feature": {Values: []string{"preserve-host-header"}},
		}}}},
		{Frame: &v1.ForwardRequest_BodyChunk{BodyChunk: body}}, {Frame: &v1.ForwardRequest_BodyEnd{BodyEnd: true}},
	}}
}

func coreResult(t *testing.T, runtime *CoreRuntime, cfg *config.Config, s *fakeStream) (int, http.Header, string) {
	t.Helper()
	if err := runtime.Forward(s, cfg); err != nil {
		t.Fatal(err)
	}
	var status int
	headers := http.Header{}
	var body strings.Builder
	ended := false
	for _, frame := range s.responses {
		if fail := frame.GetError(); fail != nil {
			t.Fatalf("framed error: %s sent=%v", fail.Code, fail.RequestSent)
		}
		if start := frame.GetStart(); start != nil {
			status = int(start.StatusCode)
			headers = headersFromWire(start.Headers)
		}
		body.Write(frame.GetBodyChunk())
		if frame.GetEnd() != nil {
			ended = true
		}
	}
	if !ended {
		t.Fatal("response never completed")
	}
	return status, headers, body.String()
}

func TestCoreAdapterCriticalHeadersProbeIsolationAndInjection(t *testing.T) {
	token := coreToken(1)
	var probes, formal atomic.Int32
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		for key, want := range map[string]string{"Authorization": "Bearer fixture-credential-12345", "ChatGPT-Account-Id": "workspace-1", "User-Agent": "codex_fixture/1", "Version": "0.154.0", "Originator": "codex_cli_rs", "OpenAI-Beta": "responses=experimental"} {
			if r.Header.Get(key) != want {
				t.Errorf("critical header %s was not preserved", key)
			}
		}
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Errorf("wrong canonical endpoint: %s", r.URL.Path)
		}
		if bytes.Contains(body, []byte("Reply with OK.")) {
			probes.Add(1)
			for _, key := range []string{"Session_id", "Conversation_id", "X-Codex-Turn-State", "X-Codex-Feature"} {
				if r.Header.Get(key) != "" {
					t.Errorf("probe copied conversation header %s", key)
				}
			}
			if bytes.Contains(body, []byte("private original prompt")) {
				t.Error("probe leaked original body")
			}
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			if payload["parallel_tool_calls"] != true || !bytes.Contains(body, []byte("reasoning.encrypted_content")) {
				t.Error("probe omitted upstream protocol fields")
			}
		} else {
			formal.Add(1)
			if string(body) != coreGeneration || r.Header.Get("X-Codex-Turn-State") != token || r.Header.Get("Session_id") != "private-session" || r.Header.Get("X-Codex-Feature") != "preserve-host-header" {
				t.Error("formal request was not preserved/injected")
			}
			if r.URL.RawQuery != "trace=a%2Fb" {
				t.Error("formal query lost")
			}
		}
		w.Header().Set("X-Codex-Turn-State", token)
		w.Header().Set("Set-Cookie", "not-forwarded=1")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	})
	for i := 0; i < 2; i++ {
		status, headers, body := coreResult(t, runtime, cfg, coreStream(origin+"/backend-api/codex/responses?trace=a%2Fb", []byte(coreGeneration)))
		if status != 200 || headers.Get("X-Codex-Turn-State") != token || headers.Get("Set-Cookie") != "" || !strings.Contains(body, "response.completed") {
			t.Fatalf("response not relayed: status=%d", status)
		}
	}
	if probes.Load() != 1 || formal.Load() != 2 {
		t.Fatalf("probe/formal calls %d/%d", probes.Load(), formal.Load())
	}
	raw, _ := json.Marshal(runtime.Status())
	for _, secret := range []string{token, "fixture-credential", "private-session", "private original prompt"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("runtime status leaked a secret")
		}
	}
}

func TestCoreAdapterLimitsSurviveReloadButNewCredentialIsIsolated(t *testing.T) {
	for _, rejection := range []int{401, 403, 429} {
		t.Run(http.StatusText(rejection), func(t *testing.T) {
			var calls atomic.Int32
			runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(rejection)
			})
			cfg.InjectState = false
			for i := 0; i < 3; i++ {
				if i > 0 {
					runtime.Invalidate()
					cfg.ResponseHeaderTimeoutSeconds++
				}
				payload := strings.ReplaceAll(coreGeneration, "gpt-6-astra", []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-6-astra"}[i])
				s := coreStream(origin+"/backend-api/codex/responses", []byte(payload))
				if i == 0 {
					status, _, _ := coreResult(t, runtime, cfg, s)
					if status != rejection {
						t.Fatalf("upstream status=%d", status)
					}
				} else {
					corePolicyFailure(t, runtime, cfg, s, "upstream_")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("reloaded engine bypassed restriction: calls=%d", calls.Load())
			}
			fresh := coreStream(origin+"/backend-api/codex/responses", []byte(coreGeneration))
			fresh.requests[0].GetStart().Headers["Authorization"].Values = []string{"Bearer fresh-credential-67890"}
			coreResult(t, runtime, cfg, fresh)
			workspace := coreStream(origin+"/backend-api/codex/responses", []byte(coreGeneration))
			workspace.requests[0].GetStart().Headers["ChatGPT-Account-Id"].Values = []string{"workspace-2"}
			coreResult(t, runtime, cfg, workspace)
			if calls.Load() != 3 {
				t.Fatalf("credential/workspace isolation broken: calls=%d", calls.Load())
			}
		})
	}
}

func TestCoreAdapterCompressionAndExpandedBounds(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var calls atomic.Int32
			runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if string(body) != coreGeneration || r.Header.Get("Content-Encoding") != "" {
					t.Error("expanded body mismatch")
				}
				fmt.Fprint(w, "ok")
			})
			cfg.InjectState = false
			compress := func(data []byte) []byte {
				if encoding == "gzip" {
					var b bytes.Buffer
					w := gzip.NewWriter(&b)
					_, _ = w.Write(data)
					_ = w.Close()
					return b.Bytes()
				}
				w, _ := zstd.NewWriter(nil)
				defer w.Close()
				return w.EncodeAll(data, nil)
			}
			s := coreStream(origin+"/responses", compress([]byte(coreGeneration)))
			s.requests[0].GetStart().Headers["Content-Encoding"] = &v1.HeaderValues{Values: []string{encoding}}
			status, _, _ := coreResult(t, runtime, cfg, s)
			if status != 200 {
				t.Fatalf("status=%d", status)
			}
			cfg.MaxBodyBytes = 512
			s = coreStream(origin+"/responses", compress([]byte(strings.Repeat("x", 2048))))
			s.requests[0].GetStart().Headers["Content-Encoding"] = &v1.HeaderValues{Values: []string{encoding}}
			corePolicyFailure(t, runtime, cfg, s, "request_too_large")
			if calls.Load() != 1 {
				t.Fatalf("expanded bound dispatched calls=%d", calls.Load())
			}
		})
	}
}

func TestCoreAdapterCompactionBridgeAndNoProbe(t *testing.T) {
	var calls atomic.Int32
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/backend-api/codex/responses" || !bytes.Contains(body, []byte(`"type":"compaction_trigger"`)) || r.Header.Get("X-Codex-Turn-State") != "client-old-state" {
			t.Error("compaction protocol changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"compaction\",\"encrypted_content\":\"opaque-real-upstream-content\"}]}}\n\n")
	})
	status, headers, body := coreResult(t, runtime, cfg, coreStream(origin+"/backend-api/codex/responses/compact", []byte(`{"model":"gpt-6-astra","input":[],"instructions":"compact"}`)))
	if status != 200 || calls.Load() != 1 || headers.Get("X-Sleep-State-Compaction") != "v1-to-v2" || !strings.Contains(body, "opaque-real-upstream-content") {
		t.Fatalf("compact bridge failed status=%d calls=%d body=%s", status, calls.Load(), body)
	}
}

func TestCoreAdapterShapeFailureNeverReplaysAndHarvestDisabled(t *testing.T) {
	var calls atomic.Int32
	token := coreToken(2)
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			w.Header().Set("X-Codex-Turn-State", token)
		} else {
			w.Header().Set("X-Codex-Turn-State", "invalid-state")
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	})
	s := coreStream(origin+"/responses", []byte(coreGeneration))
	if err := runtime.Forward(s, cfg); err != nil {
		t.Fatal(err)
	}
	if len(s.responses) != 1 || s.responses[0].GetError() == nil || s.responses[0].GetError().Code != "STATE_SHAPE_CHANGED" || !s.responses[0].GetError().RequestSent || calls.Load() != 2 {
		t.Fatalf("unsafe host replay boundary: frames=%d calls=%d", len(s.responses), calls.Load())
	}
	runtime.Invalidate()
	cfg.HarvestOnDemand = false
	corePolicyFailure(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)), "state_unavailable")
	if calls.Load() != 2 {
		t.Fatal("disabled harvest dispatched a probe")
	}
	cfg.InjectState = false
	status, _, _ := coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)))
	if status != 200 || calls.Load() != 3 {
		t.Fatal("ordinary forwarding not available after disabling injection")
	}
}

func TestCoreAdapterTruncatedResponseNeverSendsSuccessEnd(t *testing.T) {
	var calls atomic.Int32
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, "short")
	})
	cfg.InjectState = false
	s := coreStream(origin+"/responses", []byte(coreGeneration))
	if err := runtime.Forward(s, cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, frame := range s.responses {
		if frame.GetEnd() != nil {
			t.Fatal("truncated response got a successful End")
		}
		if failure := frame.GetError(); failure != nil && failure.Code == "UPSTREAM_READ_FAILED" && failure.RequestSent {
			found = true
		}
	}
	if !found || calls.Load() != 1 {
		t.Fatalf("missing no-replay truncation failure, calls=%d", calls.Load())
	}
}

func TestCoreAdapterUpstreamErrorsAndCompactFailuresKeepDistinctReplayBoundary(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprint(compact), func(t *testing.T) {
			var calls atomic.Int32
			runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Sleep-State-Error-Source", "local") // untrusted upstream cannot assert this marker
				if compact {
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
				} else {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":"upstream maintenance"}`)
				}
			})
			cfg.InjectState = false
			if !compact {
				status, header, _ := coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)))
				if status != 503 || header.Get("X-Sleep-State-Error-Source") != "upstream" {
					t.Fatal("upstream rejection converted into local failure")
				}
			} else {
				s := coreStream(origin+"/responses/compact", []byte(`{"model":"gpt-6-astra","input":[]}`))
				if err := runtime.Forward(s, cfg); err != nil {
					t.Fatal(err)
				}
				if len(s.responses) != 1 || s.responses[0].GetError() == nil || !s.responses[0].GetError().RequestSent || s.responses[0].GetError().Code != "COMPACT_INVALID_OUTPUT" {
					t.Fatal("compact failure allowed host replay")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("request replayed %d times", calls.Load())
			}
		})
	}
}

func TestCoreAdapterBufferCapacityWaitHonorsCancellationBeforeReadingBody(t *testing.T) {
	runtime, cfg, origin := coreFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("cancelled request reached upstream") })
	for i := 0; i < cap(runtime.bodySlots); i++ {
		runtime.bodySlots <- struct{}{}
	}
	defer func() {
		for len(runtime.bodySlots) > 0 {
			<-runtime.bodySlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := coreStream(origin+"/responses", []byte(coreGeneration))
	s.ctx = ctx
	if err := runtime.Forward(s, cfg); err != nil {
		t.Fatal(err)
	}
	if s.index != 1 || len(s.responses) != 1 || s.responses[0].GetError() == nil || s.responses[0].GetError().Code != "LOCAL_REQUEST_CAPACITY" || s.responses[0].GetError().RequestSent {
		t.Fatal("cancelled capacity wait read body or dispatched request")
	}
}

func TestCoreAdapterPolicySwitchPreservesSessionStateAndAuthoritativePolicy(t *testing.T) {
	token := coreToken(44)
	var probes, formal atomic.Int32
	var expectInjection atomic.Bool
	expectInjection.Store(true)
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("Reply with OK.")) {
			probes.Add(1)
		} else {
			formal.Add(1)
			want := "client-old-state"
			if expectInjection.Load() {
				want = token
			}
			if r.Header.Get("X-Codex-Turn-State") != want {
				t.Error("stale request config reverted current injection policy")
			}
		}
		w.Header().Set("X-Codex-Turn-State", token)
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	})
	runtime.UpdatePolicies(cfg)
	coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)))
	sessionID := runtime.Status()["sessions"].([]map[string]any)[0]["id"]
	off := *cfg
	off.InjectState = false
	off.StateRefreshMode = "standby"
	runtime.UpdatePolicies(&off)
	expectInjection.Store(false)
	// Simulate a request holding the previous host snapshot while an updated
	// policy has already been published. It may not turn injection back on.
	coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)))
	on := off
	on.InjectState = true
	runtime.UpdatePolicies(&on)
	expectInjection.Store(true)
	coreResult(t, runtime, &on, coreStream(origin+"/responses", []byte(coreGeneration)))
	if probes.Load() != 1 || formal.Load() != 3 || runtime.Status()["sessions"].([]map[string]any)[0]["id"] != sessionID {
		t.Fatal("policy change rebuilt engine or discarded usable state")
	}
}

func TestCoreAdapterPolicySwitchCannotResetProbeCooldown(t *testing.T) {
	var calls atomic.Int32
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	})
	runtime.UpdatePolicies(cfg)
	corePolicyFailure(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)), "state_unavailable")
	off := *cfg
	off.InjectState = false
	runtime.UpdatePolicies(&off)
	on := off
	on.InjectState = true
	on.StateRefreshMode = "standby"
	runtime.UpdatePolicies(&on)
	corePolicyFailure(t, runtime, &on, coreStream(origin+"/responses", []byte(coreGeneration)), "state_unavailable")
	if calls.Load() != 1 {
		t.Fatal("policy toggle reset failed probe cooldown")
	}
}

func TestCoreAdapterSupersededRouteGenerationStopsBackgroundWhileRequestDrains(t *testing.T) {
	runtime, cfg, origin := coreFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("fixture does not dispatch") })
	initial, err := runtime.registry(context.Background(), cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	old, err := runtime.acquire(context.Background(), 41, cfg, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := initial.At(0)
	newRegistry, err := NewRegistryFromRoutes([]Route{{ID: route.ID, Transport: route.Transport.Clone()}}, false, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer newRegistry.Close()
	runtime.registry = func(context.Context, *config.Config, string) (*Registry, error) { return newRegistry, nil }
	current, err := runtime.acquire(context.Background(), 41, cfg, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.release(current)
	defer runtime.release(old)
	if old == current || !old.retired || old.refs != 1 || len(runtime.engines) != 1 {
		t.Fatal("superseded active engine remained in cache")
	}
	select {
	case <-old.done:
	case <-time.After(time.Second):
		t.Fatal("stale background worker was not cancelled while request drained")
	}
}
