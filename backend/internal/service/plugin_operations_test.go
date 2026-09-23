package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPluginInstallationETagIsStable(t *testing.T) {
	require.Equal(t, `"plugin-12-7"`, PluginInstallationETag(12, 7))
	require.Empty(t, PluginInstallationETag(0, 7))
	require.Empty(t, PluginInstallationETag(12, 0))
}

func TestPluginOperationErrorIsBoundedAndClassified(t *testing.T) {
	require.Equal(t, "PLUGIN_REVISION_CONFLICT", pluginOperationErrorCode(ErrPluginStateChanged))
	require.Equal(t, "PLUGIN_OPERATION_FAILED", pluginOperationErrorCode(errors.New("other")))
	message := boundedPluginOperationError(errors.New("abcdefghijklmnopqrstuvwxyz"))
	require.Equal(t, "abcdefghijklmnopqrstuvwxyz", message)
	tooLong := boundedPluginOperationError(errors.New(string(make([]byte, 1024))))
	require.Len(t, tooLong, 512)
}
