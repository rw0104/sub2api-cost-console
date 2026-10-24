package service

import (
	"context"
	"log/slog"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

// Plugin route decision reason codes are part of the host diagnostics contract.
// Keep these values stable: callers may persist or aggregate them.
const (
	PluginRouteReasonNoEnabledBinding     = "NO_ENABLED_BINDING"
	PluginRouteReasonPlatformMismatch     = "PLATFORM_MISMATCH"
	PluginRouteReasonAccountTypeMismatch  = "ACCOUNT_TYPE_MISMATCH"
	PluginRouteReasonRolloutExcluded      = "ROLLOUT_EXCLUDED"
	PluginRouteReasonAccountScopeExcluded = "ACCOUNT_SCOPE_EXCLUDED"
	PluginRouteReasonUserScopeExcluded    = "USER_SCOPE_EXCLUDED"
	PluginRouteReasonGroupScopeExcluded   = "GROUP_SCOPE_EXCLUDED"
	PluginRouteReasonRuntimeUnavailable   = "RUNTIME_UNAVAILABLE"
	PluginRouteReasonCircuitOpen          = "CIRCUIT_OPEN"
	PluginRouteReasonSelected             = "SELECTED"
)

// PluginRouteDecision is the safe, host-owned result of evaluating one
// capability route. It deliberately contains no account, user, group, token,
// URL, or request-body data. The caller can redact the optional selected
// identity fields before returning the decision to an unprivileged user.
type PluginRouteDecision struct {
	Reason             string `json:"reason"`
	FirstFailureReason string `json:"first_failure_reason,omitempty"`
	CandidateCount     int    `json:"candidate_count"`
	// Candidates is a redacted host-owned projection of all routes considered
	// for this capability. It enables fallback decisions without exposing
	// account scope contents or request data.
	Candidates     []PluginRouteCandidate `json:"candidates,omitempty"`
	FallbackPolicy PluginFallbackPolicy   `json:"fallback_policy,omitempty"`
	Selected       bool                   `json:"selected"`
	Stale          bool                   `json:"stale"`
	EvaluatedAt    time.Time              `json:"evaluated_at"`

	PluginID          int64  `json:"plugin_id,omitempty"`
	Capability        string `json:"capability,omitempty"`
	BindingID         int64  `json:"binding_id,omitempty"`
	RuntimeInstanceID string `json:"runtime_instance_id,omitempty"`
}

// pluginRouteEvaluation carries the private route pointer needed by the
// existing data path. The public EvaluateRoute method exposes only the safe
// decision envelope, so callers cannot use diagnostics to reach a runtime.
type pluginRouteEvaluation struct {
	route    *extensionRoute
	decision PluginRouteDecision
}

type pluginRouteExclusionsKey struct{}

func withPluginRouteExclusion(ctx context.Context, route *extensionRoute) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current := map[*extensionRoute]bool{}
	if existing, ok := ctx.Value(pluginRouteExclusionsKey{}).(map[*extensionRoute]bool); ok {
		for candidate := range existing {
			current[candidate] = true
		}
	}
	if route != nil {
		current[route] = true
	}
	return context.WithValue(ctx, pluginRouteExclusionsKey{}, current)
}

func pluginRouteExcluded(ctx context.Context, route *extensionRoute) bool {
	if ctx == nil || route == nil {
		return false
	}
	excluded, _ := ctx.Value(pluginRouteExclusionsKey{}).(map[*extensionRoute]bool)
	return excluded[route]
}

func logPluginRouteDecision(decision PluginRouteDecision) {
	slog.Debug("plugin_route_decision", "reason", decision.Reason,
		"first_failure_reason", decision.FirstFailureReason, "candidate_count", decision.CandidateCount,
		"selected", decision.Selected, "stale", decision.Stale, "plugin_id", decision.PluginID,
		"capability", decision.Capability, "binding_id", decision.BindingID,
		"runtime_instance_id", decision.RuntimeInstanceID)
}

// EvaluateRoute evaluates the current v2 extension route table without
// invoking a plugin. requirePrincipalScope preserves the two existing call
// modes: request preprocess always checks user/group scope, while the legacy
// protection preflight may intentionally defer that check until the request
// has an authenticated principal.
func (m *PluginManager) EvaluateRoute(ctx context.Context, capability string, account *Account, requirePrincipalScope bool) PluginRouteDecision {
	return m.evaluateRoute(ctx, capability, account, requirePrincipalScope).decision
}

func (m *PluginManager) nextExtensionRoute(ctx context.Context, capability string, account *Account, requirePrincipalScope bool, current *extensionRoute) *extensionRoute {
	if m == nil || account == nil {
		return nil
	}
	table := m.extensions.Load()
	if table == nil {
		return nil
	}
	principal := pluginPrincipalFromContext(ctx)
	passed := current == nil
	for _, route := range table.routes {
		if route == nil {
			continue
		}
		if !passed {
			if route == current {
				passed = true
			}
			continue
		}
		binding := route.binding
		if !binding.Enabled || binding.Capability != capability || binding.Platform != account.Platform || binding.AccountType != account.Type ||
			binding.RolloutPercent <= 0 || int(stablePluginBucket(account.ID)) >= binding.RolloutPercent ||
			!pluginScopeContains(binding.AccountIDs, account.ID) ||
			(requirePrincipalScope && (!pluginScopeContains(binding.UserIDs, principal.userID) || !pluginScopeContains(binding.GroupIDs, principal.groupID))) {
			continue
		}
		if route.runtime == nil || route.runtime.draining.Load() || (route.runtime.client != nil && route.runtime.client.Exited()) {
			continue
		}
		if capability == pluginv2.CapabilityProtectionTransport && route.runtime.transport == nil {
			continue
		}
		if capability == pluginv2.CapabilityRequestPreprocess && route.runtime.extension == nil {
			continue
		}
		if route.calls != nil {
			route.calls.mu.Lock()
			open := time.Now().Before(route.calls.openUntil)
			route.calls.mu.Unlock()
			if open {
				continue
			}
		}
		return route
	}
	return nil
}

// evaluateRoute is the single predicate implementation for v2 route
// diagnostics. It follows the same ordering as preprocessRoute and
// protectionTransportRoute so a later integration can replace their silent
// nil returns without changing selection priority.
func (m *PluginManager) evaluateRoute(ctx context.Context, capability string, account *Account, requirePrincipalScope bool) pluginRouteEvaluation {
	decision := PluginRouteDecision{
		Reason:      PluginRouteReasonNoEnabledBinding,
		EvaluatedAt: time.Now().UTC(),
	}
	if m == nil || account == nil || capability == "" {
		return pluginRouteEvaluation{decision: decision}
	}

	table := m.extensions.Load()
	if table == nil {
		return pluginRouteEvaluation{decision: decision}
	}
	decision.Stale = table.stateUnavailable
	decision.Candidates = pluginRouteCandidates(table, capability)
	principal := pluginPrincipalFromContext(ctx)
	firstFailure := ""
	setFailure := func(reason string) {
		if firstFailure == "" {
			firstFailure = reason
		}
	}

	for _, route := range table.routes {
		if route == nil || route.binding.Capability != capability {
			continue
		}
		if pluginRouteExcluded(ctx, route) {
			continue
		}
		if !route.binding.Enabled {
			// Disabled bindings normally do not enter the immutable route table,
			// but treating a manually retained entry as non-candidate keeps the
			// diagnostic fail-closed if a stale table is observed.
			continue
		}
		decision.CandidateCount++
		binding := route.binding
		if binding.Platform != account.Platform {
			setFailure(PluginRouteReasonPlatformMismatch)
			continue
		}
		if binding.AccountType != account.Type {
			setFailure(PluginRouteReasonAccountTypeMismatch)
			continue
		}
		if binding.RolloutPercent <= 0 || int(stablePluginBucket(account.ID)) >= binding.RolloutPercent {
			setFailure(PluginRouteReasonRolloutExcluded)
			continue
		}
		if !pluginScopeContains(binding.AccountIDs, account.ID) {
			setFailure(PluginRouteReasonAccountScopeExcluded)
			continue
		}
		if requirePrincipalScope && !pluginScopeContains(binding.UserIDs, principal.userID) {
			setFailure(PluginRouteReasonUserScopeExcluded)
			continue
		}
		if requirePrincipalScope && !pluginScopeContains(binding.GroupIDs, principal.groupID) {
			setFailure(PluginRouteReasonGroupScopeExcluded)
			continue
		}

		// A nil client is accepted for in-process test runtimes. A real
		// runtime's client is non-nil; if it exists and has exited, the route
		// is unavailable. This mirrors the existing data-path checks.
		if route.runtime == nil || route.runtime.draining.Load() ||
			(route.runtime.client != nil && route.runtime.client.Exited()) ||
			(route.runtime.api == nil && route.runtime.extension == nil && route.runtime.transport == nil) {
			setFailure(PluginRouteReasonRuntimeUnavailable)
			decision.Reason = PluginRouteReasonRuntimeUnavailable
			decision.PluginID = route.pluginID
			decision.Capability = route.capability.ID
			decision.BindingID = binding.ID
			decision.FallbackPolicy = binding.EffectiveFallbackPolicy()
			if route.runtime != nil {
				decision.RuntimeInstanceID = route.runtime.instanceID
			}
			decision.FirstFailureReason = firstFailure
			return pluginRouteEvaluation{route: route, decision: decision}
		}

		if route.calls != nil {
			route.calls.mu.Lock()
			open := time.Now().Before(route.calls.openUntil)
			route.calls.mu.Unlock()
			if open {
				setFailure(PluginRouteReasonCircuitOpen)
				decision.Reason = PluginRouteReasonCircuitOpen
				decision.PluginID = route.pluginID
				decision.Capability = route.capability.ID
				decision.BindingID = binding.ID
				decision.FallbackPolicy = binding.EffectiveFallbackPolicy()
				decision.RuntimeInstanceID = route.runtime.instanceID
				decision.FirstFailureReason = firstFailure
				return pluginRouteEvaluation{route: route, decision: decision}
			}
		}

		decision.Reason = PluginRouteReasonSelected
		decision.Selected = true
		decision.PluginID = route.pluginID
		decision.Capability = route.capability.ID
		decision.BindingID = binding.ID
		decision.FallbackPolicy = binding.EffectiveFallbackPolicy()
		decision.RuntimeInstanceID = route.runtime.instanceID
		decision.FirstFailureReason = firstFailure
		return pluginRouteEvaluation{route: route, decision: decision}
	}

	decision.FirstFailureReason = firstFailure
	if firstFailure != "" {
		decision.Reason = firstFailure
	}
	return pluginRouteEvaluation{decision: decision}
}
