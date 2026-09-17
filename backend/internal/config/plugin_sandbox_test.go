package config

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPluginSandboxDefaultsAndValidation(t *testing.T) {
	defaults := PluginSandboxConfig{}.WithDefaults()
	require.Equal(t, "process", defaults.Mode)
	require.Equal(t, 256, defaults.MemoryMB)
	require.NoError(t, defaults.Validate())
	for _, invalid := range []PluginSandboxConfig{
		{Mode: "best_effort"}, {MemoryMB: -1}, {MemoryMB: 8192}, {CPUMilli: 50}, {CPUMilli: 8000}, {PidsLimit: 10}, {Image: "--privileged"},
	} {
		require.Error(t, invalid.Validate())
	}
	require.NoError(t, (PluginSandboxConfig{Mode: "container", MemoryMB: 64, CPUMilli: 100, PidsLimit: 32}).Validate())
}
