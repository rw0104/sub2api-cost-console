package pluginruntime

import (
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// These headers are accepted only by a scoped broker owner. They are
	// metadata, not secrets; the owner token is checked but never audited.
	EgressBrokerPluginKeyHeader   = "X-Sub2API-Egress-Plugin-Key"
	EgressBrokerInstanceIDHeader  = "X-Sub2API-Egress-Runtime-Instance"
	EgressBrokerScopeDigestHeader = "X-Sub2API-Egress-Scope-Digest"
	EgressBrokerOwnerTokenHeader  = "X-Sub2API-Egress-Owner-Token"
)

var errEgressOwnerIdentity = errors.New("egress broker owner identity mismatch")

// EgressBrokerIdentity binds a broker listener to one verified plugin runtime.
// The scope digest is the host snapshot identifier for the binding, while the
// owner token is a short-lived per-instance secret delivered out of band.
type EgressBrokerIdentity struct {
	PluginKey          string
	RuntimeInstanceID  string
	BindingScopeDigest string
	OwnerToken         string
}

func (i EgressBrokerIdentity) Validate() error {
	if !validOwnerValue(i.PluginKey, 1, 128) || !validOwnerValue(i.RuntimeInstanceID, 1, 128) {
		return errors.New("egress broker owner requires plugin key and runtime instance")
	}
	if len(i.BindingScopeDigest) != 16 {
		return errors.New("egress broker owner scope digest must be 16 hex characters")
	}
	if _, err := hex.DecodeString(i.BindingScopeDigest); err != nil {
		return errors.New("egress broker owner scope digest is invalid")
	}
	if !validOwnerValue(i.OwnerToken, 32, 256) {
		return errors.New("egress broker owner token is invalid")
	}
	return nil
}

// EgressBrokerOwnerOptions is intentionally listener-injected. The host must
// create a private, non-symlink Unix listener and pass it here; this package
// never replaces an existing path or silently creates a global socket.
type EgressBrokerOwnerOptions struct {
	Broker     EgressBrokerOptions
	Identity   EgressBrokerIdentity
	Listener   net.Listener
	Authorizer EgressBroker
	Audit      EgressAuditSink
}

// EgressBrokerOwner owns exactly one scoped server/listener pair. It is not
// wired into plugin runtime startup yet; callers must explicitly construct it
// after the runtime identity and binding snapshot are available.
type EgressBrokerOwner struct {
	server    *EgressBrokerServer
	listener  net.Listener
	identity  EgressBrokerIdentity
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func NewEgressBrokerOwner(options EgressBrokerOwnerOptions) (*EgressBrokerOwner, error) {
	if err := options.Identity.Validate(); err != nil {
		return nil, err
	}
	if !options.Broker.Enabled {
		return nil, errors.New("egress broker owner requires an enabled policy")
	}
	if options.Listener == nil {
		return nil, errors.New("egress broker owner requires a private listener")
	}
	if network := options.Listener.Addr().Network(); network != "unix" && network != "unixpacket" {
		return nil, errors.New("egress broker owner requires a Unix listener")
	}
	if options.Authorizer == nil {
		return nil, errors.New("egress broker owner requires an account-bound authorizer")
	}
	if err := options.Broker.Validate(); err != nil {
		return nil, err
	}
	base := filepath.Base(options.Broker.SocketPath)
	if base == "." || base == string(filepath.Separator) ||
		!strings.Contains(base, options.Identity.PluginKey) ||
		!strings.Contains(base, options.Identity.RuntimeInstanceID) {
		return nil, errors.New("egress broker socket must be unique to plugin and runtime instance")
	}
	server, err := NewEgressBrokerServer(options.Broker, options.Authorizer, options.Audit)
	if err != nil {
		return nil, err
	}
	server.identity = &EgressBrokerIdentity{
		PluginKey: options.Identity.PluginKey, RuntimeInstanceID: options.Identity.RuntimeInstanceID,
		BindingScopeDigest: options.Identity.BindingScopeDigest, OwnerToken: options.Identity.OwnerToken,
	}
	return &EgressBrokerOwner{server: server, listener: options.Listener, identity: options.Identity}, nil
}

// Serve blocks until the injected listener is closed. Closing the owner is
// idempotent and is the required drain/kill cleanup path.
func (o *EgressBrokerOwner) Serve() error {
	if o == nil {
		return errors.New("egress broker owner is not initialized")
	}
	o.mu.Lock()
	server, listener, closed := o.server, o.listener, o.closed
	o.mu.Unlock()
	if server == nil || listener == nil || closed {
		return errors.New("egress broker owner is not initialized")
	}
	return server.Serve(listener)
}

func (o *EgressBrokerOwner) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		listener, server := o.listener, o.server
		o.mu.Unlock()
		if listener != nil {
			listenerErr := listener.Close()
			serverErr := server.Close()
			o.closeErr = errors.Join(listenerErr, serverErr)
		} else {
			o.closeErr = server.Close()
		}
	})
	return o.closeErr
}

func (o *EgressBrokerOwner) Identity() EgressBrokerIdentity {
	if o == nil {
		return EgressBrokerIdentity{}
	}
	return o.identity
}

func (s *EgressBrokerServer) validateOwnerHeaders(r *http.Request) error {
	if s == nil || s.identity == nil {
		return nil
	}
	if r == nil {
		return errEgressOwnerIdentity
	}
	for _, expected := range []struct {
		header string
		value  string
	}{
		{EgressBrokerPluginKeyHeader, s.identity.PluginKey},
		{EgressBrokerInstanceIDHeader, s.identity.RuntimeInstanceID},
		{EgressBrokerScopeDigestHeader, s.identity.BindingScopeDigest},
		{EgressBrokerOwnerTokenHeader, s.identity.OwnerToken},
	} {
		value, err := boundedEgressHeader(r.Header.Get(expected.header), true)
		if err != nil || value != expected.value {
			return errEgressOwnerIdentity
		}
	}
	return nil
}

func validOwnerValue(value string, min, max int) bool {
	if len(value) < min || len(value) > max || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00/\\") {
		return false
	}
	return true
}

func egressMetadataErrorCode(err error) string {
	if errors.Is(err, errEgressOwnerIdentity) {
		return "OWNER_IDENTITY_REQUIRED"
	}
	return "INVALID_METADATA"
}
