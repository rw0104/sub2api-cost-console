package pluginruntime

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// EgressBrokerProtocol is sent on the local control request so a future
	// broker cannot accidentally accept an unrelated HTTP proxy protocol.
	EgressBrokerProtocol = "sub2api.plugin.egress.v1"

	EgressBrokerProtocolHeader    = "X-Sub2API-Egress-Protocol"
	EgressBrokerSchemeHeader      = "X-Sub2API-Egress-Scheme"
	EgressBrokerRequestIDHeader   = "X-Sub2API-Egress-Request-Id"
	EgressBrokerCorrelationHeader = "X-Sub2API-Egress-Correlation-Id"
	EgressBrokerAccountHeader     = "X-Sub2API-Egress-Account-Id"
	EgressBrokerTargetHeader      = "X-Sub2API-Egress-Target"
	EgressBrokerCodeHeader        = "X-Sub2API-Egress-Code"

	egressBrokerAuthorizationTimeout = 2 * time.Second
	egressBrokerDialTimeout          = 10 * time.Second
	egressBrokerTLSHandshakeTimeout  = 10 * time.Second
	egressBrokerMaxTargetBytes       = 4096
	egressBrokerMaxSSEBytes          = 16 * 1024 * 1024
	egressBrokerMaxConcurrent        = 16
)

// EgressAuditEvent is deliberately limited to routing metadata. It never
// contains an upstream URL, query, authorization header, or response body.
type EgressAuditEvent struct {
	Operation     string `json:"operation"`
	RequestID     string `json:"request_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	AccountID     int64  `json:"account_id,omitempty"`
	Scheme        string `json:"scheme,omitempty"`
	Host          string `json:"host,omitempty"`
	Port          uint16 `json:"port,omitempty"`
	Outcome       string `json:"outcome"`
	Code          string `json:"code"`
	StatusCode    int    `json:"status_code,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
}

// EgressAuditSink receives bounded broker decisions and lifecycle events.
// Implementations must apply their own rate limiting and persistence policy.
type EgressAuditSink func(EgressAuditEvent)

// EgressBrokerServer is a host-owned HTTP CONNECT/SSE boundary. It is an
// http.Handler so the service can attach it to a Unix-socket listener later;
// this package intentionally does not create or replace host sockets.
type EgressBrokerServer struct {
	options        EgressBrokerOptions
	allowedHosts   map[string]struct{}
	allowedSchemes map[string]struct{}
	authorizer     EgressBroker
	audit          EgressAuditSink
	dialContext    func(context.Context, string, string) (net.Conn, error)
	client         *http.Client
	slots          chan struct{}
	maxSSEBytes    int64
}

// NewEgressBrokerServer validates the policy and creates a fail-closed broker
// handler. A nil authorizer is accepted so callers can expose a health-safe
// endpoint while unconfigured requests still receive AUTHORIZER_UNAVAILABLE.
func NewEgressBrokerServer(options EgressBrokerOptions, authorizer EgressBroker, audit EgressAuditSink) (*EgressBrokerServer, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	hosts := make(map[string]struct{}, len(options.AllowedHosts))
	for _, host := range options.AllowedHosts {
		hosts[host] = struct{}{}
	}
	schemes := make(map[string]struct{}, len(options.AllowedSchemes))
	for _, scheme := range options.AllowedSchemes {
		schemes[scheme] = struct{}{}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     false,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   egressBrokerDialTimeout,
		ResponseHeaderTimeout: egressBrokerDialTimeout,
	}
	server := &EgressBrokerServer{
		options:        options,
		allowedHosts:   hosts,
		allowedSchemes: schemes,
		authorizer:     authorizer,
		audit:          audit,
		dialContext:    publicEgressDialContext,
		maxSSEBytes:    egressBrokerMaxSSEBytes,
		slots:          make(chan struct{}, egressBrokerMaxConcurrent),
	}
	transport.DialContext = server.dialContext
	server.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return server, nil
}

// Serve attaches the handler to a listener supplied by the host. The caller
// owns listener creation, permissions, and shutdown; this keeps Unix socket
// replacement and lifecycle policy outside the data-plane package.
func (s *EgressBrokerServer) Serve(listener net.Listener) error {
	if s == nil {
		return errors.New("egress broker is nil")
	}
	if listener == nil {
		return errors.New("egress broker listener is nil")
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	return server.Serve(listener)
}

// ServeHTTP accepts only the two explicitly defined data-plane operations.
// Ordinary forward-proxy requests, absolute-form URLs, and CONNECT targets
// outside the policy are rejected before any dial occurs.
func (s *EgressBrokerServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || r == nil {
		writeEgressError(w, http.StatusServiceUnavailable, "BROKER_UNAVAILABLE")
		return
	}
	switch {
	case r.Method == http.MethodConnect:
		s.handleConnect(w, r)
	case r.Method == http.MethodGet && r.URL != nil && r.URL.Path == "/sse":
		s.handleSSE(w, r)
	default:
		s.reject(w, r, "unknown", EgressRequest{}, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}

func (s *EgressBrokerServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	req, err := s.parseMetadata(r)
	if err != nil {
		s.rejectWithStarted(w, r, "connect", req, started, http.StatusBadRequest, "INVALID_METADATA")
		return
	}
	req, err = s.validateConnectTarget(r, req)
	if err != nil {
		s.rejectWithStarted(w, r, "connect", req, started, http.StatusForbidden, targetErrorCode(err))
		return
	}
	if !s.acquire(w, r, "connect", req, started) {
		return
	}
	defer s.release()
	if !s.authorize(w, r, "connect", req, started) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), egressBrokerDialTimeout)
	defer cancel()
	dial := s.dialContext
	if dial == nil {
		dial = publicEgressDialContext
	}
	upstream, err := dial(ctx, "tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
	if err != nil {
		s.rejectWithStarted(w, r, "connect", req, started, http.StatusBadGateway, "UPSTREAM_DIAL_FAILED")
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		s.rejectWithStarted(w, r, "connect", req, started, http.StatusInternalServerError, "HIJACK_UNSUPPORTED")
		return
	}
	clientConn, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		s.rejectWithStarted(w, r, "connect", req, started, http.StatusInternalServerError, "HIJACK_FAILED")
		return
	}
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n" + EgressBrokerCodeHeader + ": CONNECTED\r\n\r\n"); err != nil {
		_ = clientConn.Close()
		_ = upstream.Close()
		s.auditEvent("connect", req, "failed", "CLIENT_WRITE_FAILED", 0, 0, started)
		return
	}
	if err = buffered.Flush(); err != nil {
		_ = clientConn.Close()
		_ = upstream.Close()
		s.auditEvent("connect", req, "failed", "CLIENT_WRITE_FAILED", 0, 0, started)
		return
	}
	if err = requireTLSRecord(clientConn, buffered.Reader); err != nil {
		_ = clientConn.Close()
		_ = upstream.Close()
		s.auditEvent("connect", req, "denied", "TLS_REQUIRED", 0, 0, started)
		return
	}

	type copyResult struct{ bytes int64 }
	results := make(chan copyResult, 2)
	go func() {
		n, _ := io.Copy(upstream, buffered.Reader)
		results <- copyResult{bytes: n}
	}()
	go func() {
		n, _ := io.Copy(buffered, upstream)
		_ = buffered.Flush()
		results <- copyResult{bytes: n}
	}()
	first := <-results
	_ = clientConn.Close()
	_ = upstream.Close()
	second := <-results
	s.auditEvent("connect", req, "completed", "CONNECTED", first.bytes+second.bytes, 200, started)
}

func (s *EgressBrokerServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 || (r.Body != nil && r.Body != http.NoBody) {
		s.rejectWithStarted(w, r, "sse", EgressRequest{}, started, http.StatusBadRequest, "BODY_NOT_ALLOWED")
		return
	}
	req, err := s.parseMetadata(r)
	if err != nil {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusBadRequest, "INVALID_METADATA")
		return
	}
	target, req, err := s.validateSSETarget(r, req)
	if err != nil {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusForbidden, targetErrorCode(err))
		return
	}
	if !s.acquire(w, r, "sse", req, started) {
		return
	}
	defer s.release()
	if !s.authorize(w, r, "sse", req, started) {
		return
	}
	client := s.client
	if client == nil {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusServiceUnavailable, "BROKER_UNAVAILABLE")
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusBadRequest, "INVALID_TARGET")
		return
	}
	upstreamRequest.Header.Set("Accept", "text/event-stream")
	response, err := client.Do(upstreamRequest)
	if err != nil || response == nil || response.Body == nil {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusBadGateway, "UPSTREAM_REQUEST_FAILED")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusBadGateway, "UPSTREAM_STATUS")
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		s.rejectWithStarted(w, r, "sse", req, started, http.StatusBadGateway, "UPSTREAM_NOT_SSE")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if value := response.Header.Get("Cache-Control"); value != "" {
		w.Header().Set("Cache-Control", value)
	}
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			if total+int64(read) > s.maxSSEBytes {
				s.auditEvent("sse", req, "failed", "SSE_SIZE_LIMIT", total, response.StatusCode, started)
				return
			}
			written, writeErr := w.Write(buffer[:read])
			if writeErr != nil {
				s.auditEvent("sse", req, "failed", "CLIENT_WRITE_FAILED", total, response.StatusCode, started)
				return
			}
			total += int64(written)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			s.auditEvent("sse", req, "completed", "SSE_COMPLETE", total, response.StatusCode, started)
			return
		}
		if readErr != nil {
			code := "UPSTREAM_STREAM_FAILED"
			if r.Context().Err() != nil {
				code = "CLIENT_CANCELED"
			}
			s.auditEvent("sse", req, "failed", code, total, response.StatusCode, started)
			return
		}
	}
}

func (s *EgressBrokerServer) parseMetadata(r *http.Request) (EgressRequest, error) {
	if r.Header.Get(EgressBrokerProtocolHeader) != EgressBrokerProtocol {
		return EgressRequest{}, errors.New("protocol")
	}
	scheme := strings.ToLower(r.Header.Get(EgressBrokerSchemeHeader))
	if _, ok := s.allowedSchemes[scheme]; !ok || (scheme != "https" && scheme != "wss") {
		return EgressRequest{}, errors.New("scheme")
	}
	requestID, err := boundedEgressHeader(r.Header.Get(EgressBrokerRequestIDHeader), true)
	if err != nil {
		return EgressRequest{}, errors.New("request_id")
	}
	correlationID, err := boundedEgressHeader(r.Header.Get(EgressBrokerCorrelationHeader), true)
	if err != nil {
		return EgressRequest{}, errors.New("correlation_id")
	}
	accountID, err := strconv.ParseInt(r.Header.Get(EgressBrokerAccountHeader), 10, 64)
	if err != nil || accountID <= 0 {
		return EgressRequest{}, errors.New("account_id")
	}
	return EgressRequest{RequestID: requestID, CorrelationID: correlationID, AccountID: accountID, Scheme: scheme}, nil
}

func (s *EgressBrokerServer) validateConnectTarget(r *http.Request, req EgressRequest) (EgressRequest, error) {
	if r.Host == "" || (r.URL != nil && (r.URL.Path != "" || r.URL.RawQuery != "" || r.URL.User != nil)) {
		return req, errors.New("target")
	}
	host, port, err := parseEgressAuthority(r.Host)
	if err != nil {
		return req, err
	}
	if err := s.validateHost(host); err != nil {
		return req, err
	}
	if port != 443 {
		return req, errors.New("port")
	}
	req.Host, req.Port = host, uint16(port)
	return req, nil
}

func (s *EgressBrokerServer) validateSSETarget(r *http.Request, req EgressRequest) (string, EgressRequest, error) {
	raw := r.Header.Get(EgressBrokerTargetHeader)
	if len(raw) == 0 || len(raw) > egressBrokerMaxTargetBytes || strings.ContainsAny(raw, "\r\n") {
		return "", req, errors.New("target")
	}
	target, err := url.Parse(raw)
	if err != nil || !target.IsAbs() || target.User != nil || target.Fragment != "" || target.Host == "" || target.Opaque != "" {
		return "", req, errors.New("target")
	}
	if strings.ToLower(target.Scheme) != req.Scheme || req.Scheme != "https" {
		return "", req, errors.New("scheme")
	}
	host := strings.ToLower(target.Hostname())
	if err := s.validateHost(host); err != nil {
		return "", req, err
	}
	port := 443
	if target.Port() != "" {
		port, err = strconv.Atoi(target.Port())
		if err != nil {
			return "", req, errors.New("port")
		}
	}
	if port != 443 {
		return "", req, errors.New("port")
	}
	req.Host, req.Port = host, uint16(port)
	return target.String(), req, nil
}

func (s *EgressBrokerServer) validateHost(host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || net.ParseIP(host) != nil || !egressHostPattern.MatchString(host) {
		return errors.New("host")
	}
	if _, ok := s.allowedHosts[host]; !ok {
		return errors.New("allowlist")
	}
	return nil
}

func parseEgressAuthority(authority string) (string, int, error) {
	if strings.ContainsAny(authority, "\r\n/@?[]") {
		return "", 0, errors.New("target")
	}
	host, portText, err := net.SplitHostPort(authority)
	if err != nil || host == "" || portText == "" {
		return "", 0, errors.New("target")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("port")
	}
	return strings.ToLower(host), port, nil
}

func (s *EgressBrokerServer) authorize(w http.ResponseWriter, r *http.Request, operation string, req EgressRequest, started time.Time) bool {
	if s.authorizer == nil {
		s.rejectWithStarted(w, r, operation, req, started, http.StatusServiceUnavailable, "AUTHORIZER_UNAVAILABLE")
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), egressBrokerAuthorizationTimeout)
	defer cancel()
	decision, err := s.authorizer.Authorize(ctx, req)
	if err != nil {
		s.rejectWithStarted(w, r, operation, req, started, http.StatusServiceUnavailable, "AUTHORIZATION_ERROR")
		return false
	}
	if !decision.Allowed {
		code := stableEgressCode(decision.Code, "EGRESS_DENIED")
		s.rejectWithStarted(w, r, operation, req, started, http.StatusForbidden, code)
		return false
	}
	return true
}

func (s *EgressBrokerServer) acquire(w http.ResponseWriter, r *http.Request, operation string, req EgressRequest, started time.Time) bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		s.rejectWithStarted(w, r, operation, req, started, http.StatusTooManyRequests, "BROKER_BUSY")
		return false
	}
}

func (s *EgressBrokerServer) release() {
	select {
	case <-s.slots:
	default:
	}
}

func (s *EgressBrokerServer) reject(w http.ResponseWriter, r *http.Request, operation string, req EgressRequest, status int, code string) {
	s.rejectWithStarted(w, r, operation, req, time.Now(), status, code)
}

func (s *EgressBrokerServer) rejectWithStarted(w http.ResponseWriter, _ *http.Request, operation string, req EgressRequest, started time.Time, status int, code string) {
	code = stableEgressCode(code, "EGRESS_ERROR")
	writeEgressError(w, status, code)
	s.auditEvent(operation, req, "denied", code, 0, status, started)
}

func (s *EgressBrokerServer) auditEvent(operation string, req EgressRequest, outcome, code string, bytes int64, status int, started time.Time) {
	if s.audit == nil {
		return
	}
	s.audit(EgressAuditEvent{Operation: operation, RequestID: req.RequestID, CorrelationID: req.CorrelationID,
		AccountID: req.AccountID, Scheme: req.Scheme, Host: req.Host, Port: req.Port, Outcome: outcome,
		Code: stableEgressCode(code, "EGRESS_ERROR"), StatusCode: status, Bytes: bytes,
		DurationMS: time.Since(started).Milliseconds()})
}

func writeEgressError(w http.ResponseWriter, status int, code string) {
	if w == nil {
		return
	}
	w.Header().Set(EgressBrokerCodeHeader, stableEgressCode(code, "EGRESS_ERROR"))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, stableEgressCode(code, "EGRESS_ERROR")+"\n")
}

func stableEgressCode(value, fallback string) string {
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			return r
		}
		return -1
	}, strings.ToUpper(value))
	if value == "" {
		value = fallback
	}
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func boundedEgressHeader(value string, required bool) (string, error) {
	if required && strings.TrimSpace(value) == "" {
		return "", errors.New("missing")
	}
	if strings.TrimSpace(value) != value || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("invalid")
	}
	return value, nil
}

func targetErrorCode(err error) string {
	if err == nil {
		return "INVALID_TARGET"
	}
	switch err.Error() {
	case "allowlist":
		return "TARGET_NOT_ALLOWED"
	case "scheme":
		return "TLS_SCHEME_REQUIRED"
	case "port":
		return "TLS_PORT_REQUIRED"
	default:
		return "INVALID_TARGET"
	}
}

func requireTLSRecord(conn net.Conn, reader *bufio.Reader) error {
	if conn == nil || reader == nil {
		return errors.New("missing connection")
	}
	_ = conn.SetReadDeadline(time.Now().Add(egressBrokerTLSHandshakeTimeout))
	prefix, err := reader.Peek(5)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || len(prefix) < 5 || prefix[0] != 0x16 || prefix[1] != 0x03 {
		return errors.New("tls record required")
	}
	return nil
}

func publicEgressDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("invalid egress address")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: egressBrokerDialTimeout}
	var lastErr error
	for _, resolved := range addresses {
		if !publicEgressIP(resolved.IP) {
			lastErr = errors.New("resolved egress address is private")
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("no public egress address")
	}
	return nil, lastErr
}

func publicEgressIP(ip net.IP) bool {
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

var _ http.Handler = (*EgressBrokerServer)(nil)
