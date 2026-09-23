package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/mihomoroute"
	"local.sub2api/ccodex-sleep-state/internal/proxyroute"
)

// NewRegistryFromConfig resolves explicit URLs, environment-backed URLs and
// subscription sources before constructing the long-lived route registry.
// Sources are loaded on demand when a config is first used; applying a new
// config closes the old registry in the plugin lifecycle.
func NewRegistryFromConfig(ctx context.Context, c *config.Config, fallback string) (*Registry, error) {
	if c == nil {
		return nil, errors.New("configuration required")
	}
	routes, err := resolveRoutes(ctx, c, fallback)
	if err != nil {
		return nil, err
	}
	for _, route := range routes {
		route.Transport.ResponseHeaderTimeout = time.Duration(c.ResponseHeaderTimeoutSeconds) * time.Second
		route.Transport.DisableCompression = true
	}
	return NewRegistryFromRoutes(routes, c.Direct, c.ResponseHeaderTimeoutSeconds)
}

func resolveRoutes(ctx context.Context, c *config.Config, fallback string) ([]Route, error) {
	values := c.RouteURLs()
	if len(values) == 0 && !c.Direct && strings.TrimSpace(fallback) != "" {
		values = append(values, strings.TrimSpace(fallback))
	}
	for _, envName := range c.ProxyEnvs {
		value, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, errors.New("proxy environment source is empty")
		}
		values = append(values, strings.TrimSpace(value))
	}
	routes := make([]Route, 0, len(values))
	success := false
	defer func() {
		if !success {
			for _, route := range routes {
				if route.CloseFunc != nil {
					route.CloseFunc()
				} else if route.Transport != nil {
					route.Transport.CloseIdleConnections()
				}
			}
		}
	}()
	for index, raw := range values {
		route, err := buildNodeRoute(raw, index)
		if err != nil {
			return nil, fmt.Errorf("proxy source %d: %w", index+1, err)
		}
		routes = append(routes, route)
	}
	for index, source := range c.Subscriptions {
		data, err := readSubscription(ctx, source, c.SubscriptionProxyEnv)
		if err != nil {
			return nil, fmt.Errorf("subscription %d: %w", index+1, err)
		}
		nodes, err := mihomoroute.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("subscription %d: %w", index+1, err)
		}
		nodes = filterMihomoNodes(nodes, source)
		if len(nodes) == 0 {
			return nil, fmt.Errorf("subscription %d has no nodes after filters", index+1)
		}
		for nodeIndex, node := range nodes {
			route, buildErr := buildMihomoRoute(node, len(routes)+nodeIndex)
			if buildErr != nil {
				return nil, fmt.Errorf("subscription %d node %d: %w", index+1, nodeIndex+1, buildErr)
			}
			routes = append(routes, route)
			if len(routes) > config.MaxRouteURLs {
				return nil, errors.New("combined route sources exceed 256 entries")
			}
		}
	}
	if len(routes) > config.MaxRouteURLs {
		return nil, errors.New("combined route sources exceed 256 entries")
	}
	success = true
	return routes, nil
}

func buildNodeRoute(raw string, index int) (Route, error) {
	node, err := mihomoroute.ParseURI(raw)
	if err != nil {
		return Route{}, err
	}
	return buildMihomoRoute(node, index)
}

func buildMihomoRoute(node map[string]any, index int) (Route, error) {
	route, err := mihomoroute.Build(node, index)
	if err != nil {
		return Route{}, err
	}
	return Route{ID: route.ID, Name: route.DisplayName, Protocol: route.Protocol, Transport: route.Transport, CloseFunc: route.Close}, nil
}

func filterMihomoNodes(nodes []map[string]any, source config.Subscription) []map[string]any {
	include := make(map[string]struct{}, len(source.IncludeProtocols))
	for _, value := range source.IncludeProtocols {
		include[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	filtered := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		protocol, _ := node["type"].(string)
		if len(include) > 0 {
			if _, ok := include[strings.ToLower(protocol)]; !ok {
				continue
			}
		}
		name, _ := node["name"].(string)
		server, _ := node["server"].(string)
		label := strings.ToLower(name + " " + server)
		excluded := false
		for _, keyword := range source.ExcludeKeywords {
			if strings.Contains(label, strings.ToLower(keyword)) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func resolveRouteURLs(ctx context.Context, c *config.Config, fallback string) ([]string, error) {
	urls := c.RouteURLs()
	if len(urls) == 0 && !c.Direct && strings.TrimSpace(fallback) != "" {
		urls = append(urls, strings.TrimSpace(fallback))
	}
	for index, envName := range c.ProxyEnvs {
		value, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("proxy environment variable %d is empty", index+1)
		}
		urls = append(urls, strings.TrimSpace(value))
	}
	for index, source := range c.Subscriptions {
		data, err := readSubscription(ctx, source, c.SubscriptionProxyEnv)
		if err != nil {
			return nil, fmt.Errorf("subscription %d: %w", index+1, err)
		}
		nodes, err := proxyroute.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("subscription %d: %w", index+1, err)
		}
		filtered := filterNodes(nodes, source)
		if len(filtered) == 0 {
			return nil, fmt.Errorf("subscription %d has no nodes after filters", index+1)
		}
		for _, node := range filtered {
			proxyURL, err := node.URL()
			if err != nil {
				return nil, fmt.Errorf("subscription %d contains an invalid node", index+1)
			}
			urls = append(urls, proxyURL)
		}
	}
	if len(urls) > config.MaxRouteURLs {
		return nil, errors.New("combined route sources exceed 256 entries")
	}
	return urls, nil
}

func readSubscription(ctx context.Context, source config.Subscription, downloadProxyEnv string) ([]byte, error) {
	if source.File != "" {
		file, err := os.Open(source.File)
		if err != nil {
			return nil, errors.New("subscription file cannot be opened")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, proxyroute.MaxSubscriptionBytes+1))
		if err != nil {
			return nil, errors.New("subscription file cannot be read")
		}
		if len(data) > proxyroute.MaxSubscriptionBytes {
			return nil, errors.New("subscription exceeds 2 MiB")
		}
		return data, nil
	}
	rawURL := source.URL
	if source.URLEnv != "" {
		var ok bool
		rawURL, ok = os.LookupEnv(source.URLEnv)
		if !ok || strings.TrimSpace(rawURL) == "" {
			return nil, errors.New("subscription URL environment variable is empty")
		}
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("subscription URL is invalid")
	}
	if strings.ToLower(u.Scheme) != "https" && !isLiteralLoopbackHTTP(u) {
		return nil, errors.New("subscription URL must use HTTPS or literal loopback HTTP")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	if downloadProxyEnv != "" {
		rawProxy, ok := os.LookupEnv(downloadProxyEnv)
		if !ok || strings.TrimSpace(rawProxy) == "" {
			return nil, errors.New("subscription download proxy environment variable is empty")
		}
		proxyTransport, err := buildTransport(strings.TrimSpace(rawProxy), 30)
		if err != nil {
			return nil, errors.New("subscription download proxy is invalid")
		}
		transport = proxyTransport
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || strings.ToLower(next.URL.Scheme) != "https" {
			return errors.New("subscription redirect rejected")
		}
		return nil
	}}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("subscription request is invalid")
	}
	userAgent := source.UserAgent
	if userAgent == "" {
		userAgent = "ccodex-sleep-state/0.1"
	}
	req.Header.Set("User-Agent", userAgent)
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("subscription download failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("subscription returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, proxyroute.MaxSubscriptionBytes+1))
	if err != nil {
		return nil, errors.New("subscription response cannot be read")
	}
	if len(data) > proxyroute.MaxSubscriptionBytes {
		return nil, errors.New("subscription exceeds 2 MiB")
	}
	return data, nil
}

func filterNodes(nodes []proxyroute.Node, source config.Subscription) []proxyroute.Node {
	if len(source.IncludeProtocols) == 0 && len(source.ExcludeKeywords) == 0 {
		return nodes
	}
	include := make(map[string]struct{}, len(source.IncludeProtocols))
	for _, value := range source.IncludeProtocols {
		include[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	filtered := make([]proxyroute.Node, 0, len(nodes))
	for _, node := range nodes {
		protocol := strings.ToLower(node.Scheme)
		if len(include) > 0 {
			if _, ok := include[protocol]; !ok {
				continue
			}
		}
		label := strings.ToLower(node.Name + " " + node.Host)
		excluded := false
		for _, keyword := range source.ExcludeKeywords {
			if strings.Contains(label, strings.ToLower(keyword)) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func isLiteralLoopbackHTTP(u *url.URL) bool {
	return strings.ToLower(u.Scheme) == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}
