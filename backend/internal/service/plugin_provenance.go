package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	openaiidentity "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

const maxPluginProvenanceValue = 128

type pluginRequestProvenanceKey struct{}

// PluginRequestProvenance is a host-owned snapshot. It is captured before
// outbound identity rewriting and is safe to pass across the plugin boundary.
type PluginRequestProvenance struct {
	CorrelationID   string
	ClientFamily    string
	ClientVersion   string
	OriginalIngress string
	RouteDecision   string
}

// WithPluginRequestProvenance lets an ingress adapter freeze identity before
// the gateway prepares an outbound request. Later transformations must retain
// this value instead of inferring identity from rewritten headers.
func WithPluginRequestProvenance(ctx context.Context, provenance PluginRequestProvenance) context.Context {
	return context.WithValue(ctx, pluginRequestProvenanceKey{}, sanitizePluginProvenance(provenance))
}

func pluginRequestProvenanceFromContext(ctx context.Context) PluginRequestProvenance {
	if ctx == nil {
		return PluginRequestProvenance{}
	}
	provenance, _ := ctx.Value(pluginRequestProvenanceKey{}).(PluginRequestProvenance)
	return sanitizePluginProvenance(provenance)
}

func buildPluginRequestContext(ctx context.Context, request *http.Request, account *Account, deadline time.Time, routeDecision string) pluginv2.RequestContext {
	if ctx == nil {
		ctx = context.Background()
	}
	provenance := pluginRequestProvenanceFromContext(ctx)
	if provenance.OriginalIngress == "" {
		if value, ok := ctx.Value(ctxkey.PluginOriginalIngress).(string); ok {
			provenance.OriginalIngress = sanitizePluginValue(value)
		}
	}
	if provenance.ClientFamily == "" {
		if value, ok := ctx.Value(ctxkey.PluginClientFamily).(string); ok {
			provenance.ClientFamily = sanitizePluginValue(value)
		}
	}
	if provenance.ClientVersion == "" {
		if value, ok := ctx.Value(ctxkey.PluginClientVersion).(string); ok {
			provenance.ClientVersion = sanitizePluginValue(value)
		}
	}
	if provenance.CorrelationID == "" {
		if value, ok := ctx.Value(ctxkey.RequestID).(string); ok {
			provenance.CorrelationID = sanitizePluginValue(value)
		}
	}
	if provenance.CorrelationID == "" {
		if value, ok := ctx.Value(ctxkey.ClientRequestID).(string); ok {
			provenance.CorrelationID = sanitizePluginValue(value)
		}
	}
	if provenance.CorrelationID == "" {
		provenance.CorrelationID = newPluginCorrelationID()
	}
	if request != nil {
		family, version := classifyPluginClient(ctx, request.Header)
		if provenance.ClientFamily == "" {
			provenance.ClientFamily = family
		}
		if provenance.ClientVersion == "" {
			provenance.ClientVersion = version
		}
	}
	if provenance.RouteDecision == "" {
		provenance.RouteDecision = sanitizePluginValue(routeDecision)
	}
	if provenance.OriginalIngress == "" {
		provenance.OriginalIngress = "unknown"
	}
	input := pluginv2.RequestContext{
		CorrelationID: provenance.CorrelationID, ClientFamily: provenance.ClientFamily,
		ClientVersion: provenance.ClientVersion, OriginalIngress: provenance.OriginalIngress,
		RouteDecision: provenance.RouteDecision, Deadline: deadline,
		Headers: map[string][]string{},
	}
	if account != nil {
		input.Platform, input.AccountType, input.AccountID = account.Platform, account.Type, account.ID
	}
	return input
}

func newPluginCorrelationID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return sanitizePluginValue(time.Now().UTC().Format("20060102T150405.000000000Z07:00"))
}

// pluginForwardRequestID preserves a stable logical correlation while keeping
// each plugin transport attempt distinct. v1 has only request_id on the wire;
// the delimiter is therefore the compatibility envelope until formal optional
// provenance fields are added to the generated protocol.
func pluginForwardRequestID(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	provenance := pluginRequestProvenanceFromContext(ctx)
	if provenance.CorrelationID == "" {
		if value, ok := ctx.Value(ctxkey.RequestID).(string); ok {
			provenance.CorrelationID = sanitizePluginValue(value)
		}
	}
	if provenance.CorrelationID == "" {
		provenance.CorrelationID = newPluginCorrelationID()
	}
	return provenance.CorrelationID + ":" + newPluginCorrelationID()
}

func classifyPluginClient(ctx context.Context, headers http.Header) (string, string) {
	if IsClaudeCodeClient(ctx) {
		return "claude_code", sanitizePluginValue(GetClaudeCodeVersion(ctx))
	}
	ua := headers.Get("User-Agent")
	originator := headers.Get("originator")
	if openaiidentity.IsCodexOfficialClientByHeaders(ua, originator) {
		family := "codex"
		switch strings.ToLower(strings.TrimSpace(originator)) {
		case "codex_cli_rs", "codex-tui":
			family = "codex_cli"
		case "codex_vscode", "codex_jetbrains":
			family = "codex_ide"
		case "codex_chatgpt_desktop", "codex_atlas":
			family = "codex_desktop"
		}
		version := openaiidentity.CodexUserAgentVersion(ua)
		if version == "" {
			version = sanitizePluginValue(headers.Get("version"))
		}
		return family, sanitizePluginValue(version)
	}
	lowerUA := strings.ToLower(ua)
	if strings.Contains(lowerUA, "openai") || strings.Contains(lowerUA, "openai-python") || strings.Contains(lowerUA, "openai-node") {
		return "openai_sdk", ""
	}
	return "unknown", ""
}

func sanitizePluginProvenance(provenance PluginRequestProvenance) PluginRequestProvenance {
	provenance.CorrelationID = sanitizePluginValue(provenance.CorrelationID)
	provenance.ClientFamily = sanitizePluginValue(provenance.ClientFamily)
	provenance.ClientVersion = sanitizePluginValue(provenance.ClientVersion)
	provenance.OriginalIngress = sanitizePluginValue(provenance.OriginalIngress)
	provenance.RouteDecision = sanitizePluginValue(provenance.RouteDecision)
	return provenance
}

func sanitizePluginValue(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if len(value) > maxPluginProvenanceValue || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}
