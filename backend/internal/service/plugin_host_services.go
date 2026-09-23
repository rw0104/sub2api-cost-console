package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PluginHostEvent struct {
	Sequence   uint64    `json:"sequence"`
	Capability string    `json:"capability"`
	Name       string    `json:"name"`
	Value      int64     `json:"value"`
	Time       time.Time `json:"time"`
}
type PluginHostSnapshot struct {
	Logs                   uint64                             `json:"logs"`
	Metrics                map[string]float64                 `json:"metrics"`
	EventsAccepted         uint64                             `json:"events_accepted"`
	EventsDropped          uint64                             `json:"events_dropped"`
	RecentEvents           []PluginHostEvent                  `json:"recent_events"`
	SecretReads            map[PluginSecretAuditResult]uint64 `json:"secret_reads,omitempty"`
	RecentSecretAudits     []PluginSecretAuditEvent           `json:"recent_secret_audits,omitempty"`
	SecretAuditSuppressed  uint64                             `json:"secret_audit_suppressed,omitempty"`
	SecretAuditEvicted     uint64                             `json:"secret_audit_evicted,omitempty"`
	SecretAuditSinkDropped uint64                             `json:"secret_audit_sink_dropped,omitempty"`
	SecretAuditSinkErrors  uint64                             `json:"secret_audit_sink_errors,omitempty"`
}
type pluginHostConfig struct{ data []byte }
type pluginHostServices struct {
	wire.UnimplementedHostServicesServer
	pluginID            int64
	permissions         map[string]map[pluginv2.Permission]bool
	config              atomic.Pointer[pluginHostConfig]
	readSecret          func(context.Context, string, string) (pluginv2.SecretValue, error)
	readAccountMetadata func(context.Context, string, int64) ([]byte, error)
	accountScope        PluginAccountScope
	mu                  sync.Mutex
	window              time.Time
	calls               int
	stats               PluginHostSnapshot
	secretAuditLast     map[string]pluginSecretAuditBucket
	secretAuditSink     PluginSecretAuditSink
	secretAuditSlots    chan struct{}
	bindingIDs          map[string]int64
	instanceID          string
	events              chan PluginHostEvent
	stop                chan struct{}
	done                chan struct{}
	closed              atomic.Bool
}

func newPluginHostServices(i *PluginInstallation, readSecret func(context.Context, string, string) (pluginv2.SecretValue, error), metadataReaders ...func(context.Context, string, int64) ([]byte, error)) *pluginHostServices {
	h := &pluginHostServices{permissions: map[string]map[pluginv2.Permission]bool{}, readSecret: readSecret,
		stats:           PluginHostSnapshot{Metrics: map[string]float64{}, SecretReads: map[PluginSecretAuditResult]uint64{}},
		secretAuditLast: map[string]pluginSecretAuditBucket{}, bindingIDs: map[string]int64{},
		events: make(chan PluginHostEvent, 64), stop: make(chan struct{}), done: make(chan struct{})}
	if len(metadataReaders) > 0 {
		h.readAccountMetadata = metadataReaders[0]
	}
	if i != nil {
		h.pluginID = i.ID
		h.accountScope = pluginAccountScopeForInstallation(i)
	}
	for _, binding := range func() []PluginBinding {
		if i == nil {
			return nil
		}
		return i.Bindings
	}() {
		if binding.Enabled && binding.ID > 0 && h.bindingIDs[binding.Capability] == 0 {
			h.bindingIDs[binding.Capability] = binding.ID
		}
	}
	if i == nil {
		return h
	}
	for _, cap := range i.Manifest.Capabilities {
		h.permissions[cap.ID] = map[pluginv2.Permission]bool{}
		for _, permission := range cap.Permissions {
			h.permissions[cap.ID][permission] = true
		}
	}
	go h.consumeEvents()
	return h
}
func hasHostPermissions(manifest PluginManifest) bool {
	for _, cap := range manifest.Capabilities {
		for _, permission := range cap.Permissions {
			switch permission {
			case pluginv2.PermissionHostLog, pluginv2.PermissionHostMetric, pluginv2.PermissionHostConfig, pluginv2.PermissionSecretBroker, pluginv2.PermissionAccountMetadata, pluginv2.PermissionEventPublish:
				return true
			}
		}
	}
	return false
}
func (h *pluginHostServices) Close() {
	if h.closed.CompareAndSwap(false, true) {
		close(h.stop)
		select {
		case <-h.done:
		case <-time.After(time.Second):
		}
	}
}
func (h *pluginHostServices) authorize(ctx context.Context, capability string, permission pluginv2.Permission) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if h.closed.Load() {
		return status.Error(codes.Unavailable, "host service stopped")
	}
	if !h.permissions[capability][permission] {
		return status.Error(codes.PermissionDenied, "capability permission not granted")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if now.Sub(h.window) >= time.Second {
		h.window = now
		h.calls = 0
	}
	if h.calls >= 100 {
		return status.Error(codes.ResourceExhausted, "host service rate limit reached")
	}
	h.calls++
	return nil
}
func validHostEventCode(code string) bool {
	switch code {
	case "policy.pass", "policy.deny", "config.applied", "plugin.ready", "plugin.error":
		return true
	}
	return false
}
func (h *pluginHostServices) Log(ctx context.Context, r *wire.HostLogRequest) (*wire.HostAck, error) {
	if err := h.authorize(ctx, r.GetCapability(), pluginv2.PermissionHostLog); err != nil {
		return nil, err
	}
	if !validHostEventCode(r.GetCode()) {
		return nil, status.Error(codes.InvalidArgument, "unknown log code")
	}
	level := slog.LevelInfo
	switch r.GetLevel() {
	case "info":
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown log level")
	}
	// The plugin cannot supply a log format, message body, headers, or fields.
	slog.Log(ctx, level, "plugin_host_log", "plugin_id", h.pluginID, "capability", r.Capability, "code", r.Code)
	h.mu.Lock()
	h.stats.Logs++
	h.mu.Unlock()
	return &wire.HostAck{Accepted: true}, nil
}
func (h *pluginHostServices) Metric(ctx context.Context, r *wire.HostMetricRequest) (*wire.HostAck, error) {
	if err := h.authorize(ctx, r.GetCapability(), pluginv2.PermissionHostMetric); err != nil {
		return nil, err
	}
	switch r.GetName() {
	case "requests", "denied", "errors", "latency_ms":
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown metric")
	}
	if math.IsNaN(r.Value) || math.IsInf(r.Value, 0) || r.Value < 0 || r.Value > 1e6 || (r.Name != "latency_ms" && math.Trunc(r.Value) != r.Value) {
		return nil, status.Error(codes.InvalidArgument, "metric value is invalid")
	}
	h.mu.Lock()
	h.stats.Metrics[r.Name] += r.Value
	h.mu.Unlock()
	return &wire.HostAck{Accepted: true}, nil
}
func (h *pluginHostServices) ReadConfig(ctx context.Context, r *wire.HostCapabilityRequest) (*wire.HostConfigResponse, error) {
	if err := h.authorize(ctx, r.GetCapability(), pluginv2.PermissionHostConfig); err != nil {
		return nil, err
	}
	cfg := h.config.Load()
	if cfg == nil {
		return nil, status.Error(codes.FailedPrecondition, "configuration has not been applied")
	}
	return &wire.HostConfigResponse{ConfigJson: append([]byte(nil), cfg.data...)}, nil
}
func (h *pluginHostServices) setConfig(raw json.RawMessage) {
	h.config.Store(&pluginHostConfig{data: append([]byte(nil), raw...)})
}

// setSecretAuditSink installs an optional persistence/observability port. It
// is intentionally separate from the constructor so runtime setup can attach
// the sink without changing older plugin call sites.
func (h *pluginHostServices) setSecretAuditSink(sink PluginSecretAuditSink) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.secretAuditSink = sink
	if sink != nil && h.secretAuditSlots == nil {
		h.secretAuditSlots = make(chan struct{}, pluginSecretAuditSinkSlots)
	}
	h.mu.Unlock()
}

// setRuntimeMetadata is called by the runtime owner when the host connector is
// attached. Empty values are allowed for old runtimes and are omitted from the
// resulting event rather than guessed from a plugin-controlled field.
func (h *pluginHostServices) setRuntimeMetadata(instanceID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.instanceID = sanitizePluginSecretAuditField(instanceID)
	h.mu.Unlock()
}

func (h *pluginHostServices) recordSecretAudit(ctx context.Context, capability, alias string, result PluginSecretAuditResult, errorCode string, expiresAt time.Time, startedAt time.Time) {
	if h == nil {
		return
	}
	result = normalizePluginSecretAuditResult(result)
	capability = sanitizePluginSecretAuditField(capability)
	alias = sanitizePluginSecretAuditAlias(alias)
	if errorCode != "" {
		errorCode = sanitizePluginSecretAuditCode(errorCode)
	}
	now := time.Now().UTC()
	durationMS := now.Sub(startedAt).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}
	ttlMS := int64(0)
	if !expiresAt.IsZero() {
		ttlMS = expiresAt.Sub(now).Milliseconds()
		if ttlMS < 0 {
			ttlMS = 0
		}
	}

	provenance := pluginRequestProvenanceFromContext(ctx)
	// The throttle scope is the plugin/alias pair. Capability is deliberately
	// excluded so a plugin cannot bypass the audit budget by declaring aliases
	// through several capabilities.
	key := alias
	h.mu.Lock()
	if h.secretAuditLast == nil {
		h.secretAuditLast = map[string]pluginSecretAuditBucket{}
	}
	if h.stats.SecretReads == nil {
		h.stats.SecretReads = map[PluginSecretAuditResult]uint64{}
	}
	h.stats.SecretReads[result]++
	bucket := h.secretAuditLast[key]
	if bucket.lastResult == result && !bucket.lastAt.IsZero() && now.Sub(bucket.lastAt) < pluginSecretAuditRateWindow {
		bucket.suppressed++
		h.secretAuditLast[key] = bucket
		h.stats.SecretAuditSuppressed++
		h.mu.Unlock()
		return
	}

	event := PluginSecretAuditEvent{
		Time: now, PluginID: h.pluginID, Capability: capability, Alias: alias,
		BindingID: h.bindingIDs[capability], InstanceID: h.instanceID,
		CorrelationID: sanitizePluginSecretAuditField(provenance.CorrelationID),
		Result:        result, DurationMS: durationMS, TTLMS: ttlMS,
	}
	if result != PluginSecretAuditGranted || errorCode != "" {
		event.ErrorCode = errorCode
	}
	if bucket.suppressed > 0 {
		event.Suppressed = bucket.suppressed
	}
	bucket = pluginSecretAuditBucket{lastAt: now, lastResult: result}
	h.secretAuditLast[key] = bucket
	if len(h.secretAuditLast) > pluginSecretAuditMaxRateKeys {
		// The map is only a duplicate-suppression cache. Clear it as a bounded,
		// deterministic fallback; the durable event ring remains untouched.
		h.secretAuditLast = map[string]pluginSecretAuditBucket{key: bucket}
	}
	h.stats.RecentSecretAudits = append(h.stats.RecentSecretAudits, event)
	if len(h.stats.RecentSecretAudits) > pluginSecretAuditMaxEvents {
		copy(h.stats.RecentSecretAudits, h.stats.RecentSecretAudits[len(h.stats.RecentSecretAudits)-pluginSecretAuditMaxEvents:])
		h.stats.RecentSecretAudits = h.stats.RecentSecretAudits[:pluginSecretAuditMaxEvents]
		h.stats.SecretAuditEvicted++
	}
	sink, slots := h.secretAuditSink, h.secretAuditSlots
	if sink != nil && slots == nil {
		slots = make(chan struct{}, pluginSecretAuditSinkSlots)
		h.secretAuditSlots = slots
	}
	h.mu.Unlock()

	if sink == nil || slots == nil {
		return
	}
	select {
	case slots <- struct{}{}:
		go h.writeSecretAudit(sink, slots, event)
	default:
		h.mu.Lock()
		h.stats.SecretAuditSinkDropped++
		h.mu.Unlock()
	}
}

func (h *pluginHostServices) writeSecretAudit(sink PluginSecretAuditSink, slots chan struct{}, event PluginSecretAuditEvent) {
	defer func() { <-slots }()
	writeCtx, cancel := context.WithTimeout(context.Background(), pluginSecretAuditSinkTimeout)
	defer cancel()
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = errPluginSecretAuditSinkPanic
			}
		}()
		err = sink.RecordPluginSecretAudit(writeCtx, event)
	}()
	if err == nil {
		return
	}
	h.mu.Lock()
	h.stats.SecretAuditSinkErrors++
	pluginID := h.pluginID
	h.mu.Unlock()
	// Keep the failure diagnostic stable and free of sink/provider text. The
	// in-memory event is already available through HostStats for retrieval.
	slog.Warn("plugin_secret_audit_sink_failed", "plugin_id", pluginID, "error_code", pluginSecretAuditErrorCode(err))
}

var errPluginSecretAuditSinkPanic = &PluginSecretReadError{Result: PluginSecretAuditInternalError, ErrorCode: "audit_sink_panic"}

func (h *pluginHostServices) ReadSecret(ctx context.Context, r *wire.HostSecretRequest) (*wire.HostSecretResponse, error) {
	startedAt := time.Now()
	capability, alias := r.GetCapability(), r.GetAlias()
	if err := h.authorize(ctx, capability, pluginv2.PermissionSecretBroker); err != nil {
		result, errorCode := pluginSecretAuditOutcomeForError(err)
		if status.Code(err) == codes.ResourceExhausted {
			result, errorCode = PluginSecretAuditRateLimited, "rate_limited"
		} else if status.Code(err) == codes.PermissionDenied {
			result, errorCode = PluginSecretAuditDenied, "permission_denied"
		} else {
			result = PluginSecretAuditInternalError
		}
		h.recordSecretAudit(ctx, capability, alias, result, errorCode, time.Time{}, startedAt)
		return nil, err
	}
	if !pluginSecretAliasPattern.MatchString(alias) || h.readSecret == nil {
		h.recordSecretAudit(ctx, capability, alias, PluginSecretAuditDenied, "secret_alias_not_granted", time.Time{}, startedAt)
		return nil, status.Error(codes.PermissionDenied, "secret alias not granted")
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	secret, err := h.readSecret(callCtx, capability, alias)
	if err != nil {
		result, errorCode := pluginSecretAuditOutcomeForError(err)
		h.recordSecretAudit(ctx, capability, alias, result, errorCode, time.Time{}, startedAt)
		return nil, status.Error(codes.PermissionDenied, "secret alias not granted or expired")
	}
	if h.closed.Load() {
		h.recordSecretAudit(ctx, capability, alias, PluginSecretAuditInternalError, "host_unavailable", secret.ExpiresAt, startedAt)
		return nil, status.Error(codes.Unavailable, "host service stopped")
	}
	if !time.Now().Before(secret.ExpiresAt) {
		h.recordSecretAudit(ctx, capability, alias, PluginSecretAuditExpired, "secret_expired", secret.ExpiresAt, startedAt)
		return nil, status.Error(codes.PermissionDenied, "secret grant unavailable")
	}
	if len(secret.Value) == 0 || len(secret.Value) > pluginSecretMaxBytes {
		h.recordSecretAudit(ctx, capability, alias, PluginSecretAuditInternalError, "invalid_secret_value", secret.ExpiresAt, startedAt)
		return nil, status.Error(codes.PermissionDenied, "secret grant unavailable")
	}
	h.recordSecretAudit(ctx, capability, alias, PluginSecretAuditGranted, "", secret.ExpiresAt, startedAt)
	return &wire.HostSecretResponse{Value: secret.Value, ExpiresUnixMillis: secret.ExpiresAt.UnixMilli()}, nil
}

func (h *pluginHostServices) ReadAccountMetadata(ctx context.Context, r *wire.HostAccountMetadataRequest) (*wire.HostAccountMetadataResponse, error) {
	if r == nil || r.GetAccountId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id 无效")
	}
	if err := h.authorize(ctx, r.GetCapability(), pluginv2.PermissionAccountMetadata); err != nil {
		return nil, err
	}
	if h.readAccountMetadata == nil || !h.accountScope.allowsID(r.GetAccountId()) {
		return &wire.HostAccountMetadataResponse{Found: false}, nil
	}
	metadata, err := h.readAccountMetadata(ctx, r.GetCapability(), r.GetAccountId())
	if err != nil {
		return nil, status.Error(codes.Internal, "读取账号元数据失败")
	}
	if len(metadata) == 0 || len(metadata) > 128*1024 || !json.Valid(metadata) {
		return &wire.HostAccountMetadataResponse{Found: false}, nil
	}
	return &wire.HostAccountMetadataResponse{Found: true, MetadataJson: append([]byte(nil), metadata...)}, nil
}
func (h *pluginHostServices) PublishEvent(ctx context.Context, r *wire.HostEventRequest) (*wire.HostAck, error) {
	if err := h.authorize(ctx, r.GetCapability(), pluginv2.PermissionEventPublish); err != nil {
		return nil, err
	}
	if !validHostEventCode(r.GetName()) || r.Value < 0 || r.Value > 1000000 {
		return nil, status.Error(codes.InvalidArgument, "invalid event")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	event := PluginHostEvent{Sequence: h.stats.EventsAccepted + 1, Capability: r.Capability, Name: r.Name, Value: r.Value, Time: time.Now()}
	select {
	case h.events <- event:
		h.stats.EventsAccepted++
		return &wire.HostAck{Accepted: true}, nil
	default:
		h.stats.EventsDropped++
		return nil, status.Error(codes.ResourceExhausted, "host event queue is full")
	}
}
func (h *pluginHostServices) consumeEvents() {
	defer close(h.done)
	for {
		select {
		case <-h.stop:
			return
		case event := <-h.events:
			h.mu.Lock()
			if len(h.stats.RecentEvents) == 64 {
				copy(h.stats.RecentEvents, h.stats.RecentEvents[1:])
				h.stats.RecentEvents = h.stats.RecentEvents[:63]
			}
			h.stats.RecentEvents = append(h.stats.RecentEvents, event)
			h.mu.Unlock()
			slog.Info("plugin_host_event", "plugin_id", h.pluginID, "capability", event.Capability, "event", event.Name, "value", event.Value)
		}
	}
}
func (h *pluginHostServices) Snapshot() PluginHostSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.stats
	out.Metrics = map[string]float64{}
	for name, value := range h.stats.Metrics {
		out.Metrics[name] = value
	}
	out.RecentEvents = append([]PluginHostEvent(nil), h.stats.RecentEvents...)
	out.SecretReads = map[PluginSecretAuditResult]uint64{}
	for result, count := range h.stats.SecretReads {
		out.SecretReads[result] = count
	}
	out.RecentSecretAudits = append([]PluginSecretAuditEvent(nil), h.stats.RecentSecretAudits...)
	return out
}

func (m *PluginManager) HostStats(ctx context.Context, id int64) (PluginHostSnapshot, error) {
	if _, err := m.repo.GetByID(ctx, id); err != nil {
		return PluginHostSnapshot{}, err
	}
	m.mu.Lock()
	process := m.runtimes[id]
	m.mu.Unlock()
	if process == nil || process.host == nil {
		return PluginHostSnapshot{
			Metrics:            map[string]float64{},
			RecentEvents:       []PluginHostEvent{},
			SecretReads:        map[PluginSecretAuditResult]uint64{},
			RecentSecretAudits: []PluginSecretAuditEvent{},
		}, nil
	}
	return process.host.Snapshot(), nil
}

// 通用宿主服务（HostService）的资源上限。这些限制与任何具体插件能力无关，
// 只用于约束单个插件对共享存储的占用，属于防御性护栏而非业务策略。
const (
	// pluginKVMaxPluginKeyLen 与清单 id 的 maxLength 一致（manifest.schema.json），
	// 确保任何可安装插件的 pluginKey 都能通过校验，不会被误判为不可用。
	pluginKVMaxPluginKeyLen  = 160
	pluginKVMaxNamespaceLen  = 128
	pluginKVMaxKeyLen        = 256
	pluginKVMaxValueBytes    = 256 * 1024
	pluginKVMaxListLimit     = 1000
	pluginKVDefaultListLimit = 100
	pluginKVMaxTTL           = 90 * 24 * time.Hour
)

// PluginKVStore 是宿主向插件提供的通用命名空间键值存储端口。它对存储介质保持中立
// （由 repository 层用 Redis 等实现），供任何插件持久化跨请求 / 跨副本 / 跨重启的
// 状态。pluginKey 由宿主根据服务该连接的运行时注入，插件无法伪造，从而保证不同
// 插件之间命名空间严格隔离。
type PluginKVStore interface {
	Get(ctx context.Context, pluginKey, namespace, key string) ([]byte, bool, error)
	Set(ctx context.Context, pluginKey, namespace, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, pluginKey, namespace, key string) error
	List(ctx context.Context, pluginKey, namespace, keyPrefix string, limit int) ([]string, error)
}

// PluginOutboundIdentity 是宿主为某账号解析出的、可直接用于出站请求的身份材料：
// 访问令牌、宿主会附加的出站请求头，以及账号代理。
type PluginOutboundIdentity struct {
	AccountID   int64
	Platform    string
	AccountType string
	ProxyURL    string
	Token       string
	Headers     http.Header
}

// pluginHostServiceServer 实现 pluginv1.HostServiceServer，是宿主经 go-plugin broker
// 反向暴露给单个插件进程的服务端点。它绑定到具体插件的 pluginKey，因此每个运行时都有
// 自己的实例；所有键值操作都被强制限定在该插件的命名空间内。
type pluginHostServiceServer struct {
	pluginv1.UnimplementedHostServiceServer
	pluginKey string
	store     PluginKVStore
	directory any
	scope     PluginAccountScope
}

func newPluginHostServiceServer(pluginKey string, store PluginKVStore, directory any, scopes ...PluginAccountScope) *pluginHostServiceServer {
	server := &pluginHostServiceServer{pluginKey: pluginKey, store: store, directory: directory}
	if len(scopes) > 0 {
		server.scope = scopes[0]
	}
	return server
}

func (s *pluginHostServiceServer) ready() bool {
	return s != nil && s.store != nil && isValidPluginKVSegment(s.pluginKey, pluginKVMaxPluginKeyLen)
}

func (s *pluginHostServiceServer) KVGet(ctx context.Context, req *pluginv1.KVGetRequest) (*pluginv1.KVGetResponse, error) {
	if !s.ready() {
		return nil, status.Error(codes.Unavailable, "宿主键值存储不可用")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "请求为空")
	}
	if err := validatePluginKVNamespace(req.Namespace); err != nil {
		return nil, err
	}
	if err := validatePluginKVKey(req.Key); err != nil {
		return nil, err
	}
	value, found, err := s.store.Get(ctx, s.pluginKey, req.Namespace, req.Key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "读取键值失败: %v", err)
	}
	if !found {
		return &pluginv1.KVGetResponse{Found: false}, nil
	}
	return &pluginv1.KVGetResponse{Found: true, Value: value}, nil
}

func (s *pluginHostServiceServer) KVSet(ctx context.Context, req *pluginv1.KVSetRequest) (*pluginv1.KVSetResponse, error) {
	if !s.ready() {
		return nil, status.Error(codes.Unavailable, "宿主键值存储不可用")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "请求为空")
	}
	if err := validatePluginKVNamespace(req.Namespace); err != nil {
		return nil, err
	}
	if err := validatePluginKVKey(req.Key); err != nil {
		return nil, err
	}
	if len(req.Value) > pluginKVMaxValueBytes {
		return nil, status.Errorf(codes.InvalidArgument, "值超过 %d 字节上限", pluginKVMaxValueBytes)
	}
	ttl, err := pluginKVTTL(req.TtlSeconds)
	if err != nil {
		return nil, err
	}
	if err := s.store.Set(ctx, s.pluginKey, req.Namespace, req.Key, req.Value, ttl); err != nil {
		return nil, status.Errorf(codes.Internal, "写入键值失败: %v", err)
	}
	return &pluginv1.KVSetResponse{}, nil
}

func (s *pluginHostServiceServer) KVDelete(ctx context.Context, req *pluginv1.KVDeleteRequest) (*pluginv1.KVDeleteResponse, error) {
	if !s.ready() {
		return nil, status.Error(codes.Unavailable, "宿主键值存储不可用")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "请求为空")
	}
	if err := validatePluginKVNamespace(req.Namespace); err != nil {
		return nil, err
	}
	if err := validatePluginKVKey(req.Key); err != nil {
		return nil, err
	}
	if err := s.store.Delete(ctx, s.pluginKey, req.Namespace, req.Key); err != nil {
		return nil, status.Errorf(codes.Internal, "删除键值失败: %v", err)
	}
	return &pluginv1.KVDeleteResponse{}, nil
}

func (s *pluginHostServiceServer) KVList(ctx context.Context, req *pluginv1.KVListRequest) (*pluginv1.KVListResponse, error) {
	if !s.ready() {
		return nil, status.Error(codes.Unavailable, "宿主键值存储不可用")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "请求为空")
	}
	if err := validatePluginKVNamespace(req.Namespace); err != nil {
		return nil, err
	}
	if req.KeyPrefix != "" {
		if err := validatePluginKVKey(req.KeyPrefix); err != nil {
			return nil, err
		}
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = pluginKVDefaultListLimit
	}
	if limit > pluginKVMaxListLimit {
		limit = pluginKVMaxListLimit
	}
	keys, err := s.store.List(ctx, s.pluginKey, req.Namespace, req.KeyPrefix, limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "列举键值失败: %v", err)
	}
	return &pluginv1.KVListResponse{Keys: keys}, nil
}

func (s *pluginHostServiceServer) ListAccounts(ctx context.Context, req *pluginv1.ListAccountsRequest) (*pluginv1.ListAccountsResponse, error) {
	if s == nil || s.directory == nil {
		return nil, status.Error(codes.Unavailable, "账号目录不可用")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "请求为空")
	}
	// A host service without an explicit binding scope is never allowed to
	// fall back to a legacy directory implementation. Empty scope means the
	// capability has no currently enabled grant, not "all accounts".
	if len(s.scope.entries) == 0 {
		return &pluginv1.ListAccountsResponse{AccountIds: []int64{}}, nil
	}
	switch directory := s.directory.(type) {
	case ScopedPluginAccountDirectory:
		infos, err := directory.ListPluginAccounts(ctx, s.scope, req.Platform, req.AccountType)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "列举账号失败: %v", err)
		}
		ids := make([]int64, 0, len(infos))
		accounts := make([]*pluginv1.AccountInfo, 0, len(infos))
		for _, info := range infos {
			ids = append(ids, info.ID)
			accounts = append(accounts, accountInfoToPlugin(info))
		}
		return &pluginv1.ListAccountsResponse{AccountIds: ids, Accounts: accounts}, nil
	case PluginAccountDirectory:
		ids, err := directory.ListPluginAccounts(ctx, req.Platform, req.AccountType)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "列举账号失败: %v", err)
		}
		if len(s.scope.entries) > 0 {
			filtered := ids[:0]
			for _, id := range ids {
				if s.scope.allowsID(id) {
					filtered = append(filtered, id)
				}
			}
			ids = filtered
		}
		return &pluginv1.ListAccountsResponse{AccountIds: ids}, nil
	default:
		return nil, status.Error(codes.Unavailable, "账号目录实现不支持作用域接口")
	}
}

// accountInfoToPlugin maps the host-owned readable view to the wire contract.
// MetadataJSON is already bounded and stripped of credentials by the directory;
// this adapter never reads or reconstructs account credentials.
func accountInfoToPlugin(info PluginAccountInfo) *pluginv1.AccountInfo {
	return &pluginv1.AccountInfo{
		Id:           info.ID,
		Platform:     info.Platform,
		AccountType:  info.AccountType,
		Name:         info.Name,
		Status:       info.Status,
		Schedulable:  info.Schedulable,
		IsShadow:     info.IsShadow,
		MetadataJson: append([]byte(nil), info.MetadataJSON...),
	}
}

func (s *pluginHostServiceServer) ResolveOutboundIdentity(ctx context.Context, req *pluginv1.ResolveOutboundIdentityRequest) (*pluginv1.ResolveOutboundIdentityResponse, error) {
	if s == nil || s.directory == nil {
		return nil, status.Error(codes.Unavailable, "账号目录不可用")
	}
	if req == nil || req.AccountId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id 无效")
	}
	if len(s.scope.entries) == 0 {
		return &pluginv1.ResolveOutboundIdentityResponse{Found: false}, nil
	}
	var identity *PluginOutboundIdentity
	var err error
	switch directory := s.directory.(type) {
	case ScopedPluginAccountDirectory:
		identity, err = directory.ResolvePluginOutboundIdentityScoped(ctx, s.scope, req.AccountId)
	case PluginAccountDirectory:
		if len(s.scope.entries) > 0 && !s.scope.allowsID(req.AccountId) {
			return &pluginv1.ResolveOutboundIdentityResponse{Found: false}, nil
		}
		identity, err = directory.ResolvePluginOutboundIdentity(ctx, req.AccountId)
	default:
		return nil, status.Error(codes.Unavailable, "账号目录实现不支持作用域接口")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "解析账号出站身份失败: %v", err)
	}
	if identity == nil {
		return &pluginv1.ResolveOutboundIdentityResponse{Found: false}, nil
	}
	return &pluginv1.ResolveOutboundIdentityResponse{
		Found:       true,
		AccountId:   identity.AccountID,
		Platform:    identity.Platform,
		AccountType: identity.AccountType,
		ProxyUrl:    identity.ProxyURL,
		Token:       identity.Token,
		Headers:     headersToPlugin(identity.Headers),
	}, nil
}

func pluginKVTTL(seconds int64) (time.Duration, error) {
	if seconds < 0 {
		return 0, status.Error(codes.InvalidArgument, "ttl_seconds 不能为负")
	}
	if seconds == 0 {
		return 0, nil
	}
	// 先在整数秒上比较上限，避免 seconds*time.Second 溢出 int64 后回绕成负值、
	// 从而绕过上限检查把负 TTL 传给 Redis。
	maxSeconds := int64(pluginKVMaxTTL / time.Second)
	if seconds > maxSeconds {
		return 0, status.Errorf(codes.InvalidArgument, "ttl_seconds 超过上限 %d", maxSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

func validatePluginKVNamespace(namespace string) error {
	if namespace == "" {
		return status.Error(codes.InvalidArgument, "namespace 不能为空")
	}
	if !isValidPluginKVSegment(namespace, pluginKVMaxNamespaceLen) {
		return status.Error(codes.InvalidArgument, "namespace 仅允许字母、数字、'.'、'_'、'-' 且长度受限")
	}
	return nil
}

func validatePluginKVKey(key string) error {
	if key == "" {
		return status.Error(codes.InvalidArgument, "key 不能为空")
	}
	if !isValidPluginKVSegment(key, pluginKVMaxKeyLen) {
		return status.Error(codes.InvalidArgument, "key 仅允许字母、数字、'.'、'_'、'-' 且长度受限")
	}
	return nil
}

// isValidPluginKVSegment 限定命名段字符集为 [A-Za-z0-9._-]。这既避免 Redis glob
// 元字符（*?[]）污染 SCAN，也避免 ':' 破坏内部键结构，从而保证命名空间隔离可靠。
func isValidPluginKVSegment(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}
