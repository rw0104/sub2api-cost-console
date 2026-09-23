package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"google.golang.org/grpc"
	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/upstream/gateway"
	coreRoute "local.sub2api/ccodex-sleep-state/internal/upstream/proxyroute"
	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
)

// CoreRuntime adapts the upstream HTTP/SSE gateway to the host's framed stream.
// Engines retain the upstream account/workspace/model isolation. Their immutable
// route snapshots are retired only after all active requests have finished.
type CoreRuntime struct {
	mu         sync.Mutex
	engines    map[string]*coreEngine
	limits     *gateway.CredentialLimits
	registry   func(context.Context, *config.Config, string) (*Registry, error)
	invalidate func(*config.Config, string)
	pool       *routepool.Store
	bodySlots  chan struct{}
	closed     bool
	policies   *config.Config
}

type coreEngine struct {
	engine  *gateway.Engine
	account int64
	cancel  context.CancelFunc
	done    chan struct{}
	refs    int
	retired bool
	used    time.Time
}

func NewCoreRuntime(registry func(context.Context, *config.Config, string) (*Registry, error), invalidate func(*config.Config, string), pool *routepool.Store) *CoreRuntime {
	if pool == nil {
		pool, _ = routepool.Open("")
	}
	return &CoreRuntime{engines: make(map[string]*coreEngine), limits: gateway.NewCredentialLimits(), registry: registry, invalidate: invalidate, pool: pool, bodySlots: make(chan struct{}, 4)}
}

func (c *CoreRuntime) acquire(ctx context.Context, account int64, cfg *config.Config, fallback, upstream string) (*coreEngine, error) {
	registry, err := c.registry(ctx, cfg, fallback)
	if err != nil {
		return nil, errors.New("route source unavailable")
	}
	settings := cfg.CoreSettings(upstream)
	settings.RequestLimitExactBytes = int64(cfg.MaxBodyBytes)
	settings.AllowedModels = append([]string(nil), cfg.Models...)
	identity := settings
	identity.InjectionDisabled = false
	identity.StateRefreshMode = ""
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(append(encoded, []byte(fmt.Sprintf("\x00%d\x00%p\x00%d\x00%s\x00%t", account, registry, cfg.MaxBodyBytes, strings.Join(cfg.Models, "\x00"), settings.HarvestDisabled))...))
	key := hex.EncodeToString(digest[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("runtime closed")
	}
	if current := c.engines[key]; current != nil {
		c.applyPoliciesLocked(current, cfg)
		current.refs++
		current.used = time.Now()
		return current, nil
	}
	// Bounds active immutable generations. Account limit guards live outside
	// this cache, so eviction cannot reset rejected credentials.
	for existingKey, item := range c.engines {
		if item.account == account || (item.refs == 0 && time.Since(item.used) > 30*time.Minute) {
			c.retireLocked(item)
			delete(c.engines, existingKey)
		}
	}
	if len(c.engines) >= 128 {
		return nil, errors.New("local engine capacity")
	}
	routes := make([]coreRoute.Route, 0, registry.Len())
	for i := 0; i < registry.Len(); i++ {
		route, ok := registry.At(i)
		if !ok || route.Transport == nil {
			return nil, errors.New("route snapshot expired")
		}
		node := nodeInfo(route)
		routes = append(routes, coreRoute.Route{ID: route.ID, StableID: route.ID, DisplayName: node.Name, Protocol: node.Protocol, Transport: route.Transport})
	}
	if len(routes) == 0 {
		return nil, errors.New("no routes")
	}
	engine := gateway.New(settings, routes, slog.New(slog.NewTextHandler(io.Discard, nil)), c.pool)
	engine.ShareCredentialLimits(c.limits)
	background, cancel := context.WithCancel(context.Background())
	item := &coreEngine{engine: engine, account: account, cancel: cancel, done: make(chan struct{}), refs: 1, used: time.Now()}
	c.applyPoliciesLocked(item, cfg)
	c.engines[key] = item
	go func() { defer close(item.done); engine.Run(background) }()
	return item, nil
}

// UpdatePolicies applies the same atomic switches offered by upstream without
// discarding active/standby state or session cooldown. The supplied config is an
// immutable host snapshot. Publishing it here before the host config swap keeps
// old in-flight request snapshots from reverting a newer policy on cache hits.
func (c *CoreRuntime) UpdatePolicies(cfg *config.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policies = cfg
	for _, item := range c.engines {
		c.applyPoliciesLocked(item, cfg)
	}
}

func (c *CoreRuntime) applyPoliciesLocked(item *coreEngine, fallback *config.Config) {
	policy := fallback
	if c.policies != nil {
		effective := c.policies.ForAccount(item.account)
		policy = &effective
	}
	if policy == nil {
		return
	}
	item.engine.SetInjection(policy.Enabled && policy.InjectState)
	item.engine.SetRefreshMode(policy.StateRefreshMode)
}

func (c *CoreRuntime) retireLocked(item *coreEngine) {
	item.retired = true
	item.cancel()
	if item.refs == 0 {
		go func() { <-item.done; item.engine.Close() }()
	}
}

func (c *CoreRuntime) release(item *coreEngine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item.refs--
	if item.retired && item.refs == 0 {
		go func() { <-item.done; item.engine.Close() }()
	}
}

// Invalidate retires state/route generations without clearing account limits.
func (c *CoreRuntime) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, item := range c.engines {
		c.retireLocked(item)
		delete(c.engines, key)
	}
}

func (c *CoreRuntime) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for key, item := range c.engines {
		c.retireLocked(item)
		delete(c.engines, key)
	}
}

func (c *CoreRuntime) Status() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	sessions := []map[string]any{}
	poolRows := []gateway.PoolRow{}
	seen := map[string]bool{}
	var requests uint64
	for _, item := range c.engines {
		// Marshal only upstream's explicitly safe status, never private engine
		// state, credential digests, request headers, or captured turn-state.
		status := item.engine.Status()
		if count, ok := status["requests_total"].(uint64); ok {
			requests += count
		}
		raw, _ := json.Marshal(status["sessions"])
		var rows []map[string]any
		_ = json.Unmarshal(raw, &rows)
		for _, row := range rows {
			row["account_id"] = item.account
			sessions = append(sessions, row)
		}
		for _, row := range item.engine.PoolStatus() {
			if !seen[row.ID] {
				seen[row.ID] = true
				poolRows = append(poolRows, row)
			}
		}
	}
	return map[string]any{"engines": len(c.engines), "sessions": sessions, "pool": poolRows, "requests_total": requests, "state_storage": "memory", "transport": "http-sse", "node_verifications": c.nodeVerificationReportsLocked()}
}

func (c *CoreRuntime) Retry(ctx context.Context, sessionID, routeID string) error {
	c.mu.Lock()
	var selected *coreEngine
	for _, item := range c.engines {
		raw, _ := json.Marshal(item.engine.Status()["sessions"])
		var sessions []struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &sessions)
		for _, session := range sessions {
			if session.ID == sessionID && sessionID != "" {
				selected = item
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected != nil {
		selected.refs++
	}
	c.mu.Unlock()
	if selected == nil {
		return errors.New("找不到运行中的会话；先发送实际请求，再刷新核心状态")
	}
	defer c.release(selected)
	if routeID != "" {
		return selected.engine.RetryState(ctx, sessionID, routeID)
	}
	return selected.engine.RetryState(ctx, sessionID)
}

// coreTarget accepts the host's canonical URL plus its historical shortened
// /responses form, while preserving the exact upstream origin and query.
func coreTarget(raw, method string) (*url.URL, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(u.Path, "\\\r\n") || strings.Contains(u.Path, "..") {
		return nil, "", errors.New("invalid target")
	}
	var suffix string
	for _, allowed := range []string{"/responses/compact", "/responses", "/models", "/alpha/search"} {
		if strings.HasSuffix(u.Path, allowed) {
			suffix = allowed
			break
		}
	}
	if suffix == "" || ((suffix == "/models") != (method == http.MethodGet)) || (suffix != "/models" && method != http.MethodPost) {
		return nil, "", errors.New("unsupported endpoint")
	}
	base := *u
	base.Path = strings.TrimSuffix(u.Path, suffix)
	base.RawQuery = ""
	base.ForceQuery = false
	u.Path = "/backend-api/codex" + suffix
	return u, strings.TrimSuffix(base.String(), "/"), nil
}

func (c *CoreRuntime) Forward(stream grpc.BidiStreamingServer[v1.ForwardRequest, v1.ForwardResponse], cfg *config.Config) (err error) {
	fail := func(code string, sent bool, messages ...string) error {
		message := "ccodex-sleep-state transport failed"
		if len(messages) > 0 && messages[0] != "" {
			message = messages[0]
		}
		return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Error{Error: &v1.ForwardResponseError{Code: code, Message: message, RequestSent: sent}}})
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil || start.Platform != "openai" || start.AccountType != "oauth" || start.AccountId <= 0 {
		return fail("PROTECTION_DENIED", false)
	}
	if cfg == nil {
		return fail("CONFIG_REQUIRED", false)
	}
	effective := cfg.ForAccount(start.AccountId)
	cfg = &effective
	u, upstream, err := coreTarget(start.Url, start.Method)
	if err != nil {
		return fail("INVALID_TARGET", false)
	}
	if len(start.ProxyUrl) > 8192 || start.ContentLength < -1 || start.ContentLength > int64(cfg.MaxBodyBytes) {
		return fail("BODY_LIMIT", false)
	}
	select {
	case c.bodySlots <- struct{}{}:
		defer func() { <-c.bodySlots }()
	case <-stream.Context().Done():
		return fail("LOCAL_REQUEST_CAPACITY", false)
	}
	var body bytes.Buffer
	for {
		frame, err := stream.Recv()
		if err != nil {
			return fail("INCOMPLETE_REQUEST", false)
		}
		switch f := frame.Frame.(type) {
		case *v1.ForwardRequest_BodyChunk:
			if body.Len()+len(f.BodyChunk) > cfg.MaxBodyBytes || (!start.HasBody && len(f.BodyChunk) > 0) {
				return fail("BODY_LIMIT", false)
			}
			body.Write(f.BodyChunk)
		case *v1.ForwardRequest_BodyEnd:
			if !f.BodyEnd {
				return fail("INVALID_REQUEST", false)
			}
			goto complete
		default:
			return fail("INVALID_REQUEST", false)
		}
	}
complete:
	if start.ContentLength >= 0 && start.ContentLength != int64(body.Len()) {
		return fail("INCOMPLETE_REQUEST", false)
	}
	item, err := c.acquire(stream.Context(), start.AccountId, cfg, start.ProxyUrl, upstream)
	if err != nil {
		return fail("TRANSPORT_CONFIG", false)
	}
	defer c.release(item)
	requestContext := context.WithValue(stream.Context(), http.ServerContextKey, &http.Server{})
	var dispatched atomic.Bool
	requestContext = gateway.WithDispatchObserver(requestContext, func() { dispatched.Store(true) })
	r, err := http.NewRequestWithContext(requestContext, start.Method, u.String(), bytes.NewReader(body.Bytes()))
	if err != nil {
		return fail("INVALID_REQUEST", false)
	}
	r.Header = headersFromWire(start.Headers)
	r.GetBody = nil // The user's generation must never be replayed.
	r.Host = start.Host
	w := &coreStreamWriter{stream: stream, header: make(http.Header), started: time.Now(), dispatched: &dispatched}
	// ReverseProxy intentionally aborts on a truncated upstream response. A
	// framed Error, never a successful End, preserves that distinction to host.
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered != http.ErrAbortHandler {
				panic(recovered)
			}
			err = fail("UPSTREAM_READ_FAILED", true)
		}
	}()
	item.engine.ServeHTTP(w, r)
	if w.err != nil {
		return w.err
	}
	if w.localFailure {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.failureBody.Bytes(), &failure)
		code := strings.ToUpper(failure.Error.Code)
		if code == "" {
			code = "UPSTREAM_FAILED"
		}
		if code == "UPSTREAM_UNAVAILABLE" {
			code = "UPSTREAM_FAILED"
		}
		// The host maps PROTECTION_BUSY to a terminal preprocessing error.
		// Reporting local policy as an upstream HTTP 503 caused account failover
		// and hid the actual reason, even though no formal request was sent.
		if !dispatched.Load() {
			code = "PROTECTION_BUSY"
		}
		message := failure.Error.Code + ": " + failure.Error.Message
		return fail(code, dispatched.Load(), message)
	}
	w.WriteHeader(http.StatusOK)
	if w.err != nil {
		return w.err
	}
	return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_End{End: &v1.ForwardResponseEnd{BytesReceived: w.received, DurationMs: time.Since(w.started).Milliseconds()}}})
}

type coreStreamWriter struct {
	mu           sync.Mutex
	stream       grpc.BidiStreamingServer[v1.ForwardRequest, v1.ForwardResponse]
	header       http.Header
	wrote        bool
	err          error
	received     int64
	started      time.Time
	dispatched   *atomic.Bool
	localFailure bool
	failureBody  bytes.Buffer
}

func (w *coreStreamWriter) Header() http.Header { return w.header }
func (w *coreStreamWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeHeader(status)
}
func (w *coreStreamWriter) writeHeader(status int) {
	if w.wrote || w.err != nil || status < 200 {
		return
	}
	w.wrote = true
	if w.header.Get("X-Sleep-State-Error-Source") == "local" {
		w.localFailure = true
		return
	}
	length := int64(-1)
	if raw := w.header.Get("Content-Length"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			length = parsed
		}
	}
	w.err = w.stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Start{Start: &v1.ForwardResponseStart{StatusCode: int32(status), Status: strconv.Itoa(status) + " " + http.StatusText(status), Protocol: "HTTP/1.1", ProtocolMajor: 1, ProtocolMinor: 1, Headers: headersToWire(w.header), ContentLength: length}}})
}
func (w *coreStreamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeHeader(http.StatusOK)
	if w.err != nil {
		return 0, w.err
	}
	n := len(data)
	if w.localFailure {
		if w.failureBody.Len() < 64*1024 {
			w.failureBody.Write(data[:min(len(data), 64*1024-w.failureBody.Len())])
		}
		return n, nil
	}
	for len(data) > 0 {
		size := min(32*1024, len(data))
		w.err = w.stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_BodyChunk{BodyChunk: append([]byte(nil), data[:size]...)}})
		if w.err != nil {
			return n - len(data), w.err
		}
		w.received += int64(size)
		data = data[size:]
	}
	return n, nil
}
func (w *coreStreamWriter) Flush() { w.WriteHeader(http.StatusOK) }
