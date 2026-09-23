package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PluginSecretAuditResult is the host-owned outcome of one SecretBroker read.
// It deliberately contains only stable classes; callers must not expose the
// underlying repository, decryptor, or plugin error text to the audit stream.
type PluginSecretAuditResult string

const (
	PluginSecretAuditGranted       PluginSecretAuditResult = "granted"
	PluginSecretAuditDenied        PluginSecretAuditResult = "denied"
	PluginSecretAuditExpired       PluginSecretAuditResult = "expired"
	PluginSecretAuditRateLimited   PluginSecretAuditResult = "rate_limited"
	PluginSecretAuditInternalError PluginSecretAuditResult = "internal_error"
)

// PluginSecretAuditEvent is safe to persist or send to an observability sink.
// In particular, it has no field for a secret value and aliases are included
// only after the host validates/sanitizes them.
type PluginSecretAuditEvent struct {
	Time          time.Time               `json:"time"`
	PluginID      int64                   `json:"plugin_id"`
	Capability    string                  `json:"capability"`
	Alias         string                  `json:"alias"`
	BindingID     int64                   `json:"binding_id,omitempty"`
	InstanceID    string                  `json:"instance_id,omitempty"`
	CorrelationID string                  `json:"correlation_id,omitempty"`
	Result        PluginSecretAuditResult `json:"result"`
	ErrorCode     string                  `json:"error_code,omitempty"`
	TTLMS         int64                   `json:"ttl_ms,omitempty"`
	DurationMS    int64                   `json:"duration_ms"`
	Suppressed    uint64                  `json:"suppressed,omitempty"`
}

// PluginSecretAuditSink is an optional persistence/observability port. The
// host invokes it asynchronously with a bounded timeout, so a slow or failed
// sink cannot hold a plugin RPC open. Implementations must treat the event as
// already redacted and must not attempt to add the secret value.
type PluginSecretAuditSink interface {
	RecordPluginSecretAudit(context.Context, PluginSecretAuditEvent) error
}

// PluginSecretAuditSinkFunc adapts a function to PluginSecretAuditSink. It is
// useful for the repository-backed sink and deterministic tests.
type PluginSecretAuditSinkFunc func(context.Context, PluginSecretAuditEvent) error

func (f PluginSecretAuditSinkFunc) RecordPluginSecretAudit(ctx context.Context, event PluginSecretAuditEvent) error {
	if f == nil {
		return nil
	}
	return f(ctx, event)
}

// PluginSecretReadError lets a secret provider preserve a safe outcome class
// without exposing provider error text. The value itself is never retained.
type PluginSecretReadError struct {
	Result    PluginSecretAuditResult
	ErrorCode string
}

func (e *PluginSecretReadError) Error() string { return "plugin secret read failed" }

const (
	pluginSecretAuditRateWindow        = time.Second
	pluginSecretAuditMaxEvents         = 128
	pluginSecretAuditMaxRateKeys       = 1024
	pluginSecretAuditSinkSlots         = 8
	pluginSecretAuditSinkTimeout       = 500 * time.Millisecond
	pluginSecretAuditMaxFieldRunes     = 128
	pluginSecretAuditMaxErrorCodeRunes = 64
)

type pluginSecretAuditBucket struct {
	lastAt     time.Time
	lastResult PluginSecretAuditResult
	suppressed uint64
}

func normalizePluginSecretAuditResult(result PluginSecretAuditResult) PluginSecretAuditResult {
	switch result {
	case PluginSecretAuditGranted, PluginSecretAuditDenied, PluginSecretAuditExpired,
		PluginSecretAuditRateLimited, PluginSecretAuditInternalError:
		return result
	default:
		return PluginSecretAuditInternalError
	}
}

func sanitizePluginSecretAuditField(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	runes := []rune(value)
	if len(runes) > pluginSecretAuditMaxFieldRunes {
		runes = runes[:pluginSecretAuditMaxFieldRunes]
	}
	return string(runes)
}

func sanitizePluginSecretAuditAlias(alias string) string {
	if !pluginSecretAliasPattern.MatchString(alias) {
		// An invalid alias may itself contain sensitive material. Keep the event
		// useful without copying attacker/plugin-controlled text into the log.
		return "<invalid>"
	}
	return alias
}

func sanitizePluginSecretAuditCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(code)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
		if b.Len() >= pluginSecretAuditMaxErrorCodeRunes {
			break
		}
	}
	if b.Len() == 0 {
		return "internal_error"
	}
	return b.String()
}

func pluginSecretAuditErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	switch status.Code(err) {
	case codes.PermissionDenied:
		return "permission_denied"
	case codes.ResourceExhausted:
		return "rate_limited"
	case codes.Unavailable:
		return "host_unavailable"
	case codes.InvalidArgument:
		return "invalid_argument"
	case codes.FailedPrecondition:
		return "failed_precondition"
	case codes.DeadlineExceeded:
		return "deadline_exceeded"
	case codes.Canceled:
		return "canceled"
	case codes.Internal:
		return "internal_error"
	}
	return "internal_error"
}

func pluginSecretAuditOutcomeForError(err error) (PluginSecretAuditResult, string) {
	if err == nil {
		return PluginSecretAuditInternalError, "internal_error"
	}
	var typed *PluginSecretReadError
	if errors.As(err, &typed) && typed != nil {
		return normalizePluginSecretAuditResult(typed.Result), sanitizePluginSecretAuditCode(typed.ErrorCode)
	}
	// The current repository adapter intentionally exposes a generic message
	// for a missing grant. Treat that stable sentinel as a denial; provider
	// implementations that need a finer class can return PluginSecretReadError.
	if strings.TrimSpace(err.Error()) == "secret unavailable" {
		return PluginSecretAuditDenied, "secret_unavailable"
	}
	return PluginSecretAuditInternalError, pluginSecretAuditErrorCode(err)
}
