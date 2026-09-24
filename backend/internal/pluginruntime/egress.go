package pluginruntime

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)

// EgressBrokerProtocolVersion is the control-plane version reserved for the
// host-owned egress broker. The HTTP/SSE data-plane is intentionally not
// implemented in this slice; callers must fail closed until a broker serving
// this version is attached.
const EgressBrokerProtocolVersion uint32 = 1

// EgressBrokerContainerSocket is the only path a future broker may expose in
// a network-restricted container. Docker networking remains disabled.
const EgressBrokerContainerSocket = "/rpc/egress-broker.sock"

// EgressBrokerOptions is copied from the validated host configuration into a
// container runner. It is a policy descriptor, not a permission to dial a
// target directly.
type EgressBrokerOptions struct {
	Enabled        bool
	SocketPath     string
	AllowedHosts   []string
	AllowedSchemes []string
	RequireTLS     bool
}

// EgressBroker is the host-side authorization boundary for the HTTP/SSE
// broker handler. Implementations must validate the account and target before
// opening a stream and must return a stable denial code. No service wiring is
// installed by default, so an absent authorizer remains fail-closed.
type EgressBroker interface {
	Authorize(context.Context, EgressRequest) (EgressDecision, error)
}

// EgressRequest contains only routing metadata. Credentials, arbitrary
// headers, and request bodies are deliberately outside this control protocol.
type EgressRequest struct {
	RequestID     string
	CorrelationID string
	AccountID     int64
	Scheme        string
	Host          string
	Port          uint16
}

// EgressDecision is the bounded result an authorization broker returns before
// a data stream is established.
type EgressDecision struct {
	Allowed bool
	Code    string
	Reason  string
}

var egressHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func (o EgressBrokerOptions) Validate() error {
	if !o.Enabled {
		if strings.TrimSpace(o.SocketPath) != "" || len(o.AllowedHosts) > 0 || len(o.AllowedSchemes) > 0 {
			return errors.New("sandbox egress broker is disabled but contains policy")
		}
		return nil
	}
	if strings.TrimSpace(o.SocketPath) == "" || !filepath.IsAbs(o.SocketPath) || filepath.Clean(o.SocketPath) != o.SocketPath || strings.ContainsAny(o.SocketPath, ",\r\n\x00") {
		return errors.New("sandbox egress broker socket path must be an absolute clean host path")
	}
	if len(o.AllowedHosts) == 0 || len(o.AllowedHosts) > 64 {
		return errors.New("sandbox egress broker requires 1 to 64 allowed hosts")
	}
	seenHosts := make(map[string]struct{}, len(o.AllowedHosts))
	for _, raw := range o.AllowedHosts {
		host := strings.ToLower(strings.TrimSpace(raw))
		if host == "" || host != raw || strings.ContainsAny(host, "/:*?[]\r\n\x00") || net.ParseIP(host) != nil || !egressHostPattern.MatchString(host) {
			return errors.New("sandbox egress broker contains an invalid allowed host")
		}
		if _, ok := seenHosts[host]; ok {
			return errors.New("sandbox egress broker contains duplicate allowed hosts")
		}
		seenHosts[host] = struct{}{}
	}
	if !o.RequireTLS {
		return errors.New("sandbox egress broker requires TLS")
	}
	if len(o.AllowedSchemes) == 0 {
		return errors.New("sandbox egress broker requires allowed schemes")
	}
	seenSchemes := make(map[string]struct{}, len(o.AllowedSchemes))
	for _, raw := range o.AllowedSchemes {
		scheme := strings.ToLower(strings.TrimSpace(raw))
		if scheme != raw || (scheme != "https" && scheme != "wss") {
			return errors.New("sandbox egress broker only permits https or wss")
		}
		if _, ok := seenSchemes[scheme]; ok {
			return errors.New("sandbox egress broker contains duplicate allowed schemes")
		}
		seenSchemes[scheme] = struct{}{}
	}
	return nil
}
