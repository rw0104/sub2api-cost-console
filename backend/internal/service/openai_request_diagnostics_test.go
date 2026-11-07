package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestdiag"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

type diagnosticsHTTPUpstream struct {
	HTTPUpstream
	calls      int
	lastLength int64
	err        error
}

func (u *diagnosticsHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.lastLength = req.ContentLength
	if u.err != nil {
		return nil, u.err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func TestOpenAIRequestDiagnosticsCountTransportHandoffsIncludingFailures(t *testing.T) {
	ctx := requestdiag.WithContext(context.Background())
	upstream := &diagnosticsHTTPUpstream{err: errors.New("synthetic connection failure")}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, body := range []string{`{"input":"one"}`, `{}`} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test/v1/responses", strings.NewReader(body))
		require.NoError(t, err)
		_, err = svc.doOpenAIUpstream(req, "", account)
		require.Error(t, err)
	}
	got := requestdiag.Load(ctx)
	require.EqualValues(t, upstream.calls, got.UpstreamAttempts)
	require.EqualValues(t, 2, got.UpstreamAttempts)
	require.EqualValues(t, 2, got.UpstreamBodyBytes)
	require.True(t, got.UpstreamBodyKnown)
}

func TestOpenAIRequestDiagnosticsPreprocessDenialIsZeroDispatches(t *testing.T) {
	manager, _ := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionDeny, Reason: "denied"}, nil
	}})
	upstream := &diagnosticsHTTPUpstream{}
	svc := &OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream}
	ctx := requestdiag.WithContext(context.Background())
	req := testPreprocessRequest(t).WithContext(ctx)
	_, err := svc.doOpenAIUpstream(req, "", &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.Error(t, err)
	got := requestdiag.Load(ctx)
	require.True(t, got.UpstreamObserved)
	require.Zero(t, upstream.calls)
	require.Zero(t, got.UpstreamAttempts)
	require.False(t, got.UpstreamBodyKnown)
}

func TestOpenAIRequestDiagnosticsMeasureBodyAfterPluginPreprocessing(t *testing.T) {
	modified := []byte(`{"model":"gpt-test","stream":true,"max_output_tokens":25}`)
	manager, _ := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionModify, Patch: &pluginv2.RequestPatch{BodyChanged: true, BodyJSON: modified}}, nil
	}})
	upstream := &diagnosticsHTTPUpstream{}
	svc := &OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream}
	ctx := requestdiag.WithContext(context.Background())
	resp, err := svc.doOpenAIUpstream(testPreprocessRequest(t).WithContext(ctx), "", &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	got := requestdiag.Load(ctx)
	require.EqualValues(t, len(modified), got.UpstreamBodyBytes)
	require.Equal(t, upstream.lastLength, got.UpstreamBodyBytes)
	require.EqualValues(t, 1, got.UpstreamAttempts)
}

func TestOpenAIRequestDiagnosticsOmitMixedPluginTransportCounts(t *testing.T) {
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "test unavailable"})
	upstream := &diagnosticsHTTPUpstream{}
	svc := &OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream}
	ctx := requestdiag.WithContext(context.Background())
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeAPIKey} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test/v1/responses", strings.NewReader(`{}`))
		require.NoError(t, err)
		resp, dispatchErr := svc.doOpenAIUpstream(req, "", &Account{ID: 1, Platform: PlatformOpenAI, Type: kind})
		if kind == AccountTypeOAuth {
			require.Error(t, dispatchErr)
		} else {
			require.NoError(t, dispatchErr)
			require.NoError(t, resp.Body.Close())
		}
	}
	require.True(t, requestdiag.Load(ctx).UpstreamIncomplete)
	require.Equal(t, 2, upstream.calls)
}

func TestRequestDiagnosticAccessEventRespectsPersistenceOptIn(t *testing.T) {
	sink := &OpsSystemLogSink{}
	event := &logger.LogEvent{Level: "info", Fields: map[string]any{"component": "http.access", "request_body_bytes": int64(42), "upstream_attempts": int64(0)}}
	require.False(t, sink.shouldIndex(event))
	sink.SetPersistAccessLogs(true)
	require.True(t, sink.shouldIndex(event))
}
