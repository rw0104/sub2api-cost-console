package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/stretchr/testify/require"
)

type revisionRoutingMemoryRepository struct {
	*extensionMemoryRepository
}

func (r *revisionRoutingMemoryRepository) UpdateRoutingRevision(_ context.Context, expected *PluginInstallation, bindings []PluginBinding, expectedRevision int64) (*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if expectedRevision <= 0 || r.row.Revision != expectedRevision || r.row.BinarySHA256 != expected.BinarySHA256 ||
		r.row.State != expected.State || !r.row.UpdatedAt.Equal(expected.UpdatedAt) {
		return nil, ErrPluginStateChanged
	}
	r.row.Bindings = append([]PluginBinding(nil), bindings...)
	next := time.Now()
	if !next.After(r.row.UpdatedAt) {
		next = r.row.UpdatedAt.Add(time.Microsecond)
	}
	r.row.UpdatedAt = next
	r.row.Revision++
	r.row.ETag = PluginInstallationETag(r.row.ID, r.row.Revision)
	return cloneExtensionInstallation(r.row), nil
}

func TestPluginRoutingPriorityScopeAndNoFailureFallback(t *testing.T) {
	m := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	cap := testPreprocessCapability()
	add := func(id int64, priority int, users, groups []int64) {
		i := &PluginInstallation{ID: id, Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{cap}},
			Bindings: []PluginBinding{{Capability: cap.ID, Platform: cap.Platform, AccountType: cap.AccountType, Enabled: true, RolloutPercent: 100,
				Priority: priority, AccountIDs: []int64{10}, UserIDs: users, GroupIDs: groups}}}
		handler := &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
			return pluginv2.PreprocessResponse{}, errors.New("lower priority must never be invoked on failure")
		}}
		m.publishRuntimeLocked(i, &pluginRuntime{installation: i, extension: handler, done: make(chan struct{})})
	}
	add(3, 20, []int64{7}, []int64{9})
	add(2, 0, nil, nil)
	add(1, 20, []int64{7}, []int64{9})
	account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	ctx := WithPluginPrincipal(context.Background(), 7, 9)
	require.EqualValues(t, 1, m.preprocessRoute(ctx, account).pluginID, "ties must not depend on publish order")
	require.EqualValues(t, 2, m.preprocessRoute(WithPluginPrincipal(ctx, 7, 8), account).pluginID)
	require.EqualValues(t, 2, m.preprocessRoute(context.Background(), account).pluginID)
	require.Nil(t, m.preprocessRoute(ctx, &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	route := m.preprocessRoute(ctx, account)
	route.runtime = nil
	_, err := m.PreprocessOpenAI(ctx, testPreprocessRequest(t), account)
	require.Error(t, err)
	for _, candidate := range m.extensions.Load().routes {
		require.Zero(t, candidate.calls.total.Load())
	}
	route.capability.FailureMode = pluginv2.FailureModeOpen
	request := testPreprocessRequest(t)
	out, err := m.PreprocessOpenAI(ctx, request, account)
	require.NoError(t, err)
	require.Same(t, request, out)
	for _, candidate := range m.extensions.Load().routes {
		require.Zero(t, candidate.calls.total.Load())
	}
}

func TestPluginRoutingTimeoutAndAuthenticatedContext(t *testing.T) {
	m, route := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(ctx context.Context, r pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		require.EqualValues(t, 7, r.Context.UserID)
		require.EqualValues(t, 9, r.Context.GroupID)
		require.Equal(t, "host-trace-123", r.Context.TraceID)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 20*time.Millisecond)
		<-ctx.Done()
		return pluginv2.PreprocessResponse{}, ctx.Err()
	}})
	route.binding.TimeoutMS = 10
	ctx := WithPluginPrincipal(context.Background(), 7, 9)
	ctx = context.WithValue(ctx, ctxkey.RequestID, "host-trace-123")
	started := time.Now()
	_, err := m.PreprocessOpenAI(ctx, testPreprocessRequest(t), &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
}

func TestPluginRoutingReducedConcurrencyKeepsInFlightCounter(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	m, old := testPreprocessManager(t, testPreprocessCapability(), &testPreprocessor{call: func(context.Context, pluginv2.PreprocessRequest) (pluginv2.PreprocessResponse, error) {
		close(started)
		<-release
		return pluginv2.PreprocessResponse{Decision: pluginv2.DecisionPass}, nil
	}})
	old.capability.TimeoutMS = 5000
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	completed := make(chan error, 1)
	request := testPreprocessRequest(t)
	go func() { _, err := m.PreprocessOpenAI(context.Background(), request, account); completed <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first call did not start")
	}
	i := *old.runtime.installation
	i.Manifest.Capabilities = []PluginCapability{old.capability}
	i.Bindings = append([]PluginBinding(nil), i.Bindings...)
	i.Bindings[0].MaxConcurrency = 1
	m.publishExtensionRoutesLocked(&i, old.runtime, "")
	current := m.extensions.Load().routes[0]
	require.Same(t, old.calls, current.calls)
	_, err := m.PreprocessOpenAI(context.Background(), testPreprocessRequest(t), account)
	require.Error(t, err)
	close(release)
	require.NoError(t, <-completed)
	require.Zero(t, current.calls.inFlight.Load())
}

func TestPluginRoutingValidationAndStaleEditor(t *testing.T) {
	now := time.Now()
	i := &PluginInstallation{ID: 1, State: PluginStateDisabled, UpdatedAt: now, Manifest: testExtensionManifest(),
		Bindings: []PluginBinding{{Capability: pluginv2.CapabilityRequestPreprocess, Platform: "openai", AccountType: "oauth", RolloutPercent: 100}}}
	repo := &extensionMemoryRepository{row: i}
	m := &PluginManager{repo: repo, runtimes: map[int64]*pluginRuntime{}}
	policy := PluginRoutingPolicy{Capability: pluginv2.CapabilityRequestPreprocess, RolloutPercent: 25, MaxConcurrency: 4, TimeoutMS: 20, UserIDs: []int64{8, 7}}
	saved, err := m.SaveRouting(context.Background(), 1, []PluginRoutingPolicy{policy}, now)
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, saved.Bindings[0].UserIDs)
	_, err = m.SaveRouting(context.Background(), 1, []PluginRoutingPolicy{policy}, now)
	require.ErrorIs(t, err, ErrPluginStateChanged)
	for _, invalid := range []PluginRoutingPolicy{
		{Capability: policy.Capability, MaxConcurrency: 0, RolloutPercent: 100},
		{Capability: policy.Capability, MaxConcurrency: 4, RolloutPercent: 100, UserIDs: []int64{1, 1}},
		{Capability: policy.Capability, MaxConcurrency: 4, RolloutPercent: 100, GroupIDs: []int64{-1}},
		{Capability: policy.Capability, MaxConcurrency: 4, RolloutPercent: 100, TimeoutMS: 5000},
		{Capability: "unknown.v1", MaxConcurrency: 4, RolloutPercent: 100},
	} {
		_, err = m.SaveRouting(context.Background(), 1, []PluginRoutingPolicy{invalid}, saved.UpdatedAt)
		require.Error(t, err)
	}
}

func TestPluginRoutingRevisionCASRejectsStaleEditor(t *testing.T) {
	now := time.Now()
	i := &PluginInstallation{ID: 2, State: PluginStateDisabled, UpdatedAt: now, Revision: 4, ETag: PluginInstallationETag(2, 4), Manifest: testExtensionManifest(),
		Bindings: []PluginBinding{{Capability: pluginv2.CapabilityRequestPreprocess, Platform: "openai", AccountType: "oauth", RolloutPercent: 100}}}
	repo := &revisionRoutingMemoryRepository{extensionMemoryRepository: &extensionMemoryRepository{row: i}}
	m := &PluginManager{repo: repo, runtimes: map[int64]*pluginRuntime{}}
	policy := PluginRoutingPolicy{Capability: pluginv2.CapabilityRequestPreprocess, RolloutPercent: 50, MaxConcurrency: 4, TimeoutMS: 20}
	saved, err := m.SaveRoutingWithRevision(context.Background(), i.ID, []PluginRoutingPolicy{policy}, 4)
	require.NoError(t, err)
	require.EqualValues(t, 5, saved.Revision)
	require.Equal(t, PluginInstallationETag(i.ID, 5), saved.ETag)
	_, err = m.SaveRoutingWithRevision(context.Background(), i.ID, []PluginRoutingPolicy{policy}, 4)
	require.ErrorIs(t, err, ErrPluginStateChanged)
}
