package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type pluginDrainTestTransport struct {
	pluginv1.TransportPluginClient
	frames chan *pluginv1.ForwardResponse
	opened chan context.Context
}

func (p *pluginDrainTestTransport) Forward(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse], error) {
	p.opened <- ctx
	return &pluginDrainTestStream{ctx: ctx, frames: p.frames}, nil
}

type pluginDrainTestStream struct {
	grpc.ClientStream
	ctx    context.Context
	frames <-chan *pluginv1.ForwardResponse
}

func (s *pluginDrainTestStream) Context() context.Context            { return s.ctx }
func (s *pluginDrainTestStream) CloseSend() error                    { return nil }
func (s *pluginDrainTestStream) Send(*pluginv1.ForwardRequest) error { return s.ctx.Err() }
func (s *pluginDrainTestStream) Recv() (*pluginv1.ForwardResponse, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case frame := <-s.frames:
		return frame, nil
	}
}

func newPluginDrainTestManager(t *testing.T, protocol string, headers bool) (*PluginManager, *pluginRuntime, *extensionRoute, *pluginDrainTestTransport) {
	t.Helper()
	transport := &pluginDrainTestTransport{frames: make(chan *pluginv1.ForwardResponse, 8), opened: make(chan context.Context, 1)}
	if headers {
		transport.frames <- &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
			StatusCode: http.StatusOK, Status: "200 OK", Protocol: "HTTP/1.1", ProtocolMajor: 1, ProtocolMinor: 1,
			Headers: headersToPlugin(http.Header{"Content-Type": {"text/event-stream"}}), ContentLength: -1,
		}}}
	}
	runtime := &pluginRuntime{api: transport, client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{})}
	// An explicit per-request test policy must take precedence over this long
	// configured timeout, just as it does for the built-in HTTP transport.
	manager := &PluginManager{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 600}}}
	installation := &PluginInstallation{ID: 1}
	runtime.installation = installation
	if protocol == "v1" {
		manager.publishLegacyRouteLocked(installation, runtime, "")
		return manager, runtime, nil, transport
	}
	capability := protectionTestCapability()
	installation.Manifest = PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{capability}}
	installation.Bindings = []PluginBinding{{Capability: capability.ID, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}}
	runtime.transport = transport
	manager.publishExtensionRoutesLocked(installation, runtime, "")
	return manager, runtime, manager.extensions.Load().routes[0], transport
}

func pluginDrainTestRequest(t *testing.T, ctx context.Context) (*http.Request, *Account) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6.1-terra","stream":true}`))
	require.NoError(t, err)
	return request, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
}

func requirePluginDrainReleased(t *testing.T, runtime *pluginRuntime, route *extensionRoute) {
	t.Helper()
	require.Zero(t, runtime.inFlight.Load())
	if route != nil {
		require.Zero(t, route.calls.inFlight.Load())
		require.Zero(t, route.calls.errors.Load(), "client cleanup must not trip the plugin circuit")
	}
}

func pluginDrainTestBody(value string) *pluginv1.ForwardResponse {
	return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: []byte(value)}}
}

func pluginDrainTestEnd() *pluginv1.ForwardResponse {
	return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{}}}
}

func TestPluginUpstreamDrainPreservesLateUsage(t *testing.T) {
	for _, protocol := range []string{"v1", "v2"} {
		t.Run(protocol, func(t *testing.T) {
			manager, runtime, route, transport := newPluginDrainTestManager(t, protocol, true)
			client, cancelClient := context.WithCancel(context.Background())
			defer cancelClient()
			ctx, stop := context.WithCancel(pkghttputil.DetachUpstreamContext(client, time.Second))
			defer stop()
			request, account := pluginDrainTestRequest(t, ctx)
			response, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
			require.NoError(t, err)
			require.True(t, handled)
			t.Cleanup(func() { _ = response.Body.Close() })
			attempt := <-transport.opened
			cancelClient()
			require.NoError(t, attempt.Err(), "original disconnect must allow late billing usage")
			const usage = "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":17,\"output_tokens\":3}}}\n\n"
			transport.frames <- pluginDrainTestBody(usage)
			transport.frames <- pluginDrainTestEnd()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, usage, string(body))
			require.NoError(t, response.Body.Close())
			require.NoError(t, response.Body.Close(), "close is idempotent")
			require.ErrorIs(t, attempt.Err(), context.Canceled)
			requirePluginDrainReleased(t, runtime, route)
		})
	}
}

func TestPluginUpstreamDrainBoundsContinuingHeartbeats(t *testing.T) {
	for _, protocol := range []string{"v1", "v2"} {
		t.Run(protocol, func(t *testing.T) {
			manager, runtime, route, transport := newPluginDrainTestManager(t, protocol, true)
			client, cancelClient := context.WithCancel(context.Background())
			defer cancelClient()
			ctx, stop := context.WithCancel(pkghttputil.DetachUpstreamContext(client, 40*time.Millisecond))
			defer stop()
			request, account := pluginDrainTestRequest(t, ctx)
			response, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
			require.NoError(t, err)
			require.True(t, handled)
			t.Cleanup(func() { _ = response.Body.Close() })
			attempt := <-transport.opened
			producerDone := make(chan struct{})
			go func() {
				defer close(producerDone)
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-attempt.Done():
						return
					case <-ticker.C:
						select {
						case transport.frames <- pluginDrainTestBody(": heartbeat\n\n"):
						case <-attempt.Done():
							return
						}
					}
				}
			}()
			readDone := make(chan error, 1)
			go func() { _, readErr := io.ReadAll(response.Body); readDone <- readErr }()
			cancelClient()
			select {
			case err := <-readDone:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(2 * time.Second):
				stop()
				t.Fatal("plugin heartbeats extended the total disconnect drain budget")
			}
			<-producerDone
			require.NoError(t, response.Body.Close())
			requirePluginDrainReleased(t, runtime, route)
		})
	}
}

func TestPluginUpstreamDrainDoesNotLimitHealthyStream(t *testing.T) {
	for _, protocol := range []string{"v1", "v2"} {
		t.Run(protocol, func(t *testing.T) {
			manager, runtime, route, transport := newPluginDrainTestManager(t, protocol, true)
			client, cancelClient := context.WithCancel(context.Background())
			defer cancelClient()
			ctx := pkghttputil.DetachUpstreamContext(client, 20*time.Millisecond)
			request, account := pluginDrainTestRequest(t, ctx)
			response, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
			require.NoError(t, err)
			require.True(t, handled)
			t.Cleanup(func() { _ = response.Body.Close() })
			attempt := <-transport.opened
			select {
			case <-attempt.Done():
				t.Fatal("healthy plugin stream acquired an unconditional drain deadline")
			case <-time.After(60 * time.Millisecond):
			}
			transport.frames <- pluginDrainTestEnd()
			_, err = io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.ErrorIs(t, attempt.Err(), context.Canceled)
			requirePluginDrainReleased(t, runtime, route)
		})
	}
}

func TestPluginUpstreamDrainCancellationBeforeHeadersReleasesRequests(t *testing.T) {
	for _, protocol := range []string{"v1", "v2"} {
		for _, detached := range []bool{false, true} {
			t.Run(protocol+"/detached="+map[bool]string{true: "true", false: "false"}[detached], func(t *testing.T) {
				manager, runtime, route, transport := newPluginDrainTestManager(t, protocol, false)
				client, cancelClient := context.WithCancel(context.Background())
				defer cancelClient()
				parent := client
				if detached {
					parent = pkghttputil.DetachUpstreamContext(client, 40*time.Millisecond)
				}
				ctx, stop := context.WithCancel(parent)
				defer stop()
				request, account := pluginDrainTestRequest(t, ctx)
				finished := make(chan error, 1)
				go func() {
					_, _, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
					finished <- err
				}()
				<-transport.opened
				cancelClient()
				select {
				case err := <-finished:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(2 * time.Second):
					stop()
					t.Fatal("cancelled plugin request kept waiting for response headers")
				}
				requirePluginDrainReleased(t, runtime, route)
			})
		}
	}
}

func TestPluginUpstreamDrainProtectionHeaderTimeoutRemainsFailure(t *testing.T) {
	manager, runtime, route, _ := newPluginDrainTestManager(t, "v2", false)
	route.capability.TimeoutMS = 30
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, account := pluginDrainTestRequest(t, ctx)
	_, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
	require.True(t, handled)
	require.Error(t, err)
	require.NoError(t, ctx.Err(), "plugin header deadline must fire before caller deadline")
	require.Zero(t, runtime.inFlight.Load())
	require.Zero(t, route.calls.inFlight.Load())
	require.EqualValues(t, 1, route.calls.errors.Load(), "real plugin header failures must still count")
}

// A peer may report cancellation while the local client is still connected.
// This must remain a transport failure, not masquerade as our drain cleanup.
type pluginUnexpectedCancelTransport struct {
	pluginv1.TransportPluginClient
	phase string
}

func (p *pluginUnexpectedCancelTransport) Forward(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse], error) {
	if p.phase == "open" {
		return nil, context.Canceled
	}
	return &pluginUnexpectedCancelStream{ctx: ctx, phase: p.phase}, nil
}

type pluginUnexpectedCancelStream struct {
	grpc.ClientStream
	ctx        context.Context
	phase      string
	headerSent bool
}

func (s *pluginUnexpectedCancelStream) Context() context.Context { return s.ctx }
func (s *pluginUnexpectedCancelStream) CloseSend() error         { return nil }
func (s *pluginUnexpectedCancelStream) Send(request *pluginv1.ForwardRequest) error {
	if s.phase == "send_body" && len(request.GetBodyChunk()) > 0 {
		// The runtime itself cancels the receiving goroutine after this error.
		return context.Canceled
	}
	return nil
}
func (s *pluginUnexpectedCancelStream) Recv() (*pluginv1.ForwardResponse, error) {
	if s.phase == "send_body" {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	if s.phase == "receive_headers" || s.headerSent {
		return nil, context.Canceled
	}
	s.headerSent = true
	return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
		StatusCode: http.StatusOK, Status: "200 OK", Protocol: "HTTP/1.1", ProtocolMajor: 1, ProtocolMinor: 1,
		Headers: headersToPlugin(http.Header{"Content-Type": {"text/event-stream"}}), ContentLength: -1,
	}}}, nil
}

func TestPluginUpstreamDrainPeerCancellationRemainsFailure(t *testing.T) {
	for _, phase := range []string{"open", "receive_headers", "receive_body", "send_body"} {
		t.Run(phase, func(t *testing.T) {
			manager, runtime, route, _ := newPluginDrainTestManager(t, "v2", false)
			runtime.transport = &pluginUnexpectedCancelTransport{phase: phase}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request, account := pluginDrainTestRequest(t, ctx)
			response, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
			require.True(t, handled)
			if phase == "receive_body" {
				require.NoError(t, err)
				t.Cleanup(func() { _ = response.Body.Close() })
				_, err = io.ReadAll(response.Body)
				require.NoError(t, response.Body.Close())
			}
			require.NoError(t, ctx.Err(), "the local caller must still be active")
			var transportErr *PluginTransportError
			require.ErrorAs(t, err, &transportErr, "a peer cancellation must remain an upstream failure")
			require.Equal(t, phase != "open", transportErr.RequestSent, "preserve safe replay ownership")
			require.Zero(t, runtime.inFlight.Load())
			require.Zero(t, route.calls.inFlight.Load())
			require.EqualValues(t, 1, route.calls.errors.Load())
		})
	}
}

type pluginSendFailureAfterHeadersTransport struct {
	pluginv1.TransportPluginClient
	release     <-chan struct{}
	eofOnCancel bool
}

func (p *pluginSendFailureAfterHeadersTransport) Forward(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse], error) {
	return &pluginSendFailureAfterHeadersStream{ctx: ctx, release: p.release, eofOnCancel: p.eofOnCancel}, nil
}

type pluginSendFailureAfterHeadersStream struct {
	grpc.ClientStream
	ctx         context.Context
	release     <-chan struct{}
	eofOnCancel bool
	headerSent  bool
}

func (s *pluginSendFailureAfterHeadersStream) Context() context.Context { return s.ctx }
func (s *pluginSendFailureAfterHeadersStream) CloseSend() error         { return nil }
func (s *pluginSendFailureAfterHeadersStream) Send(request *pluginv1.ForwardRequest) error {
	if len(request.GetBodyChunk()) > 0 {
		select {
		case <-s.release:
			return context.Canceled
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	return nil
}
func (s *pluginSendFailureAfterHeadersStream) Recv() (*pluginv1.ForwardResponse, error) {
	if !s.headerSent {
		s.headerSent = true
		return &pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
			StatusCode: http.StatusOK, Status: "200 OK", Protocol: "HTTP/1.1", ProtocolMajor: 1, ProtocolMinor: 1,
			Headers: headersToPlugin(http.Header{"Content-Type": {"text/event-stream"}}), ContentLength: -1,
		}}}, nil
	}
	<-s.ctx.Done()
	if s.eofOnCancel {
		return nil, io.EOF
	}
	return nil, s.ctx.Err()
}

func TestPluginUpstreamDrainSendFailureAfterHeadersRemainsFailure(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(map[bool]string{false: "receive_cancelled", true: "receive_eof"}[eof], func(t *testing.T) {
			manager, runtime, route, _ := newPluginDrainTestManager(t, "v2", false)
			release := make(chan struct{})
			runtime.transport = &pluginSendFailureAfterHeadersTransport{release: release, eofOnCancel: eof}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request, account := pluginDrainTestRequest(t, ctx)
			response, handled, err := manager.RoundTripOpenAIOAuth(ctx, request, "", account)
			require.NoError(t, err)
			require.True(t, handled)
			t.Cleanup(func() { _ = response.Body.Close() })
			close(release) // The peer send failure occurs only after headers succeeded.
			_, err = io.ReadAll(response.Body)
			require.NoError(t, ctx.Err())
			var transportErr *PluginTransportError
			require.ErrorAs(t, err, &transportErr)
			require.True(t, transportErr.RequestSent, "a partial upload must never become safe to replay")
			require.NoError(t, response.Body.Close())
			require.Zero(t, runtime.inFlight.Load())
			require.Zero(t, route.calls.inFlight.Load())
			require.EqualValues(t, 1, route.calls.errors.Load(), "send cleanup must not hide peer failure")
		})
	}
}
