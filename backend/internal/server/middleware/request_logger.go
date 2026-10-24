package middleware

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const requestIDHeader = "X-Request-ID"

// RequestLogger 在请求入口注入 request-scoped logger。
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}

		requestID, validRequestID := normalizeCorrelationID(c.GetHeader(requestIDHeader))
		if !validRequestID {
			requestID = uuid.NewString()
		}
		c.Header(requestIDHeader, requestID)

		ctx := context.WithValue(c.Request.Context(), ctxkey.RequestID, requestID)
		ctx = context.WithValue(ctx, ctxkey.PluginOriginalIngress, boundedPluginContextValue(c.Request.URL.Path))
		clientFamily, clientVersion := classifyPluginClientHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
		ctx = context.WithValue(ctx, ctxkey.PluginClientFamily, clientFamily)
		ctx = context.WithValue(ctx, ctxkey.PluginClientVersion, clientVersion)
		clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string)
		clientRequestID, _ = normalizeCorrelationID(clientRequestID)

		requestLogger := logger.With(
			zap.String("component", "http"),
			zap.String("request_id", requestID),
			zap.String("client_request_id", strings.TrimSpace(clientRequestID)),
			zap.String("path", c.Request.URL.Path),
			zap.String("method", c.Request.Method),
		)

		ctx = logger.IntoContext(ctx, requestLogger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func boundedPluginContextValue(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func classifyPluginClientHeaders(userAgent, originator string) (string, string) {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	originator = strings.ToLower(strings.TrimSpace(originator))
	if strings.Contains(ua, "claude-code") {
		return "claude_code", pluginClientVersionFromUA(userAgent, "claude-code")
	}
	if strings.Contains(ua, "codex") || strings.HasPrefix(originator, "codex") {
		family := "codex"
		switch originator {
		case "codex_cli_rs", "codex-tui":
			family = "codex_cli"
		case "codex_vscode", "codex_jetbrains":
			family = "codex_ide"
		case "codex_chatgpt_desktop", "codex_atlas":
			family = "codex_desktop"
		}
		return family, pluginClientVersionFromUA(userAgent, "codex")
	}
	if strings.Contains(ua, "openai") {
		return "openai_sdk", ""
	}
	return "unknown", ""
}

func pluginClientVersionFromUA(userAgent, family string) string {
	lower := strings.ToLower(userAgent)
	markers := []string{family + "/"}
	if family == "codex" {
		markers = []string{"codex_cli_rs/", "codex-tui/", "codex_vscode/", "codex_jetbrains/", "codex_chatgpt_desktop/", "codex_atlas/"}
	}
	idx := -1
	markerLength := 0
	for _, marker := range markers {
		if candidate := strings.Index(lower, marker); candidate >= 0 && (idx < 0 || candidate < idx) {
			idx = candidate
			markerLength = len(marker)
		}
	}
	if idx < 0 {
		return ""
	}
	value := userAgent[idx+markerLength:]
	if end := strings.IndexAny(value, " (;/\\"); end >= 0 {
		value = value[:end]
	}
	return boundedPluginContextValue(value)
}
