package responseheaders

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// defaultAllowed 定义允许透传的响应头白名单
// 注意：以下头部由 Go HTTP 包自动处理，不应手动设置：
//   - content-length: 由 ResponseWriter 根据实际写入数据自动设置
//   - transfer-encoding: 由 HTTP 库根据需要自动添加/移除
//   - connection: 由 HTTP 库管理连接复用
var defaultAllowed = map[string]struct{}{
	"content-type":                   {},
	"content-encoding":               {},
	"content-language":               {},
	"cache-control":                  {},
	"etag":                           {},
	"last-modified":                  {},
	"expires":                        {},
	"vary":                           {},
	"date":                           {},
	"x-request-id":                   {},
	"x-ratelimit-limit-requests":     {},
	"x-ratelimit-limit-tokens":       {},
	"x-ratelimit-remaining-requests": {},
	"x-ratelimit-remaining-tokens":   {},
	"x-ratelimit-reset-requests":     {},
	"x-ratelimit-reset-tokens":       {},
	"retry-after":                    {},
	"location":                       {},
	"www-authenticate":               {},
	// Codex uses this response header to avoid estimating reasoning tokens a
	// second time when upstream usage already includes them.
	"x-reasoning-included": {},
}

// hopByHopHeaders 是跳过的 hop-by-hop 头部，这些头部由 HTTP 库自动处理
var hopByHopHeaders = map[string]struct{}{
	"content-length":    {},
	"transfer-encoding": {},
	"connection":        {},
}

type CompiledHeaderFilter struct {
	allowed     map[string]struct{}
	forceRemove map[string]struct{}
}

var defaultCompiledHeaderFilter = CompileHeaderFilter(config.ResponseHeaderConfig{})

func CompileHeaderFilter(cfg config.ResponseHeaderConfig) *CompiledHeaderFilter {
	allowed := make(map[string]struct{}, len(defaultAllowed)+len(cfg.AdditionalAllowed))
	for key := range defaultAllowed {
		allowed[key] = struct{}{}
	}
	// 关闭时只使用默认白名单，additional/force_remove 不生效
	if cfg.Enabled {
		for _, key := range cfg.AdditionalAllowed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "" {
				continue
			}
			allowed[normalized] = struct{}{}
		}
	}

	forceRemove := map[string]struct{}{}
	if cfg.Enabled {
		forceRemove = make(map[string]struct{}, len(cfg.ForceRemove))
		for _, key := range cfg.ForceRemove {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "" {
				continue
			}
			forceRemove[normalized] = struct{}{}
		}
	}

	return &CompiledHeaderFilter{
		allowed:     allowed,
		forceRemove: forceRemove,
	}
}

func FilterHeaders(src http.Header, filter *CompiledHeaderFilter) http.Header {
	if filter == nil {
		filter = defaultCompiledHeaderFilter
	}

	filtered := make(http.Header, len(src))
	for key, values := range src {
		lower := strings.ToLower(key)
		if _, blocked := filter.forceRemove[lower]; blocked {
			continue
		}
		if _, ok := filter.allowed[lower]; !ok {
			continue
		}
		// 跳过 hop-by-hop 头部，这些由 HTTP 库自动处理
		if _, isHopByHop := hopByHopHeaders[lower]; isHopByHop {
			continue
		}
		for _, value := range values {
			filtered.Add(key, value)
		}
	}
	return filtered
}

func WriteFilteredHeaders(dst http.Header, src http.Header, filter *CompiledHeaderFilter) {
	filtered := FilterHeaders(src, filter)
	for key, values := range filtered {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// IsSafeRetryAfter accepts only HTTP dates and nonnegative delays within the gateway's
// seven-day retry window. It can be used for both HTTP and Responses event headers.
func IsSafeRetryAfter(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	digitsOnly := true
	for _, char := range value {
		if char < '0' || char > '9' {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		seconds, err := strconv.ParseUint(value, 10, 32)
		return err == nil && seconds <= uint64((7*24*time.Hour)/time.Second)
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return false
	}
	return !retryAt.After(time.Now().Add(7 * 24 * time.Hour))
}
