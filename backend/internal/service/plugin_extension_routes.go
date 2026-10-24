package service

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const extensionConcurrencyLimit = 32

type extensionRouteTable struct {
	routes           []*extensionRoute
	stateUnavailable bool
}

// PluginRouteCandidate is the host-owned, safe projection of one route in the
// unified v1/v2 table. It is suitable for status/diagnostic responses and does
// not contain account credentials or request data.
type PluginRouteCandidate struct {
	PluginID          int64                `json:"plugin_id"`
	Capability        string               `json:"capability"`
	BindingID         int64                `json:"binding_id"`
	Priority          int                  `json:"priority"`
	FallbackPolicy    PluginFallbackPolicy `json:"fallback_policy"`
	RuntimeAvailable  bool                 `json:"runtime_available"`
	RuntimeInstanceID string               `json:"runtime_instance_id,omitempty"`
}

func (r *extensionRoute) Candidate() PluginRouteCandidate {
	if r == nil {
		return PluginRouteCandidate{}
	}
	candidate := PluginRouteCandidate{
		PluginID:         r.pluginID,
		Capability:       r.capability.ID,
		BindingID:        r.binding.ID,
		Priority:         r.binding.Priority,
		FallbackPolicy:   r.binding.EffectiveFallbackPolicy(),
		RuntimeAvailable: r.runtime != nil && !r.runtime.draining.Load(),
	}
	if r.runtime != nil {
		candidate.RuntimeInstanceID = r.runtime.instanceID
		if r.runtime.client != nil && r.runtime.client.Exited() {
			candidate.RuntimeAvailable = false
		}
	}
	return candidate
}

func pluginRouteCandidates(table *extensionRouteTable, capability string) []PluginRouteCandidate {
	if table == nil {
		return nil
	}
	out := make([]PluginRouteCandidate, 0)
	for _, route := range table.routes {
		if route == nil || route.capability.ID != capability || !route.binding.Enabled {
			continue
		}
		out = append(out, route.Candidate())
	}
	return out
}

type extensionRoute struct {
	pluginID    int64
	capability  PluginCapability
	binding     PluginBinding
	runtime     *pluginRuntime
	unavailable string
	calls       *extensionCallState
}
type extensionCallState struct {
	inFlight  atomic.Int64
	total     atomic.Uint64
	errors    atomic.Uint64
	denied    atomic.Uint64
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}
type PluginCapabilityRuntime struct {
	Capability       string `json:"capability"`
	Healthy          bool   `json:"healthy"`
	Message          string `json:"message,omitempty"`
	InFlight         int64  `json:"in_flight"`
	Calls            uint64 `json:"calls"`
	Errors           uint64 `json:"errors"`
	Denied           uint64 `json:"denied"`
	CircuitOpen      bool   `json:"circuit_open"`
	ConcurrencyLimit int    `json:"concurrency_limit"`
}

func (s *extensionCallState) acquire(limit int) error {
	s.mu.Lock()
	open := time.Now().Before(s.openUntil)
	s.mu.Unlock()
	if open {
		return errors.New("插件能力熔断中")
	}
	if s.inFlight.Add(1) > int64(limit) {
		s.inFlight.Add(-1)
		return errors.New("插件能力并发已达上限")
	}
	s.total.Add(1)
	return nil
}
func (s *extensionCallState) finish(failed bool) {
	s.inFlight.Add(-1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if failed {
		s.errors.Add(1)
		s.failures++
		if s.failures >= 3 {
			s.openUntil = time.Now().Add(10 * time.Second)
		}
	} else {
		s.failures = 0
		s.openUntil = time.Time{}
	}
}
func hasEnabledPluginBinding(bindings []PluginBinding) bool {
	for _, binding := range bindings {
		if binding.Enabled {
			return true
		}
	}
	return false
}
func samePluginBindings(a, b []PluginBinding) bool {
	if len(a) != len(b) {
		return false
	}
	// IDs and timestamps change when a transaction replaces bindings.
	for i := range a {
		if a[i].Capability != b[i].Capability || a[i].Platform != b[i].Platform || a[i].AccountType != b[i].AccountType ||
			a[i].Enabled != b[i].Enabled || a[i].RolloutPercent != b[i].RolloutPercent ||
			a[i].Priority != b[i].Priority || a[i].EffectiveConcurrency() != b[i].EffectiveConcurrency() || a[i].TimeoutMS != b[i].TimeoutMS ||
			a[i].EffectiveFallbackPolicy() != b[i].EffectiveFallbackPolicy() ||
			!pluginScopeEqual(a[i].AccountIDs, b[i].AccountIDs) || !pluginScopeEqual(a[i].UserIDs, b[i].UserIDs) || !pluginScopeEqual(a[i].GroupIDs, b[i].GroupIDs) {
			return false
		}
	}
	return true
}
func (m *PluginManager) publishExtensionRoutesLocked(i *PluginInstallation, runtime *pluginRuntime, message string) {
	old := m.extensions.Load()
	next := &extensionRouteTable{}
	if old != nil {
		next.stateUnavailable = old.stateUnavailable
		for _, route := range old.routes {
			if route.pluginID != i.ID {
				next.routes = append(next.routes, route)
			}
		}
	}
	for _, binding := range i.Bindings {
		if !binding.Enabled {
			continue
		}
		for _, capability := range i.Manifest.Capabilities {
			if capability.ID != binding.Capability {
				continue
			}
			route := &extensionRoute{pluginID: i.ID, capability: capability, binding: binding, runtime: runtime, unavailable: message, calls: &extensionCallState{}}
			if old != nil {
				for _, existing := range old.routes {
					if existing.pluginID == i.ID && existing.runtime == runtime && reflect.DeepEqual(existing.capability, capability) {
						route.calls = existing.calls
					}
				}
			}
			next.routes = append(next.routes, route)
		}
	}
	sort.SliceStable(next.routes, func(i, j int) bool {
		if next.routes[i].binding.Priority != next.routes[j].binding.Priority {
			return next.routes[i].binding.Priority > next.routes[j].binding.Priority
		}
		return next.routes[i].pluginID < next.routes[j].pluginID
	})
	m.extensions.Store(next)
}
func (m *PluginManager) removeExtensionRoutesLocked(id int64) {
	old := m.extensions.Load()
	if old == nil {
		return
	}
	next := &extensionRouteTable{stateUnavailable: old.stateUnavailable}
	for _, route := range old.routes {
		if route.pluginID != id {
			next.routes = append(next.routes, route)
		}
	}
	m.extensions.Store(next)
}
func (m *PluginManager) pruneExtensionRoutesLocked(desired map[int64]bool) {
	old := m.extensions.Load()
	if old == nil {
		return
	}
	next := &extensionRouteTable{}
	for _, route := range old.routes {
		if desired[route.pluginID] {
			next.routes = append(next.routes, route)
		}
	}
	m.extensions.Store(next)
}
func (m *PluginManager) publishInstallationUnavailable(i *PluginInstallation, message string) {
	m.mu.Lock()
	runtime := m.runtimes[i.ID]
	delete(m.runtimes, i.ID)
	if i.Manifest.SchemaVersion == 2 {
		m.publishExtensionRoutesLocked(i, nil, message)
	} else {
		m.publishLegacyRouteLocked(i, nil, message)
	}
	m.mu.Unlock()
	if runtime != nil {
		drainPluginRuntimes(context.Background(), []*pluginRuntime{runtime}, pluginDrainTimeout)
	}
}
func (m *PluginManager) publishUnavailableExtensions(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.extensions.Load()
	if old == nil {
		m.extensions.Store(&extensionRouteTable{stateUnavailable: true})
		return
	}
	next := &extensionRouteTable{stateUnavailable: true}
	for _, existing := range old.routes {
		route := *existing
		route.runtime = nil
		route.unavailable = message
		next.routes = append(next.routes, &route)
	}
	m.extensions.Store(next)
}

// markExtensionsStale records a control-plane read failure while retaining the
// last runtime and route snapshot. It is separate from publishUnavailableExtensions,
// which is used by an authoritative desktop upgrade transition and may drain.
func (m *PluginManager) markExtensionsStale(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.extensions.Load()
	if old == nil {
		m.extensions.Store(&extensionRouteTable{stateUnavailable: true})
		return
	}
	next := &extensionRouteTable{stateUnavailable: true}
	for _, existing := range old.routes {
		route := *existing
		route.unavailable = message
		next.routes = append(next.routes, &route)
	}
	m.extensions.Store(next)
}

// clearExtensionsStale is called after an authoritative repository snapshot
// has been read successfully. It keeps the last route pointers and counters,
// but removes the control-plane stale marker so status and diagnostics converge
// on the next successful reconcile.
func (m *PluginManager) clearExtensionsStale() {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.extensions.Load()
	if old == nil || !old.stateUnavailable {
		return
	}
	next := &extensionRouteTable{routes: append([]*extensionRoute(nil), old.routes...)}
	m.extensions.Store(next)
}
func (m *PluginManager) extensionStatus(id int64) []PluginCapabilityRuntime {
	var out []PluginCapabilityRuntime
	table := m.extensions.Load()
	if table == nil {
		return out
	}
	for _, route := range table.routes {
		if route.pluginID != id {
			continue
		}
		s := route.calls
		s.mu.Lock()
		open := time.Now().Before(s.openUntil)
		s.mu.Unlock()
		healthy := route.runtime != nil && !route.runtime.draining.Load() && !open
		if healthy && route.runtime.client != nil {
			healthy = !route.runtime.client.Exited()
		}
		out = append(out, PluginCapabilityRuntime{Capability: route.capability.ID, Healthy: healthy, Message: route.unavailable,
			InFlight: s.inFlight.Load(), Calls: s.total.Load(), Errors: s.errors.Load(), Denied: s.denied.Load(),
			CircuitOpen: open, ConcurrencyLimit: route.binding.EffectiveConcurrency()})
	}
	return out
}
