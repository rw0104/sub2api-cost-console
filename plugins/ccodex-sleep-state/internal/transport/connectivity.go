package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/mihomoroute"
)

const ConnectivityTarget = "https://api.openai.com/"

type RouteCheckResult struct {
	NodeInfo
	Status     string `json:"status"`
	LatencyMS  int64  `json:"latency_ms"`
	HTTPStatus int    `json:"http_status,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type ConnectivityReport struct {
	Total     int                `json:"total"`
	Available int                `json:"available"`
	Reachable int                `json:"reachable"`
	Rows      []RouteCheckResult `json:"rows"`
}

func TestConnectivity(ctx context.Context, registry *Registry) (ConnectivityReport, error) {
	return CheckRoutes(ctx, registry, "")
}

// CheckRoutes tests all nodes, or a single stable ID, without account credentials.
// A restricted HTTP response proves connectivity but not account availability.
func CheckRoutes(ctx context.Context, registry *Registry, routeID string) (ConnectivityReport, error) {
	return checkRoutes(ctx, registry, routeID, ConnectivityTarget, 7*time.Second)
}

func testConnectivity(ctx context.Context, registry *Registry, target string, timeout time.Duration) (ConnectivityReport, error) {
	return checkRoutes(ctx, registry, "", target, timeout)
}

func checkRoutes(ctx context.Context, registry *Registry, routeID, target string, timeout time.Duration) (ConnectivityReport, error) {
	if registry == nil || registry.closed.Load() {
		return ConnectivityReport{}, errors.New("route registry is unavailable")
	}
	registry.mu.RLock()
	routes := append([]Route(nil), registry.routes...)
	registry.mu.RUnlock()
	if routeID != "" {
		var selected []Route
		for _, route := range routes {
			if route.ID == routeID {
				selected = append(selected, route)
				break
			}
		}
		routes = selected
	}
	report := ConnectivityReport{Total: len(routes), Rows: make([]RouteCheckResult, len(routes))}
	if report.Total == 0 {
		return report, errors.New("selected route is unavailable")
	}
	parallel := 24
	if parallel > len(routes) {
		parallel = len(routes)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range parallel {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				report.Rows[index] = checkRouteConnectivity(ctx, routes[index], target, timeout)
			}
		}()
	}
	// Include explicit cancelled results for unstarted nodes, in stable order.
	for index, route := range routes {
		select {
		case jobs <- index:
		case <-ctx.Done():
			report.Rows[index] = RouteCheckResult{NodeInfo: nodeInfo(route), Status: "cancelled", ErrorCode: "cancelled"}
		}
	}
	close(jobs)
	workers.Wait()
	for _, row := range report.Rows {
		if row.Status == "available" {
			report.Available++
			report.Reachable++
		}
		if row.Status == "restricted" {
			report.Reachable++
		}
	}
	if report.Reachable == 0 {
		return report, errors.New("no configured route can reach the OpenAI connectivity target")
	}
	return report, nil
}

func testRouteConnectivity(ctx context.Context, route Route, target string, timeout time.Duration) bool {
	result := checkRouteConnectivity(ctx, route, target, timeout)
	return result.Status == "available" || result.Status == "restricted"
}

func checkRouteConnectivity(ctx context.Context, route Route, target string, timeout time.Duration) RouteCheckResult {
	result := RouteCheckResult{NodeInfo: nodeInfo(route), Status: "unavailable"}
	if route.Transport == nil {
		result.ErrorCode = "connect"
		return result
	}
	if ctx.Err() != nil {
		result.Status, result.ErrorCode = "cancelled", "cancelled"
		return result
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		result.ErrorCode = "target"
		return result
	}
	request.Header.Set("User-Agent", "ccodex-sleep-state")
	request.Header.Set("Range", "bytes=0-0")
	client := &http.Client{Transport: route.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := time.Now()
	response, err := client.Do(request)
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.ErrorCode = connectivityErrorCode(err)
		if ctx.Err() != nil {
			result.Status, result.ErrorCode = "cancelled", "cancelled"
		}
		return result
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	result.HTTPStatus = response.StatusCode
	switch {
	case response.StatusCode == http.StatusProxyAuthRequired:
		result.ErrorCode = "proxy_auth"
	case response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError:
		result.Status, result.ErrorCode = "restricted", "http_restricted"
	default:
		result.Status = "available"
	}
	return result
}

func connectivityErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, mihomoroute.ErrProxyTimeout) || errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, mihomoroute.ErrProxyAuth) || strings.Contains(err.Error(), "407") {
		return "proxy_auth"
	}
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	if errors.Is(err, mihomoroute.ErrProxyTLS) || errors.As(err, &certErr) || errors.As(err, &unknownAuthority) {
		return "tls"
	}
	return "connect"
}
