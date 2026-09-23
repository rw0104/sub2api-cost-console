package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPluginRuntimeTimelineRecordsBoundedLifecycle(t *testing.T) {
	runtime := &pluginRuntime{}
	runtime.markRuntimeAPIReady()
	runtime.markRuntimeHostAttached()
	runtime.markRuntimeReadiness(nil)
	runtime.markRuntimeDrainRequested()
	runtime.markRuntimeDrainFinished()
	runtime.markRuntimeExited()

	snapshot := runtime.runtimeTimelineSnapshot()
	require.False(t, snapshot.StartedAt.IsZero())
	require.False(t, snapshot.APIReadyAt.IsZero())
	require.False(t, snapshot.HostAttachedAt.IsZero())
	require.False(t, snapshot.ReadinessAt.IsZero())
	require.False(t, snapshot.DrainRequestedAt.IsZero())
	require.False(t, snapshot.DrainFinishedAt.IsZero())
	require.False(t, snapshot.ExitedAt.IsZero())
	require.LessOrEqual(t, snapshot.StartedAt, snapshot.APIReadyAt)
	require.LessOrEqual(t, snapshot.APIReadyAt, snapshot.HostAttachedAt)
	require.LessOrEqual(t, snapshot.HostAttachedAt, snapshot.ReadinessAt)
	require.LessOrEqual(t, snapshot.ReadinessAt, snapshot.DrainRequestedAt)
	require.LessOrEqual(t, snapshot.DrainRequestedAt, snapshot.DrainFinishedAt)
	require.LessOrEqual(t, snapshot.DrainFinishedAt, snapshot.ExitedAt)

	value := snapshot.mapValue()
	require.NotEmpty(t, value["started_at"])
	require.NotEmpty(t, value["exited_at"])
	for key, item := range value {
		if key == "last_error_code" || key == "last_error_at" {
			continue
		}
		require.IsType(t, "", item, key)
	}
}

func TestPluginRuntimeTimelineStoresOnlyStableErrorCode(t *testing.T) {
	runtime := &pluginRuntime{}
	runtime.markRuntimeError(errors.New("token=private-secret upstream body"))

	snapshot := runtime.runtimeTimelineSnapshot()
	require.Equal(t, "RUNTIME_ERROR", snapshot.LastErrorCode)
	require.False(t, snapshot.LastErrorAt.IsZero())
	raw, err := json.Marshal(snapshot.mapValue())
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-secret")
}

func TestPluginRuntimeErrorCodeUsesTransportAndContextClasses(t *testing.T) {
	require.Equal(t, "TIMEOUT", pluginRuntimeErrorCode(context.DeadlineExceeded))
	require.Equal(t, "CANCELED", pluginRuntimeErrorCode(context.Canceled))
	require.Equal(t, "PLUGIN_RPC_ERROR", pluginRuntimeErrorCode(&PluginTransportError{Code: "PLUGIN_RPC_ERROR", Message: "private"}))
	require.Equal(t, "RUNTIME_ERROR", pluginRuntimeErrorCode(errors.New("private")))
}
