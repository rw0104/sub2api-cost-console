package pluginruntime

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type testScopedListener struct {
	net.Listener
	address net.Addr
}

func (l *testScopedListener) Addr() net.Addr { return l.address }

func newTestScopedListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return &testScopedListener{Listener: listener, address: &net.UnixAddr{Name: filepath.Join(t.TempDir(), "owner.sock"), Net: "unix"}}
}

func scopedOwnerIdentity() EgressBrokerIdentity {
	return EgressBrokerIdentity{
		PluginKey:          "example.plugin",
		RuntimeInstanceID:  "example.plugin-instance-1",
		BindingScopeDigest: "0123456789abcdef",
		OwnerToken:         strings.Repeat("a", 48),
	}
}

func scopedOwnerOptions(t *testing.T, identity EgressBrokerIdentity, listener net.Listener, authorizer EgressBroker) EgressBrokerOwnerOptions {
	t.Helper()
	return EgressBrokerOwnerOptions{
		Broker: EgressBrokerOptions{Enabled: true, SocketPath: filepath.Join(t.TempDir(), identity.PluginKey+"-"+identity.RuntimeInstanceID+".sock"),
			AllowedHosts: []string{"api.openai.com"}, AllowedSchemes: []string{"https"}, RequireTLS: true},
		Identity: identity, Listener: listener, Authorizer: authorizer,
	}
}

func setScopedOwnerHeaders(request *http.Request, identity EgressBrokerIdentity) {
	request.Header.Set(EgressBrokerPluginKeyHeader, identity.PluginKey)
	request.Header.Set(EgressBrokerInstanceIDHeader, identity.RuntimeInstanceID)
	request.Header.Set(EgressBrokerScopeDigestHeader, identity.BindingScopeDigest)
	request.Header.Set(EgressBrokerOwnerTokenHeader, identity.OwnerToken)
}

func TestEgressBrokerOwnerRequiresPerInstanceIdentityAndListener(t *testing.T) {
	identity := scopedOwnerIdentity()
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}
	listener := newTestScopedListener(t)
	require.NoError(t, listener.Close())
	var err error

	_, err = NewEgressBrokerOwner(scopedOwnerOptions(t, identity, nil, authorizer))
	require.ErrorContains(t, err, "private listener")
	_, err = NewEgressBrokerOwner(scopedOwnerOptions(t, identity, listener, nil))
	require.ErrorContains(t, err, "authorizer")
	_, err = NewEgressBrokerOwner(scopedOwnerOptions(t, EgressBrokerIdentity{PluginKey: identity.PluginKey}, listener, authorizer))
	require.Error(t, err)

	badPath := scopedOwnerOptions(t, identity, listener, authorizer)
	badPath.Broker.SocketPath = filepath.Join(t.TempDir(), "global-egress.sock")
	_, err = NewEgressBrokerOwner(badPath)
	require.ErrorContains(t, err, "unique")
}

func TestEgressBrokerOwnerRejectsMismatchedHeadersBeforeAuthorizer(t *testing.T) {
	identity := scopedOwnerIdentity()
	listener := newTestScopedListener(t)
	defer listener.Close()
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}
	owner, err := NewEgressBrokerOwner(scopedOwnerOptions(t, identity, listener, authorizer))
	require.NoError(t, err)
	defer owner.Close()

	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "https://api.openai.com/events")
	setScopedOwnerHeaders(request, identity)
	request.Header.Set(EgressBrokerOwnerTokenHeader, "wrong-token")
	response := httptest.NewRecorder()
	owner.server.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "OWNER_IDENTITY_REQUIRED", response.Header().Get(EgressBrokerCodeHeader))
	authorizer.mu.Lock()
	require.Empty(t, authorizer.requests)
	authorizer.mu.Unlock()
}

func TestEgressBrokerOwnerAcceptsBoundIdentityAndCloseIsIdempotent(t *testing.T) {
	identity := scopedOwnerIdentity()
	listener := newTestScopedListener(t)
	authorizer := &egressTestAuthorizer{decision: EgressDecision{Allowed: true}}
	options := scopedOwnerOptions(t, identity, listener, authorizer)
	var audit EgressAuditEvent
	options.Audit = func(event EgressAuditEvent) { audit = event }
	owner, err := NewEgressBrokerOwner(options)
	require.NoError(t, err)
	server := owner.server
	server.client = &http.Client{Transport: egressRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("data: ok\n\n")), Request: request}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "http://broker/sse", nil)
	setEgressHeaders(request, "https://api.openai.com/events")
	setScopedOwnerHeaders(request, identity)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "data: ok\n\n", response.Body.String())
	require.Equal(t, identity.PluginKey, audit.PluginKey)
	require.Equal(t, identity.RuntimeInstanceID, audit.RuntimeID)
	require.Equal(t, identity.BindingScopeDigest, audit.ScopeDigest)
	require.NoError(t, owner.Close())
	require.NoError(t, owner.Close())
	require.Equal(t, identity, owner.Identity())
}

func TestEgressBrokerIdentityValidationDoesNotAcceptPathOrShortToken(t *testing.T) {
	for _, identity := range []EgressBrokerIdentity{
		{PluginKey: "../plugin", RuntimeInstanceID: "instance", BindingScopeDigest: "0123456789abcdef", OwnerToken: strings.Repeat("a", 48)},
		{PluginKey: "plugin", RuntimeInstanceID: "instance", BindingScopeDigest: "not-a-digest!!!", OwnerToken: strings.Repeat("a", 48)},
		{PluginKey: "plugin", RuntimeInstanceID: "instance", BindingScopeDigest: "0123456789abcdef", OwnerToken: "short"},
	} {
		require.Error(t, identity.Validate())
	}
}
