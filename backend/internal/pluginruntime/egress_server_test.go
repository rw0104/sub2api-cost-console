package pluginruntime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type egressTestAuthorizer struct {
	mu       sync.Mutex
	requests []EgressRequest
	decision EgressDecision
	err      error
}

func (a *egressTestAuthorizer) Authorize(_ context.Context, request EgressRequest) (EgressDecision, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	return a.decision, a.err
}

type egressRoundTripFunc func(*http.Request) (*http.Response, error)

func (f egressRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newEgressTestServer(t *testing.T, authorizer EgressBroker, audit EgressAuditSink) *EgressBrokerServer {
	t.Helper()
	server, err := NewEgressBrokerServer(EgressBrokerOptions{
		Enabled: true, SocketPath: filepath.Join(t.TempDir(), "egress.sock"),
		AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true,
	}, authorizer, audit)
	require.NoError(t, err)
	return server
}

func setEgressHeaders(request *http.Request, target string) {
	request.Header.Set(EgressBrokerProtocolHeader, EgressBrokerProtocol)
	request.Header.Set(EgressBrokerSchemeHeader, "https")
	request.Header.Set(EgressBrokerRequestIDHeader, "request-1")
	request.Header.Set(EgressBrokerCorrelationHeader, "correlation-1")
	request.Header.Set(EgressBrokerAccountHeader, "42")
	request.Header.Set(EgressBrokerTargetHeader, target)
}

func TestEgressBrokerSSEAuthorizesAndAuditsWithoutForwardingSensitiveHeaders(t *testing.T) {
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true, Code: "OK"}}
	auditEvents := make(chan EgressAuditEvent, 2)
	server := newEgressTestServer(t, authorizer, func(event EgressAuditEvent) { auditEvents <- event })
	server.client = &http.Client{Transport: egressRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "https://api.openai.com/events?opaque=query", request.URL.String())
		require.Equal(t, "text/event-stream", request.Header.Get("Accept"))
		require.Empty(t, request.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
			Body: io.NopCloser(strings.NewReader("data: ready\n\n")), Request: request}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "https://api.openai.com/events?opaque=query")
	request.Header.Set("Authorization", "should-not-forward")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "data: ready\n\n", response.Body.String())
	authorizer.mu.Lock()
	require.Len(t, authorizer.requests, 1)
	require.Equal(t, int64(42), authorizer.requests[0].AccountID)
	require.Equal(t, "correlation-1", authorizer.requests[0].CorrelationID)
	require.Equal(t, "api.openai.com", authorizer.requests[0].Host)
	authorizer.mu.Unlock()

	select {
	case event := <-auditEvents:
		require.Equal(t, "sse", event.Operation)
		require.Equal(t, "api.openai.com", event.Host)
		require.Equal(t, "correlation-1", event.CorrelationID)
		require.Equal(t, "SSE_COMPLETE", event.Code)
		require.NotContains(t, event.Code, "opaque")
	case <-time.After(time.Second):
		t.Fatal("missing SSE audit event")
	}
}

func TestEgressBrokerSSERejectsDisallowedTargetBeforeAuthorization(t *testing.T) {
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}
	server := newEgressTestServer(t, authorizer, nil)
	server.client = &http.Client{Transport: egressRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("must not dial")
	})}
	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "https://evil.example/events")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "TARGET_NOT_ALLOWED", response.Header().Get(EgressBrokerCodeHeader))
	authorizer.mu.Lock()
	require.Empty(t, authorizer.requests)
	authorizer.mu.Unlock()
}

func TestEgressBrokerFailsClosedWithoutAuthorizer(t *testing.T) {
	server := newEgressTestServer(t, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "https://api.openai.com/events")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, "AUTHORIZER_UNAVAILABLE", response.Header().Get(EgressBrokerCodeHeader))
}

func TestEgressBrokerSSERejectsRequestBody(t *testing.T) {
	server := newEgressTestServer(t, &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}, nil)
	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", strings.NewReader("unexpected"))
	setEgressHeaders(request, "https://api.openai.com/events")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "BODY_NOT_ALLOWED", response.Header().Get(EgressBrokerCodeHeader))
}

func TestEgressBrokerRejectsPlaintextSSEAndRedirectResponses(t *testing.T) {
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}
	server := newEgressTestServer(t, authorizer, nil)
	server.client = &http.Client{Transport: egressRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://api.openai.com/other"}},
			Body: io.NopCloser(strings.NewReader("redirect")), Request: request}, nil
	}), CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "https://api.openai.com/events")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadGateway, response.Code)
	require.Equal(t, "UPSTREAM_STATUS", response.Header().Get(EgressBrokerCodeHeader))

	request = httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "http://api.openai.com/events")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "TLS_SCHEME_REQUIRED", response.Header().Get(EgressBrokerCodeHeader))
}

func TestEgressBrokerConnectRequiresTLSAndTunnelsOnlyPort443(t *testing.T) {
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}
	auditEvents := make(chan EgressAuditEvent, 1)
	server := newEgressTestServer(t, authorizer, func(event EgressAuditEvent) { auditEvents <- event })
	upstreamClient, upstreamBroker := net.Pipe()
	server.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return upstreamClient, nil
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	upstreamPayload := []byte{0x16, 0x03, 0x03, 0x00, 0x02, 'o', 'k'}
	go func() {
		buffer := make([]byte, len(upstreamPayload))
		_, _ = io.ReadFull(upstreamBroker, buffer)
		_, _ = upstreamBroker.Write(buffer)
		_ = upstreamBroker.Close()
	}()

	address := strings.TrimPrefix(httpServer.URL, "http://")
	client, err := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, err)
	defer client.Close()
	_, err = io.WriteString(client, "CONNECT api.openai.com:443 HTTP/1.1\r\nHost: api.openai.com:443\r\n"+
		EgressBrokerProtocolHeader+": "+EgressBrokerProtocol+"\r\n"+
		EgressBrokerSchemeHeader+": https\r\n"+
		EgressBrokerRequestIDHeader+": request-1\r\n"+
		EgressBrokerCorrelationHeader+": correlation-1\r\n"+
		EgressBrokerAccountHeader+": 42\r\n\r\n")
	require.NoError(t, err)
	reader := bufio.NewReader(client)
	statusLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, statusLine, "200 Connection Established")
	for {
		line, readErr := reader.ReadString('\n')
		require.NoError(t, readErr)
		if line == "\r\n" {
			break
		}
	}
	_, err = client.Write(upstreamPayload)
	require.NoError(t, err)
	echo := make([]byte, len(upstreamPayload))
	_, err = io.ReadFull(reader, echo)
	require.NoError(t, err)
	require.Equal(t, upstreamPayload, echo)
	_ = client.Close()

	select {
	case event := <-auditEvents:
		require.Equal(t, "connect", event.Operation)
		require.Equal(t, "CONNECTED", event.Code)
		require.Equal(t, int64(42), event.AccountID)
	case <-time.After(time.Second):
		t.Fatal("missing CONNECT audit event")
	}
}

func TestPublicEgressDialContextRejectsPrivateResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := publicEgressDialContext(ctx, "tcp", "localhost:443")
	require.Error(t, err)
}
