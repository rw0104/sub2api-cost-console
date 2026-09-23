package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func TestPluginForwardRequestIDKeepsCorrelationAcrossAttempts(t *testing.T) {
	ctx := WithPluginRequestProvenance(context.Background(), PluginRequestProvenance{CorrelationID: "corr-123"})
	first := pluginForwardRequestID(ctx)
	second := pluginForwardRequestID(ctx)
	if !strings.HasPrefix(first, "corr-123:") || !strings.HasPrefix(second, "corr-123:") {
		t.Fatalf("attempt ids lost correlation: %q %q", first, second)
	}
	if first == second {
		t.Fatal("separate plugin attempts must have distinct request ids")
	}
}

func TestBuildPluginRequestContextUsesIngressSnapshot(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxkey.RequestID, "server-request")
	ctx = context.WithValue(ctx, ctxkey.PluginOriginalIngress, "/v1/responses")
	ctx = context.WithValue(ctx, ctxkey.PluginClientFamily, "codex_cli")
	ctx = context.WithValue(ctx, ctxkey.PluginClientVersion, "0.146.0")
	request, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := buildPluginRequestContext(ctx, request, &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, time.Now().Add(time.Second), "SELECTED")
	if got.CorrelationID != "server-request" || got.OriginalIngress != "/v1/responses" || got.ClientFamily != "codex_cli" || got.ClientVersion != "0.146.0" {
		t.Fatalf("ingress snapshot not propagated: %#v", got)
	}
}
