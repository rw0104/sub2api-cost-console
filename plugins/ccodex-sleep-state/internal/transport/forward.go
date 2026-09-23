package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/turnstate"
)

var ErrStateUnavailable = errors.New("no valid turn-state available")

type StateStore interface {
	Acquire(accountID int64, model string, policy turnstate.Policy, now time.Time) (turnstate.Snapshot, bool)
	Offer(accountID int64, model, value string, route int, policy turnstate.Policy, now time.Time) bool
	Observe(accountID int64, model, value string, used turnstate.Snapshot, policy turnstate.Policy, now time.Time) bool
	NeedsRefresh(accountID int64, model string, policy turnstate.Policy, now time.Time) bool
}

type Telemetry interface {
	Telemetry(context.Context, string, int64)
	Emit(context.Context, string)
}

type Handler struct {
	Config           func() *config.Config
	States           StateStore
	Limits           *Limits
	Metric           func(context.Context, string, int64)
	Event            func(context.Context, string)
	TransportFactory func(string, int) (*http.Transport, error)
	Registry         func(*config.Config, string) (*Registry, error)
	RegistryContext  func(context.Context, *config.Config, string) (*Registry, error)
	InvalidateRoutes func(*config.Config, string)
}

func (h *Handler) Forward(stream grpc.BidiStreamingServer[v1.ForwardRequest, v1.ForwardResponse]) error {
	ctx := stream.Context()
	fail := func(code string, sent bool) error {
		if h.Metric != nil {
			h.Metric(ctx, "errors", 1)
		}
		return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Error{Error: &v1.ForwardResponseError{
			Code: code, Message: "ccodex-sleep-state transport failed", RequestSent: sent,
		}}})
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil || start.Platform != "openai" || start.AccountType != "oauth" || start.AccountId <= 0 {
		return fail("PROTECTION_DENIED", false)
	}
	c := h.Config()
	if c == nil {
		return fail("CONFIG_REQUIRED", false)
	}
	effective := c.ForAccount(start.AccountId)
	c = &effective
	if h.Limits != nil {
		h.Limits.SetCooldown(time.Duration(c.CooldownSeconds) * time.Second)
		if status, _ := h.Limits.Check(start.AccountId, time.Now()); status != 0 {
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return fail("UPSTREAM_AUTH_REJECTED", false)
			}
			return fail("UPSTREAM_RATE_LIMITED", false)
		}
	}
	if len(start.ProxyUrl) > 8192 || start.ContentLength < -1 || start.ContentLength > int64(c.MaxBodyBytes) {
		return fail("INVALID_REQUEST", false)
	}
	u, err := url.Parse(start.Url)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fail("INVALID_TARGET", false)
	}

	var body bytes.Buffer
	for {
		frame, recvErr := stream.Recv()
		if recvErr != nil {
			return fail("INCOMPLETE_REQUEST", false)
		}
		switch f := frame.Frame.(type) {
		case *v1.ForwardRequest_BodyChunk:
			if body.Len()+len(f.BodyChunk) > c.MaxBodyBytes || (!start.HasBody && len(f.BodyChunk) > 0) {
				return fail("BODY_LIMIT", false)
			}
			_, _ = body.Write(f.BodyChunk)
		case *v1.ForwardRequest_BodyEnd:
			if !f.BodyEnd {
				return fail("INVALID_REQUEST", false)
			}
			goto bodyComplete
		default:
			return fail("INVALID_REQUEST", false)
		}
	}

bodyComplete:
	if start.ContentLength >= 0 && start.ContentLength != int64(body.Len()) {
		return fail("INCOMPLETE_REQUEST", false)
	}
	payload := body.Bytes()
	headers := headersFromWire(start.Headers)
	model := requestModel(payload)
	generation := start.Method == http.MethodPost && isGenerationPath(u.Path)
	if generation && !c.SupportsModel(model) {
		return fail("UNSUPPORTED_MODEL", false)
	}
	var registry *Registry
	var cleanupRegistry func()
	if h.Registry != nil || h.TransportFactory == nil {
		registry, cleanupRegistry, err = h.getRegistry(ctx, c, start.ProxyUrl)
		if err != nil {
			return fail("TRANSPORT_CONFIG", false)
		}
		if cleanupRegistry != nil {
			defer cleanupRegistry()
		}
	}

	statePolicy, policyOK := policyForConfig(c)
	var usedState turnstate.Snapshot
	if generation && c.Enabled && c.InjectState && h.States != nil {
		if !policyOK {
			return fail("CONFIG_REQUIRED", false)
		}
		var ok bool
		usedState, ok = h.States.Acquire(start.AccountId, model, statePolicy, time.Now())
		if c.HarvestOnDemand && (!ok || h.States.NeedsRefresh(start.AccountId, model, statePolicy, time.Now())) {
			if !h.probe(ctx, start, headers, model, c, statePolicy, registry) && registry != nil && h.InvalidateRoutes != nil {
				h.InvalidateRoutes(c, start.ProxyUrl)
			}
			usedState, ok = h.States.Acquire(start.AccountId, model, statePolicy, time.Now())
		}
		if ok {
			headers.Set(turnstate.Header, usedState.Token.Value)
		} else if c.FailClosed {
			if h.Limits != nil {
				if status, _ := h.Limits.Check(start.AccountId, time.Now()); status == http.StatusUnauthorized || status == http.StatusForbidden {
					return fail("UPSTREAM_AUTH_REJECTED", false)
				} else if status == http.StatusTooManyRequests {
					return fail("UPSTREAM_RATE_LIMITED", false)
				}
			}
			if h.Event != nil {
				h.Event(ctx, "policy.deny")
			}
			return fail("STATE_UNAVAILABLE", false)
		}
	}

	var selected Route
	if registry != nil {
		var ok bool
		if usedState.Token.Value != "" {
			selected, ok = registry.At(usedState.Route)
		} else if c.RouteMode == config.DefaultRouteMode || c.RouteMode == "" {
			_, selected, ok = registry.Next()
		} else {
			_, selected, ok = registry.ByID(c.FixedRouteID)
		}
		if !ok || selected.Transport == nil {
			if h.InvalidateRoutes != nil {
				h.InvalidateRoutes(c, start.ProxyUrl)
			}
			return fail("ROUTE_UNAVAILABLE", false)
		}
	} else {
		tr, transportErr := h.transport(start.ProxyUrl, c.ResponseHeaderTimeoutSeconds)
		if transportErr != nil {
			return fail("TRANSPORT_CONFIG", false)
		}
		selected = Route{Transport: tr}
		defer tr.CloseIdleConnections()
	}
	request, err := http.NewRequestWithContext(ctx, start.Method, start.Url, bytes.NewReader(payload))
	if err != nil {
		return fail("INVALID_REQUEST", false)
	}
	request.Host = start.Host
	request.GetBody = nil
	request.Header = headers
	request.Header.Del("Content-Length")
	request.Header.Del("Transfer-Encoding")
	request.Header.Del("Proxy-Authorization")
	client := &http.Client{Transport: selected.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if registry != nil && h.InvalidateRoutes != nil {
			h.InvalidateRoutes(c, start.ProxyUrl)
		}
		return fail("UPSTREAM_FAILED", true)
	}
	defer response.Body.Close()
	if h.Limits != nil {
		h.Limits.Record(start.AccountId, response.StatusCode, RetryAfter(response.Header.Get("Retry-After"), time.Now()), time.Now())
	}
	if h.States != nil && generation && c.Enabled && c.InjectState && usedState.Token.Value != "" {
		rawState := response.Header.Get(turnstate.Header)
		suspect := h.States.Observe(start.AccountId, model, rawState, usedState, statePolicy, time.Now())
		if response.StatusCode >= 200 && response.StatusCode < 300 && suspect {
			if h.Event != nil {
				h.Event(ctx, "policy.deny")
			}
			return fail("STATE_SHAPE_CHANGED", true)
		}
	}
	if err = stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Start{Start: &v1.ForwardResponseStart{
		StatusCode: int32(response.StatusCode), Status: response.Status, Protocol: response.Proto,
		ProtocolMajor: int32(response.ProtoMajor), ProtocolMinor: int32(response.ProtoMinor),
		Headers: headersToWire(response.Header), ContentLength: response.ContentLength,
	}}}); err != nil {
		return err
	}
	var received int64
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			received += int64(n)
			if sendErr := stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_BodyChunk{BodyChunk: append([]byte(nil), buffer[:n]...)}}); sendErr != nil {
				return sendErr
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fail("UPSTREAM_READ_FAILED", true)
		}
	}
	if h.Metric != nil {
		h.Metric(ctx, "requests", 1)
		h.Metric(ctx, "latency_ms", time.Since(started).Milliseconds())
	}
	if h.Event != nil {
		h.Event(ctx, "policy.pass")
	}
	return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_End{End: &v1.ForwardResponseEnd{
		BytesReceived: received, DurationMs: time.Since(started).Milliseconds(),
	}}})
}

func (h *Handler) probe(ctx context.Context, start *v1.ForwardRequestStart, headers http.Header, model string, c *config.Config, policy turnstate.Policy, registry *Registry) bool {
	u, err := url.Parse(start.Url)
	if err != nil {
		return false
	}
	u.Path = strings.TrimSuffix(u.Path, "/compact")
	probeBody, err := json.Marshal(map[string]any{
		"model": model, "instructions": "Reply with OK.",
		"input":  []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Reply with OK."}}}},
		"stream": true, "store": false,
	})
	if err != nil {
		return false
	}
	if registry != nil {
		if c.RouteMode == "fixed" {
			routeIndex, route, ok := registry.ByID(c.FixedRouteID)
			if !ok || route.Transport == nil {
				return false
			}
			accepted, _ := h.probeRoute(ctx, start, headers, model, c, policy, u, probeBody, route.Transport, routeIndex)
			return accepted
		}
		attempts := registry.Len()
		for range attempts {
			routeIndex, route, ok := registry.Next()
			if !ok || route.Transport == nil {
				return false
			}
			accepted, blocked := h.probeRoute(ctx, start, headers, model, c, policy, u, probeBody, route.Transport, routeIndex)
			if accepted || blocked {
				return accepted
			}
		}
		return false
	}
	proxyText := c.ProxyURL
	if proxyText == "" {
		proxyText = start.ProxyUrl
	}
	tr, err := h.transport(proxyText, c.ResponseHeaderTimeoutSeconds)
	if err != nil {
		return false
	}
	defer tr.CloseIdleConnections()
	accepted, _ := h.probeRoute(ctx, start, headers, model, c, policy, u, probeBody, tr, 0)
	return accepted
}

func (h *Handler) probeRoute(ctx context.Context, start *v1.ForwardRequestStart, headers http.Header, model string, c *config.Config, policy turnstate.Policy, u *url.URL, probeBody []byte, tr *http.Transport, routeIndex int) (accepted, blocked bool) {
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(c.ProbeTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, u.String(), bytes.NewReader(probeBody))
	if err != nil {
		return false, false
	}
	req.Host = start.Host
	req.Header = headers.Clone()
	req.Header.Del("Content-Length")
	req.Header.Del("Transfer-Encoding")
	req.Header.Del("Proxy-Authorization")
	req.Header.Del(turnstate.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	response, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return false, false
	}
	defer response.Body.Close()
	if h.Limits != nil {
		h.Limits.Record(start.AccountId, response.StatusCode, RetryAfter(response.Header.Get("Retry-After"), time.Now()), time.Now())
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		return false, true
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	completed, streamStatus := probeStreamOutcome(data)
	if streamStatus != 0 && h.Limits != nil {
		h.Limits.Record(start.AccountId, streamStatus, RetryAfter(response.Header.Get("Retry-After"), time.Now()), time.Now())
	}
	if streamStatus == http.StatusTooManyRequests {
		return false, true
	}
	if response.StatusCode != http.StatusOK || readErr != nil || len(data) > 1<<20 || !completed {
		return false, false
	}
	state := response.Header.Get(turnstate.Header)
	if state == "" || h.States == nil || !h.States.Offer(start.AccountId, model, state, routeIndex, policy, time.Now()) {
		return false, false
	}
	return true, false
}

func policyForConfig(c *config.Config) (turnstate.Policy, bool) {
	blocks, ok := turnstate.BlocksForEncodedLength(c.StateTargetLength)
	if !ok {
		return turnstate.Policy{}, false
	}
	return turnstate.Policy{Blocks: blocks, TTL: time.Duration(c.StateTTLSeconds) * time.Second, Refresh: time.Duration(c.RefreshBeforeSeconds) * time.Second}, true
}

func (h *Handler) transport(proxyText string, headerTimeoutSeconds int) (*http.Transport, error) {
	if h.TransportFactory != nil {
		return h.TransportFactory(proxyText, headerTimeoutSeconds)
	}
	return buildTransport(proxyText, headerTimeoutSeconds)
}

func (h *Handler) getRegistry(ctx context.Context, c *config.Config, fallback string) (*Registry, func(), error) {
	if h.RegistryContext != nil {
		registry, err := h.RegistryContext(ctx, c, fallback)
		return registry, nil, err
	}
	if h.Registry != nil {
		registry, err := h.Registry(c, fallback)
		return registry, nil, err
	}
	urls := c.RouteURLs()
	if len(urls) == 0 && !c.Direct && strings.TrimSpace(fallback) != "" {
		urls = []string{fallback}
	}
	registry, err := NewRegistry(urls, c.Direct, c.ResponseHeaderTimeoutSeconds)
	if err != nil {
		return nil, nil, err
	}
	return registry, registry.Close, nil
}

func probeStreamOutcome(data []byte) (completed bool, rejectedStatus int) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	for _, block := range bytes.Split(data, []byte("\n\n")) {
		var eventName string
		var payload []byte
		for _, line := range bytes.Split(block, []byte("\n")) {
			switch {
			case bytes.HasPrefix(line, []byte("event:")):
				eventName = strings.TrimSpace(string(bytes.TrimPrefix(line, []byte("event:"))))
			case bytes.HasPrefix(line, []byte("data:")):
				part := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				if len(payload) > 0 {
					payload = append(payload, '\n')
				}
				payload = append(payload, part...)
			}
		}
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if string(payload) == "response.completed" || string(payload) == "response.done" {
			completed = true
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Code  string `json:"code"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &event) != nil {
			continue
		}
		kind := event.Type
		if kind == "" {
			kind = eventName
		}
		switch kind {
		case "response.completed", "response.done":
			completed = true
		case "response.failed", "response.incomplete", "error":
			completed = false
			code := event.Response.Error.Code
			if code == "" {
				code = event.Error.Code
			}
			if code == "" {
				code = event.Code
			}
			if code == "rate_limit_exceeded" || code == "insufficient_quota" {
				rejectedStatus = http.StatusTooManyRequests
			}
			return completed, rejectedStatus
		}
	}
	return completed, rejectedStatus
}

func requestModel(body []byte) string {
	var value struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value.Model)
}

func isGenerationPath(path string) bool {
	path = strings.TrimSuffix(path, "/")
	return strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact")
}

func buildTransport(proxyText string, headerTimeoutSeconds int) (*http.Transport, error) {
	transport := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2: true, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32,
		MaxIdleConnsPerHost: 16, ResponseHeaderTimeout: time.Duration(headerTimeoutSeconds) * time.Second,
		DisableCompression: true,
	}
	if proxyText == "" {
		return transport, nil
	}
	u, err := url.Parse(proxyText)
	if err != nil || u.Hostname() == "" || u.Fragment != "" {
		return nil, errors.New("invalid proxy")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second})
		if err != nil {
			return nil, errors.New("invalid socks5 proxy")
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("socks5 proxy does not support context cancellation")
		}
		transport.Proxy = nil
		transport.DialContext = contextDialer.DialContext
	default:
		return nil, errors.New("unsupported proxy")
	}
	return transport, nil
}

func headersFromWire(h map[string]*v1.HeaderValues) http.Header {
	out := http.Header{}
	for name, values := range h {
		if values == nil {
			continue
		}
		for _, value := range values.Values {
			out.Add(name, value)
		}
	}
	return out
}

func headersToWire(h http.Header) map[string]*v1.HeaderValues {
	out := make(map[string]*v1.HeaderValues, len(h))
	for name, values := range h {
		out[name] = &v1.HeaderValues{Values: append([]string(nil), values...)}
	}
	return out
}
