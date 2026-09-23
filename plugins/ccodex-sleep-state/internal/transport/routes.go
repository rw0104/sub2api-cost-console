package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

type Route struct {
	ID        string
	Name      string
	Protocol  string
	ProxyURL  string
	Transport *http.Transport
	CloseFunc func()
}

type NodeInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

// Nodes deliberately omits proxy URLs, addresses, and authentication material.
func (r *Registry) Nodes() []NodeInfo {
	if r == nil || r.closed.Load() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	nodes := make([]NodeInfo, 0, len(r.routes))
	for _, route := range r.routes {
		nodes = append(nodes, nodeInfo(route))
	}
	return nodes
}

func nodeInfo(route Route) NodeInfo {
	protocol, name := route.Protocol, route.Name
	if route.ID == "route-direct" {
		protocol, name = "direct", "直连"
	}
	if protocol == "" && route.ProxyURL != "" {
		if u, err := url.Parse(route.ProxyURL); err == nil {
			protocol = strings.ToLower(u.Scheme)
		}
	}
	if name == "" {
		name = protocol + " 线路"
	}
	return NodeInfo{ID: route.ID, Name: name, Protocol: protocol}
}

type Registry struct {
	mu     sync.RWMutex
	routes []Route
	cursor atomic.Uint64
	closed atomic.Bool
}

func NewRegistry(proxyURLs []string, direct bool, headerTimeoutSeconds int) (*Registry, error) {
	registry := &Registry{}
	seen := map[string]bool{}
	if direct {
		transport, err := buildTransport("", headerTimeoutSeconds)
		if err != nil {
			registry.Close()
			return nil, err
		}
		seen["route-direct"] = true
		registry.routes = append(registry.routes, Route{ID: "route-direct", Transport: transport})
	}
	for _, raw := range proxyURLs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		canonical, err := canonicalProxyURL(raw)
		if err != nil {
			registry.Close()
			return nil, err
		}
		id := routeID(canonical)
		if seen[id] {
			continue
		}
		transport, err := buildTransport(canonical, headerTimeoutSeconds)
		if err != nil {
			registry.Close()
			return nil, err
		}
		seen[id] = true
		registry.routes = append(registry.routes, Route{ID: id, ProxyURL: canonical, Transport: transport})
	}
	if len(registry.routes) == 0 {
		id := "route-direct"
		if !seen[id] {
			transport, err := buildTransport("", headerTimeoutSeconds)
			if err != nil {
				registry.Close()
				return nil, err
			}
			registry.routes = append(registry.routes, Route{ID: id, Transport: transport})
		}
	}
	return registry, nil
}

func (r *Registry) Len() int {
	if r == nil || r.closed.Load() {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.routes)
}

func (r *Registry) Next() (int, Route, bool) {
	if r == nil || r.closed.Load() {
		return 0, Route{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.routes) == 0 {
		return 0, Route{}, false
	}
	index := int(r.cursor.Add(1)-1) % len(r.routes)
	return index, r.routes[index], true
}

func (r *Registry) At(index int) (Route, bool) {
	if r == nil || r.closed.Load() {
		return Route{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if index < 0 || index >= len(r.routes) {
		return Route{}, false
	}
	return r.routes[index], true
}

func (r *Registry) ByID(id string) (int, Route, bool) {
	if r == nil || r.closed.Load() || strings.TrimSpace(id) == "" {
		return 0, Route{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for index, route := range r.routes {
		if route.ID == id {
			return index, route, true
		}
	}
	return 0, Route{}, false
}

func (r *Registry) Signature() string {
	if r == nil || r.closed.Load() {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	hash := sha256.New()
	for _, route := range r.routes {
		_, _ = hash.Write([]byte(route.ID))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (r *Registry) Close() {
	if r == nil || !r.closed.CompareAndSwap(false, true) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, route := range r.routes {
		if route.Transport != nil {
			route.Transport.CloseIdleConnections()
		}
		if route.CloseFunc != nil {
			route.CloseFunc()
		}
	}
	r.routes = nil
}

func NewRegistryFromRoutes(routes []Route, direct bool, headerTimeoutSeconds int) (*Registry, error) {
	registry := &Registry{}
	seen := make(map[string]bool, len(routes)+1)
	if direct {
		transport, err := buildTransport("", headerTimeoutSeconds)
		if err != nil {
			return nil, err
		}
		seen["route-direct"] = true
		registry.routes = append(registry.routes, Route{ID: "route-direct", Transport: transport})
	}
	for _, route := range routes {
		if route.ID == "" || route.Transport == nil || seen[route.ID] {
			if route.CloseFunc != nil {
				route.CloseFunc()
			}
			continue
		}
		seen[route.ID] = true
		registry.routes = append(registry.routes, route)
	}
	if len(registry.routes) == 0 {
		transport, err := buildTransport("", headerTimeoutSeconds)
		if err != nil {
			registry.Close()
			return nil, err
		}
		registry.routes = append(registry.routes, Route{ID: "route-direct", Transport: transport})
	}
	return registry, nil
}

func canonicalProxyURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" || u.Fragment != "" || u.User != nil && u.User.Username() == "" {
		return "", errors.New("proxy URL must be an absolute URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	switch u.Scheme {
	case "http", "https":
	case "socks5", "socks5h":
	default:
		return "", errors.New("unsupported proxy scheme")
	}
	return u.String(), nil
}

func routeID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "route-" + hex.EncodeToString(sum[:8])
}
