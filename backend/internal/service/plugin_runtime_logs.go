package service

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultPluginRuntimeLogBytes = 64 * 1024
	maxPluginRuntimeLogWrite     = 256 * 1024
	pluginRuntimeLogStreamStdout = "stdout"
	pluginRuntimeLogStreamStderr = "stderr"
)

var pluginRuntimeLogSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(\b(?:authorization|proxy-authorization|x-api-key|api[-_]key|access[-_]token|client[-_]secret|password|secret|token)\s*[:=]\s*["']?)[^\s,"']+`),
	regexp.MustCompile(`(?i)([?&](?:access_token|api_key|api-key|client_secret|token)=)[^&\s]+`),
}

// pluginRuntimeLogSink is a bounded, per-runtime writer suitable for
// go-plugin's SyncStdout/SyncStderr hooks. It acknowledges every input byte so
// a noisy plugin cannot block or crash the host, while retaining only a recent
// redacted tail for diagnostics.
type pluginRuntimeLogSink struct {
	mu           sync.RWMutex
	instanceID   string
	stream       string
	maxBytes     int
	data         []byte
	droppedBytes uint64
	updatedAt    time.Time
}

type pluginRuntimeLogSnapshot struct {
	InstanceID   string    `json:"instance_id"`
	Stream       string    `json:"stream"`
	Data         string    `json:"data"`
	Bytes        int       `json:"bytes"`
	DroppedBytes uint64    `json:"dropped_bytes"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

func newPluginRuntimeLogSink(instanceID, stream string, maxBytes int) *pluginRuntimeLogSink {
	if maxBytes <= 0 {
		maxBytes = defaultPluginRuntimeLogBytes
	}
	return &pluginRuntimeLogSink{
		instanceID: instanceID,
		stream:     stream,
		maxBytes:   maxBytes,
		data:       make([]byte, 0, maxBytes),
	}
}

func (s *pluginRuntimeLogSink) Write(input []byte) (int, error) {
	if s == nil {
		return len(input), nil
	}
	written := len(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	if written == 0 {
		return 0, nil
	}
	if len(input) > maxPluginRuntimeLogWrite {
		s.droppedBytes += uint64(len(input) - maxPluginRuntimeLogWrite)
		input = input[:maxPluginRuntimeLogWrite]
	}
	redacted := redactPluginRuntimeLog(input)
	if len(redacted) > s.maxBytes {
		s.droppedBytes += uint64(len(redacted) - s.maxBytes)
		redacted = redacted[len(redacted)-s.maxBytes:]
	}
	if overflow := len(s.data) + len(redacted) - s.maxBytes; overflow > 0 {
		s.droppedBytes += uint64(overflow)
		s.data = append([]byte(nil), s.data[overflow:]...)
	}
	s.data = append(s.data, redacted...)
	s.updatedAt = time.Now().UTC()
	return written, nil
}

func (s *pluginRuntimeLogSink) snapshot() pluginRuntimeLogSnapshot {
	if s == nil {
		return pluginRuntimeLogSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginRuntimeLogSnapshot{
		InstanceID:   s.instanceID,
		Stream:       s.stream,
		Data:         string(append([]byte(nil), s.data...)),
		Bytes:        len(s.data),
		DroppedBytes: s.droppedBytes,
		UpdatedAt:    s.updatedAt,
	}
}

func (r *pluginRuntime) runtimeLogSnapshots() (pluginRuntimeLogSnapshot, pluginRuntimeLogSnapshot) {
	if r == nil {
		return pluginRuntimeLogSnapshot{}, pluginRuntimeLogSnapshot{}
	}
	return r.stdoutLog.snapshot(), r.stderrLog.snapshot()
}

func redactPluginRuntimeLog(input []byte) []byte {
	text := sanitizePluginRuntimeLogText(string(input))
	for _, pattern := range pluginRuntimeLogSecretPatterns {
		text = pattern.ReplaceAllStringFunc(text, func(match string) string {
			if strings.HasPrefix(strings.ToLower(match), "bearer ") {
				return "Bearer [REDACTED]"
			}
			if index := strings.IndexAny(match, ":="); index >= 0 {
				return match[:index+1] + "[REDACTED]"
			}
			if index := strings.Index(match, "="); index >= 0 {
				return match[:index+1] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return []byte(text)
}

func sanitizePluginRuntimeLogText(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, char := range text {
		switch char {
		case '\n', '\r', '\t':
			builder.WriteRune(char)
		default:
			if char < 0x20 || char == 0x7f {
				builder.WriteString(fmt.Sprintf("\\x%02x", char))
				continue
			}
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
