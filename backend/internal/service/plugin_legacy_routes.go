package service

import (
	"context"
	"sort"
)

func legacyBinding(installation *PluginInstallation) PluginBinding {
	if installation == nil {
		return PluginBinding{Capability: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}
	}
	for _, binding := range installation.Bindings {
		if binding.Capability == PluginCapabilityOpenAIOAuthOutbound {
			return binding
		}
	}
	return PluginBinding{Capability: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}
}

func (m *PluginManager) publishLegacyRouteLocked(installation *PluginInstallation, runtime *pluginRuntime, message string) {
	old := m.legacyRoutes.Load()
	next := &legacyRouteTable{}
	if old != nil {
		for _, route := range old.routes {
			if route != nil && route.pluginID != installation.ID {
				next.routes = append(next.routes, route)
			}
		}
	}
	binding := legacyBinding(installation)
	route := &pluginRoute{pluginID: installation.ID, runtime: runtime, rolloutPercent: binding.RolloutPercent, unavailable: message, binding: binding}
	next.routes = append(next.routes, route)
	sort.SliceStable(next.routes, func(i, j int) bool {
		left, right := next.routes[i], next.routes[j]
		if left.binding.Priority != right.binding.Priority {
			return left.binding.Priority > right.binding.Priority
		}
		return left.pluginID < right.pluginID
	})
	m.legacyRoutes.Store(next)
	if len(next.routes) > 0 {
		m.route.Store(next.routes[0])
	} else {
		m.route.Store(nil)
	}
}

func (m *PluginManager) pruneLegacyRoutesLocked(desired map[int64]bool) {
	old := m.legacyRoutes.Load()
	if old == nil {
		return
	}
	next := &legacyRouteTable{}
	for _, route := range old.routes {
		if route != nil && desired[route.pluginID] {
			next.routes = append(next.routes, route)
		}
	}
	m.legacyRoutes.Store(next)
	if len(next.routes) > 0 {
		m.route.Store(next.routes[0])
	} else {
		m.route.Store(nil)
	}
}

func (m *PluginManager) selectLegacyRoute(ctx context.Context, account *Account) *pluginRoute {
	if m == nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return nil
	}
	principal := pluginPrincipalFromContext(ctx)
	table := m.legacyRoutes.Load()
	var routes []*pluginRoute
	if table != nil {
		routes = table.routes
	} else if route := m.route.Load(); route != nil {
		routes = []*pluginRoute{route}
	}
	for _, route := range routes {
		if route == nil {
			continue
		}
		binding := route.binding
		if binding.Capability == "" {
			binding = legacyBinding(route.runtimeInstallation())
			binding.RolloutPercent = route.rolloutPercent
		}
		if !binding.Enabled || binding.Platform != account.Platform || binding.AccountType != account.Type ||
			binding.RolloutPercent <= 0 || int(stablePluginBucket(account.ID)) >= binding.RolloutPercent ||
			!pluginScopeContains(binding.AccountIDs, account.ID) || !pluginScopeContains(binding.UserIDs, principal.userID) ||
			!pluginScopeContains(binding.GroupIDs, principal.groupID) {
			continue
		}
		return route
	}
	return nil
}

func (r *pluginRoute) runtimeInstallation() *PluginInstallation {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.installation
}

func (m *PluginManager) removeLegacyRouteLocked(id int64) {
	old := m.legacyRoutes.Load()
	if old == nil {
		if route := m.route.Load(); route != nil && route.pluginID == id {
			m.route.Store(nil)
		}
		return
	}
	next := &legacyRouteTable{}
	for _, route := range old.routes {
		if route != nil && route.pluginID != id {
			next.routes = append(next.routes, route)
		}
	}
	m.legacyRoutes.Store(next)
	if len(next.routes) > 0 {
		m.route.Store(next.routes[0])
	} else {
		m.route.Store(nil)
	}
}

// markLegacyRouteUnavailableLocked replaces only the route instance that
// failed. Keeping the other candidates is required for priority/scope
// fallback; a failed lower-priority plugin must not erase healthy routes.
func (m *PluginManager) markLegacyRouteUnavailableLocked(failed *pluginRoute, message string) bool {
	if failed == nil {
		return false
	}
	old := m.legacyRoutes.Load()
	if old == nil {
		return false
	}
	next := &legacyRouteTable{routes: make([]*pluginRoute, 0, len(old.routes))}
	found := false
	for _, route := range old.routes {
		if route == nil {
			continue
		}
		if route != failed {
			next.routes = append(next.routes, route)
			continue
		}
		unavailable := *route
		unavailable.runtime = nil
		unavailable.unavailable = message
		next.routes = append(next.routes, &unavailable)
		found = true
	}
	if !found {
		return false
	}
	m.legacyRoutes.Store(next)
	if len(next.routes) > 0 {
		m.route.Store(next.routes[0])
	} else {
		m.route.Store(nil)
	}
	return true
}
