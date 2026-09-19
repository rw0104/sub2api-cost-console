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
	Logs           uint64             `json:"logs"`
	Metrics        map[string]float64 `json:"metrics"`
	EventsAccepted uint64             `json:"events_accepted"`
	EventsDropped  uint64             `json:"events_dropped"`
	RecentEvents   []PluginHostEvent  `json:"recent_events"`
}
type pluginHostConfig struct{ data []byte }
type pluginHostServices struct {
	wire.UnimplementedHostServicesServer
	pluginID    int64
	permissions map[string]map[pluginv2.Permission]bool
	config      atomic.Pointer[pluginHostConfig]
	readSecret  func(context.Context, string, string) (pluginv2.SecretValue, error)
	mu          sync.Mutex
	window      time.Time
	calls       int
	stats       PluginHostSnapshot
	events      chan PluginHostEvent
	stop        chan struct{}
	done        chan struct{}
	closed      atomic.Bool
}

func newPluginHostServices(i *PluginInstallation, readSecret func(context.Context, string, string) (pluginv2.SecretValue, error)) *pluginHostServices {
	h := &pluginHostServices{pluginID: i.ID, permissions: map[string]map[pluginv2.Permission]bool{}, readSecret: readSecret,
		stats: PluginHostSnapshot{Metrics: map[string]float64{}}, events: make(chan PluginHostEvent, 64), stop: make(chan struct{}), done: make(chan struct{})}
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
			case pluginv2.PermissionHostLog, pluginv2.PermissionHostMetric, pluginv2.PermissionHostConfig, pluginv2.PermissionSecretBroker, pluginv2.PermissionEventPublish:
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
func (h *pluginHostServices) ReadSecret(ctx context.Context, r *wire.HostSecretRequest) (*wire.HostSecretResponse, error) {
	if err := h.authorize(ctx, r.GetCapability(), pluginv2.PermissionSecretBroker); err != nil {
		return nil, err
	}
	if !pluginSecretAliasPattern.MatchString(r.GetAlias()) || h.readSecret == nil {
		return nil, status.Error(codes.PermissionDenied, "secret alias not granted")
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	secret, err := h.readSecret(callCtx, r.Capability, r.Alias)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "secret alias not granted or expired")
	}
	if h.closed.Load() || !time.Now().Before(secret.ExpiresAt) || len(secret.Value) == 0 || len(secret.Value) > pluginSecretMaxBytes {
		return nil, status.Error(codes.PermissionDenied, "secret grant unavailable")
	}
	return &wire.HostSecretResponse{Value: secret.Value, ExpiresUnixMillis: secret.ExpiresAt.UnixMilli()}, nil
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
		return PluginHostSnapshot{Metrics: map[string]float64{}, RecentEvents: []PluginHostEvent{}}, nil
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

// PluginAccountDirectory 让插件枚举其能力所覆盖的账号，并按需解析这些账号的出站身份，
// 无需等待一条真实请求流经插件。这是一项敏感能力（会把账号凭据交给插件进程），因此
// 宿主只对「其声明能力确实覆盖这些账号」的插件开放（见 PluginManager.buildHostServices）。
// 实现方自身也必须把返回范围收敛到该能力对应的账号集合。
type PluginAccountDirectory interface {
	ListPluginAccounts(ctx context.Context, platform, accountType string) ([]int64, error)
	ResolvePluginOutboundIdentity(ctx context.Context, accountID int64) (*PluginOutboundIdentity, error)
}

// pluginHostServiceServer 实现 pluginv1.HostServiceServer，是宿主经 go-plugin broker
// 反向暴露给单个插件进程的服务端点。它绑定到具体插件的 pluginKey，因此每个运行时都有
// 自己的实例；所有键值操作都被强制限定在该插件的命名空间内。
type pluginHostServiceServer struct {
	pluginv1.UnimplementedHostServiceServer
	pluginKey string
	store     PluginKVStore
	directory PluginAccountDirectory
}

func newPluginHostServiceServer(pluginKey string, store PluginKVStore, directory PluginAccountDirectory) *pluginHostServiceServer {
	return &pluginHostServiceServer{pluginKey: pluginKey, store: store, directory: directory}
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
	ids, err := s.directory.ListPluginAccounts(ctx, req.Platform, req.AccountType)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "列举账号失败: %v", err)
	}
	return &pluginv1.ListAccountsResponse{AccountIds: ids}, nil
}

func (s *pluginHostServiceServer) ResolveOutboundIdentity(ctx context.Context, req *pluginv1.ResolveOutboundIdentityRequest) (*pluginv1.ResolveOutboundIdentityResponse, error) {
	if s == nil || s.directory == nil {
		return nil, status.Error(codes.Unavailable, "账号目录不可用")
	}
	if req == nil || req.AccountId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id 无效")
	}
	identity, err := s.directory.ResolvePluginOutboundIdentity(ctx, req.AccountId)
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
