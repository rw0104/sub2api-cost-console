package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPluginPreviewRejectsProductionConnections(t *testing.T) {
	t.Setenv("SUB2API_PLUGIN_PREVIEW", "1")
	require.NoError(t, ValidatePluginPreviewDatabase("127.0.0.1", 25432, "sub2api_plugin_preview"))
	require.NoError(t, ValidatePluginPreviewRedis("localhost", 26379))
	for _, port := range []int{5432, 15432, 25432} {
		require.Error(t, ValidatePluginPreviewDatabase("127.0.0.1", port, "sub2api"))
	}
	require.Error(t, ValidatePluginPreviewDatabase("production.example", 25432, "sub2api_plugin_preview"))
	require.Error(t, ValidatePluginPreviewRedis("127.0.0.1", 16379))
	require.Error(t, ValidatePluginPreviewRedis("production.example", 26379))
	t.Setenv("SUB2API_PLUGIN_PREVIEW", "")
	require.NoError(t, ValidatePluginPreviewDatabase("production.example", 5432, "sub2api"))
}

func TestPreviewTrustedPublishersContainsCompanionKey(t *testing.T) {
	trusted := PreviewTrustedPublishers()
	require.Equal(t, PluginPreviewPublisherKeyBase64, trusted[PluginPreviewPublisherKeyID])
}
