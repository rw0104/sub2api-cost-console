package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newDrainControllerTestRuntime(id int64) *pluginRuntime {
	return &pluginRuntime{
		installation: &PluginInstallation{ID: id},
		instanceID:   "test-instance",
		done:         make(chan struct{}),
	}
}

func releaseDrainRuntimeAfterStart(runtime *pluginRuntime, delay time.Duration) {
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			if runtime.draining.Load() {
				time.Sleep(delay)
				runtime.inFlight.Store(0)
				close(runtime.done)
				return
			}
		}
	}
}

func TestDrainControllerRunsRuntimesInParallelUnderOneDeadline(t *testing.T) {
	runtimes := []*pluginRuntime{
		newDrainControllerTestRuntime(1),
		newDrainControllerTestRuntime(2),
	}
	for _, runtime := range runtimes {
		runtime.inFlight.Store(1)
		go releaseDrainRuntimeAfterStart(runtime, 100*time.Millisecond)
	}

	started := time.Now()
	progress := NewDrainController(runtimes, 500*time.Millisecond).Run(context.Background())
	elapsed := time.Since(started)

	// Sequential drains would take roughly 200 ms. Keep enough scheduling
	// margin while proving that both runtimes share the same wait window.
	require.Less(t, elapsed, 175*time.Millisecond)
	require.Equal(t, DrainStateFinished, progress.State)
	require.Equal(t, 2, progress.Total)
	require.Equal(t, 2, progress.Finished)
	require.Equal(t, 0, progress.Forced)
	for _, instance := range progress.Instances {
		require.Equal(t, DrainStateFinished, instance.State)
		require.Zero(t, instance.InFlight)
	}
}

func TestDrainControllerForcesAllRuntimesAtSharedDeadline(t *testing.T) {
	runtimes := []*pluginRuntime{
		newDrainControllerTestRuntime(10),
		newDrainControllerTestRuntime(11),
	}
	for _, runtime := range runtimes {
		runtime.inFlight.Store(1)
	}

	started := time.Now()
	progress := NewDrainController(runtimes, 40*time.Millisecond).Run(context.Background())
	elapsed := time.Since(started)

	require.GreaterOrEqual(t, elapsed, 30*time.Millisecond)
	require.Less(t, elapsed, 200*time.Millisecond)
	require.Equal(t, DrainStateForced, progress.State)
	require.Equal(t, 2, progress.Forced)
	require.Equal(t, 2, progress.Finished)
	for _, instance := range progress.Instances {
		require.Equal(t, DrainStateForced, instance.State)
		require.Equal(t, int64(1), instance.InFlight)
		require.Equal(t, "DRAIN_DEADLINE_EXCEEDED", instance.LastErrorCode)
	}
}

func TestDrainControllerSnapshotReportsInFlightWhileRunning(t *testing.T) {
	runtime := newDrainControllerTestRuntime(20)
	runtime.inFlight.Store(1)
	controller := NewDrainController([]*pluginRuntime{runtime}, time.Second)
	finished := make(chan DrainProgress, 1)
	go func() { finished <- controller.Run(context.Background()) }()

	require.Eventually(t, func() bool {
		progress := controller.Snapshot()
		return progress.State == DrainStateDraining && len(progress.Instances) == 1 &&
			progress.Instances[0].State == DrainStateDraining && progress.Instances[0].InFlight == 1
	}, time.Second, time.Millisecond)

	runtime.inFlight.Store(0)
	close(runtime.done)
	progress := <-finished
	require.Equal(t, DrainStateFinished, progress.State)
	require.Equal(t, DrainStateFinished, progress.Instances[0].State)
}
