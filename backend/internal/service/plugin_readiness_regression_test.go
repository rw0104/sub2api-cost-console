package service

import (
	"context"
	"errors"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type readinessRegressionResult struct {
	healthy bool
	err     error
}

type readinessRegressionCall struct {
	ctx   context.Context
	reply chan readinessRegressionResult
}

// Hold Health open until the test has observed reconcileOnce returning. This
// makes the old schedule-then-cancel bug deterministic without a child process.
type readinessRegressionProbe struct {
	calls   chan readinessRegressionCall
	stopped chan struct{}
}

func (p *readinessRegressionProbe) health(ctx context.Context) readinessRegressionResult {
	call := readinessRegressionCall{ctx: ctx, reply: make(chan readinessRegressionResult, 1)}
	select {
	case p.calls <- call:
	case <-p.stopped:
		return readinessRegressionResult{err: context.Canceled}
	}
	select {
	case result := <-call.reply:
		return result
	case <-ctx.Done():
		return readinessRegressionResult{err: ctx.Err()}
	case <-p.stopped:
		return readinessRegressionResult{err: context.Canceled}
	}
}

type readinessRegressionTransport struct {
	pluginv1.TransportPluginClient
	probe *readinessRegressionProbe
}

func (c *readinessRegressionTransport) Health(ctx context.Context, _ *pluginv1.HealthRequest, _ ...grpc.CallOption) (*pluginv1.HealthResponse, error) {
	result := c.probe.health(ctx)
	return &pluginv1.HealthResponse{Healthy: result.healthy}, result.err
}

type readinessRegressionExtension struct {
	pluginv2.ExtensionHandler
	probe *readinessRegressionProbe
}

func (h *readinessRegressionExtension) Health(ctx context.Context) (pluginv2.HealthStatus, error) {
	result := h.probe.health(ctx)
	return pluginv2.HealthStatus{Healthy: result.healthy}, result.err
}

func newReadinessRegressionManager(t *testing.T, protocol string) (*PluginManager, *pluginRuntime, *readinessRegressionProbe) {
	t.Helper()
	manifest := testPluginManifest(nil)
	if protocol == "extension" {
		manifest = testExtensionManifest()
	}
	capability := manifest.Capabilities[0]
	installation := &PluginInstallation{
		ID: 1, PluginKey: manifest.ID, Version: manifest.Version,
		Manifest: manifest, State: PluginStateEnabled, BinarySHA256: "same-installed-binary",
		Bindings: []PluginBinding{{
			Capability: capability.ID, Platform: capability.Platform, AccountType: capability.AccountType,
			Enabled: true, RolloutPercent: 100,
		}},
	}
	probe := &readinessRegressionProbe{
		calls: make(chan readinessRegressionCall, 1), stopped: make(chan struct{}),
	}
	runtime := &pluginRuntime{
		installation: installation, instanceID: "existing-plugin-session",
		client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{}),
		statusAt: time.Now(), statusValue: &pluginv1.HealthResponse{
			Healthy: true, StatusJson: `{"observed_requests":7,"request_bytes":128}`,
		},
	}
	if protocol == "extension" {
		runtime.extension = &readinessRegressionExtension{probe: probe}
	} else {
		runtime.api = &readinessRegressionTransport{probe: probe}
	}
	manager := NewPluginManager(
		&extensionMemoryRepository{row: cloneExtensionInstallation(installation)},
		pluginTokenEncryptor{}, testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.1.179"},
	)
	// The fixture already represents a running session after any desktop upgrade.
	manager.desktopUpgrade = nil
	manager.publishRuntimeLocked(installation, runtime)
	t.Cleanup(func() {
		close(probe.stopped)
		waitReadinessRegressionProbe(t, runtime)
		runtime.kill()
	})
	return manager, runtime, probe
}

func awaitReadinessRegressionCall(t *testing.T, probe *readinessRegressionProbe) readinessRegressionCall {
	t.Helper()
	select {
	case call := <-probe.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("reconcile did not schedule the readiness Health RPC")
		return readinessRegressionCall{}
	}
}

func waitReadinessRegressionProbe(t *testing.T, runtime *pluginRuntime) {
	t.Helper()
	require.Eventually(t, func() bool {
		runtime.readinessMu.Lock()
		defer runtime.readinessMu.Unlock()
		return !runtime.readinessInFlight
	}, time.Second, time.Millisecond, "readiness probe did not finish")
}

func expireReadinessRegressionInterval(t *testing.T, runtime *pluginRuntime) {
	t.Helper()
	runtime.readinessMu.Lock()
	defer runtime.readinessMu.Unlock()
	require.False(t, runtime.readinessInFlight)
	// Advance only the sampling interval; Health completion remains asynchronous.
	runtime.readinessAt = time.Now().Add(-pluginReadinessInterval)
}

func TestPluginReconcileReadinessPreservesRunningSession(t *testing.T) {
	for _, protocol := range []string{"transport", "extension"} {
		t.Run(protocol, func(t *testing.T) {
			manager, runtime, probe := newReadinessRegressionManager(t, protocol)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			originalStatus := runtime.statusValue

			// More successful samples than the eviction threshold must keep the
			// same runtime and its request-observation state across reconcile ticks.
			for round := 0; round < pluginReadinessFailureThreshold+2; round++ {
				expireReadinessRegressionInterval(t, runtime)
				started := time.Now()
				require.NoError(t, manager.reconcileOnce(ctx))
				call := awaitReadinessRegressionCall(t, probe)
				require.NoError(t, call.ctx.Err(), "returning from reconcile must not cancel Health")
				deadline, ok := call.ctx.Deadline()
				require.True(t, ok, "asynchronous Health must retain a bounded lifetime")
				require.WithinDuration(t, started.Add(5*time.Second), deadline, time.Second)

				for tick := 0; tick < 3; tick++ {
					require.NoError(t, manager.reconcileOnce(ctx))
					require.Same(t, runtime, manager.runtimes[1])
				}
				select {
				case <-probe.calls:
					t.Fatal("reconcile launched overlapping readiness probes")
				default:
				}
				call.reply <- readinessRegressionResult{healthy: true}
				waitReadinessRegressionProbe(t, runtime)
				require.NoError(t, manager.reconcileOnce(ctx))
				require.Same(t, runtime, manager.runtimes[1])
				require.False(t, runtime.draining.Load())
				require.Same(t, originalStatus, runtime.statusValue, "readiness must not clear observed-request status")
				runtime.readinessMu.Lock()
				failures, readinessErr := runtime.readinessFailures, runtime.readinessErr
				runtime.readinessMu.Unlock()
				require.Zero(t, failures)
				require.NoError(t, readinessErr)
			}
		})
	}
}

func TestPluginReconcileReadinessHonorsManagerCancellation(t *testing.T) {
	for _, protocol := range []string{"transport", "extension"} {
		t.Run(protocol, func(t *testing.T) {
			manager, runtime, probe := newReadinessRegressionManager(t, protocol)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, manager.reconcileOnce(ctx))
			call := awaitReadinessRegressionCall(t, probe)
			require.NoError(t, call.ctx.Err())
			cancel()
			select {
			case <-call.ctx.Done():
				require.ErrorIs(t, call.ctx.Err(), context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("manager cancellation must stop an in-flight readiness RPC")
			}
			waitReadinessRegressionProbe(t, runtime)
		})
	}
}

func TestPluginReconcileReadinessOnlyEvictsAfterConsecutiveFailures(t *testing.T) {
	for _, protocol := range []string{"transport", "extension"} {
		t.Run(protocol, func(t *testing.T) {
			manager, runtime, probe := newReadinessRegressionManager(t, protocol)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sequence := []struct {
				result   readinessRegressionResult
				failures int
			}{
				{readinessRegressionResult{err: errors.New("temporary RPC failure")}, 1},
				{readinessRegressionResult{healthy: true}, 0},
				{readinessRegressionResult{err: errors.New("RPC unavailable")}, 1},
				{readinessRegressionResult{healthy: false}, 2},
				{readinessRegressionResult{err: context.DeadlineExceeded}, 3},
			}
			for _, step := range sequence {
				expireReadinessRegressionInterval(t, runtime)
				require.NoError(t, manager.reconcileOnce(ctx))
				call := awaitReadinessRegressionCall(t, probe)
				require.NoError(t, call.ctx.Err())
				call.reply <- step.result
				waitReadinessRegressionProbe(t, runtime)
				runtime.readinessMu.Lock()
				failures := runtime.readinessFailures
				runtime.readinessMu.Unlock()
				require.Equal(t, step.failures, failures)
				err := manager.reconcileOnce(ctx)
				if step.failures < pluginReadinessFailureThreshold {
					require.NoError(t, err, "a transient failure must not evict a serving runtime")
					require.Same(t, runtime, manager.runtimes[1])
					require.False(t, runtime.draining.Load())
				} else {
					require.Error(t, err)
					require.NotContains(t, manager.runtimes, int64(1))
					require.True(t, runtime.draining.Load())
					require.False(t, runtime.runtimeTimelineSnapshot().ExitedAt.IsZero())
				}
			}
		})
	}
}
