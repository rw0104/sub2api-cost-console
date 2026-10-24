package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

func TestHeaderProbeRequestContextIncludesTransportFields(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://api.openai.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	ctx := WithOpenAIForwardModel(context.Background(), "gpt-6-sol", false)
	requestContext := buildPluginRequestContext(ctx, request, &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, time.Now().Add(time.Second), "SELECTED")
	requestContext.Method = request.Method
	requestContext.Path = request.URL.Path
	requestContext.Host = request.URL.Hostname()
	forwardModel, ok := openAIForwardModelFromContext(ctx)
	require.True(t, ok)
	requestContext.Model = forwardModel.model
	requestContext.RequestID = "header-probe-test"
	requestContext.TraceID = requestContext.RequestID
	require.NoError(t, (pluginv2.PreprocessRequest{Capability: pluginv2.CapabilityRequestHeaderProbe, Context: requestContext}).Validate())
	require.Equal(t, "gpt-6-sol", requestContext.Model)
}

func TestParseHeaderProbeStateReportsSafeShapeOnly(t *testing.T) {
	raw := make([]byte, 57+16*10)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Unix()))
	value := base64.RawURLEncoding.EncodeToString(raw)

	parsed, blocks := parseHeaderProbeState(value)
	require.True(t, parsed)
	require.Equal(t, 10, blocks)
	parsed, blocks = parseHeaderProbeState("not-a-state")
	require.False(t, parsed)
	require.Zero(t, blocks)
}

func TestHeaderProbeSignalsNeverIncludeRawState(t *testing.T) {
	secret := "opaque-state-value"
	signals := headerProbeSignals(http.Header{"X-Codex-Turn-State": []string{secret}})
	require.Equal(t, "true", signals["x-sub2api-probe-state-present"][0])
	require.NotContains(t, signals, secret)
	require.NotContains(t, signals, "X-Codex-Turn-State")
}
