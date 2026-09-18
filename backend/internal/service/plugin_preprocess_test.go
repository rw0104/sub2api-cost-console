package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type testPreprocessor struct {
	pluginv2.ExtensionHandler
	call func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error)
}

func TestPluginPreprocessConcurrencyBoundAndCancellation(t *testing.T) {
	cap := testPreprocessCapability()
	cap.TimeoutMS = 5000
	started := make(chan struct{}, extensionConcurrencyLimit)
	release := make(chan struct{})
	m, route := testPreprocessManager(t, cap, &testPreprocessor{call: func(ctx context.Context, _ pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		started <- struct{}{}
		select {
		case <-release:
			return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionPass}, nil
		case <-ctx.Done():
			return pluginv2.PreprocessResponse{}, ctx.Err()
		}
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	var requests sync.WaitGroup
	for i := 0; i < extensionConcurrencyLimit; i++ {
		request := testPreprocessRequest(t)
		requests.Add(1)
		go func() {
			defer requests.Done()
			_, _ = m.PreprocessOpenAI(ctx, request, account)
		}()
	}
	for i := 0; i < extensionConcurrencyLimit; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("plugin calls did not start")
		}
	}
	_, err := m.PreprocessOpenAI(ctx, testPreprocessRequest(t), account)
	require.Error(t, err, "33rd call cannot enter the plugin")
	require.EqualValues(t, extensionConcurrencyLimit, route.calls.inFlight.Load())
	cancel()
	requests.Wait()
	require.Zero(t, route.calls.inFlight.Load())
	require.Zero(t, route.runtime.inFlight.Load())
	require.Zero(t, route.calls.errors.Load(), "client cancellation must not trip the circuit")
}

func (p *testPreprocessor) Preprocess(ctx context.Context, r pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
	return p.call(ctx, r)
}
func testPreprocessCapability() PluginCapability {
	return PluginCapability{ID: pluginv2.CapabilityRequestPreprocess, Kind: pluginv2.CapabilityKindHook,
		Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, TimeoutMS: 100, FailureMode: pluginv2.FailureModeClosed,
		Synchronous: true, Permissions: []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionRequestBody, pluginv2.PermissionRequestMutate}}
}
func testPreprocessManager(t *testing.T, cap PluginCapability, handler *testPreprocessor) (*PluginManager, *extensionRoute) {
	t.Helper()
	i := &PluginInstallation{ID: 1, Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{cap}},
		Bindings: []PluginBinding{{Capability: cap.ID, Platform: cap.Platform, AccountType: cap.AccountType, Enabled: true, RolloutPercent: 100}}}
	m := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	m.publishRuntimeLocked(i, &pluginRuntime{installation: i, extension: handler, done: make(chan struct{})})
	return m, m.extensions.Load().routes[0]
}
func testPreprocessRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewBufferString(`{"model":"gpt-test","stream":true,"max_output_tokens":100}`))
	require.NoError(t, err)
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Api-Key", "secret2")
	r.Header.Set("X-Custom-Secret", "secret3")
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestPluginPreprocessPermissionsAndAtomicPatch(t *testing.T) {
	cap := testPreprocessCapability()
	var seen pluginv2.PreprocessRequest
	handler := &testPreprocessor{call: func(_ context.Context, request pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		seen = request
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionModify, Patch: &pluginv2.RequestPatch{
			BodyChanged: true, BodyJSON: []byte(`{"model":"gpt-test","stream":true,"max_output_tokens":25}`),
			Headers: map[string][]string{"x-sub2api-extension-policy": {"budget"}},
		}}, nil
	}}
	m, _ := testPreprocessManager(t, cap, handler)
	request := testPreprocessRequest(t)
	out, err := m.PreprocessOpenAI(context.Background(), request, &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.NoError(t, err)
	require.NotSame(t, request, out)
	require.Equal(t, "Bearer secret", out.Header.Get("Authorization"))
	require.Equal(t, map[string][]string{"content-type": {"application/json"}}, seen.Context.Headers)
	require.Empty(t, request.Header.Get("X-Sub2api-Extension-Policy"))
	require.Equal(t, "budget", out.Header.Get("X-Sub2api-Extension-Policy"))
	body, err := io.ReadAll(out.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-test","stream":true,"max_output_tokens":25}`, string(body))
	require.Equal(t, int64(len(body)), out.ContentLength)
	require.WithinDuration(t, time.Now(), seen.Context.Deadline, 200*time.Millisecond)
	require.NotEmpty(t, seen.Context.RequestID)
	require.NoError(t, seen.Validate())
}
func TestPluginPreprocessDenialDoesNotFailOver(t *testing.T) {
	cap := testPreprocessCapability()
	m, route := testPreprocessManager(t, cap, &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionDeny, Reason: "sensitive plugin reason"}, nil
	}})
	upstream := &pluginRoutingHTTPUpstream{}
	service := &OpenAIGatewayService{pluginManager: m, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	_, err := service.doOpenAIUpstream(testPreprocessRequest(t), "", account)
	var denied *PluginPreprocessError
	require.ErrorAs(t, err, &denied)
	require.True(t, denied.Denied)
	require.Zero(t, upstream.doCalls)
	require.EqualValues(t, 1, route.calls.denied.Load())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	result := service.handleOpenAIUpstreamTransportError(context.Background(), c, account, err, false)
	require.Same(t, err, result)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "sensitive")
}
func TestPluginPreprocessTimeoutCircuitAndFailOpen(t *testing.T) {
	cap := testPreprocessCapability()
	cap.TimeoutMS = 10
	m, route := testPreprocessManager(t, cap, &testPreprocessor{call: func(ctx context.Context, _ pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		<-ctx.Done()
		return pluginv2.PreprocessResponse{}, ctx.Err()
	}})
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for i := 0; i < 4; i++ {
		started := time.Now()
		_, err := m.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
		require.Error(t, err)
		require.Less(t, time.Since(started), time.Second)
	}
	require.EqualValues(t, 3, route.calls.total.Load())
	require.Zero(t, route.calls.inFlight.Load())
	require.True(t, m.extensionStatus(1)[0].CircuitOpen)
	route.capability.FailureMode = pluginv2.FailureModeOpen
	request := testPreprocessRequest(t)
	out, err := m.PreprocessOpenAI(context.Background(), request, account)
	require.NoError(t, err)
	require.Same(t, request, out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.PreprocessOpenAI(ctx, request, account)
	require.ErrorIs(t, err, context.Canceled)
}
func TestPluginPreprocessDoesNotExposeBodyWithoutPermission(t *testing.T) {
	cap := testPreprocessCapability()
	cap.Permissions = []pluginv2.Permission{pluginv2.PermissionRequestMetadata}
	m, _ := testPreprocessManager(t, cap, &testPreprocessor{call: func(_ context.Context, r pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		require.Empty(t, r.BodyJSON)
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionModify, Patch: &pluginv2.RequestPatch{Headers: map[string][]string{"x-sub2api-extension-test": {"1"}}}}, nil
	}})
	_, err := m.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.Error(t, err)
}
func TestPluginPreprocessProtectsCoreFields(t *testing.T) {
	for _, body := range []string{
		`{"model":"other","stream":true,"max_output_tokens":100}`,
		`{"model":"gpt-test","stream":false,"max_output_tokens":100}`,
		`{"model":"gpt-test","stream":true,"service_tier":"priority","max_output_tokens":100}`,
		`{"model":"gpt-test","stream":true,"max_output_tokens":-1}`,
		`{"model":"gpt-test","stream":true,"temperature":99}`,
		`{"model":"gpt-test","stream":true,"new_core_field":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			request := testPreprocessRequest(t)
			original, _ := io.ReadAll(request.Body)
			_, err := applyExtensionPatch(request, original, &pluginv2.RequestPatch{BodyChanged: true, BodyJSON: []byte(body)}, testPreprocessCapability())
			require.Error(t, err)
		})
	}
}
func TestPluginPreprocessScopeAndUnavailablePolicies(t *testing.T) {
	m, route := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		return pluginv2.PreprocessResponse{}, errors.New("must not call")
	}})
	route.runtime = nil
	request := testPreprocessRequest(t)
	out, err := m.PreprocessOpenAI(context.Background(), request, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	require.NoError(t, err)
	require.Same(t, request, out)
	_, err = m.PreprocessOpenAI(context.Background(), request, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.Error(t, err)
}
