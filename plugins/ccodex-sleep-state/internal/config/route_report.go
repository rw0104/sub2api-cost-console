package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// RouteRequest is a one-shot UI command consumed by ValidateConfig. It must
// never survive normalization into saved configuration or be replayed on start.
type RouteRequest struct {
	Operation string `json:"operation"`
	RouteID   string `json:"route_id,omitempty"`
}

// RouteReport is display-only data. Route selection and forwarding never trust
// reported availability; only actual configured sources build route registries.
type RouteReport struct {
	Schema          int         `json:"schema"`
	SourceSignature string      `json:"source_signature"`
	GeneratedAt     string      `json:"generated_at"`
	ErrorCode       string      `json:"error_code,omitempty"`
	Nodes           []RouteNode `json:"nodes"`
}

type RouteNode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	Status     string `json:"status"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

// SourceSignature identifies the inputs to global node discovery. Choosing a
// fixed node or changing state policy must not erase the corresponding results.
func (c Config) SourceSignature() string {
	payload, _ := json.Marshal(struct {
		URLs                 []string       `json:"urls,omitempty"`
		Direct               bool           `json:"direct"`
		ProxyEnvs            []string       `json:"proxy_envs,omitempty"`
		Subscriptions        []Subscription `json:"subscriptions,omitempty"`
		SubscriptionProxyEnv string         `json:"subscription_proxy_env,omitempty"`
	}{c.RouteURLs(), c.Direct, c.ProxyEnvs, c.Subscriptions, c.SubscriptionProxyEnv})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func SanitizeRouteReport(report *RouteReport, signature string) *RouteReport {
	if report == nil || report.Schema != 1 || report.SourceSignature != signature || len(report.Nodes) > MaxRouteURLs {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, report.GeneratedAt); err != nil {
		return nil
	}
	clean := &RouteReport{Schema: 1, SourceSignature: signature, GeneratedAt: report.GeneratedAt, ErrorCode: SafeReportCode(report.ErrorCode), Nodes: make([]RouteNode, 0, len(report.Nodes))}
	seen := map[string]bool{}
	for _, node := range report.Nodes {
		if !validRouteID(node.ID) || seen[node.ID] {
			continue
		}
		seen[node.ID] = true
		node.Name = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, node.Name))
		if len([]rune(node.Name)) > 80 {
			node.Name = string([]rune(node.Name)[:80])
		}
		switch node.Protocol {
		case "direct", "http", "https", "socks5", "socks5h", "ss", "ssr", "vmess", "vless", "trojan", "anytls", "hysteria", "hysteria2", "tuic":
		default:
			node.Protocol = "proxy"
		}
		if node.Name == "" || strings.Contains(node.Name, "://") || strings.Contains(node.Name, "@") {
			node.Name = node.Protocol + " 线路"
		}
		switch node.Status {
		case "untested", "available", "restricted", "unavailable", "cancelled":
		default:
			node.Status = "untested"
		}
		if node.LatencyMS < 0 || node.LatencyMS > 120000 {
			node.LatencyMS = 0
		}
		if node.HTTPStatus < 100 || node.HTTPStatus > 599 {
			node.HTTPStatus = 0
		}
		node.ErrorCode = SafeReportCode(node.ErrorCode)
		clean.Nodes = append(clean.Nodes, node)
	}
	return clean
}

func validRouteID(id string) bool {
	if id == "route-direct" {
		return true
	}
	if !strings.HasPrefix(id, "route-") || len(id) > 128 || len(id) < 7 {
		return false
	}
	for _, r := range id[6:] {
		if r < '0' || r > '9' {
			if r < 'a' || r > 'f' {
				return false
			}
		}
	}
	return true
}

// Only known categories can cross the UI boundary, never upstream error text.
func SafeReportCode(code string) string {
	switch code {
	case "proxy_auth", "timeout", "tls", "cancelled", "connect", "target", "http_restricted":
		return code
	case "", "SOURCE_TIMEOUT", "SOURCE_CANCELLED", "PROXY_ENV_EMPTY", "SUBSCRIPTION_ENV_EMPTY", "SUBSCRIPTION_DOWNLOAD_FAILED", "SUBSCRIPTION_HTTP_ERROR", "SUBSCRIPTION_FILE_ERROR", "SUBSCRIPTION_TOO_LARGE", "SUBSCRIPTION_EMPTY", "SUBSCRIPTION_INVALID", "ROUTE_LIMIT_EXCEEDED", "ROUTE_SOURCE_INVALID", "ROUTE_UNAVAILABLE", "NO_ROUTES", "NO_AVAILABLE_ROUTES", "TEST_TIMEOUT", "TEST_CANCELLED", "PROXY_AUTH_REQUIRED", "PROXY_AUTH_FAILED", "PROXY_CONNECTION_FAILED", "PROXY_TIMEOUT", "PROXY_TLS_ERROR", "UPSTREAM_RESTRICTED", "UPSTREAM_HTTP_ERROR", "CONNECT_FAILED", "CONNECT_TIMEOUT", "TLS_FAILED", "CANCELLED":
		return code
	default:
		return "ROUTE_SOURCE_INVALID"
	}
}
