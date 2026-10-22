package service

// Regression tests for the protection-transport comparison baseline.
//
// The protection transport plugin (openai.oauth.protection_transport.v1) reads
// ForwardRequestStart.OriginalBodyJson (the baseline) and
// ForwardRequestStart.AccountMetadataJson (mapped model + default instructions
// digest). The host fills both from the request context, so every real
// forwarding entry point must call withProtectionOriginal right before it
// builds the outbound request. An upstream merge once dropped those calls and
// every /responses request was then denied by the plugin's integrity check.
//
// These tests drive the real entry points with an in-memory fake plugin
// transport and assert what the plugin actually receives. No test here uses
// the network, a child process or the real direct HTTP upstream.
//
// The file is intentionally untagged: every fixture it reuses lives in untagged
// files, so it runs under both `go test` and `go test -tags unit`.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// baselineProbeAlias is the model name clients send; the account maps it to
	// baselineProbeMapped. The mapping target is a fixed point of the account
	// mapping and of the Codex model normalization, because the host derives
	// mapped_model from the already-final request body.
	baselineProbeAlias  = "client-model"
	baselineProbeMapped = "gpt-5.4"
)

// ---------------------------------------------------------------------------
// Fake protection-transport plugin (in-memory pluginv2.TransportClient).
// ---------------------------------------------------------------------------

var (
	_ pluginv2.TransportClient                                                    = (*baselineProbeTransport)(nil)
	_ grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse] = (*baselineProbeStream)(nil)
)

// baselineProbeReply is the canned upstream response the fake plugin returns.
type baselineProbeReply struct {
	status  int32
	headers map[string][]string
	chunks  [][]byte
}

// baselineProbeCall is what the fake plugin observed for one Forward stream.
type baselineProbeCall struct {
	start *pluginv1.ForwardRequestStart
	body  []byte
}

type baselineProbeTransport struct {
	mu      sync.Mutex
	reply   baselineProbeReply
	streams []*baselineProbeStream
}

func (t *baselineProbeTransport) Forward(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse], error) {
	stream := &baselineProbeStream{ctx: ctx, reply: t.reply, requestDone: make(chan struct{})}
	t.mu.Lock()
	t.streams = append(t.streams, stream)
	t.mu.Unlock()
	return stream, nil
}

func (t *baselineProbeTransport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.streams)
}

func (t *baselineProbeTransport) call(index int) baselineProbeCall {
	t.mu.Lock()
	stream := t.streams[index]
	t.mu.Unlock()
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return baselineProbeCall{start: stream.start, body: bytes.Clone(stream.body.Bytes())}
}

type baselineProbeStream struct {
	ctx   context.Context
	reply baselineProbeReply

	mu          sync.Mutex
	start       *pluginv1.ForwardRequestStart
	body        bytes.Buffer
	sent        int
	requestDone chan struct{}
	doneOnce    sync.Once
}

func (s *baselineProbeStream) Send(request *pluginv1.ForwardRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start := request.GetStart(); start != nil {
		if s.start != nil {
			return errors.New("baseline probe: duplicate start frame")
		}
		s.start = start
	}
	if chunk := request.GetBodyChunk(); len(chunk) > 0 {
		_, _ = s.body.Write(chunk)
	}
	if request.GetBodyEnd() {
		s.doneOnce.Do(func() { close(s.requestDone) })
	}
	return nil
}

// Recv answers only after the complete request body was received, like a real
// plugin that reads the request before it talks to the upstream.
func (s *baselineProbeStream) Recv() (*pluginv1.ForwardResponse, error) {
	select {
	case <-s.requestDone:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.sent
	s.sent++
	switch {
	case index == 0:
		headers := make(map[string]*pluginv1.HeaderValues, len(s.reply.headers))
		for key, values := range s.reply.headers {
			headers[key] = &pluginv1.HeaderValues{Values: append([]string(nil), values...)}
		}
		return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
			StatusCode:    s.reply.status,
			Status:        fmt.Sprintf("%d %s", s.reply.status, http.StatusText(int(s.reply.status))),
			Protocol:      "HTTP/1.1",
			ProtocolMajor: 1,
			ProtocolMinor: 1,
			Headers:       headers,
			ContentLength: -1,
		}}}, nil
	case index <= len(s.reply.chunks):
		return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: bytes.Clone(s.reply.chunks[index-1])}}, nil
	case index == len(s.reply.chunks)+1:
		return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{BytesReceived: int64(s.body.Len())}}}, nil
	default:
		return nil, io.EOF
	}
}

func (s *baselineProbeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *baselineProbeStream) Trailer() metadata.MD         { return nil }
func (s *baselineProbeStream) CloseSend() error             { return nil }
func (s *baselineProbeStream) Context() context.Context     { return s.ctx }
func (s *baselineProbeStream) SendMsg(any) error {
	return errors.New("baseline probe: SendMsg is not supported")
}
func (s *baselineProbeStream) RecvMsg(any) error {
	return errors.New("baseline probe: RecvMsg is not supported")
}

// baselineProbeSSEEvent renders one `event:` + `data:` pair of a Responses SSE stream.
func baselineProbeSSEEvent(t *testing.T, name string, payload map[string]any) []byte {
	t.Helper()
	payload["type"] = name
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return []byte("event: " + name + "\ndata: " + string(raw) + "\n\n")
}

// baselineProbeSSEReply returns a valid Responses SSE stream (created, one text
// delta, completed with usage and created_at) as separate body chunks.
func baselineProbeSSEReply(t *testing.T, model string) baselineProbeReply {
	t.Helper()
	const createdAt = 1700000000
	response := func(status string, output []any) map[string]any {
		return map[string]any{
			"id": "resp_baseline_probe", "object": "response", "created_at": createdAt,
			"model": model, "status": status, "output": output,
		}
	}
	message := map[string]any{
		"type": "message", "id": "msg_baseline_probe", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}}},
	}
	completed := response("completed", []any{message})
	completed["usage"] = map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}
	return baselineProbeReply{
		status:  http.StatusOK,
		headers: map[string][]string{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"rid_baseline_probe"}},
		chunks: [][]byte{
			baselineProbeSSEEvent(t, "response.created", map[string]any{"response": response("in_progress", []any{})}),
			baselineProbeSSEEvent(t, "response.output_text.delta", map[string]any{
				"item_id": "msg_baseline_probe", "output_index": 0, "content_index": 0, "delta": "ok",
			}),
			baselineProbeSSEEvent(t, "response.completed", map[string]any{"response": completed}),
		},
	}
}

// ---------------------------------------------------------------------------
// Plugin manager / service / account fixtures.
// ---------------------------------------------------------------------------

type baselineProbePlugin struct {
	manager   *PluginManager
	transport *baselineProbeTransport
}

// newBaselineProbePlugin publishes a protection-transport route bound to
// openai/oauth with RolloutPercent=100 and an in-process runtime (nil
// hashicorp client) whose transport is the fake. A non-empty accountIDs narrows
// the binding scope to those accounts.
func newBaselineProbePlugin(t *testing.T, reply baselineProbeReply, accountIDs ...int64) *baselineProbePlugin {
	t.Helper()
	capability := protectionTestCapability()
	installation := &PluginInstallation{
		ID:       1,
		Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{capability}},
		Bindings: []PluginBinding{{
			Capability: capability.ID, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth,
			Enabled: true, RolloutPercent: 100, AccountIDs: accountIDs,
		}},
	}
	fake := &baselineProbeTransport{reply: reply}
	runtime := &pluginRuntime{installation: installation, transport: fake, done: make(chan struct{})}
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	manager.publishRuntimeLocked(installation, runtime)
	return &baselineProbePlugin{manager: manager, transport: fake}
}

// newBaselineProbeUpstream is the real direct upstream stand-in. Tests whose
// plugin handles the request must never reach it, so by default it fails loudly.
func newBaselineProbeUpstream() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{err: errors.New("baseline probe: direct httpUpstream must not be used")}
}

func newBaselineProbeService(upstream *httpUpstreamRecorder, plugin *baselineProbePlugin) *OpenAIGatewayService {
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{ForceCodexCLI: false}},
		httpUpstream: upstream,
	}
	if plugin != nil {
		svc.SetPluginManager(plugin.manager)
	}
	return svc
}

func newBaselineProbeOAuthAccount(id int64, extra map[string]any) *Account {
	return &Account{
		ID: id, Name: "baseline-probe-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1, RateMultiplier: f64p(1),
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
			"model_mapping":      map[string]any{baselineProbeAlias: baselineProbeMapped},
		},
		Extra: extra,
	}
}

func newBaselineProbeContext(path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
	return c, rec
}

// requireBaselineProbeStart asserts the contract of a protection-transport call:
// the plugin was called once, the direct upstream never, and the Start frame
// carries a valid baseline plus the account metadata derived from it.
//
// wantBodyModel pins the final request body model when the entry point is known
// to rewrite it ("" only checks consistency with the wire body).
func requireBaselineProbeStart(
	t *testing.T,
	plugin *baselineProbePlugin,
	upstream *httpUpstreamRecorder,
	account *Account,
	wantBodyModel string,
) baselineProbeCall {
	t.Helper()
	require.Equal(t, 1, plugin.transport.callCount(), "the protection plugin must be called exactly once")
	require.Empty(t, upstream.requests, "the direct httpUpstream must not be used when the protection transport handles the request")
	require.Nil(t, upstream.lastReq)
	status := plugin.manager.extensionStatus(1)
	require.Len(t, status, 1)
	require.EqualValues(t, 1, status[0].Calls)
	require.Zero(t, status[0].Errors)
	require.Zero(t, status[0].Denied)

	call := plugin.transport.call(0)
	start := call.start
	require.NotNil(t, start)
	require.Equal(t, http.MethodPost, start.Method)
	require.Equal(t, account.ID, start.AccountId)
	require.Equal(t, PlatformOpenAI, start.Platform)
	require.Equal(t, AccountTypeOAuth, start.AccountType)
	require.True(t, start.HasBody)

	// The wire body is what the plugin would forward upstream.
	wireModel := gjson.GetBytes(call.body, "model").String()
	require.NotEmpty(t, wireModel, "the outbound request body must carry a model")
	if wantBodyModel != "" {
		require.Equal(t, wantBodyModel, wireModel)
	}

	// Baseline: present, valid JSON, same model as the final request body.
	require.NotEmpty(t, start.OriginalBodyJson, "OriginalBodyJson must carry the host baseline; an empty baseline makes the plugin deny every request")
	require.True(t, json.Valid(start.OriginalBodyJson), "OriginalBodyJson must be valid JSON: %s", start.OriginalBodyJson)
	require.Equal(t, wireModel, gjson.GetBytes(start.OriginalBodyJson, "model").String())
	require.JSONEq(t, string(call.body), string(start.OriginalBodyJson), "the baseline must be the final request body")

	// Account metadata: mapped model and default instructions digest.
	require.NotEmpty(t, start.AccountMetadataJson)
	accountMeta, err := pluginv2.DecodeProtectionAccount(start.AccountMetadataJson)
	require.NoError(t, err)
	require.Equal(t, account.ID, accountMeta.ID)
	require.NotEmpty(t, accountMeta.MappedModel, "mapped_model must be derived from the baseline")
	require.Equal(t, baselineProbeMapped, accountMeta.MappedModel)
	digest := sha256.Sum256([]byte(defaultCodexSynthInstructions(baselineProbeMapped)))
	require.Equal(t, hex.EncodeToString(digest[:]), accountMeta.DefaultInstructionsDigest)
	return call
}

func requireBaselineProbeBypassed(t *testing.T, plugin *baselineProbePlugin, upstream *httpUpstreamRecorder) {
	t.Helper()
	require.Zero(t, plugin.transport.callCount(), "the protection plugin must not be called for an unmatched account")
	require.NotNil(t, upstream.lastReq, "the direct httpUpstream must be used for an unmatched account")
	require.Len(t, upstream.requests, 1)
	require.Nil(t, upstream.lastReq.Context().Value(pluginProtectionOriginalKey{}),
		"no protection baseline may be attached for an unmatched account")
}

// ---------------------------------------------------------------------------
// A. forwardOpenAIPassthrough
// ---------------------------------------------------------------------------

func TestProtectionBaselinePassthroughForwardsBaselineToPlugin(t *testing.T) {
	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
	upstream := newBaselineProbeUpstream()
	svc := newBaselineProbeService(upstream, plugin)
	account := newBaselineProbeOAuthAccount(701, map[string]any{"openai_passthrough": true})
	body := []byte(`{"model":"` + baselineProbeAlias + `","stream":true,"store":true,"instructions":"probe-instructions","input":[{"type":"input_text","text":"hi"}]}`)
	c, rec := newBaselineProbeContext("/v1/responses", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 200, rec.Code)

	passthrough, _ := c.Get("openai_passthrough")
	require.Equal(t, true, passthrough, "the request must have taken the passthrough branch")
	// Passthrough does not rewrite the model: the baseline model is the client's
	// body model while mapped_model carries the account mapping target.
	call := requireBaselineProbeStart(t, plugin, upstream, account, baselineProbeAlias)
	require.Equal(t, baselineProbeAlias, gjson.GetBytes(call.start.OriginalBodyJson, "model").String())
}

// ---------------------------------------------------------------------------
// B. Forward (non-passthrough OAuth Responses)
// ---------------------------------------------------------------------------

func TestProtectionBaselineForwardForwardsBaselineToPlugin(t *testing.T) {
	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
	upstream := newBaselineProbeUpstream()
	svc := newBaselineProbeService(upstream, plugin)
	account := newBaselineProbeOAuthAccount(702, map[string]any{"openai_passthrough": false})
	body := []byte(`{"model":"` + baselineProbeAlias + `","stream":false,"instructions":"probe-instructions","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	c, rec := newBaselineProbeContext("/v1/responses", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 200, rec.Code)

	passthrough, _ := c.Get("openai_passthrough")
	require.Nil(t, passthrough, "the request must not have taken the passthrough branch")
	// The Forward loop sends the account-mapped, Codex-normalized model.
	call := requireBaselineProbeStart(t, plugin, upstream, account, baselineProbeMapped)
	require.NotEqual(t, baselineProbeAlias, gjson.GetBytes(call.start.OriginalBodyJson, "model").String())
}

// A protection-transport account must not leave the plugin path for a
// WebSocket upstream, even when its configuration would select WS v2.
func TestProtectionBaselineForwardKeepsHTTPWhenAccountWouldUseWSv2(t *testing.T) {
	newFixture := func(plugin *baselineProbePlugin) (*OpenAIGatewayService, *httpUpstreamRecorder, *openAIWSCaptureDialer, *Account) {
		cfg := newOpenAIWSV2TestConfig()
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
		cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
		cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
		cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
		cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
		cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
		cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
		conn := &openAIWSCaptureConn{events: [][]byte{
			[]byte(`{"type":"response.completed","response":{"id":"resp_ws_probe","model":"` + baselineProbeMapped + `","usage":{"input_tokens":1,"output_tokens":1}}}`),
		}}
		dialer := &openAIWSCaptureDialer{conn: conn}
		pool := newOpenAIWSConnPool(cfg)
		pool.setClientDialerForTest(dialer)
		upstream := newBaselineProbeUpstream()
		svc := &OpenAIGatewayService{
			cfg:              cfg,
			httpUpstream:     upstream,
			cache:            &stubGatewayCache{},
			openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
			toolCorrector:    NewCodexToolCorrector(),
			openaiWSPool:     pool,
		}
		if plugin != nil {
			svc.SetPluginManager(plugin.manager)
		}
		account := newBaselineProbeOAuthAccount(703, map[string]any{"responses_websockets_v2_enabled": true})
		return svc, upstream, dialer, account
	}
	body := []byte(`{"model":"` + baselineProbeAlias + `","stream":false,"instructions":"probe-instructions","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)

	t.Run("control without plugin dials WebSocket v2", func(t *testing.T) {
		svc, upstream, dialer, account := newFixture(nil)
		c, _ := newBaselineProbeContext("/v1/responses", body)

		result, err := svc.Forward(context.Background(), c, account, body)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, result.OpenAIWSMode, "the fixture must really select WS v2, otherwise the plugin cases prove nothing")
		require.Equal(t, 1, dialer.DialCount())
		require.Empty(t, upstream.requests)
		transport, _ := c.Get("openai_ws_transport_decision")
		require.Equal(t, string(OpenAIUpstreamTransportResponsesWebsocketV2), transport)
	})

	for _, tc := range []struct {
		name          string
		clientHTTP    bool
		wantReasonSet string
	}{
		{name: "client transport unknown", clientHTTP: false, wantReasonSet: "account_protection_plugin"},
		{name: "http inbound", clientHTTP: true, wantReasonSet: "account_protection_plugin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
			svc, upstream, dialer, account := newFixture(plugin)
			c, rec := newBaselineProbeContext("/v1/responses", body)
			if tc.clientHTTP {
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.OpenAIWSMode, "a protection-transport account must use the HTTP upstream")
			require.Equal(t, 200, rec.Code)
			require.Zero(t, dialer.DialCount(), "no WebSocket dial may bypass the protection plugin")

			transport, _ := c.Get("openai_ws_transport_decision")
			reason, _ := c.Get("openai_ws_transport_reason")
			require.Equal(t, string(OpenAIUpstreamTransportHTTPSSE), transport)
			require.Equal(t, tc.wantReasonSet, reason)
			requireBaselineProbeStart(t, plugin, upstream, account, baselineProbeMapped)
		})
	}
}

// ---------------------------------------------------------------------------
// C. forwardAsChatCompletions
// ---------------------------------------------------------------------------

func TestProtectionBaselineChatCompletionsForwardsBaselineToPlugin(t *testing.T) {
	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
	upstream := newBaselineProbeUpstream()
	svc := newBaselineProbeService(upstream, plugin)
	account := newBaselineProbeOAuthAccount(704, nil)
	body := []byte(`{"model":"` + baselineProbeAlias + `","stream":false,"messages":[{"role":"user","content":"hello"}]}`)
	c, rec := newBaselineProbeContext("/v1/chat/completions", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "ok", gjson.Get(rec.Body.String(), "choices.0.message.content").String())

	// The baseline is the converted Responses body, not the Chat Completions input.
	call := requireBaselineProbeStart(t, plugin, upstream, account, baselineProbeMapped)
	require.False(t, gjson.GetBytes(call.start.OriginalBodyJson, "messages").Exists(),
		"the baseline must be the converted Responses body, not the inbound Chat Completions body")
	require.True(t, gjson.GetBytes(call.start.OriginalBodyJson, "input").Exists())
}

// ---------------------------------------------------------------------------
// D. WebSocket HTTP bridge
// ---------------------------------------------------------------------------

func TestProtectionBaselineWSHTTPBridgeTurnForwardsBaselineToPlugin(t *testing.T) {
	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
	upstream := newBaselineProbeUpstream()
	svc := newBaselineProbeService(upstream, plugin)
	svc.cfg = &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	account := newBaselineProbeOAuthAccount(705, nil)
	// The ingress maps the model before the bridge sees the payload.
	payload := []byte(`{"type":"response.create","model":"` + baselineProbeMapped + `","stream":true,"input":"hi"}`)
	c, _ := newBaselineProbeContext("/v1/responses", nil)

	var clientMessages [][]byte
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "test-token", payload, len(payload),
		baselineProbeMapped, "", "", "", "", 1,
		func(message []byte) error {
			clientMessages = append(clientMessages, bytes.Clone(message))
			return nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.OpenAIWSMode)
	require.NotEmpty(t, clientMessages)

	call := requireBaselineProbeStart(t, plugin, upstream, account, baselineProbeMapped)
	// The bridge turns the WS frame into an HTTP body: no WS-only fields remain.
	require.False(t, gjson.GetBytes(call.start.OriginalBodyJson, "type").Exists())
	require.True(t, gjson.GetBytes(call.start.OriginalBodyJson, "stream").Bool())
}

func TestProtectionBaselineWSIngressBridgeForwardsBaselineToPlugin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
	upstream := newBaselineProbeUpstream()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
	svc := &OpenAIGatewayService{
		cfg:                cfg,
		httpUpstream:       upstream,
		cache:              &stubGatewayCache{},
		openaiWSResolver:   NewOpenAIWSProtocolResolver(cfg),
		openaiWSStateStore: NewOpenAIWSStateStore(nil),
		toolCorrector:      NewCodexToolCorrector(),
	}
	svc.SetPluginManager(plugin.manager)
	// No openai_oauth_responses_websockets_v2_mode: the protection transport
	// alone must force the HTTP bridge, otherwise the plugin would be bypassed.
	account := newBaselineProbeOAuthAccount(706, nil)

	serverErr := make(chan error, 1)
	turnResult := make(chan *OpenAIForwardResult, 1)
	hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
		if turnErr == nil && result != nil {
			turnResult <- result
		}
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		request := r.Clone(r.Context())
		request.Header = request.Header.Clone()
		request.Header.Set("User-Agent", "unit-test-agent/1.0")
		ginCtx.Request = request
		readCtx, cancelRead := context.WithTimeout(r.Context(), 5*time.Second)
		_, first, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErr <- readErr
			return
		}
		serverErr <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "test-token", first, hooks)
	}))
	defer server.Close()

	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	require.NoError(t, client.Write(ctx, coderws.MessageText,
		[]byte(`{"type":"response.create","model":"`+baselineProbeAlias+`","stream":false,"input":"hi"}`)))
	for completed := false; !completed; {
		_, event, readErr := client.Read(ctx)
		require.NoError(t, readErr)
		completed = gjson.GetBytes(event, "type").String() == "response.completed"
	}
	// The ingress loop blocks on the next client frame until the client closes.
	_ = client.Close(coderws.StatusNormalClosure, "done")
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the websocket ingress to finish")
	}
	select {
	case result := <-turnResult:
		require.True(t, result.OpenAIWSMode)
		require.Equal(t, 3, result.Usage.InputTokens)
	case <-time.After(2 * time.Second):
		t.Fatal("no turn result callback")
	}

	// The ingress maps the model before bridging; the baseline is the bridged body.
	call := requireBaselineProbeStart(t, plugin, upstream, account, baselineProbeMapped)
	require.False(t, gjson.GetBytes(call.start.OriginalBodyJson, "type").Exists())
}

// ---------------------------------------------------------------------------
// E. Counter-examples: no baseline when the protection transport does not match.
// ---------------------------------------------------------------------------

func baselineProbeDirectSSEUpstream(t *testing.T) *httpUpstreamRecorder {
	t.Helper()
	var stream bytes.Buffer
	for _, chunk := range baselineProbeSSEReply(t, baselineProbeMapped).chunks {
		_, _ = stream.Write(chunk)
	}
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid_direct"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}}
}

func TestProtectionBaselineNotAttachedWhenBindingExcludesAccount(t *testing.T) {
	// The binding only covers account 999, so account 707 is not protected.
	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped), 999)
	upstream := baselineProbeDirectSSEUpstream(t)
	svc := newBaselineProbeService(upstream, plugin)
	account := newBaselineProbeOAuthAccount(707, nil)
	body := []byte(`{"model":"` + baselineProbeAlias + `","stream":false,"instructions":"probe-instructions","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	c, _ := newBaselineProbeContext("/v1/responses", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	requireBaselineProbeBypassed(t, plugin, upstream)
}

func TestProtectionBaselineNotAttachedForAPIKeyAccount(t *testing.T) {
	// The route is bound to all OAuth accounts; an API key account is never protected.
	plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"rid_apikey"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_apikey","model":"gpt-5.2","usage":{"input_tokens":1,"output_tokens":1}}`)),
	}}
	svc := newBaselineProbeService(upstream, plugin)
	account := &Account{
		ID: 708, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
		Extra:       map[string]any{"openai_passthrough": true}, Status: StatusActive, Schedulable: true,
	}
	body := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)
	c, _ := newBaselineProbeContext("/v1/responses", body)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	requireBaselineProbeBypassed(t, plugin, upstream)
}

func TestProtectionBaselineHelperAttachesOnlyForMatchedAccounts(t *testing.T) {
	body := []byte(`{"model":"` + baselineProbeAlias + `","input":"hi"}`)
	matched := newBaselineProbeOAuthAccount(709, nil)
	apiKey := &Account{ID: 710, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	snapshot := func(ctx context.Context) (pluginProtectionOriginal, bool) {
		value, ok := ctx.Value(pluginProtectionOriginalKey{}).(pluginProtectionOriginal)
		return value, ok
	}

	t.Run("matched oauth account gets a cloned baseline", func(t *testing.T) {
		plugin := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
		svc := newBaselineProbeService(newBaselineProbeUpstream(), plugin)
		payload := bytes.Clone(body)
		ctx := svc.withProtectionOriginal(context.Background(), matched, payload)
		value, ok := snapshot(ctx)
		require.True(t, ok)
		require.Equal(t, matched.ID, value.accountID)
		require.JSONEq(t, string(body), string(value.body))
		require.Equal(t, baselineProbeMapped, value.mappedModel)
		payload[0] = 'X'
		require.JSONEq(t, string(body), string(value.body), "the baseline must not alias the caller's buffer")
	})
	t.Run("unmatched accounts and missing managers keep the context untouched", func(t *testing.T) {
		scoped := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped), 999)
		all := newBaselineProbePlugin(t, baselineProbeSSEReply(t, baselineProbeMapped))
		for name, tc := range map[string]struct {
			svc     *OpenAIGatewayService
			account *Account
		}{
			"no plugin manager":      {svc: newBaselineProbeService(newBaselineProbeUpstream(), nil), account: matched},
			"nil service":            {svc: nil, account: matched},
			"binding excludes":       {svc: newBaselineProbeService(newBaselineProbeUpstream(), scoped), account: matched},
			"api key account":        {svc: newBaselineProbeService(newBaselineProbeUpstream(), all), account: apiKey},
			"nil account with route": {svc: newBaselineProbeService(newBaselineProbeUpstream(), all), account: nil},
		} {
			base := context.Background()
			ctx := tc.svc.withProtectionOriginal(base, tc.account, body)
			_, ok := snapshot(ctx)
			require.False(t, ok, name)
			require.Equal(t, base, ctx, name)
		}
	})
}
