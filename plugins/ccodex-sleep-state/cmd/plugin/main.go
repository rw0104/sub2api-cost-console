package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	v2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"google.golang.org/grpc"
	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/transport"
	"local.sub2api/ccodex-sleep-state/internal/turnstate"
	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
)

const (
	pluginID      = "local.sub2api.ccodex-sleep-state"
	pluginVersion = "0.5.1"
)

type plugin struct {
	config     atomic.Pointer[config.Config]
	states     *turnstate.Store
	limits     *transport.Limits
	mu         sync.Mutex
	host       v2.HostClient
	routeMu    sync.Mutex
	registries map[string]cachedRegistry
	lastTest   atomic.Pointer[routeTestStatus]
	coreMu     sync.Mutex
	core       *transport.CoreRuntime
	pool       *routepool.Store
	openPool   func() (*routepool.Store, error)
}

type routeTestStatus struct {
	total, available int
	at               time.Time
}

type cachedRegistry struct {
	registry *transport.Registry
	loadedAt time.Time
}

func main() { v2.Serve(newPlugin()) }

func newPlugin() *plugin {
	return &plugin{states: turnstate.New(), limits: transport.NewLimits(config.DefaultCooldownSeconds * time.Second), registries: make(map[string]cachedRegistry), openPool: openInstalledPool}
}

func capabilities() []v2.Capability {
	return []v2.Capability{{
		ID:          v2.CapabilityProtectionTransport,
		Kind:        v2.CapabilityKindProvider,
		Platform:    "openai",
		AccountType: "oauth",
		Permissions: []v2.Permission{
			v2.PermissionRequestMetadata,
			v2.PermissionRequestBody,
			v2.PermissionCredentialsForward,
			v2.PermissionNetworkOutbound,
			v2.PermissionAccountProtection,
			v2.PermissionOriginalRequest,
			v2.PermissionHostLog,
			v2.PermissionHostMetric,
			v2.PermissionEventPublish,
		},
		TimeoutMS:   120000,
		FailureMode: v2.FailureModeClosed,
		Synchronous: true,
	}}
}

func (p *plugin) SetHost(host v2.HostClient) error {
	p.mu.Lock()
	p.host = host
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = host.Log(ctx, v2.CapabilityProtectionTransport, "info", "plugin.ready")
	return nil
}

func (*plugin) GetInfo(context.Context) (v2.PluginInfo, error) {
	return v2.PluginInfo{PluginID: pluginID, PluginVersion: pluginVersion, ProtocolVersion: v2.ProtocolVersion, Capabilities: capabilities()}, nil
}

func (p *plugin) Health(context.Context) (v2.HealthStatus, error) {
	message := "in-memory state store ready"
	if status := p.lastTest.Load(); status != nil {
		message = fmt.Sprintf("route connectivity: %d/%d available", status.available, status.total)
	}
	return v2.HealthStatus{Healthy: p.states != nil, Message: message}, nil
}

func (p *plugin) ValidateConfig(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := config.Normalize(raw, p.config.Load())
	if err != nil {
		return nil, err
	}
	next, err := config.Parse(normalized)
	if err != nil {
		return nil, err
	}
	request := next.RouteRequest
	next.RouteRequest = nil
	if request != nil {
		actionCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		next.RouteReport = p.inspectRoutes(actionCtx, next, *request)
	}
	coreRequest := next.CoreRequest
	next.CoreRequest = nil
	if coreRequest != nil {
		actionCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		next.CoreReport = p.inspectCore(actionCtx, next, *coreRequest)
	}
	return json.Marshal(next)
}

func (p *plugin) ApplyConfig(_ context.Context, raw json.RawMessage) error {
	next, err := config.Parse(raw)
	if err != nil {
		return err
	}
	// Applying persisted configuration never replays one-shot network actions.
	next.RouteRequest = nil
	next.CoreRequest = nil
	old := p.config.Load()
	unchanged := sameRuntimeConfig(old, next)
	livePoliciesOnly := sameExceptLivePolicies(old, next)
	p.coreMu.Lock()
	if p.core != nil {
		if !unchanged && !livePoliciesOnly {
			p.core.Invalidate()
		}
		p.core.UpdatePolicies(next)
	}
	p.coreMu.Unlock()
	p.routeMu.Lock()
	if unchanged || livePoliciesOnly {
		p.config.Store(next)
		p.routeMu.Unlock()
		return nil
	}
	if old == nil || old.RouteSignature() != next.RouteSignature() {
		p.states.Clear()
	}
	for key, cached := range p.registries {
		closeRegistryLater(cached.registry)
		delete(p.registries, key)
	}
	p.config.Store(next)
	p.routeMu.Unlock()
	return nil
}

func (p *plugin) TestConfig(ctx context.Context, raw json.RawMessage) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	started := time.Now()
	next, err := config.Parse(raw)
	if err == nil {
		registry, registryErr := transport.NewRegistryFromConfig(ctx, next, "")
		if registryErr != nil {
			err = registryErr
		} else {
			if next.HasRouteSources() {
				report, testErr := transport.TestConnectivity(ctx, registry)
				p.lastTest.Store(&routeTestStatus{total: report.Total, available: report.Available, at: time.Now()})
				if testErr != nil {
					err = testErr
				}
			}
			registry.Close()
		}
	}
	return time.Since(started), err
}

func sameRuntimeConfig(left, right *config.Config) bool {
	if left == nil || right == nil {
		return left == right
	}
	a, b := *left, *right
	a.RouteRequest, b.RouteRequest = nil, nil
	a.RouteReport, b.RouteReport = nil, nil
	a.CoreRequest, b.CoreRequest = nil, nil
	a.CoreReport, b.CoreReport = nil, nil
	a.Revision, b.Revision = 0, 0
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(rawA, rawB)
}

func (*plugin) Preprocess(context.Context, v2.PreprocessRequest) (v2.PreprocessResponse, error) {
	return v2.PreprocessResponse{}, errors.New("preprocess capability not declared")
}

func (p *plugin) telemetry(ctx context.Context, name string, value int64) {
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	if host == nil {
		return
	}
	call, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = host.Metric(call, v2.CapabilityProtectionTransport, name, float64(value))
}

func (p *plugin) emit(ctx context.Context, code string) {
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	if host == nil {
		return
	}
	call, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = host.Publish(call, v2.CapabilityProtectionTransport, code, 1)
}

func (p *plugin) registryFor(ctx context.Context, c *config.Config, fallback string) (*transport.Registry, error) {
	if c == nil {
		return nil, errors.New("configuration required")
	}
	urls := c.RouteURLs()
	if len(urls) == 0 && !c.Direct && strings.TrimSpace(fallback) != "" {
		urls = []string{fallback}
	}
	key := registryKey(c, fallback, urls)
	now := time.Now()
	refresh := time.Duration(c.SubscriptionRefreshSeconds) * time.Second
	dynamic := len(c.Subscriptions) > 0 || len(c.ProxyEnvs) > 0
	var stale cachedRegistry
	p.routeMu.Lock()
	if cached, ok := p.registries[key]; ok && (!dynamic || now.Sub(cached.loadedAt) < refresh) {
		p.routeMu.Unlock()
		return cached.registry, nil
	} else if ok {
		stale = cached
	}
	p.routeMu.Unlock()
	registry, err := transport.NewRegistryFromConfig(ctx, c, fallback)
	if err != nil {
		if stale.registry != nil {
			p.routeMu.Lock()
			if current, ok := p.registries[key]; ok && current.registry == stale.registry {
				current.loadedAt = now.Add(-refresh + time.Minute)
				p.registries[key] = current
			}
			p.routeMu.Unlock()
			return stale.registry, nil
		}
		return nil, err
	}
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	if existing, ok := p.registries[key]; ok && (!dynamic || now.Sub(existing.loadedAt) < refresh) {
		registry.Close()
		return existing.registry, nil
	}
	old := p.registries[key]
	if old.registry != nil && old.registry.Signature() == registry.Signature() {
		old.loadedAt = now
		p.registries[key] = old
		registry.Close()
		return old.registry, nil
	}
	p.registries[key] = cachedRegistry{registry: registry, loadedAt: now}
	if old.registry != nil {
		p.states.Clear()
		closeRegistryLater(old.registry)
	}
	return registry, nil
}

func (p *plugin) invalidateRegistry(c *config.Config, fallback string) {
	if c == nil {
		return
	}
	urls := c.RouteURLs()
	if len(urls) == 0 && !c.Direct && strings.TrimSpace(fallback) != "" {
		urls = []string{fallback}
	}
	key := registryKey(c, fallback, urls)
	p.routeMu.Lock()
	old := p.registries[key]
	delete(p.registries, key)
	p.routeMu.Unlock()
	if old.registry != nil {
		p.states.Clear()
		closeRegistryLater(old.registry)
	}
}

func registryKey(c *config.Config, fallback string, urls []string) string {
	return c.RouteSignature() + "\x00" + strings.Join(urls, "\x00") + "\x00" + strconv.FormatBool(c.Direct) + "\x00" + strconv.Itoa(c.ResponseHeaderTimeoutSeconds) + "\x00" + strings.TrimSpace(fallback)
}

func closeRegistryLater(registry *transport.Registry) {
	if registry == nil {
		return
	}
	time.AfterFunc(2*time.Minute, registry.Close)
}

func (p *plugin) Forward(stream grpc.BidiStreamingServer[v1.ForwardRequest, v1.ForwardResponse]) error {
	core, err := p.ensureCore()
	if err != nil {
		return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Error{Error: &v1.ForwardResponseError{Code: "CORE_STORAGE_UNAVAILABLE", Message: "插件节点池存储不可用，请检查安装目录权限", RequestSent: false}}})
	}
	return core.Forward(stream, p.config.Load())
}

var _ v2.ExtensionHandler = (*plugin)(nil)
var _ v2.TransportHandler = (*plugin)(nil)
var _ v2.HostAware = (*plugin)(nil)
