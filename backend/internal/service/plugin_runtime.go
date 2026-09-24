package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pluginruntime"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	hclog "github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type pluginRuntime struct {
	installation      *PluginInstallation
	instanceID        string
	timelineMu        sync.Mutex
	timeline          *pluginRuntimeTimeline
	client            *hcplugin.Client
	api               pluginv1.TransportPluginClient
	transport         pluginv2.TransportClient
	extension         pluginv2.ExtensionHandler
	isolation         string
	host              *pluginHostServices
	inFlight          atomic.Int64
	draining          atomic.Bool
	done              chan struct{}
	doneOnce          sync.Once
	readinessMu       sync.Mutex
	readinessAt       time.Time
	readinessErr      error
	readinessFailures int
	readinessInFlight bool
	statusMu          sync.Mutex
	statusAt          time.Time
	statusValue       *pluginv1.HealthResponse
	statusInFlight    bool
	statusStale       bool
	stdoutLog         *pluginRuntimeLogSink
	stderrLog         *pluginRuntimeLogSink
}

func startPluginRuntime(ctx context.Context, installation *PluginInstallation, startTimeout time.Duration, socketDir string, hostServices ...pluginv1.HostServiceServer) (*pluginRuntime, error) {
	var host pluginv1.HostServiceServer
	if len(hostServices) > 0 {
		host = hostServices[0]
	}
	return startPluginRuntimeWithSandboxAndHost(ctx, installation, startTimeout, socketDir, config.PluginSandboxConfig{}, host)
}

func startPluginRuntimeWithSandbox(ctx context.Context, installation *PluginInstallation, startTimeout time.Duration, socketDir string, sandbox config.PluginSandboxConfig) (*pluginRuntime, error) {
	return startPluginRuntimeWithSandboxAndHost(ctx, installation, startTimeout, socketDir, sandbox, nil)
}

func startPluginRuntimeWithSandboxAndHost(ctx context.Context, installation *PluginInstallation, startTimeout time.Duration, socketDir string, sandbox config.PluginSandboxConfig, hostServices pluginv1.HostServiceServer) (*pluginRuntime, error) {
	if installation == nil {
		return nil, errors.New("插件安装记录为空")
	}
	if installation.Manifest.SchemaVersion == 2 {
		sandbox = sandbox.WithDefaults()
		if err := sandbox.Validate(); err != nil {
			return nil, err
		}
	}
	if installation.Manifest.SchemaVersion == 2 && sandbox.Mode == "container" {
		for _, capability := range installation.Manifest.Capabilities {
			if capability.ID == pluginv2.CapabilityProtectionTransport {
				if !sandbox.EgressBroker.Enabled {
					return nil, errors.New("账号保护传输需要配置 egress broker；网络受限容器默认拒绝出站连接")
				}
				return nil, errors.New("sandbox egress broker data plane is not implemented; refusing to start network capability")
			}
		}
	}
	checksum, err := hex.DecodeString(installation.BinarySHA256)
	if err != nil || len(checksum) != sha256.Size {
		return nil, errors.New("插件二进制哈希无效")
	}
	instanceID := fmt.Sprintf("%s-%d", installation.PluginKey, time.Now().UnixNano())
	stdoutLog := newPluginRuntimeLogSink(instanceID, pluginRuntimeLogStreamStdout, defaultPluginRuntimeLogBytes)
	stderrLog := newPluginRuntimeLogSink(instanceID, pluginRuntimeLogStreamStderr, defaultPluginRuntimeLogBytes)
	cmd := exec.CommandContext(context.WithoutCancel(ctx), installation.BinaryPath)
	handshake, plugins, name := pluginv1.HandshakeConfig, pluginv1.ClientPluginMap(), pluginv1.TransportPluginName
	if installation.Manifest.SchemaVersion == 2 {
		handshake, plugins, name = pluginv2.HandshakeConfig, pluginv2.ClientPluginMap(), pluginv2.ExtensionPluginName
	}
	clientConfig := &hcplugin.ClientConfig{
		HandshakeConfig:  handshake,
		Plugins:          plugins,
		Cmd:              cmd,
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
		StartTimeout:     startTimeout,
		SecureConfig: &hcplugin.SecureConfig{
			Checksum: checksum,
			Hash:     sha256.New(),
		},
		Logger:           hclog.NewNullLogger(),
		SyncStdout:       stdoutLog,
		SyncStderr:       stderrLog,
		UnixSocketConfig: &hcplugin.UnixSocketConfig{TempDir: socketDir},
		SkipHostEnv:      true,
	}
	isolation := "process"
	if installation.Manifest.SchemaVersion == 2 {
		clientConfig.AutoMTLS = true
		clientConfig.GRPCBrokerMultiplex = hasHostPermissions(installation.Manifest)
		if sandbox.Mode == "container" {
			isolation = "container"
			clientConfig.Cmd = nil
			// The runner hashes the exact private copy mounted into the container.
			clientConfig.SecureConfig = nil
			clientConfig.RunnerFunc = func(_ hclog.Logger, spec *exec.Cmd, workDir string) (runner.Runner, error) {
				return pluginruntime.NewContainer(pluginruntime.ContainerOptions{BinaryPath: installation.BinaryPath,
					BinarySHA256: installation.BinarySHA256, WorkDir: workDir, Image: sandbox.Image,
					MemoryMB: sandbox.MemoryMB, CPUMilli: sandbox.CPUMilli, PidsLimit: sandbox.PidsLimit, Env: spec.Env,
					EgressBroker: pluginruntime.EgressBrokerOptions{Enabled: sandbox.EgressBroker.Enabled,
						SocketPath: sandbox.EgressBroker.SocketPath, AllowedHosts: append([]string(nil), sandbox.EgressBroker.AllowedHosts...),
						AllowedSchemes: append([]string(nil), sandbox.EgressBroker.AllowedSchemes...), RequireTLS: sandbox.EgressBroker.RequireTLS}})
			}
		}
	}
	client := hcplugin.NewClient(clientConfig)
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("启动插件进程: %w", err)
	}
	dispensed, err := rpcClient.Dispense(name)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("获取插件传输能力: %w", err)
	}
	runtime := &pluginRuntime{
		installation: installation,
		instanceID:   instanceID,
		timeline:     newPluginRuntimeTimeline(time.Now()),
		client:       client,
		done:         make(chan struct{}),
		isolation:    isolation,
		stdoutLog:    stdoutLog,
		stderrLog:    stderrLog,
	}
	infoCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	if err := runtime.initializeAPI(infoCtx, dispensed); err != nil {
		runtime.markRuntimeError(err)
		runtime.kill()
		return nil, err
	}
	runtime.markRuntimeAPIReady()
	// v1 plugins may opt into the generic host service over the same broker. v2
	// extension plugins keep their dedicated host connector path in PluginManager.
	if transportClient, ok := dispensed.(*pluginv1.TransportClient); ok {
		offerPluginHostServices(ctx, installation, transportClient.TransportPluginClient, transportClient.Broker, hostServices, startTimeout)
	}
	return runtime, nil
}

// offerPluginHostServices 在 go-plugin broker 上启动一个宿主服务实例，并通过
// InitHostServices 把 broker 流 id 交给插件。服务生命周期与插件进程绑定：client.Kill()
// 会关闭 broker，AcceptAndServe 随之 GracefulStop，无需手动清理。整个过程尽力而为，
// 任何失败都只记录日志、不影响插件转发能力。
func offerPluginHostServices(
	ctx context.Context,
	installation *PluginInstallation,
	api pluginv1.TransportPluginClient,
	broker *hcplugin.GRPCBroker,
	hostServices pluginv1.HostServiceServer,
	startTimeout time.Duration,
) {
	if broker == nil || hostServices == nil || api == nil {
		return
	}
	brokerID := broker.NextId()
	go broker.AcceptAndServe(brokerID, func(opts []grpc.ServerOption) *grpc.Server {
		server := grpc.NewServer(opts...)
		pluginv1.RegisterHostServiceServer(server, hostServices)
		return server
	})
	initCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	resp, err := api.InitHostServices(initCtx, &pluginv1.InitHostServicesRequest{
		HostServiceId:         brokerID,
		HostServiceApiVersion: pluginv1.HostServiceAPIVersion,
	})
	pluginKey := ""
	if installation != nil {
		pluginKey = installation.PluginKey
	}
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			slog.Debug("plugin_host_services_unimplemented", "plugin", pluginKey)
		} else {
			slog.Warn("plugin_host_services_init_failed", "plugin", pluginKey, "error", err)
		}
		return
	}
	if resp != nil && !resp.Ready {
		slog.Debug("plugin_host_services_declined", "plugin", pluginKey, "message", resp.Message)
	}
}

func (r *pluginRuntime) validateAndApplyConfig(ctx context.Context, configJSON []byte) error {
	// Stored protection snapshots already passed validation on save. Replaying
	// one must not increment its revision or execute its transition twice.
	if r.transport != nil {
		return restoreStoredProtectionConfig(ctx, r, configJSON)
	}
	_, err := r.validateAndApplyNormalizedConfig(ctx, configJSON)
	return err
}

func (r *pluginRuntime) validateAndApplyNormalizedConfig(ctx context.Context, configJSON []byte) ([]byte, error) {
	normalized, err := r.validateNormalizedConfig(ctx, configJSON)
	if err != nil {
		return nil, err
	}
	if err := r.applyNormalizedConfig(ctx, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (r *pluginRuntime) validateNormalizedConfig(ctx context.Context, configJSON []byte) ([]byte, error) {
	validation, err := r.validateConfig(ctx, configJSON)
	if err != nil {
		return nil, fmt.Errorf("插件配置校验失败: %w", err)
	}
	if !validation.Valid {
		return nil, fmt.Errorf("插件配置无效: %s", validation.Message)
	}
	if len(validation.NormalizedConfigJson) > 0 {
		configJSON = validation.NormalizedConfigJson
	}
	if len(configJSON) == 0 || len(configJSON) > pluginConfigMaxBytes || !json.Valid(configJSON) {
		return nil, errors.New("插件返回的规范化配置不是有效且大小受限的 JSON")
	}
	var normalized any
	if err := json.Unmarshal(configJSON, &normalized); err != nil {
		return nil, fmt.Errorf("解析插件规范化配置: %w", err)
	}
	if normalized == nil {
		return nil, errors.New("插件返回的规范化配置根节点必须是对象")
	}
	if _, ok := normalized.(map[string]any); !ok {
		return nil, errors.New("插件返回的规范化配置根节点必须是对象")
	}
	configJSON, err = json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("序列化插件规范化配置: %w", err)
	}
	return configJSON, nil
}

func (r *pluginRuntime) applyNormalizedConfig(ctx context.Context, configJSON []byte) error {
	applied, err := r.applyConfig(ctx, configJSON)
	if err != nil {
		return fmt.Errorf("应用插件配置失败: %w", err)
	}
	if applied == nil || !applied.Applied {
		return errors.New("插件拒绝应用已保存配置")
	}
	if r.host != nil {
		r.host.setConfig(configJSON)
	}
	return nil
}

func (r *pluginRuntime) checkHealth(ctx context.Context) error {
	if r == nil || (r.api == nil && r.extension == nil) || r.client == nil || r.client.Exited() {
		err := errors.New("插件进程已退出")
		r.markRuntimeError(err)
		return err
	}
	health, err := r.health(ctx)
	if err != nil {
		wrapped := fmt.Errorf("插件健康检查失败: %w", err)
		r.markRuntimeError(wrapped)
		return wrapped
	}
	if health == nil || !health.Healthy {
		message := "插件报告不健康"
		if health != nil && strings.TrimSpace(health.Message) != "" {
			message = "插件不健康: " + health.Message
		}
		err := errors.New(message)
		r.markRuntimeError(err)
		return err
	}
	return nil
}

// checkReadiness caches the active capability probe. Reconcile runs every
// second, but a readiness RPC is deliberately sampled at a slower interval and
// must fail consecutively before the route is removed. A single slow probe can
// therefore never kill an otherwise serving runtime.
func (r *pluginRuntime) checkReadiness(ctx context.Context) error {
	if r == nil || r.client == nil || r.client.Exited() {
		err := errors.New("插件进程已退出")
		r.markRuntimeReadiness(err)
		return err
	}
	now := time.Now()
	r.readinessMu.Lock()
	if r.readinessInFlight {
		err := r.readinessDecisionLocked()
		r.readinessMu.Unlock()
		return err
	}
	if !r.readinessAt.IsZero() && now.Sub(r.readinessAt) < pluginReadinessInterval {
		err := r.readinessDecisionLocked()
		r.readinessMu.Unlock()
		return err
	}
	r.readinessAt = now
	r.readinessInFlight = true
	previous := r.readinessDecisionLocked()
	r.readinessMu.Unlock()

	// The reconcile loop must never wait on a remote readiness RPC. One probe
	// is allowed per interval; its result is consumed by later iterations.
	probeCtx, cancel := context.WithTimeout(ctx, pluginHealthTimeout)
	go func() {
		err := r.checkHealth(probeCtx)
		cancel()
		r.markRuntimeReadiness(err)
		r.readinessMu.Lock()
		r.readinessInFlight = false
		if err == nil {
			r.readinessErr = nil
			r.readinessFailures = 0
		} else {
			r.readinessErr = err
			r.readinessFailures++
		}
		r.readinessMu.Unlock()
	}()
	return previous
}

func (r *pluginRuntime) readinessDecisionLocked() error {
	if r.readinessFailures >= pluginReadinessFailureThreshold {
		return r.readinessErr
	}
	return nil
}

const pluginRuntimeStatusCacheTTL = 2 * time.Second

// status returns a cached passive Health response for the config UI. At most
// one remote Health RPC is in flight per runtime; concurrent UI polls receive
// the last snapshot marked stale instead of multiplying slow probes.
func (r *pluginRuntime) status(ctx context.Context) (*pluginv1.HealthResponse, error) {
	if r == nil || (r.api == nil && r.extension == nil) || r.client == nil || r.client.Exited() {
		return nil, errors.New("插件进程已退出")
	}
	now := time.Now()
	r.statusMu.Lock()
	if r.statusValue != nil && now.Sub(r.statusAt) < pluginRuntimeStatusCacheTTL {
		cached := clonePluginHealthResponse(r.statusValue)
		r.statusMu.Unlock()
		return cached, nil
	}
	if r.statusInFlight {
		if r.statusValue != nil {
			r.statusStale = true
			cached := clonePluginHealthResponse(r.statusValue)
			r.statusMu.Unlock()
			return cached, nil
		}
		r.statusMu.Unlock()
		return nil, errors.New("插件状态查询正在进行")
	}
	r.statusInFlight = true
	previous := clonePluginHealthResponse(r.statusValue)
	r.statusMu.Unlock()

	health, err := r.fetchStatus(ctx)
	r.statusMu.Lock()
	r.statusInFlight = false
	if err != nil {
		r.statusStale = true
		r.statusMu.Unlock()
		if previous != nil {
			return previous, nil
		}
		return nil, err
	}
	r.statusValue = clonePluginHealthResponse(health)
	r.statusAt = time.Now()
	r.statusStale = false
	r.statusMu.Unlock()
	return health, nil
}

func (r *pluginRuntime) fetchStatus(ctx context.Context) (*pluginv1.HealthResponse, error) {
	if r.extension != nil {
		result, err := r.extension.Health(ctx)
		if err != nil {
			return nil, safeExtensionRPCError("扩展状态查询失败", err)
		}
		health := &pluginv1.HealthResponse{Healthy: result.Healthy, Message: "扩展运行状态"}
		// v2 has a bounded Health message; structured display data is forwarded
		// through the existing status_json channel, never interpreted as commands.
		if len(result.Message) <= pluginConfigMaxBytes && json.Valid([]byte(result.Message)) {
			health.StatusJson = result.Message
		}
		return health, nil
	}
	health, err := r.api.Health(ctx, &pluginv1.HealthRequest{})
	if err != nil {
		return nil, fmt.Errorf("插件状态查询失败: %w", err)
	}
	if health == nil {
		return nil, errors.New("插件未返回状态")
	}
	return health, nil
}

func clonePluginHealthResponse(value *pluginv1.HealthResponse) *pluginv1.HealthResponse {
	if value == nil {
		return nil
	}
	return &pluginv1.HealthResponse{Healthy: value.Healthy, Message: value.Message, StatusJson: value.StatusJson}
}

func (r *pluginRuntime) statusIsStale() bool {
	if r == nil {
		return false
	}
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.statusStale
}

func (r *pluginRuntime) beginRequest() bool {
	if r == nil || r.draining.Load() {
		return false
	}
	r.inFlight.Add(1)
	if r.draining.Load() {
		r.finishRequest()
		return false
	}
	return true
}

func (r *pluginRuntime) finishRequest() {
	if r.inFlight.Add(-1) == 0 && r.draining.Load() {
		r.doneOnce.Do(func() { close(r.done) })
	}
}

func (r *pluginRuntime) drain(timeout time.Duration) {
	if r == nil {
		return
	}
	r.markRuntimeDrainRequested()
	r.draining.Store(true)
	if r.inFlight.Load() == 0 {
		r.doneOnce.Do(func() { close(r.done) })
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-r.done:
	case <-timer.C:
	}
	r.markRuntimeDrainFinished()
	r.kill()
}

func (r *pluginRuntime) kill() {
	if r == nil {
		return
	}
	if r.host != nil {
		r.host.Close()
	}
	if r.client != nil {
		r.client.Kill()
	}
	r.markRuntimeExited()
}

func (r *pluginRuntime) roundTrip(ctx context.Context, request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if request == nil || request.URL == nil || account == nil {
		return nil, errors.New("插件出站请求参数不完整")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	forwarder := pluginv2.TransportClient(r.api)
	if r.transport != nil {
		forwarder = r.transport
	}
	stream, err := forwarder.Forward(streamCtx)
	if err != nil {
		cancel()
		return nil, normalizePluginRPCError(ctx, "创建插件转发流", err, false)
	}
	requestID := pluginForwardRequestID(ctx)
	if err := stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{
		RequestId:           requestID,
		Method:              request.Method,
		Url:                 request.URL.String(),
		Host:                request.Host,
		Headers:             headersToPlugin(request.Header),
		ProxyUrl:            proxyURL,
		AccountId:           account.ID,
		AccountConcurrency:  int32(account.Concurrency),
		Platform:            account.Platform,
		AccountType:         account.Type,
		ContentLength:       request.ContentLength,
		HasBody:             request.Body != nil && request.Body != http.NoBody,
		OriginalBodyJson:    r.protectionOriginalBody(ctx, account),
		AccountMetadataJson: r.protectionAccountMetadata(ctx, account),
	}}}); err != nil {
		cancel()
		// gRPC Send 返回错误时无法证明服务端没有收到元数据，必须禁止自动重放。
		return nil, normalizePluginRPCError(ctx, "发送插件请求元数据", err, true)
	}
	sendErr := make(chan error, 1)
	go func() {
		err := sendPluginRequestBody(stream, request.Body)
		sendErr <- err
		if err != nil {
			cancel()
		}
	}()

	first, err := stream.Recv()
	if err != nil {
		cancel()
		select {
		case bodyErr := <-sendErr:
			if bodyErr != nil {
				return nil, normalizePluginRPCError(ctx, "发送插件请求体", bodyErr, true)
			}
		default:
		}
		return nil, normalizePluginRPCError(ctx, "接收插件响应头", err, true)
	}
	if frameError := first.GetError(); frameError != nil {
		cancel()
		return nil, &PluginTransportError{Code: frameError.Code, Message: frameError.Message, RequestSent: frameError.RequestSent}
	}
	start := first.GetStart()
	if start == nil || start.StatusCode < 100 || start.StatusCode > 599 {
		cancel()
		return nil, &PluginTransportError{
			Code:        "PLUGIN_INVALID_RESPONSE",
			Message:     "插件未返回有效的 HTTP 响应头",
			RequestSent: true,
		}
	}
	pipeReader, pipeWriter := io.Pipe()
	body := &pluginResponseBody{
		reader: pipeReader,
		cancel: cancel,
		done:   r.finishRequest,
	}
	go receivePluginResponseBody(stream, pipeWriter, sendErr)
	return &http.Response{
		Status:        start.Status,
		StatusCode:    int(start.StatusCode),
		Proto:         start.Protocol,
		ProtoMajor:    int(start.ProtocolMajor),
		ProtoMinor:    int(start.ProtocolMinor),
		Header:        headersFromPlugin(start.Headers),
		Body:          body,
		ContentLength: start.ContentLength,
		Request:       request,
	}, nil
}

type PluginTransportError struct {
	Code        string
	Message     string
	RequestSent bool
}

func (e *PluginTransportError) Error() string {
	if e == nil {
		return "插件传输失败"
	}
	code := strings.Map(func(value rune) rune {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '-' || value == '.' {
			return value
		}
		return -1
	}, e.Code)
	if len(code) > 64 {
		code = code[:64]
	}
	return fmt.Sprintf("插件传输失败 [%s]: %s", code, sanitizeUpstreamErrorMessage(e.Message))
}

func normalizePluginRPCError(ctx context.Context, operation string, err error, requestMayHaveBeenSent bool) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return &PluginTransportError{
		Code:        "PLUGIN_RPC_ERROR",
		Message:     fmt.Sprintf("%s: %v", operation, err),
		RequestSent: requestMayHaveBeenSent,
	}
}

func sendPluginRequestBody(stream pluginv1.TransportPlugin_ForwardClient, body io.ReadCloser) error {
	if body != nil {
		defer func() { _ = body.Close() }()
		buffer := make([]byte, 32*1024)
		for {
			read, err := body.Read(buffer)
			if read > 0 {
				chunk := append([]byte(nil), buffer[:read]...)
				if sendErr := stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyChunk{BodyChunk: chunk}}); sendErr != nil {
					return sendErr
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
	}
	if err := stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}); err != nil {
		return err
	}
	return stream.CloseSend()
}

func receivePluginResponseBody(stream pluginv1.TransportPlugin_ForwardClient, writer *io.PipeWriter, sendErr <-chan error) {
	defer func() { _ = writer.Close() }()
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			_ = writer.CloseWithError(normalizePluginRPCError(stream.Context(), "接收插件响应体", err, true))
			return
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			if _, err := writer.Write(chunk); err != nil {
				return
			}
			continue
		}
		if frame.GetEnd() != nil {
			select {
			case err := <-sendErr:
				if err != nil {
					_ = writer.CloseWithError(normalizePluginRPCError(stream.Context(), "发送插件请求体", err, true))
				}
			default:
			}
			return
		}
		if frameError := frame.GetError(); frameError != nil {
			_ = writer.CloseWithError(&PluginTransportError{Code: frameError.Code, Message: frameError.Message, RequestSent: frameError.RequestSent})
			return
		}
	}
}

type pluginResponseBody struct {
	reader *io.PipeReader
	cancel context.CancelFunc
	done   func()
	once   sync.Once
}

func (b *pluginResponseBody) Read(data []byte) (int, error) {
	return b.reader.Read(data)
}

func (b *pluginResponseBody) Close() error {
	var err error
	b.once.Do(func() {
		b.cancel()
		err = b.reader.Close()
		b.done()
	})
	return err
}

func headersToPlugin(headers http.Header) map[string]*pluginv1.HeaderValues {
	out := make(map[string]*pluginv1.HeaderValues, len(headers))
	for key, values := range headers {
		out[key] = &pluginv1.HeaderValues{Values: append([]string(nil), values...)}
	}
	return out
}

func headersFromPlugin(headers map[string]*pluginv1.HeaderValues) http.Header {
	out := make(http.Header, len(headers))
	for key, values := range headers {
		if values != nil {
			out[key] = append([]string(nil), values.Values...)
		}
	}
	return out
}
