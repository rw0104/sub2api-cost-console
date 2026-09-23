package mihomoroute

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
	corelog "github.com/metacubex/mihomo/log"
)

type Route struct {
	ID          string
	StableID    string
	DisplayName string
	Protocol    string
	Transport   *http.Transport
	close       func() error
}

var quietOnce sync.Once

var (
	ErrProxyAuth       = errors.New("proxy authentication rejected")
	ErrProxyTimeout    = errors.New("proxy connection timed out")
	ErrProxyTLS        = errors.New("proxy TLS verification failed")
	ErrProxyConnection = errors.New("proxy connection failed")
)

func QuietCore() { quietOnce.Do(func() { corelog.SetLevel(corelog.SILENT) }) }

func (r Route) Close() {
	if r.Transport != nil {
		r.Transport.CloseIdleConnections()
	}
	if r.close != nil {
		_ = r.close()
	}
}

func Build(node map[string]any, index int) (Route, error) {
	QuietCore()
	if err := ValidateNode(node); err != nil {
		return Route{}, err
	}
	copyNode := make(map[string]any, len(node))
	for key, value := range node {
		copyNode[key] = value
	}
	stableID, err := nodeIdentity(copyNode)
	if err != nil {
		return Route{}, err
	}
	protocol, _ := copyNode["type"].(string)
	name := displayName(copyNode, protocol)
	copyNode["name"] = fmt.Sprintf("route-%03d", index+1)
	proxy, err := adapter.ParseProxy(copyNode)
	if err != nil {
		return Route{}, errors.New("node rejected by Mihomo outbound core")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 120 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16, MaxResponseHeaderBytes: 1 << 20}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		metadata := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
		if err := metadata.SetRemoteAddress(address); err != nil {
			return nil, errors.New("invalid upstream address")
		}
		conn, err := proxy.DialContext(ctx, metadata)
		if err != nil {
			return nil, safeDialError(err)
		}
		return conn, nil
	}
	return Route{ID: stableID, StableID: stableID, DisplayName: name, Protocol: protocol, Transport: transport, close: proxy.Close}, nil
}

// Core errors may include proxy addresses or credentials. Preserve only the
// diagnostic category across the transport boundary.
func safeDialError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		return ErrProxyTimeout
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "need auth") || strings.Contains(message, "auth failed") || strings.Contains(message, "authentication") || strings.Contains(message, "username/password") || strings.Contains(message, "407") {
		return ErrProxyAuth
	}
	if strings.Contains(message, "certificate") || strings.Contains(message, "tls:") {
		return ErrProxyTLS
	}
	return ErrProxyConnection
}

func nodeIdentity(node map[string]any) (string, error) {
	canonical := make(map[string]any, len(node))
	for key, value := range node {
		if key != "name" {
			canonical[key] = value
		}
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", errors.New("node settings cannot be canonicalized")
	}
	sum := sha256.Sum256(data)
	return "route-" + hex.EncodeToString(sum[:16]), nil
}

func displayName(node map[string]any, protocol string) string {
	name, _ := node["name"].(string)
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "imported" || strings.Contains(name, "://") || strings.Contains(name, "@") {
		return protocol + " 线路"
	}
	for _, key := range []string{"server", "password", "username", "uuid"} {
		value, _ := node[key].(string)
		if value != "" && strings.Contains(name, value) {
			return protocol + " 线路"
		}
	}
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}
