package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPluginRuntimeLogSinkRedactsAndBoundsOutput(t *testing.T) {
	sink := newPluginRuntimeLogSink("plugin-1-instance", pluginRuntimeLogStreamStdout, 64)
	secretLog := "Authorization: Bearer super-secret-token api_key=private-api-key\n"
	input := strings.Repeat("prefix ", 32) + secretLog

	written, err := sink.Write([]byte(input))
	require.NoError(t, err)
	require.Equal(t, len(input), written)

	snapshot := sink.snapshot()
	require.Equal(t, "plugin-1-instance", snapshot.InstanceID)
	require.Equal(t, pluginRuntimeLogStreamStdout, snapshot.Stream)
	require.LessOrEqual(t, snapshot.Bytes, 64)
	require.Greater(t, snapshot.DroppedBytes, uint64(0))
	require.NotContains(t, snapshot.Data, "super-secret-token")
	require.NotContains(t, snapshot.Data, "private-api-key")
}

func TestPluginRuntimeLogSinkRetainsRecentTailAndAssociatesRuntime(t *testing.T) {
	runtime := &pluginRuntime{
		instanceID: "plugin-2-instance",
		stdoutLog:  newPluginRuntimeLogSink("plugin-2-instance", pluginRuntimeLogStreamStdout, 16),
		stderrLog:  newPluginRuntimeLogSink("plugin-2-instance", pluginRuntimeLogStreamStderr, 16),
	}
	_, err := runtime.stdoutLog.Write([]byte("old output"))
	require.NoError(t, err)
	_, err = runtime.stdoutLog.Write([]byte(" newest"))
	require.NoError(t, err)
	_, err = runtime.stderrLog.Write([]byte("stderr failure"))
	require.NoError(t, err)

	stdout, stderr := runtime.runtimeLogSnapshots()
	require.Equal(t, runtime.instanceID, stdout.InstanceID)
	require.Equal(t, pluginRuntimeLogStreamStdout, stdout.Stream)
	require.Contains(t, stdout.Data, "newest")
	require.Equal(t, pluginRuntimeLogStreamStderr, stderr.Stream)
	require.Contains(t, stderr.Data, "stderr failure")
	require.NotZero(t, stdout.UpdatedAt)
	require.NotZero(t, stderr.UpdatedAt)
}

func TestPluginRuntimeLogSinkAcknowledgesLargeWritesWithoutGrowing(t *testing.T) {
	sink := newPluginRuntimeLogSink("plugin-3-instance", pluginRuntimeLogStreamStderr, 32)
	input := strings.Repeat("x", maxPluginRuntimeLogWrite*2)
	written, err := sink.Write([]byte(input))
	require.NoError(t, err)
	require.Equal(t, len(input), written)
	snapshot := sink.snapshot()
	require.LessOrEqual(t, snapshot.Bytes, 32)
	require.GreaterOrEqual(t, snapshot.DroppedBytes, uint64(len(input)-32))
}
