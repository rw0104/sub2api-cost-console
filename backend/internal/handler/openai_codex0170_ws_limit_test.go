package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodex0170WebSocketAdvertisesIngressMessageLimit(t *testing.T) {
	for _, configured := range []int64{0, 128} {
		t.Run(strconv.FormatInt(configured, 10), func(t *testing.T) {
			h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
			h.cfg = &config.Config{}
			h.cfg.Gateway.OpenAIWS.ClientReadLimitBytes = configured
			server := newOpenAIWSHandlerTestServer(t, h, middleware.AuthSubject{UserID: 1, Concurrency: 1})
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			const header = "x-codex-websocket-max-message-bytes"
			options := &coderws.DialOptions{HTTPHeader: http.Header{}}
			options.HTTPHeader.Set(header, "999999")
			conn, res, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", options)
			require.NoError(t, err)
			defer func() { _ = conn.CloseNow() }()
			require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
			require.Equal(t, strconv.FormatInt(service.ResolveOpenAIWSClientReadLimitBytes(h.cfg), 10), res.Header.Get(header))
			if configured != 0 {
				require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(strings.Repeat("x", int(configured)+1))))
				_, _, err = conn.Read(ctx)
				require.Equal(t, coderws.StatusMessageTooBig, coderws.CloseStatus(err))
			}
		})
	}
}
func TestCodex0170WSPreservesPromptCacheTTL(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:  `{"type":"response.create","model":"gpt-6.2-sol","prompt_cache_options":{"ttl":"30m"},"stream":true}`,
		secondPayload: `{"type":"response.create","model":"gpt-6.2-sol","prompt_cache_options":{"ttl":"12h"},"stream":true}`,
		userAgent:     testStringPtr("codex_exec/0.170.0"),
	})
	require.Len(t, got.upstreamPayloads, 2)
	require.Equal(t, "30m", gjson.GetBytes(got.upstreamPayloads[0], "prompt_cache_options.ttl").String())
	require.Equal(t, "12h", gjson.GetBytes(got.upstreamPayloads[1], "prompt_cache_options.ttl").String())
}
